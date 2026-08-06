// Package settings loads + merges biu's settings.json files.
//
// Three layers, lower-precedence first:
//
//   user      ~/.biumind/settings.json                    (global preferences)
//   project   <repo>/.biumind/settings.json               (team-shared, in git)
//   local     <repo>/.biumind/settings.local.json         (per-machine, gitignored)
//
// Only `.biumind/` is read. The TOML config (~/.biu/config.toml)
// keeps owning model-relay URL + providers + biu-specific things; this
// package is purely for the permissions / hooks / memory triad.
//
// Schema (fields we care about for Phase B):
//
//   {
//     "permissions": {
//       "allow":  ["Bash(npm install)", "Edit(/repo/**)"],
//       "deny":   ["Bash(rm -rf /)"],
//       "ask":    ["Bash(git push:*)"],
//       "defaultMode": "default" | "acceptEdits" | "plan" | "bypassPermissions"
//     },
//     "hooks": { "PreToolUse": [...], "PostToolUse": [...], ... },
//     "model":  "claude-sonnet-4-6",
//     "env":    { "API_HOST": "..." }
//   }
//
// Hooks are loaded as opaque JSON in this package; the hooks/ package
// (Phase B W5) interprets them.

package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

// Settings is one parsed settings.json file. Pointer types are used so
// "absent" and "empty array" can be distinguished — needed for the
// merge layer to know whether to overwrite or skip.
type Settings struct {
	Permissions *PermissionsBlock          `json:"permissions,omitempty"`
	Hooks       map[string]json.RawMessage `json:"hooks,omitempty"`
	Model       string                     `json:"model,omitempty"`
	Env         map[string]string          `json:"env,omitempty"`

	// ClaudeMdExcludes are glob patterns the memory loader uses to
	// skip large / generated trees. The setting key is kept verbatim
	// for cross-tool config portability.
	ClaudeMdExcludes []string `json:"claudeMdExcludes,omitempty"`

	// StatusLine plugs a user shell command into the REPL status
	// bar's right cluster. The command receives a small JSON
	// payload describing the session on stdin; first line of stdout
	// becomes a status segment. Errors / non-zero exits silently
	// degrade to no segment.
	StatusLine *StatusLineCommand `json:"statusLine,omitempty"`

	// Sandbox configures the BashTool's filesystem allow/deny lists
	// — the layered surface biu's sandbox.Wrap consumes. See
	// SandboxSection's docs + cmd/biu/wiring/wiring.go for how
	// these flow through to a BashTool instance.
	Sandbox *SandboxSection `json:"sandbox,omitempty"`

	// Plugins controls which plugins (loaded by internal/plugins)
	// are active for this session. The PluginsBlock shape lets a
	// user porting from Claude Code keep their disable list working
	// unchanged. nil = "all discovered plugins enabled".
	Plugins *PluginsBlock `json:"plugins,omitempty"`

	// Plus an opaque catch-all so unknown settings keys (skills,
	// outputStyle, etc.) survive a round-trip without us having to
	// model every field in this package.
	Other map[string]json.RawMessage `json:"-"`
}

