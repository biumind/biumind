// Package wiring assembles the engine + its supporting subsystems
// (MCP, hooks, permissions, trust, memory, skills, background tasks)
// from a parsed Config + a small Flags shape. Lives under cmd/biu/
// because it pulls together internal/* packages in a way that's
// specific to how the biu CLI exposes them — the SDK (pkg/biumindkit)
// has its own simpler wiring.
//
// Why a dedicated package:
//
//   - main.go used to carry ~500 lines of bootstrap glue (engine
//     provider selection, MCP registry assembly, settings loading,
//     hook registration, plan verifier, skill auto-attach, worktree
//     + cron + interactive tools). That made main.go hard to read
//     and the helpers hard to test in isolation.
//   - Splitting it here keeps main.go focused on cobra command tree
//     + I/O concerns. The wiring layer is a pure function of
//     (Config, Flags, model) → (engine, bg store, mcp registry,
//     trust store).
//
// Flags is a minimal struct rather than a *rootFlags pointer so the
// CLI parser stays in main.go and this package doesn't import its
// private types. Callers fill the fields they care about; everything
// else falls back to config-file defaults.

package wiring

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/adapters"
	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/lsp"
	"github.com/biumind/biumind/apps/cli/biu/internal/mcp"
	"github.com/biumind/biumind/apps/cli/biu/internal/memory"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/planhint"
	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
	"github.com/biumind/biumind/apps/cli/biu/internal/plugins/bundled"
	"github.com/biumind/biumind/apps/cli/biu/internal/planverify"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/files"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/interactive"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
	webtools "github.com/biumind/biumind/apps/cli/biu/internal/tools/web"
	"github.com/biumind/biumind/apps/cli/biu/internal/trust"
	"github.com/biumind/biumind/apps/cli/biu/internal/worktree"
)

// Flags is the slice of CLI/env state the wiring layer needs. Mirrors
// the subset of cmd/biu's rootFlags that BuildEngine + provider
// helpers consume; main.go fills it before dispatch so the wiring
// package never imports main.
type Flags struct {
	Mode     string // "cloud" | "byo_endpoint" | "direct"
	RelayURL string // model-relay base URL (cloud / byo_endpoint)
	Token    string // virtual key for model-relay auth
	System   string // --system override / extension to the system prompt
	AddDirs  []string // --add-dir entries (CLI source). Joined with settings.json on startup.
}

