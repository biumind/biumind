// Aggregator: walk multiple plugin search roots, load every plugin
// found, then dispatch each plugin's components to the matching
// per-component registry.
//
// The aggregator does NOT own the registries — callers (the wiring
// layer) hold them. We just route each plugin's data into them
// via the Attach* methods. This keeps the dependency direction
// one-way: aggregate.go imports per-component packages; nobody
// imports aggregate.go upstream.
//
// MCP servers are special-cased: connecting an MCP server is async
// and stateful (handshake + tool discovery), so the aggregator
// surfaces them as data and lets wiring connect them on the same
// schedule it connects settings.json servers. See McpServerConfigs().
package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/commands"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/output"
	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
)

// Aggregator holds the result of LoadAll. Callers use the Attach*
// methods to push each plugin's contributions into the live
// registries the engine uses.
type Aggregator struct {
	// Plugins is the loaded set, in load order. Disabled plugins
	// are still present here (Enabled = false) so /plugin list can
	// show them; Attach* methods skip them.
	Plugins []*LoadedPlugin

	// Errors collects per-plugin failures. LoadAll is best-effort:
	// one broken plugin doesn't stop the rest. Callers can surface
	// this slice via /plugin doctor or stderr at startup.
	Errors []LoadError
}

// LoadError ties a directory to its load failure so the user can
// fix the right plugin without grepping.
type LoadError struct {
	Path string
	Err  error
}

func (e LoadError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }
func (e LoadError) Unwrap() error { return e.Err }

// extraRootsProviders are callbacks registered by sister packages
// (today: internal/plugins/bundled) that contribute additional
// SearchRoots after user / project / compat. Used so the bundled
// extraction can join the default load pipeline without every
// call site (slash handlers, CLI, wiring) having to import the
// bundled package — DefaultRoots stays the single front door.
//
// Read-mostly: each provider registers once at init() time; reads
// happen on every DefaultRoots call (potentially thousands per
// session via slash dispatch). RWMutex matches the existing
// concurrency model in this package.
var (
	extraRootsProviders   []func() []SearchRoot
	extraRootsProvidersMu sync.RWMutex
)

// RegisterRootsProvider lets a sister package contribute extra
// SearchRoots to DefaultRoots. Providers run AFTER user / project
// / compat so bundled / future remote roots land at the end of the
// load order — matching the first-wins precedence rule (a user-
// installed plugin shadows a bundled one of the same name).
//
// Idempotent on identical (name, fn) is NOT enforced because Go
// init() runs exactly once per package, so duplicate registration
// shouldn't occur in practice. Tests that need to reset must use
// resetExtraRootsProviders.
func RegisterRootsProvider(fn func() []SearchRoot) {
	if fn == nil {
		return
	}
	extraRootsProvidersMu.Lock()
	defer extraRootsProvidersMu.Unlock()
	extraRootsProviders = append(extraRootsProviders, fn)
}

// resetExtraRootsProviders is a test-only helper. Lowercase to
// keep production code from accidentally clearing the bundled
// registration.
func resetExtraRootsProviders() {
	extraRootsProvidersMu.Lock()
	defer extraRootsProvidersMu.Unlock()
	extraRootsProviders = nil
}

// DefaultRoots returns biu's standard plugin search roots in load
// order. Three layers, matching the agents/commands/skills/memory
// precedence convention:
//
//	user      ~/.biumind/plugins/    (per-user installs)
//	project   <cwd>/.biumind/plugins/ (team-shared, in git)
//	compat    ~/.claude/plugins/     (PP8a — drop-in compatibility root)
//
// cwd may be empty (skips project layer). Roots that don't exist
// on disk are silently dropped by LoadAll, so the returned slice
// is safe to pass through unchanged on first-run systems.
//
// Layer ordering matters for the first-wins collision rule in
// LoadAll: a plugin name installed under ~/.biumind/plugins/ wins
// over a same-named one in ~/.claude/plugins/. This makes
// "I installed a fresh copy in biu" the way to override a stale
// compat install without uninstalling the original tree.
func DefaultRoots(cwd string) []SearchRoot {
	var roots []SearchRoot
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots,
			SearchRoot{Path: filepath.Join(home, ".biumind", "plugins"), Source: SrcUser},
		)
	}
	if cwd != "" {
		roots = append(roots,
			SearchRoot{Path: filepath.Join(cwd, ".biumind", "plugins"), Source: SrcProject},
		)
	}
	if compat := CompatClaudeRoot(); compat != "" {
		roots = append(roots, SearchRoot{Path: compat, Source: SrcCompat})
	}
	// Extra providers (bundled extraction etc.) plug in last so
	// user-installed plugins win on name collision.
	extraRootsProvidersMu.RLock()
	providers := append([]func() []SearchRoot(nil), extraRootsProviders...)
	extraRootsProvidersMu.RUnlock()
	for _, p := range providers {
		roots = append(roots, p()...)
	}
	return roots
}

