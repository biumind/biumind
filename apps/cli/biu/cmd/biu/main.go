// biu CLI entry point.
//
//	biu                            # interactive REPL
//	biu --headless --prompt "..."  # one-shot, AG-UI JSONL on stdout
//	biu doctor                     # check model-relay reachability + config
//	biu version
//
// All commands respect:
//
//	BIU_CONFIG=<path>            # alternate config file
//	BIUMIND_MODEL_RELAY_URL=<url>        # override model-relay.endpoint
//	BIUMIND_TOKEN=<jwt>          # override model-relay.virtual_key
package main

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/cmd/biu/wiring"
	"github.com/biumind/biumind/apps/cli/biu/internal/agentplane"
	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/headless"
	"github.com/biumind/biumind/apps/cli/biu/internal/memory"
	"github.com/biumind/biumind/apps/cli/biu/internal/output"
	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
	"github.com/biumind/biumind/apps/cli/biu/internal/repl"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/biumind/biumind/apps/cli/biu/internal/statusline"
	"github.com/biumind/biumind/apps/cli/biu/internal/telemetry"
	"github.com/biumind/biumind/apps/cli/biu/internal/worktree"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/exechost"
	"github.com/spf13/cobra"
)

// Build metadata. Defaults are sane for `go install`-style local builds;
// release binaries get these overwritten via -ldflags by GoReleaser:
//
//	-X main.version=0.1.0 -X main.commit=abc1234 -X main.buildDate=2026-05-25T12:00:00Z
var (
	version   = "0.1.0-dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	// Push build vars into internal/repl so /install slash can show
	// them. Done in main() (not init()) so test builds without
	// -ldflags fall back to the package-level defaults.
	repl.SetInstallInfo(version, commit, buildDate)

	// Build the telemetry reporter early — disabled by default, so the
	// fast path is a no-op. We instrument at the root level rather than
	// per-subcommand so adding a new subcommand can't accidentally lose
	// observability.
	tel, _ := telemetry.New(version, commit)
	start := time.Now()

	root := newRootCmd()
	cmd, _, _ := root.Find(os.Args[1:])
	subcommand := "(root)"
	if cmd != nil && cmd != root {
		subcommand = cmd.CommandPath()
		// Strip the leading "biu " prefix for cleaner telemetry buckets.
		subcommand = strings.TrimPrefix(subcommand, "biu ")
	}

	err := root.Execute()
	elapsed := time.Since(start)

	if tel != nil && !tel.Disabled() {
		ev := telemetry.Event{
			Subcommand: subcommand,
			DurationMs: elapsed.Milliseconds(),
			Outcome:    "ok",
		}
		if err != nil {
			ev.Outcome = "error"
			ev.ErrorClass = string(telemetry.ClassifyError(err))
			ev.ExitCode = 1
		}
		tel.Record(ev)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "biu: %v\n", err)
		os.Exit(1)
	}
}

type rootFlags struct {
	cfgPath     string
	headless    bool
	jsonOut     bool
	prompt      string
	model       string
	system      string
	relayURL    string
	runtimeURL  string // override BIUMIND_RUNTIME_URL for `biu skill` subcommands
	token       string
	noLog       bool
	mode        string   // override [default].mode: cloud | byo_endpoint | direct
	resume      string   // session id to resume (replays its event log)
	cont        bool     // --continue: auto-resume the most recent session (P20.52)
	fork        bool     // --fork-session: explicit fork-from-resume; biu default is already fork (P20.52)
	rewindFiles string   // --rewind-files <uuid>: restore files to pre-message state then exit (P20.57); requires --resume
	permPolicy  string   // headless permission policy: allow | deny | stdin
	permMode    string   // engine permission mode: default | acceptEdits | bypassPermissions
	addDirs     []string // --add-dir: extra working directories (CLI-source, ephemeral)
}

// wiringFlags packs the subset of rootFlags the wiring layer
// consumes. Lives here (not in wiring) so wiring stays independent
// of cmd/biu's private flag struct.
func (f *rootFlags) wiringFlags() wiring.Flags {
	return wiring.Flags{
		Mode:     f.mode,
		RelayURL: f.relayURL,
		Token:    f.token,
		System:   f.system,
		AddDirs:  f.addDirs,
	}
}