// BuildEngine returns a configured QueryEngine + the matching
// background-task store + the MCP registry + the trust store +
// the file-based Skill registry.
//
// Supports both direct-Anthropic and model-relay modes — model-relay forwards the
// same `/v1/messages` wire format with Bearer auth so we share most
// of the wiring. Returns all-nils only when neither path can be
// configured (e.g. missing API key + missing model-relay URL); the REPL
// falls back to the legacy chat path in that case.
//
// All five handles are paired so REPL slashes see the live state
// the engine actually uses: /tasks sees the same bg queue the Bash
// tool writes to; /mcp sees the same servers the catalog routes to;
// /trust sees the same gate the hook registry consults; /<skill>
// sees the same SKILL.md registry the engine's SkillTool resolves
// against (so an auto-attached skill in the system prompt and an
// explicit /<skill> invocation can never see different versions of
// the file).
func BuildEngine(cfg *config.Config, f Flags, model string) (*engine.QueryEngine, *bgtask.Store, *mcp.Registry, *trust.Store, *skills.Registry) {
	prov := BuildEngineProvider(cfg, f)
	if prov == nil {
		return nil, nil, nil, nil, nil
	}
	cwd, _ := os.Getwd()

	// Trust gate: loaded early so anything that spawns shell
	// commands during engine setup (MCP servers, hook registration)
	// can consult it. Errors here are non-fatal — falling back to
	// nil keeps biu's pre-P20.15 "everything trusted" posture and
	// at worst surfaces a stderr line.
	var trustStore *trust.Store
	if home, err := os.UserHomeDir(); err == nil {
		if ts, err := trust.Load(home); err == nil {
			trustStore = ts
		} else {
			fmt.Fprintf(os.Stderr, "[biu] trust gate: %v (running open)\n", err)
		}
	}

	st := state.New()
	st.OriginalCwd = cwd
	reg := tools.Defaults().EngineRegistrySimple()
	// Native file tools overwrite the legacy adapters by name —
	// engine.SimpleRegistry.Register does last-write-wins, so register
	// them after Defaults() to gain the freshness-aware Read/Edit/Write.
	files.Register(reg)
	// Plugins aggregator — built once per session, consulted by
	// every component loader below so plugin-contributed agents /
	// commands / skills / hooks / output-styles / mcp servers all
	// land in the same registries as user/project sources. The
	// disabled list comes from layered settings (plugins.disabled);
	// project / local files can disable additional plugins on top
	// of user but cannot re-enable what user disabled. Plugin
	// errors are surfaced via stderr but never block startup.
	var disabledPlugins []string
	if layered, err := clauseSettings.Load(cwd); err == nil && layered != nil {
		disabledPlugins = layered.MergedDisabledPlugins()
	}
	// Bundled plugins ship inside the binary and register themselves
	// as a roots provider via plugins.DefaultRoots. We just surface
	// the most recent extraction error so disk-full / permission
	// problems show up in startup logs rather than silently dropping
	// the bundled set.
	if err := bundled.MaterialiseErr(); err != nil {
		fmt.Fprintf(os.Stderr,
			"[biu] bundled plugins: %v (continuing without them)\n", err)
	}
	pluginAgg := plugins.LoadAll(plugins.DefaultRoots(cwd), disabledPlugins)
	if n := len(pluginAgg.Plugins); n > 0 {
		off := 0
		for _, lp := range pluginAgg.Plugins {
			if !lp.Enabled {
				off++
			}
		}
		if off > 0 {
			fmt.Fprintf(os.Stderr, "[biu] plugins loaded: %d (%d disabled)\n", n, off)
		} else {
			fmt.Fprintf(os.Stderr, "[biu] plugins loaded: %d\n", n)
		}
	}
	for _, e := range pluginAgg.Errors {
		fmt.Fprintf(os.Stderr, "[biu] plugin %s: %v\n", e.Path, e.Err)
	}

	// Sub-agent definitions from ~/.biumind/agents + project layer.
	// AgentTool consults this to apply per-type system prompt /
	// model / mode / tool whitelist on dispatch.
	agentReg, _ := agents.Load(cwd)
	pluginAgg.AttachAgents(agentReg)
	// Shared team + message registries (P20.53-2). Created here, passed
	// to BOTH the engine and the orchestration tool layer so SendMessage
	// enqueues into the same inbox SpawnAsync's goroutine drains. Nil
	// these out and the swarm tools degrade gracefully (soft errors).
	teamRegistry := engine.NewTeamRegistry()
	teamMessages := engine.NewMessageInbox()
	orchestration.Register(reg, orchestration.Options{
		Agents:   agentReg,
		Teams:    teamRegistry,
		Messages: teamMessages,
	})
	lspPool := lsp.NewPool(cwd, lsp.DefaultServers())
	// Background-task store: shared by Bash{run_in_background:true},
	// the BashOutput / KillBash partner tools, and the /tasks REPL
	// command. One store per biu process so all three see the same
	// queue.
	bgStore := bgtask.NewStore()

	// Load layered settings early so we can lift the merged
	// sandbox config into BashTool's allow/deny lists at
	// registration time. The same `layered` value is re-used
	// further down for permissions + hooks; we ignore errors here
	// and let the second Load below surface them — duplicating
	// the call is cheap (file reads, no I/O on misses).
	var sandboxCfg *clauseSettings.SandboxConfig
	if layered, err := clauseSettings.Load(cwd); err == nil && layered != nil {
		sandboxCfg = layered.MergedSandboxConfig(cwd)
	}

	// Permission context is built BEFORE webtools register so the
	// BashTool can hold a pointer to it. /add-dir at REPL time and
	// settings hot-reloads then propagate to the sandbox allowWrite
	// list without re-registering tools.
	permCtx := permissions.NewContext()
	permCtx.SetOriginalCwd(cwd)

	webtoolsOpt := webtools.Options{
		SearxNGURL: cfg.Search.SearxNGURL,
		LSPBackend: lspPool,
		BgTasks:    bgStore,
		PermCtx:    permCtx,
	}
	if sandboxCfg != nil {
		webtoolsOpt.SandboxFSReadDeny = sandboxCfg.FSReadDeny
		webtoolsOpt.SandboxFSReadAllowWithinDeny = sandboxCfg.FSReadAllowWithinDeny
		webtoolsOpt.SandboxFSWriteAllowExtra = sandboxCfg.FSWriteAllowExtra
		webtoolsOpt.SandboxFSWriteDenyWithinAllow = sandboxCfg.FSWriteDenyWithinAllow
		// Stderr breadcrumb so the operator can see whether their
		// sandbox section actually loaded — silent activation is a
		// debugging trap when the rules don't take effect because
		// of a typo.
		fmt.Fprintf(os.Stderr,
			"[biu] sandbox: %d read-deny, %d read-allow, %d write-extra, %d write-deny\n",
			len(sandboxCfg.FSReadDeny),
			len(sandboxCfg.FSReadAllowWithinDeny),
			len(sandboxCfg.FSWriteAllowExtra),
			len(sandboxCfg.FSWriteDenyWithinAllow))
	}
	webtools.Register(reg, webtoolsOpt)
	// MCP servers from `~/.biu/config.toml` (user-trusted) + the
	// project-local `<cwd>/.biumind/config.toml` (gated by trust).
	// The project layer needs the trust store so we hand it
	// through; user-layer entries always pass.
	mcpReg := BootstrapMCP(cfg, trustStore)
	if mcpReg != nil {
		names := mcpReg.RegisterEngineTools(reg)
		if len(names) > 0 {
			fmt.Fprintf(os.Stderr, "[biu] MCP tools registered: %d\n", len(names))
		}
	}

	// Layered ~/.biumind/settings.json + project + local. Feed
	// merged rules into the engine's permission context + hook
	// registry. CLI-level overrides (legacy permissions.* TOML,
	// --dangerously-skip-permissions) win.
	// permCtx was created above before webtools.Register so BashTool
	// can hold a pointer; here we just use it.
	hookReg := hooks.NewRegistry()

	// Trust gate (loaded above) wires into the hook registry so a
	// malicious project's settings.json can't auto-execute hook
	// commands when biu opens an untrusted directory. Same store
	// shared with MCP bootstrap + the REPL's /trust slash.
	if trustStore != nil {
		hookReg.SetTrustGate(adapters.HookTrustGate(trustStore))
		hookReg.SetSkipNotifier(func(evt hooks.Event, count int) {
			fmt.Fprintf(os.Stderr,
				"[biu] %d %s hook(s) skipped: untrusted dir (run /trust here)\n",
				count, evt)
		})
	}
	applySettings := func(layered *clauseSettings.Layered) {
		// Permissions: ReplaceRules clears previous entries per
		// (source, behavior) tuple before applying new ones, so a
		// reload that removed rules really removes them. Same logic
		// for additionalDirectories — clear settings-sourced dirs so
		// a removed entry actually disappears from the running ctx
		// while session / cliArg dirs survive the reload.
		for _, src := range []permissions.Source{
			permissions.SrcUserSettings,
			permissions.SrcProjectSettings,
			permissions.SrcLocalSettings,
		} {
			permCtx.ReplaceRules(src, permissions.BehaviorAllow, nil)
			permCtx.ReplaceRules(src, permissions.BehaviorDeny, nil)
			permCtx.ReplaceRules(src, permissions.BehaviorAsk, nil)
			permCtx.ClearDirectoriesBySource(src)
		}
		layered.ApplyToContext(permCtx)
	}

	if layered, err := clauseSettings.Load(cwd); err == nil {
		applySettings(layered)
		registerLayerHooks(hookReg, "user", layered.User)
		registerLayerHooks(hookReg, "project", layered.Project)
		registerLayerHooks(hookReg, "local", layered.Local)
		if layered.UserPath != "" || layered.ProjectPath != "" || layered.LocalPath != "" {
			fmt.Fprintf(os.Stderr, "[biu] settings loaded: user=%s project=%s local=%s\n",
				or(layered.UserPath, "-"), or(layered.ProjectPath, "-"),
				or(layered.LocalPath, "-"))
		}
		// Settings validation warnings — non-fatal, surfaced on
		// stderr so the operator notices misconfigured working dirs
		// without blocking startup.
		for _, w := range layered.ValidationWarnings(cwd) {
			fmt.Fprintf(os.Stderr, "[biu] warning: %s\n", w)
		}
	}

	// Plugin hooks merge onto the same registry. Plugin sources tag
	// as "plugin:<name>" so the trust gate / skip notifier can show
	// which plugin's hooks were silenced when an untrusted dir is
	// opened.
	pluginAgg.AttachHooks(hookReg)

	// Hot reload: poll user / project / local files every 2s. On
	// change, refresh the permission rules in-place. Hooks are not
	// re-applied today (would require an engine restart) — we log a
	// hint when the hooks block looks different so the user knows
	// they need to restart.
	clauseSettings.NewWatcher(cwd, func(l *clauseSettings.Layered) {
		applySettings(l)
		fmt.Fprintln(os.Stderr,
			"[biu] settings reloaded (permissions refreshed; hooks need a restart to pick up)")
	})
	// Legacy [permissions] section in config.toml still contributes
	// rules — treat the whole list as user-allowlist.
	if cfg.Permissions.Mode != "" {
		permCtx.SetMode(permissions.ModeFromString(cfg.Permissions.Mode))
	}
	if len(cfg.Permissions.Allowlist) > 0 {
		permCtx.AddRules(permissions.SrcUserSettings,
			permissions.BehaviorAllow, cfg.Permissions.Allowlist)
	}

	// PWD symlink case: when the shell entered cwd via a symlink (e.g.
	// `cd /home/user/proj` where /home/user is a symlink), os.Getwd()
	// returns the resolved path but the user's intent is the symlink
	// path.
	// session-source dir if it differs from cwd and points at the same
	// inode. This way file tools using the symlinked spelling (e.g.
	// from a Bash command using $PWD) don't get rejected as "outside
	// working dir".
	if pwdEnv := os.Getenv("PWD"); pwdEnv != "" && pwdEnv != cwd {
		if isSymlinkAlias(pwdEnv, cwd) {
			permCtx.AddDirectories(permissions.SrcSession, []string{pwdEnv})
		}
	}

	// CLI `--add-dir` flag: validate + apply to ctx, source CliArg.
	// Validation failures emit a stderr warning but don't abort startup
	// .
	for _, raw := range f.AddDirs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		result := permissions.ValidateDirectoryForWorkspace(raw, permCtx, cwd)
		switch result.Kind {
		case permissions.DirValidSuccess:
			permCtx.AddDirectories(permissions.SrcCLIArg, []string{result.AbsolutePath})
		case permissions.DirValidAlreadyInWorkingDir:
			// Silent: already covered, no action needed 
		case permissions.DirValidPathNotFound:
			// Silent on missing dirs
		default:
			fmt.Fprintf(os.Stderr, "[biu] --add-dir: %s\n", result.HelpMessage())
		}
	}
	if extras := permCtx.AdditionalDirectoryPaths(); len(extras) > 0 {
		fmt.Fprintf(os.Stderr,
			"[biu] working directories: cwd=%s extras=%d %v\n",
			cwd, len(extras), extras)
	}

	// Memory: load BIUMIND.md from user/project/local layers and
	// merge into the system prompt. User-provided --system flag
	// content stays in front so the user voice always wins.
	// claudeMdExcludes from settings.json suppress big monorepos.
	// ExtraDirs comes from the live permission ctx so that any dir
	// added via /add-dir, --add-dir, or settings.permissions.
	// additionalDirectories contributes its own BIUMIND.md content
	// to the system prompt —
	// scan.
	memOpt := memory.Options{
		ExtraDirs: permCtx.AdditionalDirectoryPaths(),
	}
	if layered, err := clauseSettings.Load(cwd); err == nil {
		memOpt.Excludes = layered.MergedClaudeMdExcludes()
	}
	mem := memory.LoadWithOptions(cwd, memOpt)
	system := f.System
	if memSys := mem.SystemPrompt(); memSys != "" {
		if system == "" {
			system = memSys
		} else {
			system = system + "\n\n" + memSys
		}
		fmt.Fprintf(os.Stderr, "[biu] memory loaded: %d files\n", len(mem.Files))
	}

	// Auto-memory primer: tell the model it has a persistent memory
	// store under ~/.biumind/memory and how to use it. Always folded
	// in (even when MEMORY.md is missing) — the primer's value is
	// announcing that the directory exists and memories CAN be saved.
	// Skipped only when HOME can't be resolved (rare, e.g. in init
	// containers without a HOME env).
	if home, err := os.UserHomeDir(); err == nil {
		auto := memory.LoadAuto(home)
		if autoSys := auto.SystemPrompt(); autoSys != "" {
			if system == "" {
				system = autoSys
			} else {
				system = system + "\n\n" + autoSys
			}
			if auto.Exists() {
				fmt.Fprintf(os.Stderr, "[biu] auto-memory index loaded: %s\n", auto.IndexPath)
			}
		}
		// Register the structured Memory tool so the model can
		// save / list / remove memories in one round-trip instead of
		// driving Write+Edit by hand. Wired even when Exists()=false:
		// the primer announces the directory and Memory{action:save}
		// creates it lazily. (P20.54)
		reg.Register(memory.MemoryTool{Auto: auto})
	}

	// Skills: load file-based skills before constructing the engine
	// so we can fold path-matched skills into the system prompt.
	// Plugin skills attach onto the same registry so AutoAttach
	// considers them in the path-match pass.
	skillReg, _ := skills.LoadWithExtraDirs(cwd, permCtx.AdditionalDirectoryPaths())
	pluginAgg.AttachSkills(skillReg)
	if attached := skillReg.AutoAttachPrompt(cwd); attached != "" {
		if system == "" {
			system = attached
		} else {
			system = system + "\n\n" + attached
		}
		hits := len(skillReg.AutoAttach(cwd))
		fmt.Fprintf(os.Stderr, "[biu] skills auto-attached: %d\n", hits)
	}

	tracker := cost.NewTracker(model)
	usageLog, _ := cost.NewLogger("") // ~/.biu/usage.jsonl

	// Plan drift verifier: wired into permissions.Context so SetPlan
	// fires automatically whenever ExitPlanMode runs (no explicit
	// per-call coordination needed).
	verifier := planverify.New()
	permCtx.SetPlanObserver(verifier.SetPlan)

	// Plan-mode auto-suggest: opt-out via [permissions]
	// suggest_plan_disabled. Custom keywords live in suggest_plan_for.
	hinter := adapters.PlanHint(planhint.New(
		!cfg.Permissions.SuggestPlanDisabled,
		cfg.Permissions.SuggestPlanFor,
	))

	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov,
		Model: model, System: system,
		Cwd: cwd,
		Permissions: permCtx, Hooks: hookReg, Cost: tracker,
		UsageLogger: usageLog,
		// Edit / Write / MultiEdit / NotebookEdit ping the LSP pool
		// so language servers see incremental updates instead of
		// stale didOpen content.
		FileChanged:        lspPool.Touch,
		PlanVerifier:       verifier,
		PlanDriftThreshold: cfg.Permissions.PlanDriftThreshold,
		PlanHinter:         hinter,
		// Background-task completion notifier: same store the Bash
		// tool writes to, so `Bash{run_in_background:true}`'s exit
		// auto-surfaces in the next user turn. Nil-safe at the
		// engine.
		BgTaskNotifier: adapters.BgTask(bgStore),
		// Swarm registries shared with the orchestration tool layer
		// above (P20.53-2). SendMessage enqueues into TeamMessages;
		// SpawnAsync's goroutine drains it between Submits.
		Teams:        teamRegistry,
		TeamMessages: teamMessages,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[biu] engine disabled: %v\n", err)
		return nil, nil, nil, nil, nil
	}

	// Interactive tools depend on engine handles (cwd switcher,
	// permission mode toggle) — register after engine.New so we can
	// pass live references rather than constructor-time copies.
	wtStore, _ := worktree.NewStore("")
	// Cron: durable store survives `biu` restarts. The fire callback
	// is a no-op here because main.go's REPL pump catches the prompt
	// directly through state — sub-process tools just log.
	cronStore, err := interactive.NewDurableCronStore(func(j interactive.CronJob) {
		fmt.Fprintf(os.Stderr, "[biu] cron fired: #%s — %s\n", j.ID, j.Prompt)
	}, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[biu] durable cron disabled: %v\n", err)
		cronStore = interactive.NewCronStore(nil)
	}

	interactive.Register(reg, interactive.Options{
		Perms:         permCtx,
		CwdSwitcher:   eng,
		Cron:          cronStore,
		Skills:        skillRegistryAdapter{R: skillReg},
		Notifier:      interactive.SystemNotifier("biu"),
		WorktreeState: worktreeAdapter{store: wtStore},
		Plans:         interactive.NewDiskPlanStore(""),
	})
	return eng, bgStore, mcpReg, trustStore, skillReg
}