// SearchRoot pairs a directory to scan with the Source label every
// plugin found inside it should carry. The aggregator scans roots
// in order, so later roots' plugins override earlier ones on name
// collision (matches biu's user → project → plugin precedence).
type SearchRoot struct {
	Path   string
	Source Source
}

// LoadAll scans every search root, loads every direct subdirectory
// as a plugin candidate, and returns the aggregated set. Roots that
// don't exist are silently skipped (first-run users with no
// ~/.biumind/plugins/).
//
// Disabled is the set of plugin names the user has disabled in
// settings (PP4 wires this from settings.json plugins.disabled).
// Pass nil for "everything enabled".
//
// Loaded plugins keep their first-encountered Source; on a name
// collision the second occurrence is appended to Errors and ignored
// — the aggregator is conservative about silent overrides because
// they make "why didn't my plugin load" debugging painful.
func LoadAll(roots []SearchRoot, disabled []string) *Aggregator {
	disabledSet := map[string]bool{}
	for _, n := range disabled {
		disabledSet[n] = true
	}

	a := &Aggregator{}
	seen := map[string]*LoadedPlugin{}

	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			a.Errors = append(a.Errors, LoadError{Path: root.Path, Err: err})
			continue
		}
		// Sort for deterministic load order — `/plugin list` should
		// be alphabetical regardless of filesystem readdir order.
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// Skip reserved subdirectories used as siblings to the
			// plugin tree. `marketplaces/` holds cloned marketplace
			// trees + the marketplaces.json store; treating it as a
			// plugin produces noisy "manifest not found" errors when
			// the user has any marketplaces registered.
			if isReservedPluginsSibling(e.Name()) {
				continue
			}
			pluginDir := filepath.Join(root.Path, e.Name())
			lp, err := Load(pluginDir)
			if err != nil {
				a.Errors = append(a.Errors, LoadError{Path: pluginDir, Err: err})
				continue
			}
			lp.Source = root.Source
			if disabledSet[lp.Manifest.Name] {
				lp.Enabled = false
			}
			if existing, dup := seen[lp.Manifest.Name]; dup {
				a.Errors = append(a.Errors, LoadError{
					Path: pluginDir,
					Err:  fmt.Errorf("plugin name %q already loaded from %s", lp.Manifest.Name, existing.Path),
				})
				continue
			}
			seen[lp.Manifest.Name] = lp
			a.Plugins = append(a.Plugins, lp)
		}
	}
	return a
}

// isReservedPluginsSibling returns true when a directory under
// ~/.biumind/plugins/ is biu-reserved infrastructure (cache of
// fetched marketplaces, future indices, …) rather than a user
// plugin. Aggregator skips these names in LoadAll to keep the
// `(no plugins installed)` UX clean for users who have only added
// a marketplace.
func isReservedPluginsSibling(name string) bool {
	switch name {
	case "marketplaces", ".bundled", ".cache", ".tmp":
		return true
	}
	return false
}

// enabledPlugins returns just the plugins the user hasn't disabled.
// Used internally by every Attach* method so a single disable check
// covers all six surfaces.
func (a *Aggregator) enabledPlugins() []*LoadedPlugin {
	if a == nil {
		return nil
	}
	out := make([]*LoadedPlugin, 0, len(a.Plugins))
	for _, lp := range a.Plugins {
		if lp.Enabled {
			out = append(out, lp)
		}
	}
	return out
}

// pluginSourceLabel formats the source string passed to per-component
// registries so /plugin show can attribute each item to its origin.
// Format: "plugin:<name>" — same shape commands.SrcPlugin expects.
func pluginSourceLabel(name string) string { return "plugin:" + name }

// ─── Attach* — one per registry the aggregator drives ──────────

// AttachAgents pushes every enabled plugin's agents/ directory into
// reg. Errors are collected on the Aggregator (since LoadDir's only
// failure mode is "directory unreadable", which a plugin author
// should know about) but don't stop the loop.
func (a *Aggregator) AttachAgents(reg *agents.Registry) {
	if a == nil || reg == nil {
		return
	}
	for _, lp := range a.enabledPlugins() {
		if lp.AgentsPath == "" {
			continue
		}
		if err := reg.LoadDir(lp.AgentsPath, pluginSourceLabel(lp.Manifest.Name)); err != nil {
			a.Errors = append(a.Errors, LoadError{Path: lp.AgentsPath, Err: err})
		}
	}
}

