// `biu plugin` — local plugin management.
//
//	biu plugin list                       list all discovered plugins
//	biu plugin show <name>                full details for one plugin
//	biu plugin install <path>             copy a local plugin dir into
//	                                      ~/.biumind/plugins/<name>/
//	biu plugin uninstall <name>           remove from ~/.biumind/plugins/
//	biu plugin enable <name>              clear name from settings disabled list
//	biu plugin disable <name>             add name to settings disabled list
//	biu plugin validate <path>            run manifest validator (for authors)
//
// Mutations land at the USER layer (~/.biumind/settings.json,
// ~/.biumind/plugins/) — project / local-layer files stay untouched.
// This matches the principle "user owns their machine; team-shared
// project settings shouldn't be edited by a single user's CLI run".
//
// Git / URL / marketplace install paths are deferred to PP7 — this
// PR only handles local-path install + on-disk lifecycle.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/spf13/cobra"
)

func newPluginCmd(_ *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage plugins (commands + agents + skills + hooks + mcp bundled in one dir)",
	}
	cmd.AddCommand(
		newPluginListCmd(),
		newPluginShowCmd(),
		newPluginInstallCmd(),
		newPluginUninstallCmd(),
		newPluginToggleCmd(true),  // enable
		newPluginToggleCmd(false), // disable
		newPluginValidateCmd(),
		newPluginMarketplaceCmd(),
	)
	return cmd
}

// ─── marketplace subcommands ──────────────────────────────────

func newPluginMarketplaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "marketplace",
		Aliases: []string{"market"},
		Short:   "Manage plugin marketplaces (catalogs of installable plugins)",
	}
	cmd.AddCommand(
		newMarketplaceListCmd(),
		newMarketplaceAddCmd(),
		newMarketplaceRemoveCmd(),
		newMarketplaceShowCmd(),
	)
	return cmd
}

func newMarketplaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered marketplaces",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := plugins.LoadMarketplaceStore()
			if err != nil {
				return clierr.Wrapf("plugin marketplace list", err, "load")
			}
			if len(store.Marketplaces) == 0 {
				fmt.Println("(no marketplaces — `biu plugin marketplace add <name> <source>`)")
				return nil
			}
			fmt.Printf("%-22s  %-8s  %s\n", "name", "signed", "source")
			for _, m := range store.Marketplaces {
				signed := "no"
				if m.PinnedKey != "" {
					signed = "yes"
				}
				fmt.Printf("%-22s  %-8s  %s\n", m.Name, signed, m.Source)
			}
			return nil
		},
	}
}

