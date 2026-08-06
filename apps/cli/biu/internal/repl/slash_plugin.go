// /plugin slash. REPL-side counterpart to `biu plugin` (PP5):
//
//	/plugin                 — list installed plugins (✓/✗ + components)
//	/plugin <name>          — show details for one plugin
//	/plugin enable <name>   — write to settings.json + suggest reload
//	/plugin disable <name>  — write to settings.json + suggest reload
//	/plugin reload          — re-scan plugin dirs (best-effort hot reload)
//
// Hot-reload caveat: agents / commands / skills / output-styles
// rebuild on every fresh load (slash handlers and `biu plugin list`
// already call commands.Load / agents.Load per invocation), so
// disable→reload propagates immediately for those. Hooks register at
// startup into the engine's hook runner — disabling a plugin doesn't
// unregister already-registered hook entries, so the slash handler
// surfaces a "restart biu to drop hooks" hint after any toggle that
// touches a hooks-providing plugin.

package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
)

// handlePlugin dispatches /plugin and its subcommands. Stateless —
// every call re-walks the plugin search roots so install / uninstall
// outside the session shows up on the next /plugin without a manual
// /plugin reload.
func (m model) handlePlugin(parts []string) string {
	cwd, _ := os.Getwd()

	// Reload is just a re-scan. Symbolic command for users who
	// installed a plugin in another terminal and want to confirm
	// it's visible without scrolling history.
	if len(parts) >= 2 && parts[1] == "reload" {
		agg, disabledFromSettings := loadAggForREPL(cwd)
		var b strings.Builder
		fmt.Fprintf(&b, "/plugin reload: %d plugin(s) discovered",
			len(agg.Plugins))
		if len(disabledFromSettings) > 0 {
			fmt.Fprintf(&b, " (%d disabled in settings)", len(disabledFromSettings))
		}
		if len(agg.Errors) > 0 {
			fmt.Fprintf(&b, "; %d error(s):", len(agg.Errors))
			for _, e := range agg.Errors {
				fmt.Fprintf(&b, "\n  ! %s: %v", e.Path, e.Err)
			}
		}
		b.WriteString(
			"\nnote: commands / agents / skills / output-styles refresh on next /<slash>;\n" +
				"      hooks need a biu restart to pick up changes")
		return b.String()
	}

	// enable / disable mutate ~/.biumind/settings.json then re-scan.
	if len(parts) >= 3 && (parts[1] == "enable" || parts[1] == "disable") {
		return handlePluginToggle(parts[1] == "enable", parts[2])
	}

	// /plugin <name> — drill in.
	if len(parts) >= 2 {
		return handlePluginShow(cwd, parts[1])
	}

	return handlePluginList(cwd)
}

// handlePluginList renders the same shape `biu plugin list` uses,
// minus the absolute paths so the REPL line stays narrow. We do
// surface the source label so users can tell user / project / compat
// origins apart.
func handlePluginList(cwd string) string {
	agg, _ := loadAggForREPL(cwd)
	if len(agg.Plugins) == 0 && len(agg.Errors) == 0 {
		return "/plugin: none installed. Drop a plugin under " +
			"~/.biumind/plugins/<name>/ or run `biu plugin install <path>`."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "plugins (%d):\n", len(agg.Plugins))
	for _, lp := range agg.Plugins {
		flag := "✓"
		if !lp.Enabled {
			flag = "✗"
		}
		fmt.Fprintf(&b, "  %s %-22s %-10s [%s] %s\n",
			flag,
			lp.Manifest.Name,
			trimVerREPL(lp.Manifest.Version),
			lp.Source,
			summariseComponentsREPL(lp))
	}
	for _, e := range agg.Errors {
		fmt.Fprintf(&b, "  ! %s: %v\n", filepath.Base(e.Path), e.Err)
	}
	b.WriteString("  (drill in: /plugin <name> ; toggle: /plugin enable|disable <name>)")
	return b.String()
}