func newRootCmd() *cobra.Command {
	var f rootFlags

	cmd := &cobra.Command{
		Use:   "biu",
		Short: "BiuMind CLI — chat with your AI from the terminal",
		// Cobra prints its own "Error: …" + usage on RunE failure; we
		// re-print via the main() catcher with the unified `biu: …`
		// prefix. Silence cobra's so the message lands once. Usage on
		// genuine flag errors is still reachable via `biu --help`.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			cfg, _, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}
			model := firstNonEmpty(f.model, cfg.Default.Model)
			provider, mode, err := wiring.BuildProvider(cfg, f.wiringFlags())
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[biu] mode=%s provider=%s model=%s\n", mode, provider.Name(), model)

			if f.headless || f.jsonOut {
				// Engine path is preferred when it's available — the
				// SDK agent gives us tool calls + permissions + hooks.
				agent, _ := buildSDKAgent(cfg, &f, model, nil)
				return headless.Run(ctx, os.Stdout, headless.Options{
					Provider: provider,
					Model:    model, System: f.system, Prompt: f.prompt,
					JSON:  f.jsonOut,
					Agent: agent,
				})
			}

			var sess *session.Writer
			if !f.noLog {
				dir, err := config.SessionsDir()
				if err == nil {
					sess, _ = session.Open(dir, "default")
					defer sess.Close()
				}
			}

			// Engine path is opt-in for now: only direct-anthropic users
			// get the agent loop until model-relay gains tool forwarding.
			eng, bgStore, mcpReg, trustStore, skillReg := wiring.BuildEngine(cfg, f.wiringFlags(), model)

			// Wire file-snapshot capture (P20.57) once both the engine
			// and the session writer exist. SnapshotStore is keyed by
			// the session id so each session gets its own history.
			if eng != nil && sess != nil {
				if home, err := os.UserHomeDir(); err == nil {
					if snaps, err := session.NewSnapshotStore(home, sess.ID()); err == nil {
						eng.SetSnapshotCapture(snaps.Capture)
					} else {
						fmt.Fprintf(os.Stderr,
							"[biu] snapshot store unavailable: %v (--rewind-files won't work)\n",
							err)
					}
				}
			}

			// --rewind-files <uuid>: restore filesystem then exit. Must
			// come BEFORE the resume/replay block — rewind is a
			// standalone operation; we don't want to spin up the engine
			// or replay messages, just touch files and quit. (P20.57)
			if f.rewindFiles != "" {
				if f.resume == "" && !f.cont {
					return clierr.WithHint(
						clierr.Newf("--rewind-files", "requires --resume <id> or --continue"),
						"pick the session whose history holds the snapshot")
				}
				targetID := f.resume
				if targetID == "" {
					dir, _ := config.SessionsDir()
					if s, ok := session.FindLatest(dir); ok {
						targetID = s.ID
					} else {
						return clierr.Newf("--rewind-files",
							"no saved sessions to rewind into")
					}
				}
				home, err := os.UserHomeDir()
				if err != nil {
					return clierr.Wrapf("--rewind-files", err, "resolve home")
				}
				snaps, err := session.NewSnapshotStore(home, targetID)
				if err != nil {
					return clierr.Wrapf("--rewind-files", err, "open snapshot store")
				}
				ok, err := snaps.HasUUID(f.rewindFiles)
				if err != nil {
					return clierr.Wrapf("--rewind-files", err, "scan snapshots")
				}
				if !ok {
					return clierr.WithHint(
						clierr.Newf("--rewind-files", "no snapshots tagged uuid %q in session %s", f.rewindFiles, targetID),
						"the UUID must come from a user_message event in the session JSONL")
				}
				count, paths, err := snaps.Rewind(f.rewindFiles)
				if err != nil {
					return clierr.Wrapf("--rewind-files", err, "rewind")
				}
				fmt.Fprintf(os.Stderr,
					"[biu] rewound %d file(s) to state before message %s:\n", count, f.rewindFiles)
				for _, p := range paths {
					fmt.Fprintf(os.Stderr, "  %s\n", p)
				}
				return nil
			}

			// --resume / --continue: replay an existing session log into
			// the engine's state before the REPL boots so the model
			// continues with full prior context. (P20.52)
			//
			// Mutually exclusive: pick the most-specific request the
			// user expressed — `--resume <id>` wins over `--continue`,
			// which auto-picks the latest session in the current
			// project. `--fork-session` is documented as a no-op
			// because biu always forks: the resume path replays into a
			// fresh session writer (sess opened above with a new id),
			// so the original session file is never overwritten. The
			// flag exists for biu agent users with muscle memory.
			if eng != nil {
				dir, _ := config.SessionsDir()
				resolved, ok, err := resolveResumeID(dir, f.resume, f.cont, session.FindLatest)
				if err != nil {
					return err
				}
				if ok {
					f.resume = resolved
				}
			}
			if f.resume != "" && eng != nil {
				dir, _ := config.SessionsDir()
				s, ok := session.FindByID(dir, f.resume)
				if !ok {
					return clierr.WithHint(
						clierr.Newf("--resume", "no session %q", f.resume),
						"run `biu sessions list` to see saved sessions")
				}
				if err := session.Replay(s.Path, eng.State()); err != nil {
					return clierr.Wrapf("--resume", err, "replay %s",
						clierr.DisplayPath(s.Path))
				}
				// Restore worktree cwd if the session was inside one.
				// Sidecar absent / dir gone → silently fall back to
				// the launch cwd (engine.cwd already set by buildEngine).
				if wts, err := worktree.NewStore(""); err == nil {
					if st, restored, _ := wts.VerifyAndResume(f.resume); restored {
						if cwErr := eng.SetCwd(st.Current); cwErr != nil {
							// R6.4 floor 拒了越界 resume → 回落 launch cwd（已 set）。
							fmt.Fprintf(os.Stderr,
								"[biu] resume worktree %s rejected by floor, using launch cwd: %v\n",
								st.Current, cwErr)
						} else {
							fmt.Fprintf(os.Stderr,
								"[biu] resumed worktree at %s (branch %s)\n",
								st.Current, st.Branch)
						}
					}
				}
				origin := s.ID
				newID := ""
				if sess != nil {
					newID = sess.ID()
				}
				if newID != "" && newID != origin {
					fmt.Fprintf(os.Stderr,
						"[biu] resumed session %s into fork %s (%d messages replayed)\n",
						origin, newID, s.MessageCount)
				} else {
					fmt.Fprintf(os.Stderr, "[biu] resumed session %s (%d messages)\n",
						origin, s.MessageCount)
				}
			}

			// Output-style + memory views are independent of the engine
			// path — REPL renders them either way.
			cwd, _ := os.Getwd()
			styles := output.NewRegistry()
			_ = styles.Load(cwd)
			// Plugin output-styles attach onto the same registry so
			// /output-style autocomplete picks them up alongside
			// user/project files.
			plugins.LoadAll(plugins.DefaultRoots(cwd), nil).AttachOutputStyles(styles)
			mem := memory.Load(cwd)

			// Status-line user-script hook (settings.statusLine).
			// Layered settings already loaded earlier when applying
			// rules; reload here is cheap and keeps the wiring
			// localised to the REPL handoff.
			var slRunner *statusline.Runner
			if layered, err := clauseSettings.Load(cwd); err == nil {
				if cfg := layered.PreferredStatusLine(); cfg != nil {
					slRunner = statusline.New(statusline.Config{
						Command: cfg.Command,
						Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond,
					})
				}
			}

			// Fire SessionStart hooks NOW that everything's wired
			// (engine + bg-store + status-line). Defer SessionEnd
			// to after repl.Run returns so users can configure
			// "log when biu exits" hooks. Both are idempotent so
			// double-firing is safe.
			if eng != nil {
				eng.FireSessionStart()
				defer func() { _ = eng.Close() }()
			}

			// trustStore was loaded inside buildEngineIfPossible
			// alongside the hook registry's gate — we share it
			// with the REPL so /trust mutations are reflected in
			// hook firing immediately (no second load needed).

			return repl.Run(ctx, repl.Options{
				Provider: provider,
				Engine:   eng,
				Model:    model, System: f.system,
				Session:     sess,
				Styles:      styles,
				MemoryFiles: mem.Files,
				StatusLine:  slRunner,
				BgTasks:     bgStore,
				MCP:         mcpReg,
				Trust:       trustStore,
				Skills:      skillReg,
			})
		},
	}

	cmd.Flags().StringVar(&f.cfgPath, "config", "", "path to config.toml")
	cmd.Flags().BoolVar(&f.headless, "headless", false, "non-interactive single-prompt mode")
	cmd.Flags().BoolVar(&f.jsonOut, "json", false, "emit JSONL events on stdout (implies --headless)")
	cmd.Flags().StringVar(&f.prompt, "prompt", "", "prompt text (headless mode); reads stdin if empty")
	cmd.Flags().StringVar(&f.model, "model", "", "override default model")
	cmd.Flags().StringVar(&f.system, "system", "", "system prompt prefix")
	cmd.Flags().StringVar(&f.relayURL, "model-relay-url", "", "override model-relay endpoint")
	cmd.Flags().StringVar(&f.token, "token", "", "override bearer token")
	cmd.Flags().StringVar(&f.mode, "mode", "", "cloud | byo_endpoint | direct (overrides config)")
	cmd.Flags().BoolVar(&f.noLog, "no-log", false, "disable JSONL session log")
	cmd.Flags().StringVar(&f.resume, "resume", "", "session id to resume (replays its event log into the engine)")
	cmd.Flags().BoolVar(&f.cont, "continue", false, "resume the most recent session (no id required); ignored when --resume is set")
	cmd.Flags().BoolVar(&f.fork, "fork-session", false, "explicit fork from --resume; no-op flag — biu always replays into a fresh session id, the original is preserved")
	cmd.Flags().StringVar(&f.rewindFiles, "rewind-files", "",
		"restore filesystem to its state before the user message with this UUID, then exit. Requires --resume. (P20.57)")
	cmd.Flags().StringVar(&f.permPolicy, "permission-policy", "deny",
		"headless / SDK permission policy: deny (default, fail loud) | allow | stdin (interactive prompt) | stdin-json (GUI: AG-UI PERMISSION_ASK on stdout, JSON decision on stdin)")
	cmd.Flags().StringVar(&f.permMode, "permission-mode", "",
		"engine permission mode (auto-allow read-only / accept-edits / bypass): default | acceptEdits | bypassPermissions. Empty falls back to settings.json defaultMode.")
	cmd.Flags().StringSliceVar(&f.addDirs, "add-dir", nil,
		"extra working directory the model may read/write in. Repeatable; comma-separated values also supported. Equivalent to /add-dir at REPL but ephemeral (not persisted).")

	cmd.AddCommand(newDoctorCmd(&f), newVersionCmd(), newIngestCmd(&f), newPluginCmd(&f),
		newMCPCmd(&f), newBridgeCmd(&f), newAgentCmd(&f), newServeCmd(&f), newPairCmd(&f), newSessionsCmd(),
		newAuthCmd(&f), newInitCmd(&f), newUsageCmd(&f),
		newConfigCmd(&f), newPlanCmd(&f), newSkillCmd(&f),
		newAppCmd(&f))

	// Make root flags also visible on subcommands (Cobra's "persistent"
	// semantics). Recursive so 2-level subcommands like `biu mcp list`
	// also inherit (mcp itself is a parent, its children need the
	// flags too).
	var copyFlags func(parent *cobra.Command)
	copyFlags = func(parent *cobra.Command) {
		for _, sub := range parent.Commands() {
			sub.Flags().AddFlagSet(cmd.Flags())
			copyFlags(sub)
		}
	}
	copyFlags(cmd)
	return cmd
}