// AttachCommands pushes every plugin's commands/ directory.
func (a *Aggregator) AttachCommands(reg *commands.Registry) {
	if a == nil || reg == nil {
		return
	}
	for _, lp := range a.enabledPlugins() {
		if lp.CommandsPath == "" {
			continue
		}
		src := commands.Source(pluginSourceLabel(lp.Manifest.Name))
		if err := reg.LoadDir(lp.CommandsPath, src); err != nil {
			a.Errors = append(a.Errors, LoadError{Path: lp.CommandsPath, Err: err})
		}
	}
}

// AttachSkills pushes every plugin's skills/ directory.
func (a *Aggregator) AttachSkills(reg *skills.Registry) {
	if a == nil || reg == nil {
		return
	}
	for _, lp := range a.enabledPlugins() {
		if lp.SkillsPath == "" {
			continue
		}
		if err := reg.LoadDir(lp.SkillsPath, pluginSourceLabel(lp.Manifest.Name)); err != nil {
			a.Errors = append(a.Errors, LoadError{Path: lp.SkillsPath, Err: err})
		}
	}
}

// AttachOutputStyles pushes every plugin's output-styles/ directory.
func (a *Aggregator) AttachOutputStyles(reg *output.Registry) {
	if a == nil || reg == nil {
		return
	}
	for _, lp := range a.enabledPlugins() {
		if lp.OutputStylesPath == "" {
			continue
		}
		if err := reg.LoadDir(lp.OutputStylesPath, pluginSourceLabel(lp.Manifest.Name)); err != nil {
			a.Errors = append(a.Errors, LoadError{Path: lp.OutputStylesPath, Err: err})
		}
	}
}

// AttachHooks merges every plugin's hooks JSON. The plugin loader
// has already expanded ${PLUGIN_ROOT}, so commands are ready-to-run
// when hooks.Runner picks them up.
//
// Hooks are append-only — multiple plugins each contributing a
// PreToolUse hook all fire (in load order). This matches
// settings.json semantics; collision detection is not the right
// model for hooks.
func (a *Aggregator) AttachHooks(reg *hooks.Registry) {
	if a == nil || reg == nil {
		return
	}
	for _, lp := range a.enabledPlugins() {
		if len(lp.HooksJSON) == 0 {
			continue
		}
		reg.MergeJSON(pluginSourceLabel(lp.Manifest.Name), lp.HooksJSON)
	}
}

// McpServerConfig is the flat shape the wiring layer consumes when
// translating plugin-declared MCP servers into mcp.Connect /
// mcp.ConnectHTTP calls. Returned via McpServerConfigs() instead of
// pushed into a registry directly because:
//
//	1. mcp.Registry.Connect is async + blocking and depends on
//	   ctx — not the aggregator's job to manage that lifecycle.
//	2. settings.json MCP servers go through the same wiring path,
//	   so plugin and settings sources merge naturally there.
type McpServerConfig struct {
	Name       string // namespaced as "<plugin>__<server>" to avoid collisions
	PluginName string // origin plugin name
	Spec       McpServerSpec
}

// McpServerConfigs returns the flat list of MCP server specs every
// enabled plugin declared. Wiring connects them after settings.json
// servers so the same-name precedence (settings wins) is preserved.
//
// Server names are namespaced as "<plugin>__<server>" so two plugins
// declaring a "github" server don't collide; users can still override
// from settings.json by matching either the namespaced or original
// name (settings layer takes precedence at Connect time).
func (a *Aggregator) McpServerConfigs() []McpServerConfig {
	if a == nil {
		return nil
	}
	var out []McpServerConfig
	for _, lp := range a.enabledPlugins() {
		// Sort server names for deterministic ordering — useful for
		// `biu plugin show` and for tests.
		names := make([]string, 0, len(lp.McpServers))
		for n := range lp.McpServers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, McpServerConfig{
				Name:       lp.Manifest.Name + "__" + n,
				PluginName: lp.Manifest.Name,
				Spec:       lp.McpServers[n],
			})
		}
	}
	return out
}

// AttachAll is a convenience that calls every Attach* method. The
// wiring layer can use this when it owns one of each registry type
// — or call individual methods when it needs more control (e.g.
// wanting to skip output styles in headless mode).
//
// MCP servers are NOT included here because connecting them requires
// a context, network IO, and timing that only the wiring layer
// orchestrates. Use McpServerConfigs() to retrieve the data.
func (a *Aggregator) AttachAll(
	agentsReg *agents.Registry,
	commandsReg *commands.Registry,
	skillsReg *skills.Registry,
	outputReg *output.Registry,
	hooksReg *hooks.Registry,
) {
	a.AttachAgents(agentsReg)
	a.AttachCommands(commandsReg)
	a.AttachSkills(skillsReg)
	a.AttachOutputStyles(outputReg)
	a.AttachHooks(hooksReg)
}
