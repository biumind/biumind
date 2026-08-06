// `biu config` — inspection and validation of the layered config
// surface that drives every other subcommand.
//
// Three subcommands:
//
//   biu config show              — print the resolved config.toml
//   biu config show --settings   — print the merged settings.json layers
//   biu config validate          — load every layer, report concrete errors
//   biu config schema config     — emit JSON schema for ~/.biu/config.toml
//   biu config schema settings   — emit JSON schema for settings.json
//
// The schemas double as IDE autocomplete fodder — add a `$schema`
// annotation at the top of your settings.json (see commands.md) and
// VS Code / IntelliJ pick up validation + completion for free.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	cfgschema "github.com/biumind/biumind/apps/cli/biu/internal/config/schema"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/biumind/biumind/apps/cli/biu/internal/telemetry"
	"github.com/spf13/cobra"
)

func newConfigCmd(f *rootFlags) *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Inspect, validate, and emit JSON schemas for biu config files",
	}
	c.AddCommand(newConfigShowCmd(f), newConfigValidateCmd(f), newConfigSchemaCmd(),
		newConfigTelemetryCmd())
	return c
}

// newConfigTelemetryCmd manages the opt-in telemetry control file.
//
//   biu config telemetry status   — print current state + jsonl path
//   biu config telemetry on       — enable; rotates install_id
//   biu config telemetry off      — disable; preserves the jsonl
//   biu config telemetry endpoint <url> — set / clear remote URL
func newConfigTelemetryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "telemetry",
		Short: "Manage opt-in usage telemetry (default: off)",
		Long: `Manage opt-in usage telemetry.

biu collects nothing by default. When enabled, anonymous events
(subcommand name, outcome, duration, version, os/arch) are appended
to ~/.biu/telemetry.jsonl. NEVER includes prompt content, file
paths, or API keys. See the file directly to audit what would be
sent if a remote endpoint is also configured.

Environment overrides:
  BIU_TELEMETRY_DISABLED=1     hard-off regardless of saved config
  BIU_TELEMETRY_ENABLED=1      opt-in for one run without saving
  BIU_TELEMETRY_ENDPOINT=…     override remote URL`,
	}

	c.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Print current telemetry state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := telemetry.LoadConfig()
			if err != nil {
				return clierr.Wrapf("config telemetry", err, "load")
			}
			ctrlPath, _ := telemetry.ConfigPath()
			eventsPath, _ := telemetry.EventsPath()
			fmt.Printf("enabled    : %v\n", cfg.Enabled)
			fmt.Printf("install_id : %s\n", or(cfg.InstallID, "(not set)"))
			fmt.Printf("endpoint   : %s\n", or(cfg.Endpoint, "(local only)"))
			fmt.Printf("control    : %s\n", clierr.DisplayPath(ctrlPath))
			fmt.Printf("events     : %s\n", clierr.DisplayPath(eventsPath))
			return nil
		},
	})

	var endpoint string
	on := &cobra.Command{
		Use:   "on",
		Short: "Enable telemetry (rotates install_id)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := telemetry.Enable(endpoint)
			if err != nil {
				return clierr.Wrapf("config telemetry on", err, "save")
			}
			fmt.Fprintf(os.Stderr, "[biu] telemetry enabled (install_id=%s, endpoint=%s)\n",
				cfg.InstallID, or(cfg.Endpoint, "local only"))
			return nil
		},
	}
	on.Flags().StringVar(&endpoint, "endpoint", "",
		"optional HTTPS URL to POST events to")
	c.AddCommand(on)

	c.AddCommand(&cobra.Command{
		Use:   "off",
		Short: "Disable telemetry (preserves the local jsonl on disk)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := telemetry.Disable(); err != nil {
				return clierr.Wrapf("config telemetry off", err, "save")
			}
			fmt.Fprintln(os.Stderr, "[biu] telemetry disabled")
			return nil
		},
	})

	return c
}

// or() lives in main.go (same package).

