// Package plugins is biu's plugin system: a single bundle of
// agents / commands / skills / hooks / mcp-servers / output-styles
// loaded from one directory and routed to the existing per-component
// loaders without rewriting them.
//
// Conceptually mirrors the upstream plugin model — same on-disk
// layout (plugin.json + commands/ agents/ skills/ hooks/
// output-styles/) so users with an existing `~/.claude/plugins/`
// directory can drop it in unchanged. The wire-level compatibility
// is a goal, not the implementation: every loader is biu-native.
//
// Three layers in this package, each in its own file:
//
//	types.go    — Plugin, LoadedPlugin, Author, errors.
//	manifest.go — PluginManifest schema + JSON parse / validate.
//	loader.go   — Load(dir) → LoadedPlugin: walk one plugin directory,
//	              parse manifest, resolve component paths, normalise
//	              hooks JSON, expand ${CLAUDE_PLUGIN_ROOT}.
//
// Aggregation across multiple plugins (Aggregator) is a separate
// PR (PP3); this file owns only single-plugin types.
package plugins

import (
	"errors"
	"fmt"
)

// Source is where a LoadedPlugin came from. Surfaces in `/plugin
// list` so users can tell a bundled plugin from a marketplace
// install at a glance.
type Source string

const (
	SrcBundled  Source = "bundled"        // shipped inside the biu binary (PP8b)
	SrcUser     Source = "user"           // ~/.biumind/plugins/
	SrcProject  Source = "project"        // <cwd>/.biumind/plugins/
	SrcMarket   Source = "marketplace"    // installed via marketplace (PP7)
	SrcCompat   Source = "compat-claude"  // ~/.claude/plugins/ (PP8a)
)

// LoadedPlugin is one plugin after the loader has resolved every
// path on disk and parsed its manifest. The aggregator (PP3) takes
// a slice of these and routes each component to the matching
// per-component loader (agents.LoadDirs, commands.LoadDirs, …).
//
// Path is the absolute root directory of the plugin. Every
// *Path field below is either empty (component absent) or absolute
// (already joined to Path).
type LoadedPlugin struct {
	Manifest PluginManifest
	Path     string // absolute plugin root
	Source   Source

	// Per-component absolute paths. Empty when the manifest didn't
	// declare the component AND the convention directory doesn't
	// exist on disk. The aggregator only walks non-empty entries.
	CommandsPath     string
	AgentsPath       string
	SkillsPath       string
	OutputStylesPath string

	// HooksJSON is the manifest's `hooks` field, already normalised
	// into the same shape settings.json hooks have. Pass-through to
	// hooks.Registry.MergeJSON in PP3.
	HooksJSON []byte // nil when no hooks declared

	// McpServers is what the manifest declared, with ${PLUGIN_ROOT}
	// already expanded in command paths. Aggregator hands them to
	// mcp.Registry.AddServerConfigs in PP3.
	McpServers map[string]McpServerSpec

	// Enabled reflects the user's choice in settings.plugins.disabled.
	// The loader populates it; downstream callers (Aggregator) skip
	// disabled plugins. Default true.
	Enabled bool
}

// McpServerSpec is the manifest's representation of one MCP server.
// Mirrors the subset of internal/config.MCPServerSection that's
// portable across plugins — no biu-specific OAuth schema, no
// ergonomic tweaks. The aggregator translates this into a
// MCPServerSection at attach time.
//
// Why a separate type: importing internal/config from plugins would
// create a wiring cycle (config → settings → plugins → config); a
// flat schema-only struct keeps this package leaf-level.
type McpServerSpec struct {
	Transport string            `json:"transport,omitempty"` // "stdio" (default) | "http" | "sse"
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// Author identifies who shipped a plugin. Only Name is required;
// Email + URL are surfaced in `/plugin show <name>` for credit but
// not used for any access decision.
type Author struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// ─── errors ──────────────────────────────────────────────────

var (
	// ErrManifestMissing — no plugin.json (or .claude-plugin/plugin.json)
	// in the supplied directory. Distinct from "manifest unparseable" so
	// the CLI can give a different error message ("not a plugin" vs
	// "broken plugin").
	ErrManifestMissing = errors.New("plugin manifest not found")

	// ErrManifestInvalid — manifest parsed as JSON but failed schema
	// validation. The wrapped *ValidationError carries field-level
	// detail.
	ErrManifestInvalid = errors.New("plugin manifest invalid")

	// ErrPathEscape — a manifest-declared path tried to escape the
	// plugin directory via "..". Refuse rather than silently follow,
	// to keep the trust boundary at the plugin root.
	ErrPathEscape = errors.New("plugin path escapes plugin root")
)

// ValidationError carries one or more field-level problems found by
// PluginManifest.Validate. Wraps cleanly through fmt.Errorf("%w") via
// the Unwrap() returning ErrManifestInvalid sentinel.
type ValidationError struct {
	Fields []FieldError
}

// FieldError is one validation failure. Field is the JSON path
// (e.g. "name", "author.name"). Message is human-readable.
type FieldError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "plugin manifest invalid"
	}
	if len(e.Fields) == 1 {
		return fmt.Sprintf("plugin manifest invalid: %s: %s", e.Fields[0].Field, e.Fields[0].Message)
	}
	msg := fmt.Sprintf("plugin manifest invalid (%d issues):", len(e.Fields))
	for _, f := range e.Fields {
		msg += fmt.Sprintf("\n  - %s: %s", f.Field, f.Message)
	}
	return msg
}

func (e *ValidationError) Unwrap() error { return ErrManifestInvalid }