func newMarketplaceAddCmd() *cobra.Command {
	var pinnedKey string
	cmd := &cobra.Command{
		Use:   "add <name> <source>",
		Short: "Register a marketplace by name + source (path / https URL / git+https://...)",
		Long: `Examples:

  biu plugin marketplace add biumind-official git+https://github.com/biumind/marketplace
  biu plugin marketplace add local /Users/me/work/my-marketplace
  biu plugin marketplace add web https://example.com/.claude-plugin/marketplace.json

Optionally pin a public key so future fetches verify the
marketplace.json signature:

  biu plugin marketplace add x git+... --pinned-key 'ed25519:<base64-spki>'

Generate keypairs with 'biu skill keygen' (the same ed25519 surface
is reused for marketplace signing).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := plugins.LoadMarketplaceStore()
			if err != nil {
				return clierr.Wrapf("plugin marketplace add", err, "load")
			}
			entry := plugins.MarketplaceEntry{
				Name:      args[0],
				Source:    args[1],
				PinnedKey: pinnedKey,
			}
			// Round-trip the source through fetch to make sure it
			// resolves before we persist; bad URLs / paths surface now,
			// not at install time.
			pub, err := plugins.ParsePinnedKey(pinnedKey)
			if err != nil {
				return clierr.Wrapf("plugin marketplace add", err, "parse pinned key")
			}
			mp, _, err := plugins.FetchMarketplace(args[1], pub)
			if err != nil {
				return clierr.Wrapf("plugin marketplace add", err, "fetch %s", args[1])
			}
			if err := store.Add(entry); err != nil {
				return clierr.Wrapf("plugin marketplace add", err, "")
			}
			if err := store.Save(); err != nil {
				return clierr.Wrapf("plugin marketplace add", err, "save")
			}
			fmt.Printf("+ %s registered (source: %s)\n", entry.Name, entry.Source)
			fmt.Printf("  marketplace lists %d plugin(s)\n", len(mp.Plugins))
			if pinnedKey != "" {
				fmt.Printf("  signed with pinned key: %s…\n", pinnedKey[:32])
			} else {
				fmt.Println("  unsigned — fetches will not verify integrity (use --pinned-key for trusted sources)")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pinnedKey, "pinned-key", "",
		"ed25519 public key used to verify marketplace.json signatures (format: ed25519:<base64-spki>)")
	return cmd
}

func newMarketplaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Unregister a marketplace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := plugins.LoadMarketplaceStore()
			if err != nil {
				return clierr.Wrapf("plugin marketplace remove", err, "load")
			}
			if err := store.Remove(args[0]); err != nil {
				return clierr.Wrapf("plugin marketplace remove", err, "%s", args[0])
			}
			if err := store.Save(); err != nil {
				return clierr.Wrapf("plugin marketplace remove", err, "save")
			}
			fmt.Printf("- %s removed\n", args[0])
			return nil
		},
	}
}

func newMarketplaceShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Fetch a marketplace and list its plugins",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := plugins.LoadMarketplaceStore()
			if err != nil {
				return clierr.Wrapf("plugin marketplace show", err, "load")
			}
			entry, pub, err := store.Lookup(args[0])
			if err != nil {
				return clierr.Wrapf("plugin marketplace show", err, "%s", args[0])
			}
			mp, _, err := plugins.FetchMarketplace(entry.Source, pub)
			if err != nil {
				return clierr.Wrapf("plugin marketplace show", err, "fetch %s", entry.Source)
			}
			fmt.Printf("marketplace: %s\n", mp.Name)
			if mp.Description != "" {
				fmt.Printf("  description: %s\n", mp.Description)
			}
			if mp.Owner.Name != "" {
				fmt.Printf("  owner:       %s\n", mp.Owner.Name)
			}
			fmt.Printf("  source:      %s\n", entry.Source)
			fmt.Printf("  signed:      %v\n", pub != nil)
			fmt.Printf("\nplugins (%d):\n", len(mp.Plugins))
			fmt.Printf("  %-22s  %-8s  %s\n", "name", "via", "description")
			for _, p := range mp.Plugins {
				fmt.Printf("  %-22s  %-8s  %s\n", p.Name, p.Source.Type,
					oneLine(p.Description))
			}
			fmt.Printf("\ninstall: `biu plugin install <name>@%s`\n", mp.Name)
			return nil
		},
	}
}

// oneLine collapses multi-line descriptions for table rendering;
// long lines truncate at 60 chars with an ellipsis so the column
// stays bounded.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}


// ─── list ─────────────────────────────────────────────────────

func newPluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed plugins (across user + project + compat roots)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			disabled, _ := loadDisabledList(cwd)
			agg := plugins.LoadAll(plugins.DefaultRoots(cwd), disabled)

			if len(agg.Plugins) == 0 && len(agg.Errors) == 0 {
				fmt.Println("(no plugins installed — `biu plugin install <path>`)")
				return nil
			}

			// Stable column widths so the eye can scan quickly.
			fmt.Printf("%-3s  %-24s  %-10s  %-10s  %s\n",
				"", "name", "version", "source", "components")
			for _, lp := range agg.Plugins {
				flag := "✓"
				if !lp.Enabled {
					flag = "✗"
				}
				fmt.Printf("%-3s  %-24s  %-10s  %-10s  %s\n",
					flag,
					lp.Manifest.Name,
					trimVer(lp.Manifest.Version),
					lp.Source,
					summariseComponents(lp))
			}
			for _, e := range agg.Errors {
				fmt.Fprintf(os.Stderr, "  ! %s: %v\n", e.Path, e.Err)
			}
			return nil
		},
	}
}

// summariseComponents builds a short comma-list of which components a
// plugin contributes. Empty components are omitted entirely so the
// output stays compact for plugins that ship just one surface.
func summariseComponents(lp *plugins.LoadedPlugin) string {
	var parts []string
	if lp.CommandsPath != "" {
		parts = append(parts, "commands")
	}
	if lp.AgentsPath != "" {
		parts = append(parts, "agents")
	}
	if lp.SkillsPath != "" {
		parts = append(parts, "skills")
	}
	if lp.OutputStylesPath != "" {
		parts = append(parts, "output-styles")
	}
	if len(lp.HooksJSON) > 0 {
		parts = append(parts, "hooks")
	}
	if len(lp.McpServers) > 0 {
		parts = append(parts, fmt.Sprintf("mcp(%d)", len(lp.McpServers)))
	}
	if len(parts) == 0 {
		return "(metadata-only)"
	}
	return strings.Join(parts, ", ")
}

// trimVer prints "1.0.0" not "v1.0.0" for a tighter table; pure
// presentation, the manifest value is preserved on disk.
func trimVer(v string) string { return strings.TrimPrefix(v, "v") }

// ─── show ─────────────────────────────────────────────────────

func newPluginShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show full details for one plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cwd, _ := os.Getwd()
			disabled, _ := loadDisabledList(cwd)
			agg := plugins.LoadAll(plugins.DefaultRoots(cwd), disabled)

			lp := findPlugin(agg, name)
			if lp == nil {
				return clierr.Newf("plugin show",
					"%q not found — `biu plugin list` shows installed names", name)
			}
			fmt.Printf("name:        %s\n", lp.Manifest.Name)
			fmt.Printf("version:     %s\n", lp.Manifest.Version)
			fmt.Printf("description: %s\n", lp.Manifest.Description)
			fmt.Printf("author:      %s", lp.Manifest.Author.Name)
			if lp.Manifest.Author.Email != "" {
				fmt.Printf(" <%s>", lp.Manifest.Author.Email)
			}
			fmt.Println()
			if lp.Manifest.Homepage != "" {
				fmt.Printf("homepage:    %s\n", lp.Manifest.Homepage)
			}
			if lp.Manifest.Repository != "" {
				fmt.Printf("repository:  %s\n", lp.Manifest.Repository)
			}
			fmt.Printf("path:        %s\n", clierr.DisplayPath(lp.Path))
			fmt.Printf("source:      %s\n", lp.Source)
			fmt.Printf("enabled:     %v\n", lp.Enabled)

			fmt.Println()
			fmt.Println("components:")
			printComponent("commands", lp.CommandsPath)
			printComponent("agents", lp.AgentsPath)
			printComponent("skills", lp.SkillsPath)
			printComponent("output-styles", lp.OutputStylesPath)
			if len(lp.HooksJSON) > 0 {
				fmt.Printf("  hooks:         %d bytes (use `cat %s/hooks/hooks.json` to view)\n",
					len(lp.HooksJSON), clierr.DisplayPath(lp.Path))
			}
			if len(lp.McpServers) > 0 {
				fmt.Printf("  mcp servers:   %d\n", len(lp.McpServers))
				names := make([]string, 0, len(lp.McpServers))
				for n := range lp.McpServers {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					fmt.Printf("    - %s\n", n)
				}
			}
			if len(lp.Manifest.Unrecognised) > 0 {
				fmt.Println()
				fmt.Println("unrecognised manifest keys (parsed but not consumed by biu):")
				for k := range lp.Manifest.Unrecognised {
					fmt.Printf("  - %s\n", k)
				}
			}
			return nil
		},
	}
}

func printComponent(label, path string) {
	if path == "" {
		return
	}
	fmt.Printf("  %-13s  %s\n", label+":", clierr.DisplayPath(path))
}

func findPlugin(agg *plugins.Aggregator, name string) *plugins.LoadedPlugin {
	for _, lp := range agg.Plugins {
		if lp.Manifest.Name == name {
			return lp
		}
	}
	return nil
}

// ─── install ──────────────────────────────────────────────────

func newPluginInstallCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install <path|plugin@marketplace>",
		Short: "Install a plugin from a local directory or registered marketplace",
		Long: `Three install forms:

  biu plugin install ./my-plugin              local directory
  biu plugin install /abs/path/to/plugin      absolute path
  biu plugin install code-review@biumind      from registered marketplace

Local installs copy the source tree into ~/.biumind/plugins/<name>/.
Marketplace installs resolve the source via the marketplace's manifest
(supports local + git source types today; url is reserved for a future
release).

Refuses to overwrite an existing install unless --force is passed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Marketplace form takes precedence: split before path
			// resolution so `code-review@market` doesn't get
			// stat()'d as a path.
			if pluginName, marketName, ok := plugins.SplitPluginRef(args[0]); ok {
				return runMarketplaceInstall(pluginName, marketName, force)
			}

			src, err := filepath.Abs(args[0])
			if err != nil {
				return clierr.Wrapf("plugin install", err, "resolve %s", args[0])
			}
			st, err := os.Stat(src)
			if err != nil {
				return clierr.Wrapf("plugin install", err, "stat %s", src)
			}
			if !st.IsDir() {
				return clierr.Newf("plugin install",
					"%s is not a directory — local install needs an unpacked plugin tree", src)
			}
			lp, err := plugins.Load(src)
			if err != nil {
				return clierr.Wrapf("plugin install", err, "validate %s", src)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return clierr.Wrapf("plugin install", err, "resolve $HOME")
			}
			dst := filepath.Join(home, ".biumind", "plugins", lp.Manifest.Name)
			if _, err := os.Stat(dst); err == nil {
				if !force {
					return clierr.Newf("plugin install",
						"%s already exists — pass --force to overwrite", clierr.DisplayPath(dst))
				}
				if err := os.RemoveAll(dst); err != nil {
					return clierr.Wrapf("plugin install", err, "remove existing %s", dst)
				}
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return clierr.Wrapf("plugin install", err, "mkdir %s", filepath.Dir(dst))
			}
			if err := copyDir(src, dst); err != nil {
				return clierr.Wrapf("plugin install", err, "copy")
			}
			fmt.Printf("+ %s -> %s\n", lp.Manifest.Name, clierr.DisplayPath(dst))
			fmt.Println("  restart biu (or `/plugin reload`) to activate")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing install of the same name")
	return cmd
}

// runMarketplaceInstall is the marketplace branch of `biu plugin
// install`. Resolves the (plugin, marketplace) pair against the
// known-marketplaces store, fetches the manifest (verifying any
// pinned signature), then materialises the plugin source via
// ResolveInstall and copies it into ~/.biumind/plugins/.
func runMarketplaceInstall(pluginName, marketName string, force bool) error {
	store, err := plugins.LoadMarketplaceStore()
	if err != nil {
		return clierr.Wrapf("plugin install", err, "load marketplace store")
	}
	entry, pub, err := store.Lookup(marketName)
	if err != nil {
		return clierr.Wrapf("plugin install", err, "marketplace %q", marketName)
	}
	mp, baseDir, err := plugins.FetchMarketplace(entry.Source, pub)
	if err != nil {
		return clierr.Wrapf("plugin install", err, "fetch marketplace %q", marketName)
	}
	mpEntry, ok := mp.Lookup(pluginName)
	if !ok {
		available := make([]string, 0, len(mp.Plugins))
		for _, p := range mp.Plugins {
			available = append(available, p.Name)
		}
		return clierr.Newf("plugin install",
			"%q not found in marketplace %q. Available: %s",
			pluginName, marketName, strings.Join(available, ", "))
	}
	srcDir, err := plugins.ResolveInstall(mpEntry, baseDir)
	if err != nil {
		return clierr.Wrapf("plugin install", err, "resolve %s@%s", pluginName, marketName)
	}
	// Validate before copying — same shape the local-path branch
	// uses, so a broken marketplace plugin doesn't land half-installed.
	lp, err := plugins.Load(srcDir)
	if err != nil {
		return clierr.Wrapf("plugin install", err,
			"validate %s from %s", pluginName, marketName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return clierr.Wrapf("plugin install", err, "resolve $HOME")
	}
	dst := filepath.Join(home, ".biumind", "plugins", lp.Manifest.Name)
	if _, err := os.Stat(dst); err == nil {
		if !force {
			return clierr.Newf("plugin install",
				"%s already exists — pass --force to overwrite",
				clierr.DisplayPath(dst))
		}
		if err := os.RemoveAll(dst); err != nil {
			return clierr.Wrapf("plugin install", err, "remove existing %s", dst)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return clierr.Wrapf("plugin install", err, "mkdir %s", filepath.Dir(dst))
	}
	if err := copyDir(srcDir, dst); err != nil {
		return clierr.Wrapf("plugin install", err, "copy")
	}
	fmt.Printf("+ %s@%s -> %s\n", pluginName, marketName, clierr.DisplayPath(dst))
	if mpEntry.PinnedKey != "" {
		fmt.Printf("  marketplace recommends pinned key: %s…\n", mpEntry.PinnedKey[:32])
	}
	fmt.Println("  restart biu (or `/plugin reload`) to activate")
	return nil
}

// ─── uninstall ────────────────────────────────────────────────

func newPluginUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Remove a plugin from ~/.biumind/plugins/",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			home, err := os.UserHomeDir()
			if err != nil {
				return clierr.Wrapf("plugin uninstall", err, "resolve $HOME")
			}
			dst := filepath.Join(home, ".biumind", "plugins", name)
			if _, err := os.Stat(dst); err != nil {
				return clierr.Newf("plugin uninstall",
					"%s not found at %s — nothing to remove",
					name, clierr.DisplayPath(dst))
			}
			if err := os.RemoveAll(dst); err != nil {
				return clierr.Wrapf("plugin uninstall", err, "remove %s", dst)
			}
			fmt.Printf("- %s removed from %s\n", name, clierr.DisplayPath(dst))
			return nil
		},
	}
}