// StatusLineCommand describes a shell command biu invokes to render
// a custom status segment. The wire shape lets users porting from
// Claude Code reuse their config verbatim.
type StatusLineCommand struct {
	// Type must be "command" today. Reserved for future shapes
	// (e.g. "static" / "lua") — unrecognised values are ignored.
	Type string `json:"type,omitempty"`
	// Command is the shell snippet executed via /bin/sh -c. Receives
	// the StatusLineInput JSON on stdin.
	Command string `json:"command,omitempty"`
	// TimeoutMs caps execution. 0 → use the package default (5 s).
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// SandboxSection is the `sandbox` block of settings.json. Mirrors
// the four list fields BashTool / sandbox.Options expose:
//
//	fsReadDeny             — paths the sandbox blocks reads to
//	                          (e.g. ~/.ssh, ~/.aws). Append-only
//	                          across layers — project / local can
//	                          ADD entries but cannot remove user-
//	                          layer denies.
//	fsReadAllowWithinDeny  — re-allow inside an fsReadDeny entry
//	                          (e.g. block ~/.aws but allow
//	                          ~/.aws/config).
//	fsWriteAllowExtra      — extra writable roots beyond cwd.
//	fsWriteDenyWithinAllow — surgical denies inside an
//	                          fsWriteAllowExtra root.
//
// Path values may use "~" (home), "${VAR}" (env), and
// "${PROJECT_ROOT}" (current cwd) — the merger expands these
// before handing the slices to the sandbox layer.
//
// The wire shape exposes the fsRead / fsWrite config surface so a
// future cross-tool migration helper can map fields 1-to-1 without
// renaming.
type SandboxSection struct {
	FSReadDeny             []string `json:"fsReadDeny,omitempty"`
	FSReadAllowWithinDeny  []string `json:"fsReadAllowWithinDeny,omitempty"`
	FSWriteAllowExtra      []string `json:"fsWriteAllowExtra,omitempty"`
	FSWriteDenyWithinAllow []string `json:"fsWriteDenyWithinAllow,omitempty"`
}

// SandboxConfig is the merged result handed to BashTool. Same
// shape as SandboxSection but every path is fully expanded
// (absolute, ~ resolved, ${VAR} substituted) so the sandbox layer
// doesn't have to do path arithmetic at command time.
type SandboxConfig struct {
	FSReadDeny             []string
	FSReadAllowWithinDeny  []string
	FSWriteAllowExtra      []string
	FSWriteDenyWithinAllow []string
}

// PluginsBlock is the `plugins` section. Two surfaces:
//
//	disabled — names of plugins to NOT activate, even when present
//	           in a search root. Plugin authors / users can use
//	           this to pin a known-good set without uninstalling
//	           the broken one.
//	configs  — per-plugin user overrides forwarded to plugin hook
//	           handlers via env (BIU_PLUGIN_CONFIG_<name>) and to
//	           the manifest's `settings` defaults at attach time.
//	           Currently opaque; future PRs can grow per-plugin
//	           userConfig schemas.
//
// Layer semantics: union (project + local can disable additional
// plugins on top of user; cannot RE-enable what user disabled).
// Symmetric with sandbox: lower layers tighten, never relax.
type PluginsBlock struct {
	Disabled []string                  `json:"disabled,omitempty"`
	Configs  map[string]map[string]any `json:"configs,omitempty"`
}

// PermissionsBlock is the `permissions` section.
type PermissionsBlock struct {
	Allow                 []string `json:"allow,omitempty"`
	Deny                  []string `json:"deny,omitempty"`
	Ask                   []string `json:"ask,omitempty"`
	DefaultMode           string   `json:"defaultMode,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

// Layered holds the three discrete files that contributed rules so we
// can attribute decisions back to user / project / local sources.
type Layered struct {
	User    *Settings // ~/.biumind/settings.json
	Project *Settings // <cwd>/.biumind/settings.json
	Local   *Settings // <cwd>/.biumind/settings.local.json

	// Resolved file paths actually loaded; useful for error messages.
	UserPath, ProjectPath, LocalPath string
}

// Load reads all three layers from disk. Missing files are not
// errors. The returned Layered is always non-nil; individual layers
// may be nil if their file was absent / unreadable.
//
// `cwd` is the project root. Pass os.Getwd() in normal operation. Tests
// typically pass a tempdir.
func Load(cwd string) (*Layered, error) {
	out := &Layered{}

	// User layer.
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".biumind", "settings.json")
		s, err := readFile(path)
		if err != nil {
			return nil, fmt.Errorf("settings: read user: %w", err)
		}
		if s != nil {
			out.User = s
			out.UserPath = path
		}
	}

	if cwd == "" {
		// No project context — only user settings apply.
		return out, nil
	}

	// Project layer.
	projectPath := filepath.Join(cwd, ".biumind", "settings.json")
	if s, err := readFile(projectPath); err != nil {
		return nil, fmt.Errorf("settings: read project: %w", err)
	} else if s != nil {
		out.Project = s
		out.ProjectPath = projectPath
	}

	// Local layer.
	localPath := filepath.Join(cwd, ".biumind", "settings.local.json")
	if s, err := readFile(localPath); err != nil {
		return nil, fmt.Errorf("settings: read local: %w", err)
	} else if s != nil {
		out.Local = s
		out.LocalPath = localPath
	}

	return out, nil
}

// readFile parses settings.json at p. Returns (nil, nil) when the file
// doesn't exist; (nil, err) on read or parse error. Empty file →
// empty Settings (so callers can rely on non-nil when the file is
// present but blank).
func readFile(p string) (*Settings, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if len(raw) == 0 {
		return &Settings{}, nil
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &s, nil
}

// ApplyToContext folds the three layers into a permissions.Context so
// the engine can decide tool calls against the merged rule set. Source
// attribution is preserved (each rule in the Context carries the layer
// it came from).
//
// Rule precedence is determined by Decide()'s ordering, NOT by who
// wrote them — i.e. a deny rule in user settings still beats an allow
// rule in local settings. The source label is for UI / error messages.
//
// Returns the active Mode resolved by walking layers high-to-low
// (local > project > user > built-in default). Caller can override
// post-load with ctx.SetMode(...) for CLI flags.
func (l *Layered) ApplyToContext(ctx *permissions.Context) permissions.Mode {
	if l == nil || ctx == nil {
		return permissions.ModeDefault
	}

	// projectRoot is the directory the project / local layer's
	// .biumind/ lives under. Inferred from ProjectPath when present
	// (.biumind/settings.json → cwd), else LocalPath, else "" so
	// expandPath leaves ${PROJECT_ROOT} literal.
	projectRoot := ""
	switch {
	case l.ProjectPath != "":
		projectRoot = filepath.Dir(filepath.Dir(l.ProjectPath))
	case l.LocalPath != "":
		projectRoot = filepath.Dir(filepath.Dir(l.LocalPath))
	}

	add := func(src permissions.Source, p *PermissionsBlock) {
		if p == nil {
			return
		}
		if len(p.Allow) > 0 {
			ctx.AddRules(src, permissions.BehaviorAllow, p.Allow)
		}
		if len(p.Deny) > 0 {
			ctx.AddRules(src, permissions.BehaviorDeny, p.Deny)
		}
		if len(p.Ask) > 0 {
			ctx.AddRules(src, permissions.BehaviorAsk, p.Ask)
		}
		// Pull configured additionalDirectories into the runtime
		// context. Each entry runs through ~ expansion +
		// ${PROJECT_ROOT} substitution + Abs so downstream consumers
		// (sandbox allowWrite, working-dir gate) compare against the
		// canonical absolute path. Invalid entries are silently
		// dropped here — callers wanting validation feedback should
		// use ValidationWarnings further below.
		if len(p.AdditionalDirectories) > 0 {
			canon := canonicalizeDirs(p.AdditionalDirectories, projectRoot)
			if len(canon) > 0 {
				ctx.AddDirectories(src, canon)
			}
		}
	}
	if l.User != nil {
		add(permissions.SrcUserSettings, l.User.Permissions)
	}
	if l.Project != nil {
		add(permissions.SrcProjectSettings, l.Project.Permissions)
	}
	if l.Local != nil {
		add(permissions.SrcLocalSettings, l.Local.Permissions)
	}

	// Mode resolution: local wins, then project, then user.
	mode := permissions.ModeDefault
	for _, layer := range []*Settings{l.User, l.Project, l.Local} {
		if layer == nil || layer.Permissions == nil {
			continue
		}
		if dm := layer.Permissions.DefaultMode; dm != "" {
			mode = permissions.ModeFromString(dm)
		}
	}
	ctx.SetMode(mode)
	return mode
}

// MergedHooks returns the union of every layer's hooks. Layers
// concatenate, so a PreToolUse hook in user + project + local all run.
// Caller (hooks package, Phase B W5) decodes the per-event arrays.
// ValidationWarnings returns a slice of human-readable warnings about
// settings entries that are accepted but suspect. Caller (wiring)
// emits each on stderr at startup.
//
// Currently checks:
//
//   - permissions.additionalDirectories: relative path, missing dir,
//     duplicate within same layer.
//
// Posture: warn but don't fail — settings.json may live in a repo
// where some entries don't apply to the current host (e.g.
// /mnt/scratch on a teammate's machine).
func (l *Layered) ValidationWarnings(projectRoot string) []string {
	if l == nil {
		return nil
	}
	var warns []string
	check := func(label, root string, p *PermissionsBlock) {
		if p == nil || len(p.AdditionalDirectories) == 0 {
			return
		}
		seen := map[string]int{}
		for _, raw := range p.AdditionalDirectories {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				warns = append(warns, fmt.Sprintf(
					"%s: permissions.additionalDirectories has an empty entry", label))
				continue
			}
			expanded := expandPath(trimmed, root)
			if expanded == "" || !filepath.IsAbs(expanded) {
				warns = append(warns, fmt.Sprintf(
					"%s: permissions.additionalDirectories[%q] is not absolute (resolves to %q)",
					label, raw, expanded))
				continue
			}
			seen[expanded]++
			if seen[expanded] == 2 {
				warns = append(warns, fmt.Sprintf(
					"%s: permissions.additionalDirectories has %q listed more than once",
					label, raw))
			}
			if info, err := os.Stat(expanded); err != nil {
				warns = append(warns, fmt.Sprintf(
					"%s: permissions.additionalDirectories[%q] not accessible: %v",
					label, raw, err))
			} else if !info.IsDir() {
				warns = append(warns, fmt.Sprintf(
					"%s: permissions.additionalDirectories[%q] is not a directory",
					label, raw))
			}
		}
	}
	if l.User != nil {
		check("~/.biumind/settings.json", "", l.User.Permissions)
	}
	if l.Project != nil {
		check(".biumind/settings.json", projectRoot, l.Project.Permissions)
	}
	if l.Local != nil {
		check(".biumind/settings.local.json", projectRoot, l.Local.Permissions)
	}
	return warns
}

func (l *Layered) MergedHooks() map[string][]json.RawMessage {
	out := map[string][]json.RawMessage{}
	for _, layer := range []*Settings{l.User, l.Project, l.Local} {
		if layer == nil {
			continue
		}
		for evt, raw := range layer.Hooks {
			if len(raw) == 0 {
				continue
			}
			// Each layer may be a single hook object OR an array; we
			// always normalize to []json.RawMessage. Defer parsing.
			var arr []json.RawMessage
			if err := json.Unmarshal(raw, &arr); err == nil {
				out[evt] = append(out[evt], arr...)
				continue
			}
			// Single object form.
			out[evt] = append(out[evt], raw)
		}
	}
	return out
}

// MergedClaudeMdExcludes concatenates exclude patterns from every
// layer, deduplicated. Order: user → project → local (later wins
// for dedup, but order matters for telemetry).
func (l *Layered) MergedClaudeMdExcludes() []string {
	if l == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, layer := range []*Settings{l.User, l.Project, l.Local} {
		if layer == nil {
			continue
		}
		for _, p := range layer.ClaudeMdExcludes {
			if seen[p] || p == "" {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// MergedDisabledPlugins is the union of every layer's
// plugins.disabled list, deduplicated. Layer-union (no removal)
// matches sandbox: a project / local file can ADD names but cannot
// silently re-enable what user disabled.
//
// nil-safe: returns nil for a nil receiver, an empty slice when no
// layer disabled anything. Callers (wiring) feed the result
// straight into plugins.LoadAll(roots, disabled).
func (l *Layered) MergedDisabledPlugins() []string {
	if l == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, layer := range []*Settings{l.User, l.Project, l.Local} {
		if layer == nil || layer.Plugins == nil {
			continue
		}
		for _, name := range layer.Plugins.Disabled {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// MergedPluginConfig returns the user-supplied config for one plugin
// merged across layers (later layer keys win, matching MergedEnv).
// Returns nil when no layer supplied a config block — callers
// distinguish "no config" from "empty config" via this nil result.
func (l *Layered) MergedPluginConfig(pluginName string) map[string]any {
	if l == nil || pluginName == "" {
		return nil
	}
	out := map[string]any{}
	any := false
	for _, layer := range []*Settings{l.User, l.Project, l.Local} {
		if layer == nil || layer.Plugins == nil {
			continue
		}
		cfg, ok := layer.Plugins.Configs[pluginName]
		if !ok {
			continue
		}
		any = true
		for k, v := range cfg {
			out[k] = v
		}
	}
	if !any {
		return nil
	}
	return out
}

// MergedEnv returns the union of env maps; later layers win on key
// conflicts.
func (l *Layered) MergedEnv() map[string]string {
	out := map[string]string{}
	for _, layer := range []*Settings{l.User, l.Project, l.Local} {
		if layer == nil {
			continue
		}
		for k, v := range layer.Env {
			out[k] = v
		}
	}
	return out
}

// MergedSandboxConfig unions every layer's sandbox section into a
// single SandboxConfig with paths fully expanded.
//
// The merge is intentionally union-only on every list — project
// and local settings can ADD entries to user's lists but cannot
// remove or override them. This preserves the security baseline:
// a malicious project's `.biumind/settings.json` can't ship
// `fsReadDeny: []` to silently undo a user's "block ~/.ssh" rule.
//
// Path expansion runs per entry:
//   - leading "~"           → user home (skipped when no home)
//   - "${VAR}"              → env value (empty if unset)
//   - "${PROJECT_ROOT}"     → projectRoot arg, or skipped when empty
//   - relative paths        → dropped (sandbox.absOnly will drop them
//     again, but better to surface here)
//
// Duplicates after expansion are deduplicated; order is preserved
// (user → project → local) so the sandbox profile reads top-down
// in the order users configured.
func (l *Layered) MergedSandboxConfig(projectRoot string) *SandboxConfig {
	if l == nil {
		return nil
	}
	any := false
	out := &SandboxConfig{}
	for _, layer := range []*Settings{l.User, l.Project, l.Local} {
		if layer == nil || layer.Sandbox == nil {
			continue
		}
		s := layer.Sandbox
		out.FSReadDeny = appendExpanded(out.FSReadDeny, s.FSReadDeny, projectRoot)
		out.FSReadAllowWithinDeny = appendExpanded(out.FSReadAllowWithinDeny, s.FSReadAllowWithinDeny, projectRoot)
		out.FSWriteAllowExtra = appendExpanded(out.FSWriteAllowExtra, s.FSWriteAllowExtra, projectRoot)
		out.FSWriteDenyWithinAllow = appendExpanded(out.FSWriteDenyWithinAllow, s.FSWriteDenyWithinAllow, projectRoot)
		any = true
	}
	if !any {
		return nil
	}
	return out
}

// appendExpanded runs path expansion + dedup over `extra` and
// returns the new slice. Drops empty / unresolvable entries
// silently — a half-misconfigured rule yields a partial sandbox
// rather than a panic.
func appendExpanded(into, extra []string, projectRoot string) []string {
	seen := map[string]bool{}
	for _, p := range into {
		seen[p] = true
	}
	for _, raw := range extra {
		expanded := expandPath(raw, projectRoot)
		if expanded == "" {
			continue
		}
		if seen[expanded] {
			continue
		}
		seen[expanded] = true
		into = append(into, expanded)
	}
	return into
}

// canonicalizeDirs runs the input through expandPath, drops blanks,
// and absolutises any survivors. Used by ApplyToContext when pumping
// settings.permissions.additionalDirectories into a Context — keeps
// the in-memory representation comparable across all four ingestion
// sources (settings file, --add-dir CLI flag, /add-dir slash, SDK
// PermissionUpdate).
//
// projectRoot is the base for ${PROJECT_ROOT} substitution and
// relative-path resolution. Pass "" for the user layer (no project
// context); ApplyToContext infers it from Layered.ProjectPath when
// possible.
//
// The return is always absolute (or empty, dropped). Errors are
// silently swallowed — validation warnings happen elsewhere.
func canonicalizeDirs(raw []string, projectRoot string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, p := range raw {
		expanded := expandPath(p, projectRoot)
		if expanded == "" {
			continue
		}
		if !filepath.IsAbs(expanded) {
			// Relative leftover (no projectRoot, no ~). Try cwd as a
			// last resort so settings written without ${PROJECT_ROOT}
			// still work for the common "user runs biu in repo" case.
			if abs, err := filepath.Abs(expanded); err == nil {
				expanded = abs
			} else {
				continue
			}
		}
		expanded = filepath.Clean(expanded)
		if seen[expanded] {
			continue
		}
		seen[expanded] = true
		out = append(out, expanded)
	}
	return out
}

// expandPath resolves "~", "${VAR}", and "${PROJECT_ROOT}" tokens
// in a path. Returns "" when the result would be relative or the
// path is empty after trimming — those cases are unsafe to feed to
// the sandbox layer (sandbox.absOnly drops them anyway, but we
// filter early so the rendered config matches what's enforced).
//
// Order of operations:
//  1. trim
//  2. ${PROJECT_ROOT} → projectRoot   (custom token, expanded first
//     so a user-set $PROJECT_ROOT
//     env var doesn't override it)
//  3. ${VAR} / $VAR → os.Expand
//  4. leading ~ → home dir
func expandPath(raw, projectRoot string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	if projectRoot != "" {
		p = strings.ReplaceAll(p, "${PROJECT_ROOT}", projectRoot)
	}
	p = os.Expand(p, func(name string) string {
		if name == "PROJECT_ROOT" {
			// Already substituted above when projectRoot is set;
			// leave the original literal when it isn't.
			return "${PROJECT_ROOT}"
		}
		return os.Getenv(name)
	})
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if p == "~" {
				p = home
			} else if strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

// PreferredModel returns the model override picked from the layered
// settings. Local wins; empty string when nothing set.
func (l *Layered) PreferredModel() string {
	for _, layer := range []*Settings{l.Local, l.Project, l.User} {
		if layer != nil && layer.Model != "" {
			return layer.Model
		}
	}
	return ""
}

// PreferredStatusLine returns the most-specific StatusLineCommand —
// local > project > user. Returns nil when no layer configures one
// or when every configured layer has an empty command (treated as
// "explicitly disabled at this layer", letting the project default
// hide a user-level command if needed).
func (l *Layered) PreferredStatusLine() *StatusLineCommand {
	for _, layer := range []*Settings{l.Local, l.Project, l.User} {
		if layer == nil || layer.StatusLine == nil {
			continue
		}
		// Empty command at any precedence level intentionally
		// short-circuits — it lets a project disable a user-level
		// status line without forcing a delete from the home file.
		if layer.StatusLine.Command == "" {
			return nil
		}
		return layer.StatusLine
	}
	return nil
}