// handlePluginShow surfaces detail for a single plugin. Matches
// `biu plugin show` but renders inside a one-line-per-field block
// since the REPL system note pane wraps wider columns awkwardly.
func handlePluginShow(cwd, name string) string {
	agg, _ := loadAggForREPL(cwd)
	for _, lp := range agg.Plugins {
		if lp.Manifest.Name != name {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "plugin: %s v%s\n", lp.Manifest.Name, lp.Manifest.Version)
		if lp.Manifest.Description != "" {
			fmt.Fprintf(&b, "  description: %s\n", lp.Manifest.Description)
		}
		if lp.Manifest.Author.Name != "" {
			fmt.Fprintf(&b, "  author:      %s\n", lp.Manifest.Author.Name)
		}
		fmt.Fprintf(&b, "  source:      %s\n", lp.Source)
		fmt.Fprintf(&b, "  enabled:     %v\n", lp.Enabled)
		comps := summariseComponentsREPL(lp)
		fmt.Fprintf(&b, "  components:  %s\n", comps)
		if len(lp.McpServers) > 0 {
			names := make([]string, 0, len(lp.McpServers))
			for n := range lp.McpServers {
				names = append(names, n)
			}
			sort.Strings(names)
			fmt.Fprintf(&b, "  mcp:         %s\n", strings.Join(names, ", "))
		}
		if len(lp.Manifest.Unrecognised) > 0 {
			keys := make([]string, 0, len(lp.Manifest.Unrecognised))
			for k := range lp.Manifest.Unrecognised {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Fprintf(&b, "  unknown manifest keys: %s\n", strings.Join(keys, ", "))
		}
		return strings.TrimRight(b.String(), "\n")
	}
	available := make([]string, 0, len(agg.Plugins))
	for _, lp := range agg.Plugins {
		available = append(available, lp.Manifest.Name)
	}
	if len(available) == 0 {
		return fmt.Sprintf("/plugin %s: not found (none installed)", name)
	}
	return fmt.Sprintf("/plugin %s: not found. Installed: %s",
		name, strings.Join(available, ", "))
}

// handlePluginToggle flips one plugin's disabled state in user
// settings. Mirrors the CLI subcommand exactly so users get the same
// behaviour regardless of which surface they reach for.
func handlePluginToggle(enable bool, name string) string {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Sprintf("/plugin %s: cannot resolve $HOME: %v", verb, err)
	}
	settingsPath := filepath.Join(home, ".biumind", "settings.json")
	if err := plugins.SetPluginDisabled(settingsPath, name, !enable); err != nil {
		return fmt.Sprintf("/plugin %s %s: %v", verb, name, err)
	}

	// Re-load to confirm the plugin actually exists (guard against
	// typos that would otherwise persist a no-op disable forever).
	cwd, _ := os.Getwd()
	agg, _ := loadAggForREPL(cwd)
	var lp *plugins.LoadedPlugin
	for _, p := range agg.Plugins {
		if p.Manifest.Name == name {
			lp = p
			break
		}
	}
	if lp == nil {
		return fmt.Sprintf(
			"/plugin %s %s: settings updated, but no plugin with that "+
				"name is installed — fix the name then `/plugin %s %s`",
			verb, name, verb, name)
	}

	hooksHint := ""
	if len(lp.HooksJSON) > 0 {
		hooksHint = "\n  note: this plugin contributes hooks — restart biu " +
			"to make the hook change fully take effect"
	}
	if enable {
		return fmt.Sprintf("/plugin enable %s: removed from settings.disabled. "+
			"Commands / agents / skills / output-styles activate on next /<slash>.%s",
			name, hooksHint)
	}
	return fmt.Sprintf("/plugin disable %s: added to settings.disabled. "+
		"Commands / agents / skills / output-styles drop on next /<slash>.%s",
		name, hooksHint)
}

// loadAggForREPL is the centralised plugin loader for slash handlers.
// Pulls disabled list from settings so /plugin list's ✗ flags reflect
// the same state the engine will see at next startup.
func loadAggForREPL(cwd string) (*plugins.Aggregator, []string) {
	var disabled []string
	if layered, err := clauseSettings.Load(cwd); err == nil && layered != nil {
		disabled = layered.MergedDisabledPlugins()
	}
	return plugins.LoadAll(plugins.DefaultRoots(cwd), disabled), disabled
}

// summariseComponentsREPL is a more compact variant of the CLI helper
// — abbreviates "output-styles" to "styles" so the row fits into the
// REPL's tighter column budget.
func summariseComponentsREPL(lp *plugins.LoadedPlugin) string {
	var parts []string
	if lp.CommandsPath != "" {
		parts = append(parts, "cmds")
	}
	if lp.AgentsPath != "" {
		parts = append(parts, "agents")
	}
	if lp.SkillsPath != "" {
		parts = append(parts, "skills")
	}
	if lp.OutputStylesPath != "" {
		parts = append(parts, "styles")
	}
	if len(lp.HooksJSON) > 0 {
		parts = append(parts, "hooks")
	}
	if len(lp.McpServers) > 0 {
		parts = append(parts, fmt.Sprintf("mcp:%d", len(lp.McpServers)))
	}
	if len(parts) == 0 {
		return "(meta)"
	}
	return strings.Join(parts, ",")
}

func trimVerREPL(v string) string { return strings.TrimPrefix(v, "v") }