// registerLayerHooks lifts a single Settings layer's hooks map into a
// shape the hook registry understands. Hooks may be either an array
// of Matcher objects or a bare object — the registry tolerates both.
func registerLayerHooks(reg *hooks.Registry, source string, s *clauseSettings.Settings) {
	if reg == nil || s == nil || len(s.Hooks) == 0 {
		return
	}
	out := make(map[string][]json.RawMessage, len(s.Hooks))
	for evt, raw := range s.Hooks {
		if len(raw) == 0 {
			continue
		}
		out[evt] = []json.RawMessage{raw}
	}
	reg.Add(source, out)
}

// worktreeAdapter bridges internal/worktree.Store into the
// interactive package's WorktreeStore interface (which intentionally
// doesn't import worktree to avoid a cycle).
type worktreeAdapter struct{ store *worktree.Store }

func (a worktreeAdapter) Save(s interactive.WorktreeState) error {
	if a.store == nil {
		return nil
	}
	return a.store.Save(worktree.State{
		SessionID: s.SessionID,
		Previous:  s.Previous,
		Current:   s.Current,
		Branch:    s.Branch,
	})
}

func (a worktreeAdapter) Delete(sessionID string) error {
	if a.store == nil {
		return nil
	}
	return a.store.Delete(sessionID)
}