// ─── helpers ────────────────────────────────────────────

// resolveResumeID picks the session id the engine should replay based
// on --resume / --continue (P20.52). Precedence: explicit --resume
// wins; --continue falls back to FindLatest. Returns ("", false, nil)
// when neither flag is set so the caller can short-circuit. Returns a
// hinted error when --continue is set but no sessions exist.
func resolveResumeID(dir string, resumeFlag string, continueFlag bool, find func(dir string) (session.Summary, bool)) (string, bool, error) {
	if resumeFlag != "" {
		return resumeFlag, true, nil
	}
	if !continueFlag {
		return "", false, nil
	}
	s, ok := find(dir)
	if !ok {
		return "", false, clierr.WithHint(
			clierr.Newf("--continue", "no saved sessions in %s",
				clierr.DisplayPath(dir)),
			"start a session by running biu without --continue first")
	}
	return s.ID, true, nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// buildSDKAgent assembles an SDK agent for headless / bridge use. It
// reuses biumindkit which already wraps every Phase A-D wiring
// (tools, hooks, permissions, compact, memory). Returns nil when the
// engine path isn't available (model-relay mode, no API key, etc).
// buildSDKAgent 构造 biumindkit.Agent。
//
// permPolicyOverride 用于让 bridge mode 注入 WS-driven permission policy ——
// 当前 bridge 在 session 创建时把 askPermission 闭包通过 AgentExtras 传过来，
// 它会替换默认的 --permission-policy flag 行为。其他场景（biu chat / headless）
// 传 nil，走 policyForFlag 老逻辑。
// buildSDKAgentOpts 给 buildSDKAgent 的可变参数 —— 不影响老调用方,
// daemon worker 模式可以通过 withCwd() / withBearer() 注入 work payload
// 透传过来的 workdir / 委托 user JWT。
type buildSDKAgentOpts struct {
	cwd           string
	bearer        string // P4: work.UserBearer,非空时覆盖 cloud mode token
	execHost      exechost.Host
	allowedRoots  []string
	toolFloor     *biumindkit.ToolFloor
	priorMessages []biumindkit.Message
	// clientSide: 非空时覆盖 cloud mode —— daemon 跳 relay，用本地 key 建
	// engine 直连上游。builder 从 identity (user JWT 取 client-side key) 取好后注入。
	clientSide *clientSideCreds
}

// clientSideCreds 是 client-side BYOK 直连凭据（buildSDKAgent 内部用）。
type clientSideCreds struct {
	key      string
	baseURL  string
	protocol string // anthropic | google | openai_compat | custom
}

type buildSDKAgentOption func(*buildSDKAgentOpts)

// withExecHost 让 daemon worker 把 brain 投下来的 work.RuntimeEnvMode（轴 B）
// 翻成 biumindkit.Options.ExecHost。nil → biumindkit 默认本机 local。
// none/cloud → 内置 Bash 工具 divert（cloud 当前 stub，none 拒绝）。
func withExecHost(h exechost.Host) buildSDKAgentOption {
	return func(o *buildSDKAgentOpts) { o.execHost = h }
}

// withCwd 让 daemon worker 在 buildSDKAgent 时把 brain 投下来的 work.Workdir
// 写到 biumindkit.Options.Cwd。空字符串 → 不覆盖,fall back 到 os.Getwd()。
//
// 安全门:daemon 端 --allowed-roots flag (TODO) 应当在这之前校验 cwd 合法。
// 当前没接入,brain 让 daemon 跑任意路径在协议层是允许的;部署 daemon 的
// 用户责任(单机端;企业版需要再加 capabilities 校验)。
func withCwd(cwd string) buildSDKAgentOption {
	return func(o *buildSDKAgentOpts) { o.cwd = cwd }
}

// withAllowedRoots / withToolFloor（R6.3 / D7）把 daemon 解析出的路径根 + 能力
// 地板注入 biumindkit.Options，落到 permission Context 强制（见 sdk.go）。空
// roots / nil floor → 无地板（今天行为）。
func withAllowedRoots(roots []string) buildSDKAgentOption {
	return func(o *buildSDKAgentOpts) { o.allowedRoots = roots }
}

func withToolFloor(f *biumindkit.ToolFloor) buildSDKAgentOption {
	return func(o *buildSDKAgentOpts) { o.toolFloor = f }
}

// withPriorMessages 把 brain 服务端组装的 prior 多轮(work.History)注入
// biumindkit.Options.PriorMessages，让无状态 work 的 agent 看到对话上下文。
// 空 → 单轮(向后兼容)。Runtime v3 §8.2 翻案:历史真相源在 brain。
func withPriorMessages(msgs []biumindkit.Message) buildSDKAgentOption {
	return func(o *buildSDKAgentOpts) { o.priorMessages = msgs }
}

// priorMessagesFromHistory 把 WorkPayload.History(brain 服务端组装的文本级
// prior 多轮)转成 biumindkit.Message[]。P1 只有 user/assistant 文本轮(工具
// 轮保真留 P2);非法/空轮跳过。
func priorMessagesFromHistory(history []agentplane.ChatTurn) []biumindkit.Message {
	if len(history) == 0 {
		return nil
	}
	out := make([]biumindkit.Message, 0, len(history))
	for _, t := range history {
		if t.Content == "" || (t.Role != "user" && t.Role != "assistant") {
			continue
		}
		out = append(out, biumindkit.Message{
			Role:    t.Role,
			Content: []biumindkit.ContentBlock{{Type: biumindkit.ContentText, Text: t.Content}},
		})
	}
	return out
}

// withBearer 让 daemon worker 把 brain 投下来的 work.UserBearer（委托 user JWT,
// P4）优先于本地 BIUMIND_TOKEN/PAT 作为 model-relay 的 Authorization。非空 →
// relay 拿 claims.UserID 原生解析该 user 的 BYOK（与 chat 路径同构）。
// 空（离线重派 / 未带 JWT）→ 落老路径（BIUMIND_TOKEN/PAT 走平台池）。
func withBearer(bearer string) buildSDKAgentOption {
	return func(o *buildSDKAgentOpts) { o.bearer = bearer }
}

// withClientSide: daemon builder 从 identity 取出 client-side 凭据 (明文 key) 后注入。
// key 空 → no-op (record 不存在 / 已 revoke → buildSDKAgent 走 relay fallback)。
// 非空 → buildSDKAgent 跳 relay，用本地 key 建 engine 直连上游。
func withClientSide(key, baseURL, protocol string) buildSDKAgentOption {
	return func(o *buildSDKAgentOpts) {
		if key == "" {
			return
		}
		o.clientSide = &clientSideCreds{key: key, baseURL: baseURL, protocol: protocol}
	}
}

func buildSDKAgent(cfg *config.Config, f *rootFlags, model string, permPolicyOverride biumindkit.PermissionPolicyFn, opts ...buildSDKAgentOption) (*biumindkit.Agent, error) {
	mode := firstNonEmpty(f.mode, cfg.Default.Mode, string(client.ModeCloud))
	var o buildSDKAgentOpts
	for _, opt := range opts {
		opt(&o)
	}
	cwd := o.cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	policy := permPolicyOverride
	var policyExt biumindkit.PermissionPolicyExtFn
	if policy == nil {
		policy = policyForFlag(f.permPolicy)
		// stdin-json gets the suggestion-aware variant on top so the
		// GUI client can pick "Allow + add dir" without a round-trip.
		// Other policies stay basic — they don't see suggestions.
		if f.permPolicy == "stdin-json" {
			policyExt = stdinJSONPermissionPolicyExt
		}
	}

	switch client.Mode(mode) {
	case client.ModeDirect:
		// Direct mode: local Anthropic key required, talks to api.anthropic.com.
		providerName := firstNonEmpty(cfg.Default.Provider, "anthropic")
		ps, ok := cfg.Providers[providerName]
		if !ok || ps.APIKey == "" {
			return nil, clierr.WithHint(
				clierr.Newf("sdk", "missing [providers.%s].api_key", providerName),
				"add the api_key to ~/.biu/config.toml or run `biu init --mode=direct`")
		}
		opts := biumindkit.Options{
			Model:               model,
			System:              f.system,
			Cwd:                 cwd,
			PermissionPolicy:    policy,
			PermissionPolicyExt: policyExt,
			PermissionMode:      f.permMode,
			// P20.47x: bootstrap MCP servers from cfg.MCPServers and
			// hand the registry to the SDK so headless / SDK callers
			// reach parity with the REPL path. Trust gate stays nil —
			// the SDK doesn't own a trust store; users opting out of
			// trust live with hooks-on-everywhere semantics.
			MCPRegistry:   biumindkit.NewMCPRegistry(wiring.BootstrapMCP(cfg, nil)),
			ExecHost:      o.execHost,
			AllowedRoots:  o.allowedRoots,
			ToolFloor:     o.toolFloor,
			PriorMessages: o.priorMessages,
		}
		switch providerName {
		case "anthropic":
			opts.APIKey = ps.APIKey
			opts.AnthropicEndpoint = ps.Endpoint
		default:
			// 非 anthropic provider: 注入 OpenAI 兼容 engine
			// (biumindkit.New 优先用 Options.Provider)
			opts.Provider = client.NewOpenAIEngine(ps.APIKey, ps.Endpoint)
		}
		return biumindkit.New(opts)

	case client.ModeCloud:
		// B2: client-side BYOK 命中 → daemon 用本地 key 直连上游，跳 relay。
		// key 经 loopback 注入 daemon 内存（不经 brain/NATS），key 不离设备。
		// Google/openai_compat/custom 走 OpenAI-compat（与 model-relay
		// byokAdaptorName 一致），anthropic 走原生 AnthropicEngine。
		if o.clientSide != nil {
			csOpts := biumindkit.Options{
				Model:               model,
				System:              f.system,
				Cwd:                 cwd,
				PermissionPolicy:    policy,
				PermissionPolicyExt: policyExt,
				PermissionMode:      f.permMode,
				MCPRegistry:         biumindkit.NewMCPRegistry(wiring.BootstrapMCP(cfg, nil)),
				ExecHost:            o.execHost,
				AllowedRoots:        o.allowedRoots,
				ToolFloor:           o.toolFloor,
				PriorMessages:       o.priorMessages,
			}
			if o.clientSide.protocol == "anthropic" {
				csOpts.Provider = client.NewAnthropicEngine(o.clientSide.key, o.clientSide.baseURL)
			} else {
				csOpts.Provider = client.NewOpenAIEngine(o.clientSide.key, o.clientSide.baseURL)
			}
			return biumindkit.New(csOpts)
		}
		// Cloud mode: route LLM through BiuMind model-relay /v1/messages with
		// the user's bearer token. No local Anthropic key needed —
		// quota / billing / audit happens server-side. Tool calls /
		// permissions still run in this process so the SDK can prompt
		// the user (via PermissionPolicy) before destructive ops.
		relayURL := firstNonEmpty(f.relayURL, os.Getenv("BIUMIND_MODEL_RELAY_URL"), cfg.Relay.Endpoint)
		// P4: daemon worker 优先用 brain 投下来的委托 user JWT (work.UserBearer) —
		// relay 拿 claims.UserID 原生解析 BYOK。空 → 回退本地 BIUMIND_TOKEN/PAT
		// (平台池)。非 daemon 调用 (REPL/headless) o.bearer 恒空, 走老路径。
		token := firstNonEmpty(o.bearer, f.token, os.Getenv("BIUMIND_TOKEN"), cfg.Relay.VirtualKey)
		if relayURL == "" || token == "" {
			return nil, clierr.WithHint(
				clierr.Newf("sdk", "cloud mode SDK requires model-relay URL + bearer token"),
				"set BIUMIND_MODEL_RELAY_URL + BIUMIND_TOKEN env vars, or pass --model-relay-url + --token")
		}
		return biumindkit.New(biumindkit.Options{
			APIKey:              token,
			AnthropicEndpoint:   relayURL,
			UseRelayAuth:        true,
			Model:               model,
			System:              f.system,
			Cwd:                 cwd,
			PermissionPolicy:    policy,
			PermissionPolicyExt: policyExt,
			PermissionMode:      f.permMode,
			// P20.47x: same MCP bootstrap as Direct mode so cloud-mode
			// SDK / headless callers also see [[mcp_servers]].
			MCPRegistry:   biumindkit.NewMCPRegistry(wiring.BootstrapMCP(cfg, nil)),
			ExecHost:      o.execHost,
			AllowedRoots:  o.allowedRoots,
			ToolFloor:     o.toolFloor,
			PriorMessages: o.priorMessages,
		})

	default:
		// byo_endpoint not supported — its raw OpenAI / LiteLLM proxies
		// don't speak the Anthropic Messages API the SDK relies on.
		return nil, clierr.WithHint(
			clierr.Newf("sdk", "engine path does not support mode=%s", mode),
			"use --mode=cloud (model-relay) or --mode=direct (local Anthropic key)")
	}
}

// policyForFlag maps the --permission-policy CLI flag to one of the
// SDK's canned policy callbacks. Default: deny so unattended runs
// fail loud rather than hang.
func policyForFlag(flag string) biumindkit.PermissionPolicyFn {
	switch flag {
	case "allow":
		return biumindkit.PermissionAllow()
	case "stdin":
		return stdinPermissionPolicy
	case "stdin-json":
		return stdinJSONPermissionPolicy
	default: // "" or "deny"
		return biumindkit.PermissionDeny()
	}
}

// stdinJSONPermissionPolicy is the GUI-facing policy. On each ask it
// emits an AG-UI envelope (`type:"PERMISSION_ASK"`) to stdout and
// blocks reading one JSON line from stdin shaped like
// `{"tool_use_id":"...","decision":"allow"|"deny"|"always"}`.
// Used by `biu --headless --json --permission-policy=stdin-json`.
//
// Caller (Flutter biu_adapter) must keep stdin open and write decisions
// promptly — the agent run blocks until each answer arrives. Decisions
// for an unknown tool_use_id are ignored (defensive; the engine asks
// one at a time in current implementation).
//
// Reads serialised under stdinJSONMu so out-of-order asks don't
// interleave (future engine concurrency).
var stdinJSONMu sync.Mutex
var stdinJSONReader = bufio.NewReader(os.Stdin)

// stdinJSONPermissionPolicyExt is the suggestion-aware sibling of
// stdinJSONPermissionPolicy. Emits a richer PERMISSION_ASK envelope
// that includes the runner's pre-computed shortcut suggestions
// (e.g. "Allow + add scratch/ to working dirs"), and parses an
// optional `selected_suggestion` integer from the reply line.
//
// AG-UI envelope shape (forward-compatible — the basic policy still
// works for clients that ignore `suggestions`):
//
//	stdout (PERMISSION_ASK):
//	  {
//	    "type": "PERMISSION_ASK",
//	    "tool_use_id": "...",
//	    "name": "...",
//	    "input": {...},
//	    "reason": "...",
//	    "suggestions": [
//	      {"label": "Allow + add scratch/ to working dirs",
//	       "hot_key": "w",
//	       "kind": "addDirectories"}
//	    ]
//	  }
//	stdin (response):
//	  {
//	    "tool_use_id": "...",
//	    "decision": "allow"|"deny"|"always",
//	    "selected_suggestion": 0  // optional; 0-indexed, omit for none
//	  }
//
// When decision=allow + selected_suggestion is in range, the agent
// applies that suggestion's update before resuming. selected_suggestion
// is silently ignored on deny/always to avoid surprising side-effects.
func stdinJSONPermissionPolicyExt(ctx context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionResponse {
	// Build suggestion array for the envelope. Empty when the runner
	// produced none; clients see suggestions:[] and behave as before.
	sgList := make([]map[string]any, 0, len(req.Suggestions))
	for _, s := range req.Suggestions {
		sgList = append(sgList, map[string]any{
			"label":   s.Label,
			"hot_key": s.HotKey,
			"kind":    s.Kind,
		})
	}
	envelope := map[string]any{
		"id":          newAGUIID(),
		"type":        "PERMISSION_ASK",
		"ts":          time.Now().UTC().Format(time.RFC3339Nano),
		"tool_use_id": req.ToolUseID,
		"name":        req.ToolName,
		"input":       req.Input,
		"reason":      req.Reason,
		"suggestions": sgList,
	}
	if buf, err := json.Marshal(envelope); err == nil {
		_, _ = os.Stdout.Write(buf)
		_, _ = os.Stdout.Write([]byte{'\n'})
	}

	stdinJSONMu.Lock()
	defer stdinJSONMu.Unlock()
	for {
		line, err := stdinJSONReader.ReadString('\n')
		if err != nil {
			return biumindkit.PermissionResponse{
				Decision:           biumindkit.PermDeny,
				SelectedSuggestion: -1,
			}
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg struct {
			ToolUseID          string `json:"tool_use_id"`
			Decision           string `json:"decision"`
			SelectedSuggestion *int   `json:"selected_suggestion,omitempty"`
		}
		if jerr := json.Unmarshal([]byte(line), &msg); jerr != nil {
			continue
		}
		if msg.ToolUseID != "" && msg.ToolUseID != req.ToolUseID {
			continue
		}
		sel := -1
		if msg.SelectedSuggestion != nil {
			sel = *msg.SelectedSuggestion
		}
		switch strings.ToLower(msg.Decision) {
		case "allow", "allow_once":
			return biumindkit.PermissionResponse{
				Decision:           biumindkit.PermAllow,
				SelectedSuggestion: sel,
			}
		case "always", "allow_always":
			return biumindkit.PermissionResponse{
				Decision:           biumindkit.PermAlways,
				SelectedSuggestion: -1, // ignored on always
			}
		default:
			return biumindkit.PermissionResponse{
				Decision:           biumindkit.PermDeny,
				SelectedSuggestion: -1,
			}
		}
	}
}

func stdinJSONPermissionPolicy(ctx context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
	// Emit AG-UI PERMISSION_ASK envelope. Shape mirrors headless.emit().
	envelope := map[string]any{
		"id":          newAGUIID(),
		"type":        "PERMISSION_ASK",
		"ts":          time.Now().UTC().Format(time.RFC3339Nano),
		"tool_use_id": req.ToolUseID,
		"name":        req.ToolName,
		"input":       req.Input,
		"reason":      req.Reason,
	}
	if buf, err := json.Marshal(envelope); err == nil {
		_, _ = os.Stdout.Write(buf)
		_, _ = os.Stdout.Write([]byte{'\n'})
	}

	stdinJSONMu.Lock()
	defer stdinJSONMu.Unlock()
	for {
		line, err := stdinJSONReader.ReadString('\n')
		if err != nil {
			return biumindkit.PermDeny // EOF / pipe closed → fail closed
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg struct {
			ToolUseID string `json:"tool_use_id"`
			Decision  string `json:"decision"`
		}
		if jerr := json.Unmarshal([]byte(line), &msg); jerr != nil {
			continue // skip malformed
		}
		if msg.ToolUseID != "" && msg.ToolUseID != req.ToolUseID {
			continue // for someone else; in practice serial so unlikely
		}
		switch strings.ToLower(msg.Decision) {
		case "allow", "allow_once":
			return biumindkit.PermAllow
		case "always", "allow_always":
			return biumindkit.PermAlways
		default:
			return biumindkit.PermDeny
		}
	}
}

func newAGUIID() string {
	b := make([]byte, 12)
	_, _ = cryptorand.Read(b)
	return hex.EncodeToString(b)
}

// stdinPermissionPolicy reads one line from stdin per ask. Format:
// `a` / `allow` = allow once, `s` / `always` = remember + allow,
// anything else (incl. EOF) = deny. Output goes to stderr so JSONL
// stays clean. Used by `biu --headless --permission-policy=stdin`.
func stdinPermissionPolicy(ctx context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
	fmt.Fprintf(os.Stderr,
		"\n[biu] permission required: %s — %s\n  input: %v\n  [a]llow once · [s]hift+a always · [d]eny: ",
		req.ToolName, req.Reason, req.Input)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return biumindkit.PermDeny
	}
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "a", "allow":
		return biumindkit.PermAllow
	case "s", "always", "shift+a":
		return biumindkit.PermAlways
	}
	return biumindkit.PermDeny
}

func min4(n int) int {
	if n < 4 {
		return n
	}
	return 4
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