// ─── enable / disable ─────────────────────────────────────────

func newPluginToggleCmd(enable bool) *cobra.Command {
	verb := "disable"
	short := "Disable a plugin (adds to ~/.biumind/settings.json plugins.disabled)"
	if enable {
		verb = "enable"
		short = "Enable a previously-disabled plugin (removes from disabled list)"
	}
	return &cobra.Command{
		Use:   verb + " <name>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			home, err := os.UserHomeDir()
			if err != nil {
				return clierr.Wrapf("plugin "+verb, err, "resolve $HOME")
			}
			settingsPath := filepath.Join(home, ".biumind", "settings.json")
			if err := plugins.SetPluginDisabled(settingsPath, name, !enable); err != nil {
				return clierr.Wrapf("plugin "+verb, err, "write %s", settingsPath)
			}
			if enable {
				fmt.Printf("+ %s enabled (removed from %s)\n",
					name, clierr.DisplayPath(settingsPath))
			} else {
				fmt.Printf("- %s disabled (added to %s)\n",
					name, clierr.DisplayPath(settingsPath))
			}
			fmt.Println("  restart biu to apply")
			return nil
		},
	}
}

// ─── validate (for plugin authors) ────────────────────────────

func newPluginValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <path>",
		Short: "Validate a plugin directory's manifest + component layout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lp, err := plugins.Load(args[0])
			if err != nil {
				// Even on validation failure, print the path that was
				// inspected so authors don't have to hunt for the abs
				// path their RunE was given.
				return clierr.Wrapf("plugin validate", err, "%s", args[0])
			}
			fmt.Printf("✓ %s v%s — %s\n",
				lp.Manifest.Name, lp.Manifest.Version, summariseComponents(lp))
			if len(lp.Manifest.Unrecognised) > 0 {
				fmt.Println()
				fmt.Println("note: manifest contains keys biu doesn't yet consume:")
				for k := range lp.Manifest.Unrecognised {
					fmt.Printf("  - %s\n", k)
				}
				fmt.Println("(these will round-trip but won't activate any biu surface)")
			}
			return nil
		},
	}
}

// ─── helpers: settings mutation + filesystem copy ─────────────

// loadDisabledList returns the merged disabled list from layered
// settings, or nil + nil error when no settings exist. Used by
// list / show so they reflect the same enable state the engine
// will see at next startup.
func loadDisabledList(cwd string) ([]string, error) {
	layered, err := clauseSettings.Load(cwd)
	if err != nil || layered == nil {
		return nil, err
	}
	return layered.MergedDisabledPlugins(), nil
}

// copyDir recursively copies src to dst. Symlinks are followed
// (they almost always point inside the source tree for plugins
// shipped from a build). Hidden files are NOT skipped — plugin.json
// lives under .claude-plugin/.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		return copyFile(path, out)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