// skillRegistryAdapter bridges skills.Registry into the
// interactive.SkillRegistry interface. Lives here because
// internal/skills can't import internal/tools/interactive (cycle).
type skillRegistryAdapter struct{ R *skills.Registry }

func (a skillRegistryAdapter) Lookup(name string) (interactive.Skill, bool) {
	if a.R == nil {
		return nil, false
	}
	rs, ok := a.R.Lookup(name)
	if !ok {
		return nil, false
	}
	return rs, true
}


// isSymlinkAlias reports whether `pwd` and `cwd` resolve to the same
// underlying directory inode. Used at startup to detect the case
// where the user `cd`'d through a symlink — os.Getwd() returns the
// realpath, but $PWD preserves the symlink spelling. Both spellings
// should be valid working dirs so the model + Bash see what the
// shell sees.
//
// Conservative: any error during resolution returns false (we'd
// rather skip a legit alias than silently expand the working set).
func isSymlinkAlias(pwd, cwd string) bool {
	pwdReal, err := os.Readlink(pwd)
	if err != nil || pwdReal == "" {
		// Not a symlink itself — but $PWD may still alias cwd via
		// some intermediate symlink. Stat both and compare device + inode.
		pInfo, perr := os.Stat(pwd)
		cInfo, cerr := os.Stat(cwd)
		if perr != nil || cerr != nil {
			return false
		}
		return os.SameFile(pInfo, cInfo)
	}
	// Direct symlink: pwd → pwdReal. Match if pwdReal == cwd OR
	// resolves under cwd.
	if !strings.HasPrefix(pwdReal, "/") {
		// relative symlink — cheap fallback: compare via stat
		pInfo, perr := os.Stat(pwd)
		cInfo, cerr := os.Stat(cwd)
		return perr == nil && cerr == nil && os.SameFile(pInfo, cInfo)
	}
	return pwdReal == cwd
}