func newConfigShowCmd(f *rootFlags) *cobra.Command {
	var showSettings bool
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the resolved configuration",
		Long: `Prints the resolved configuration. By default shows
the parsed ~/.biu/config.toml; pass --settings to dump the merged
user/project/local settings.json layers instead.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showSettings {
				cwd, _ := os.Getwd()
				layered, err := clauseSettings.Load(cwd)
				if err != nil {
					return clierr.Wrapf("config show", err, "load settings")
				}
				return printSettings(layered)
			}
			cfg, path, err := config.Load(f.cfgPath)
			if err != nil {
				return clierr.Wrapf("config show", err, "load config")
			}
			if path == "" {
				fmt.Fprintln(os.Stderr, "(no config file found — using defaults)")
			} else {
				fmt.Fprintf(os.Stderr, "# %s\n", clierr.DisplayPath(path))
			}
			return printConfigToml(cfg)
		},
	}
	c.Flags().BoolVar(&showSettings, "settings", false,
		"show merged settings.json layers instead of config.toml")
	return c
}

func newConfigValidateCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Load every config + settings layer and report problems",
		Long: `Validates ~/.biu/config.toml and the four settings.json
layers (user / project / local). Each layer is reported with ok / warn /
fail and a concrete reason. Exits non-zero when any layer fails.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := newDoctorReport()

			// config.toml
			cfg, cfgPath, err := config.Load(f.cfgPath)
			if err != nil {
				report.fail("config.toml", err.Error())
			} else if cfgPath == "" {
				report.warn("config.toml", "no file found — defaults in use")
			} else {
				report.ok("config.toml", clierr.DisplayPath(cfgPath))
				if reasons := cfgschema.ValidateConfig(cfg); len(reasons) > 0 {
					for _, r := range reasons {
						report.warn("config.toml", r)
					}
				}
			}

			// settings.json layers
			cwd, _ := os.Getwd()
			if l, err := clauseSettings.Load(cwd); err != nil {
				report.fail("settings.json", err.Error())
			} else {
				validateLayer(report, "settings.user", l.UserPath, l.User)
				validateLayer(report, "settings.project", l.ProjectPath, l.Project)
				validateLayer(report, "settings.local", l.LocalPath, l.Local)
			}

			report.print()
			if report.hasFail() {
				return clierr.WithHint(
					clierr.Newf("config validate", "%d failed check(s)", report.failCount()),
					"fix the ✗ entries above; ! warnings are non-blocking but worth reviewing")
			}
			return nil
		},
	}
}

