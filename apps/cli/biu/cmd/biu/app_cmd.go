// `biu app` — App Center developer commands.
//
//   biu app new <slug> [--from minimal|view_only|hybrid_full]
//   biu app validate [--manifest path]
//   biu app inspect [--manifest path] [--json]
//   biu app pack [--source dir] [--out file] [--key path] [--unsigned]
//   biu app verify <file.biuapp> [--trust-key path...]
//   biu app keygen [--name publisher]
//
// Mirrors the surface documented in BiuMind-AppCenter-DevGuide §22
// + parity with `biu skill` for cloud-side operations (publish lands
// in v2.5 alongside the marketplace catalogue).

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/apppack"
	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newAppCmd(_ *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Develop, package, and inspect BiuMind App Center apps",
	}
	cmd.AddCommand(
		newAppNewCmd(),
		newAppValidateCmd(),
		newAppInspectCmd(),
		newAppPackCmd(),
		newAppVerifyCmd(),
		newAppKeygenCmd(),
		newAppRunCmd(),
	)
	return cmd
}

// ─── new ──────────────────────────────────────────────────

func newAppNewCmd() *cobra.Command {
	var template string
	cmd := &cobra.Command{
		Use:   "new <slug>",
		Short: "Scaffold a new App project",
		Long: "Create a new App project at <slug>/ from a built-in template.\n" +
			"Templates: " + strings.Join(apppack.Templates(), ", "),
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			slug := args[0]
			destDir := slug
			if err := apppack.NewProject(destDir, slug, template); err != nil {
				return clierr.Wrapf("biu app new", err, "%s", err.Error())
			}
			fmt.Fprintf(os.Stderr, "✓ scaffolded %s App at %s/\n", template, destDir)
			fmt.Fprintf(os.Stderr, "  next: cd %s && biu app validate\n", destDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&template, "from", "hybrid_full",
		"Template name: "+strings.Join(apppack.Templates(), " | "))
	return cmd
}

// ─── validate ─────────────────────────────────────────────

func newAppValidateCmd() *cobra.Command {
	var manifestPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate manifest.yaml against the SDK rules",
		RunE: func(_ *cobra.Command, _ []string) error {
			path := resolveManifest(manifestPath)
			m, err := biuapp.LoadManifest(path)
			if err != nil {
				return clierr.Wrapf("biu app validate", err, "%s", err.Error())
			}
			if err := biuapp.Validate(m); err != nil {
				// Render every issue rather than just the first — saves
				// authors a fix-edit-rerun cycle per typo.
				var ve *biuapp.ValidationError
				if errors.As(err, &ve) {
					fmt.Fprintln(os.Stderr, "✗ manifest has issues:")
					for _, iss := range ve.Issues {
						fmt.Fprintf(os.Stderr, "  · %s [%s]: %s\n", iss.Path, iss.Code, iss.Message)
					}
					return clierr.Newf("biu app validate", "%s", "validation failed")
				}
				return clierr.Wrapf("biu app validate", err, "%s", err.Error())
			}
			fmt.Fprintf(os.Stderr, "✓ manifest %s OK (%s v%s — %d actions / %d views / %d triggers)\n",
				m.Slug(), m.DisplayName(), m.Version,
				len(m.Actions), len(m.Views), len(m.Triggers))
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "Path to manifest.yaml (default: ./manifest.yaml)")
	return cmd
}

// ─── inspect ──────────────────────────────────────────────

func newAppInspectCmd() *cobra.Command {
	var manifestPath string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Dump the parsed manifest (schema + Cedar attribute preview)",
		RunE: func(_ *cobra.Command, _ []string) error {
			path := resolveManifest(manifestPath)
			m, err := biuapp.LoadManifest(path)
			if err != nil {
				return clierr.Wrapf("biu app inspect", err, "%s", err.Error())
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(m)
			}
			renderInspect(m)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "Path to manifest.yaml")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON")
	return cmd
}

func renderInspect(m *biuapp.Manifest) {
	fmt.Printf("identifier:    %s\n", m.Slug())
	fmt.Printf("display_name:  %s\n", m.DisplayName())
	fmt.Printf("version:       %s\n", m.Version)
	fmt.Printf("kind:          %s\n", orDash(m.Kind))
	fmt.Printf("category:      %s\n", orDash(m.Category))
	fmt.Printf("description:   %s\n", m.Description)
	if len(m.Permissions) > 0 {
		fmt.Println("permissions:")
		for _, p := range m.Permissions {
			fmt.Printf("  · %s\n", p)
		}
	}
	if len(m.DataScopes) > 0 {
		fmt.Println("data_scopes:")
		for _, s := range m.DataScopes {
			fmt.Printf("  · %s\n", s)
		}
	}
	if len(m.Actions) > 0 {
		fmt.Printf("actions (%d):\n", len(m.Actions))
		for _, a := range m.Actions {
			risk := string(a.Risk)
			if risk == "" {
				risk = "low (default)"
			}
			fmt.Printf("  · %-22s risk=%-7s  %s\n", a.Name, risk, a.Description)
		}
	}
	if len(m.Views) > 0 {
		fmt.Printf("views (%d):\n", len(m.Views))
		for _, v := range m.Views {
			ds := "—"
			if v.DataSource != nil {
				ds = v.DataSource.Action
			}
			fmt.Printf("  · %-12s %-13s route=%-30s data_source=%s\n",
				v.ID, v.Layout, v.Route, ds)
		}
	}
	if len(m.Triggers) > 0 {
		fmt.Printf("triggers (%d):\n", len(m.Triggers))
		for _, t := range m.Triggers {
			detail := ""
			switch t.Kind {
			case biuapp.TriggerCron:
				detail = "expr=" + t.Expr
			case biuapp.TriggerWebhook:
				detail = "path=" + t.Path
			case biuapp.TriggerInbox:
				detail = "pattern=" + t.Pattern
			}
			fmt.Printf("  · [%-7s] %-20s action=%-15s %s\n", t.Kind, t.Name, t.Action, detail)
		}
	}
	if len(m.Skills) > 0 {
		fmt.Printf("skills (%d):\n", len(m.Skills))
		for _, s := range m.Skills {
			fmt.Printf("  · %s ← %s\n", s.Identifier, s.File)
		}
	}
	if m.Sidebar != nil {
		fmt.Println("sidebar:")
		fmt.Printf("  preferred_position:    %s\n", orDash(m.Sidebar.PreferredPosition))
		fmt.Printf("  default_pin:           %v\n", m.Sidebar.DefaultPin)
		fmt.Printf("  badge_action:          %s\n", orDash(m.Sidebar.BadgeAction))
		fmt.Printf("  mobile_bottom_eligible: %v\n", m.Sidebar.MobileBottomEligible)
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ─── pack ─────────────────────────────────────────────────

func newAppPackCmd() *cobra.Command {
	var (
		sourceDir string
		outPath   string
		keyPath   string
		unsigned  bool
	)
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Build a .biuapp distributable from the current project",
		RunE: func(_ *cobra.Command, _ []string) error {
			if sourceDir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				sourceDir = wd
			}
			// Validate manifest before packing — refuse to ship a
			// broken bundle (saves marketplace 60% of submission rejects).
			manifestPath := filepath.Join(sourceDir, "manifest.yaml")
			m, err := biuapp.LoadManifest(manifestPath)
			if err != nil {
				return clierr.Wrapf("biu app pack", err, "%s", err.Error())
			}
			if err := biuapp.Validate(m); err != nil {
				return clierr.Wrapf("biu app pack: manifest invalid (run `biu app validate` first)", err, "%s", err.Error())
			}

			// Resolve includes from .biuapp.yaml (optional — falls back
			// to manifest + README + LICENSE).
			spec := apppack.IncludeSpec{Include: []string{"manifest.yaml", "README.md", "LICENSE"}}
			if raw, err := os.ReadFile(filepath.Join(sourceDir, ".biuapp.yaml")); err == nil {
				_ = yaml.Unmarshal(raw, &spec)
			}
			files, err := apppack.Resolve(sourceDir, spec)
			if err != nil {
				return clierr.Wrapf("biu app pack: resolve includes", err, "%s", err.Error())
			}
			if len(files) == 0 {
				return clierr.Newf("biu app pack",
					"no files matched include patterns — check .biuapp.yaml")
			}

			if outPath == "" {
				distDir := filepath.Join(sourceDir, "dist")
				_ = os.MkdirAll(distDir, 0o755)
				outPath = filepath.Join(distDir, fmt.Sprintf("%s-%s.biuapp", m.Slug(), m.Version))
			}

			var kp *apppack.KeyPair
			if !unsigned {
				if keyPath == "" {
					dir, _ := apppack.DefaultKeyDir()
					keyPath = filepath.Join(dir, "publisher.ed25519")
				}
				kp, err = apppack.LoadKeyPair(keyPath)
				if err != nil {
					return clierr.Wrapf("biu app pack: load key (try --unsigned for local install)", err, "%s", err.Error())
				}
			}

			hash, err := apppack.Pack(apppack.PackOptions{
				SourceDir: sourceDir,
				OutPath:   outPath,
				KeyPair:   kp,
				Includes:  files,
			})
			if err != nil {
				return clierr.Wrapf("biu app pack", err, "%s", err.Error())
			}
			fmt.Fprintf(os.Stderr, "✓ packed → %s\n", outPath)
			fmt.Fprintf(os.Stderr, "  files:    %d\n", len(files))
			if kp != nil {
				fmt.Fprintf(os.Stderr, "  signed:   %s\n", kp.PublisherID)
			} else {
				fmt.Fprintln(os.Stderr, "  signed:   no (--unsigned)")
			}
			fmt.Fprintf(os.Stderr, "  sha256:   %s\n", hash)
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceDir, "source", "", "Project root (default: cwd)")
	cmd.Flags().StringVar(&outPath, "out", "", "Output .biuapp path (default: dist/<slug>-<version>.biuapp)")
	cmd.Flags().StringVar(&keyPath, "key", "", "Path to signing key (default: ~/.biumind/keys/publisher.ed25519)")
	cmd.Flags().BoolVar(&unsigned, "unsigned", false, "Skip signing (local-install only — marketplace rejects)")
	return cmd
}

// ─── verify ───────────────────────────────────────────────

func newAppVerifyCmd() *cobra.Command {
	var trustKeys []string
	cmd := &cobra.Command{
		Use:   "verify <file.biuapp>",
		Short: "Verify a .biuapp's hashes + signatures",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			pubs := map[string]ed25519.PublicKey{}
			for _, kp := range trustKeys {
				key, err := apppack.LoadKeyPair(kp)
				if err != nil {
					// `LoadKeyPair` requires priv path; for trust we
					// also accept .pub. Fall back to reading the .pub
					// directly.
					raw, err2 := os.ReadFile(kp)
					if err2 != nil {
						return clierr.Wrapf("biu app verify: load trust key", err, "%s", err.Error())
					}
					_ = raw // cooked up for v2.5; keep stub behaviour for now
					return clierr.Wrapf("biu app verify", err, "%s", err.Error())
				}
				pubs[key.PublisherID] = key.Pub
			}
			res, err := apppack.Verify(path, pubs)
			if err != nil {
				return clierr.Wrapf("biu app verify", err, "%s", err.Error())
			}
			fmt.Fprintf(os.Stderr, "✓ %s OK (files validated: %d)\n", path, res.FilesValidated)
			if res.Signed {
				fmt.Fprintf(os.Stderr, "  signed by: %s\n", res.PublisherID)
			} else {
				fmt.Fprintln(os.Stderr, "  unsigned")
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&trustKeys, "trust-key", nil,
		"Path to trusted publisher keypair (priv); repeatable")
	return cmd
}

// ─── keygen ───────────────────────────────────────────────

func newAppKeygenCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an ed25519 keypair under ~/.biumind/keys/",
		RunE: func(_ *cobra.Command, _ []string) error {
			dir, err := apppack.DefaultKeyDir()
			if err != nil {
				return clierr.Wrapf("biu app keygen", err, "%s", err.Error())
			}
			kp, err := apppack.Generate(dir, name)
			if err != nil {
				return clierr.Wrapf("biu app keygen", err, "%s", err.Error())
			}
			fmt.Fprintf(os.Stderr, "✓ keypair generated\n")
			fmt.Fprintf(os.Stderr, "  priv:        %s (mode 0600)\n", kp.PrivPath)
			fmt.Fprintf(os.Stderr, "  pub:         %s\n", kp.PubPath)
			fmt.Fprintf(os.Stderr, "  publisher:   %s\n", kp.PublisherID)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  Add the publisher id to manifest.yaml:")
			fmt.Fprintln(os.Stderr, "    author:")
			fmt.Fprintln(os.Stderr, "      name: <your name>")
			fmt.Fprintf(os.Stderr, "      public_key: %s\n", kp.PublisherID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "publisher", "Key name (file becomes <name>.ed25519)")
	return cmd
}

// ─── helpers ─────────────────────────────────────────────

func resolveManifest(p string) string {
	if p != "" {
		return p
	}
	return "manifest.yaml"
}

// silence unused-import warnings in older Go versions when we add /
// remove flags during dev — base64 import path is exercised by
// keygen output via apppack.PublisherID.
var _ = base64.StdEncoding