func newConfigSchemaCmd() *cobra.Command {
	c := &cobra.Command{
		Use:       "schema [config|settings]",
		Short:     "Emit a JSON schema for the named file",
		Args:      cobra.ExactValidArgs(1),
		ValidArgs: []string{"config", "settings"},
		Long: `Emits a JSON schema document on stdout. Pipe it to a
file and reference it from your config / settings to get IDE
autocomplete + validation:

  biu config schema settings > ~/.biumind/settings.schema.json

Then add ` + "`\"$schema\": \"./settings.schema.json\"`" + ` as the
first key of your settings.json (VS Code, IntelliJ, neovim with
yaml-language-server all pick this up).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var schema any
			switch args[0] {
			case "config":
				schema = cfgschema.ConfigSchema()
			case "settings":
				schema = cfgschema.SettingsSchema()
			default:
				return clierr.Newf("config schema", "unknown target %q (want `config` or `settings`)", args[0])
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(schema)
		},
	}
	return c
}

// validateLayer reports on one settings.json layer in the validate
// pipeline. Empty path / nil settings → silently skipped (the layer
// is optional).
func validateLayer(r *doctorReport, name, path string, s *clauseSettings.Settings) {
	if path == "" {
		return
	}
	if s == nil {
		// Layer file exists per Load() bookkeeping but parsed to nil —
		// treat as warn rather than fail since the loader already
		// surfaced any hard error.
		r.warn(name, "loaded but produced no settings ("+clierr.DisplayPath(path)+")")
		return
	}
	r.ok(name, clierr.DisplayPath(path))
	if reasons := cfgschema.ValidateSettings(s); len(reasons) > 0 {
		for _, reason := range reasons {
			r.warn(name, reason)
		}
	}
}

// printConfigToml dumps the resolved Config as TOML (round-trip via
// the same encoder writeConfig uses, deterministic ordering).
func printConfigToml(cfg *config.Config) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[default]\nmode = %q\nprovider = %q\nmodel = %q\n\n",
		cfg.Default.Mode, cfg.Default.Provider, cfg.Default.Model)
	if cfg.Relay.Endpoint != "" || cfg.Relay.VirtualKey != "" {
		fmt.Fprintf(&b, "[model-relay]\nendpoint = %q\nvirtual_key = %q\n\n",
			cfg.Relay.Endpoint, redactSecret(cfg.Relay.VirtualKey))
	}
	if cfg.Permissions.Mode != "" {
		fmt.Fprintf(&b, "[permissions]\nmode = %q\n\n", cfg.Permissions.Mode)
	}
	if cfg.Search.Mode != "" {
		fmt.Fprintf(&b, "[search]\nmode = %q\n", cfg.Search.Mode)
		if cfg.Search.SearxNGURL != "" {
			fmt.Fprintf(&b, "searxng_url = %q\n", cfg.Search.SearxNGURL)
		}
		fmt.Fprintln(&b)
	}
	for name, p := range cfg.Providers {
		fmt.Fprintf(&b, "[providers.%s]\n", name)
		if p.APIKey != "" {
			fmt.Fprintf(&b, "api_key = %q\n", redactSecret(p.APIKey))
		}
		if p.Endpoint != "" {
			fmt.Fprintf(&b, "endpoint = %q\n", p.Endpoint)
		}
		fmt.Fprintln(&b)
	}
	if cfg.Auth.AuthorizeURL != "" || cfg.Auth.ClientID != "" {
		fmt.Fprintf(&b, "[auth]\nauthorize_url = %q\ntoken_url = %q\nclient_id = %q\n",
			cfg.Auth.AuthorizeURL, cfg.Auth.TokenURL, cfg.Auth.ClientID)
		if len(cfg.Auth.Scopes) > 0 {
			fmt.Fprintf(&b, "scopes = [%s]\n", strings.Join(quoteAll(cfg.Auth.Scopes), ", "))
		}
		fmt.Fprintln(&b)
	}
	for _, m := range cfg.MCPServers {
		fmt.Fprintf(&b, "[[mcp_servers]]\nname = %q\ncommand = %q\n", m.Name, m.Command)
		if len(m.Args) > 0 {
			fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quoteAll(m.Args), ", "))
		}
		if m.Cwd != "" {
			fmt.Fprintf(&b, "cwd = %q\n", clierr.DisplayPath(m.Cwd))
		}
		fmt.Fprintln(&b)
	}
	_, err := os.Stdout.WriteString(b.String())
	return err
}

// printSettings dumps every loaded settings.json layer with a clear
// header. JSON is pretty-printed; secrets are not redacted because
// settings shouldn't carry them — but `env` values get the redact
// treatment as a defence in depth.
func printSettings(l *clauseSettings.Layered) error {
	for _, layer := range []struct {
		name string
		path string
		s    *clauseSettings.Settings
	}{
		{"user", l.UserPath, l.User},
		{"project", l.ProjectPath, l.Project},
		{"local", l.LocalPath, l.Local},
	} {
		if layer.s == nil {
			fmt.Fprintf(os.Stderr, "# %-7s  (not present)\n", layer.name)
			continue
		}
		fmt.Fprintf(os.Stderr, "# %-7s  %s\n", layer.name, clierr.DisplayPath(layer.path))
		// Make a copy with env values redacted before serialising.
		clone := *layer.s
		if len(clone.Env) > 0 {
			redacted := make(map[string]string, len(clone.Env))
			for k, v := range clone.Env {
				redacted[k] = redactSecret(v)
			}
			clone.Env = redacted
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(&clone); err != nil {
			return err
		}
	}
	return nil
}

// redactSecret returns a fingerprint-style summary of secret-looking
// strings. We don't try to detect "is this a secret" — every value
// we print here is a known credential field.
func redactSecret(s string) string {
	switch {
	case s == "":
		return ""
	case len(s) <= 8:
		return "***"
	default:
		return s[:4] + "…" + s[len(s)-4:]
	}
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = fmt.Sprintf("%q", v)
	}
	return out
}

// pathRel is an unused helper kept for symmetry with display logic
// elsewhere — silences the "unused import" linter when filepath is
// otherwise only referenced indirectly via config helpers. Removed
// once all path display goes through clierr.
var _ = filepath.Join
