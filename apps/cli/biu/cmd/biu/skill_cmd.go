// `biu skill` — local file-based + cloud-synced Skills management.
//
//   biu skill list                         list cloud skills (org-scoped)
//   biu skill pull                         cloud → ~/.biumind/skills/
//   biu skill push <id>                    local SKILL.md → cloud (create or update)
//   biu skill diff <id>                    compare local vs cloud hash
//   biu skill run <id> [args]              expand SKILL.md $ARGS, write to stdout
//   biu skill enable <id> --agent <uuid>   toggle on for an agent
//   biu skill disable <id> --agent <uuid>  toggle off
//
// `pull / push / diff / list / enable / disable` need a runtime URL.
// Set BIUMIND_RUNTIME_URL or pass --runtime-url. The Bearer token
// is the same one /v1/agents/run consumes; reuse --token / config
// model-relay.virtual_key.
//
// `run` is offline-only — useful in scripts and CI to render the
// expanded body without touching the model.

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/skillmarket"
	"github.com/biumind/biumind/apps/cli/biu/internal/skillpack"
	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
	"github.com/biumind/biumind/apps/cli/biu/internal/skillsync"
	"github.com/spf13/cobra"
)

func newSkillCmd(f *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage SKILL.md files locally and on the cloud",
	}
	cmd.PersistentFlags().StringVar(&f.runtimeURL, "runtime-url", "",
		"Runtime endpoint (overrides BIUMIND_RUNTIME_URL); needed for pull / push / list / toggle")

	cmd.AddCommand(
		newSkillListCmd(f),
		newSkillPullCmd(f),
		newSkillPushCmd(f),
		newSkillDiffCmd(f),
		newSkillRunCmd(),
		newSkillInstallCmd(f),
		newSkillPackCmd(),
		newSkillUnpackCmd(),
		newSkillKeygenCmd(),
		newSkillSignCmd(),
		newSkillVerifyCmd(),
		newSkillToggleCmd(f, true),  // enable
		newSkillToggleCmd(f, false), // disable
	)
	return cmd
}

// ─── keygen / sign / verify (PS4.4) ─────────────────────────

func newSkillKeygenCmd() *cobra.Command {
	var prefix string
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an ed25519 keypair for signing .biuskill archives",
		Long: `keygen writes two PEM files:
  <prefix>.key      private key (PKCS#8) — keep secret
  <prefix>.key.pub  public key (SPKI)    — publish alongside marketplace listing

Default prefix: ./biuskill (so files are biuskill.key + biuskill.key.pub).
Use openssl pkey -in <file>.key -text -noout to inspect.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if prefix == "" {
				prefix = "biuskill"
			}
			privPath := prefix + ".key"
			pubPath := prefix + ".key.pub"
			// Refuse to overwrite a private key — silent overwrite of
			// signing material is the kind of mistake that's worth a
			// loud error.
			if _, err := os.Stat(privPath); err == nil {
				return clierr.Newf("skill keygen",
					"%s already exists — refusing to overwrite (delete or pass --prefix)", privPath)
			}
			privPEM, pubPEM, err := skillpack.GenerateKeypair()
			if err != nil {
				return clierr.Wrapf("skill keygen", err, "")
			}
			if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
				return clierr.Wrapf("skill keygen", err, "write %s", privPath)
			}
			if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
				return clierr.Wrapf("skill keygen", err, "write %s", pubPath)
			}
			fmt.Printf("+ %s (private; mode 0600)\n", privPath)
			fmt.Printf("+ %s (public)\n", pubPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "",
		"Output filename prefix (default: biuskill)")
	return cmd
}

func newSkillSignCmd() *cobra.Command {
	var keyPath string
	cmd := &cobra.Command{
		Use:   "sign <pack.biuskill>",
		Short: "Sign a .biuskill archive with an ed25519 private key (writes <pack>.sig)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyPath == "" {
				return clierr.Newf("skill sign", "--key <path> required")
			}
			archive, err := os.ReadFile(args[0])
			if err != nil {
				return clierr.Wrapf("skill sign", err, "read %s", args[0])
			}
			keyPEM, err := os.ReadFile(keyPath)
			if err != nil {
				return clierr.Wrapf("skill sign", err, "read key %s", keyPath)
			}
			priv, err := skillpack.ParsePrivateKey(keyPEM)
			if err != nil {
				return clierr.Wrapf("skill sign", err, "parse key")
			}
			sig, err := skillpack.Sign(archive, priv)
			if err != nil {
				return clierr.Wrapf("skill sign", err, "")
			}
			sigPath := args[0] + ".sig"
			if err := os.WriteFile(sigPath, []byte(sig), 0o644); err != nil {
				return clierr.Wrapf("skill sign", err, "write %s", sigPath)
			}
			fmt.Printf("+ %s (sig=%s…)\n", sigPath, sig[:24])
			return nil
		},
	}
	cmd.Flags().StringVar(&keyPath, "key", "",
		"Path to the PEM-encoded ed25519 private key (required)")
	return cmd
}

func newSkillVerifyCmd() *cobra.Command {
	var (
		pubPath string
		sigPath string
	)
	cmd := &cobra.Command{
		Use:   "verify <pack.biuskill>",
		Short: "Verify a .biuskill archive against a publisher public key + .sig sidecar",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pubPath == "" {
				return clierr.Newf("skill verify", "--pubkey <path> required")
			}
			archive, err := os.ReadFile(args[0])
			if err != nil {
				return clierr.Wrapf("skill verify", err, "read %s", args[0])
			}
			if sigPath == "" {
				sigPath = args[0] + ".sig"
			}
			sigRaw, err := os.ReadFile(sigPath)
			if err != nil {
				return clierr.Wrapf("skill verify", err, "read sig %s", sigPath)
			}
			pubPEM, err := os.ReadFile(pubPath)
			if err != nil {
				return clierr.Wrapf("skill verify", err, "read pubkey %s", pubPath)
			}
			pub, err := skillpack.ParsePublicKey(pubPEM)
			if err != nil {
				return clierr.Wrapf("skill verify", err, "parse pubkey")
			}
			sig := strings.TrimSpace(string(sigRaw))
			if err := skillpack.Verify(archive, sig, pub); err != nil {
				return clierr.Wrapf("skill verify", err, "")
			}
			fmt.Printf("✓ %s verified against %s\n", args[0], pubPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&pubPath, "pubkey", "",
		"Path to the publisher's PEM-encoded ed25519 public key (required)")
	cmd.Flags().StringVar(&sigPath, "sig", "",
		"Signature path (default: <pack>.sig)")
	return cmd
}

// ─── pack / unpack (.biuskill bundles) ──────────────────────

func newSkillPackCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Build a deterministic .biuskill archive from a skill directory",
		Long: `pack walks <dir> for SKILL.md + scripts/ + references/ + assets/
and writes a .biuskill (zip) at the path given by -o (default
<dir>.biuskill). Mtime / order / mode are pinned so two packs of the
same source bytes match — required by PS4.4 ed25519 signing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			res, err := skillpack.Pack(src)
			if err != nil {
				return clierr.Wrapf("skill pack", err, src)
			}
			outPath := out
			if outPath == "" {
				outPath = strings.TrimRight(src, "/") + ".biuskill"
			}
			if err := os.WriteFile(outPath, res.Bytes, 0o644); err != nil {
				return clierr.Wrapf("skill pack", err, "write %s", outPath)
			}
			fmt.Printf("+ %s (%d entries, %d bytes, sha256=%s)\n",
				outPath, res.EntryCount, len(res.Bytes), res.Sha256[:16])
			for _, p := range res.Skipped {
				fmt.Fprintf(os.Stderr, "  skipped %s (not in SKILL.md / scripts / references / assets)\n", p)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "",
		"Output path (default: <dir>.biuskill)")
	return cmd
}

func newSkillUnpackCmd() *cobra.Command {
	var dst string
	cmd := &cobra.Command{
		Use:   "unpack <file.biuskill>",
		Short: "Extract a .biuskill archive into a directory for inspection / editing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return clierr.Wrapf("skill unpack", err, "read %s", args[0])
			}
			outDir := dst
			if outDir == "" {
				// Default: strip the extension; "foo.biuskill" → "foo".
				outDir = strings.TrimSuffix(args[0], ".biuskill")
				if outDir == args[0] {
					outDir = strings.TrimSuffix(args[0], ".zip")
				}
				if outDir == args[0] {
					outDir = args[0] + ".unpacked"
				}
			}
			res, err := skillpack.Unpack(raw, outDir)
			if err != nil {
				return clierr.Wrapf("skill unpack", err, "")
			}
			fmt.Printf("+ %s (%d files, sha256=%s)\n",
				outDir, len(res.Written), res.Sha256[:16])
			for _, p := range res.Written {
				fmt.Printf("  %s\n", p)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dst, "output", "o", "",
		"Output directory (default: <file> with .biuskill stripped)")
	return cmd
}

// ─── install (URL or .biuskill local file) ──────────────────

func newSkillInstallCmd(f *rootFlags) *cobra.Command {
	var (
		agentID string
		pin     bool
		dryRun  bool
	)
	cmd := &cobra.Command{
		Use:   "install <url|path-to-biuskill>",
		Short: "Install a skill from a URL (HTTPS SKILL.md) or local .biuskill bundle",
		Long: `install detects the source by argument shape:

  https://...        — server fetches the SKILL.md and registers it (source=imported)
  ./my-skill.biuskill— uploaded as base64; server unzips and parses (source=user)
  inline             — use 'biu skill push <id>' instead for local-authored skills

The skill registers as status='active'. Pass --agent <uuid> [--pin] to bind to
an agent in the same call. --dry-run resolves the source (URL rewrite or
local-zip parse) and prints what WOULD be installed without contacting
the server — handy for verifying marketplace adapters or zip layout
before commit.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := args[0]
			// In dry-run we don't need a runtime URL — but every other
			// path does. Defer the client creation so dry-run works
			// without BIUMIND_RUNTIME_URL set.
			var c *skillsync.Client
			if !dryRun {
				cli, err := skillClient(f)
				if err != nil {
					return err
				}
				c = cli
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			var sk *skillsync.Skill
			var err error
			switch {
			case strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://"):
				// Marketplace catalog rewrite — turn lobehub.com /
				// skills.sh / claude-plugins.dev catalog pages into
				// direct SKILL.md URLs the runtime can actually fetch.
				resolved, adapter, rerr := skillmarket.Resolve(arg)
				if rerr != nil {
					return clierr.Wrapf("skill install", rerr,
						"resolve marketplace URL %s", arg)
				}
				if adapter != "" && resolved != arg {
					fmt.Fprintf(os.Stderr,
						"  via %s adapter: %s → %s\n", adapter, arg, resolved)
				}
				if dryRun {
					fmt.Printf("dry-run: would POST /v1/skills with url=%s\n", resolved)
					if agentID != "" {
						fmt.Printf("dry-run: would bind to agent %s%s\n",
							agentID, ifElse(pin, " (pinned)", ""))
					}
					return nil
				}
				sk, err = c.InstallURL(ctx, resolved, agentID, pin)
			case strings.HasSuffix(arg, ".biuskill") || strings.HasSuffix(arg, ".zip"):
				raw, readErr := os.ReadFile(arg)
				if readErr != nil {
					return clierr.Wrapf("skill install", readErr, "read %s", arg)
				}
				if dryRun {
					// Parse the archive locally so the user sees what the
					// server would extract without burning a round-trip
					// (or hitting a stale runtime in CI).
					return printZipDryRun(arg, raw, agentID, pin)
				}
				sk, err = c.InstallZip(ctx, base64.StdEncoding.EncodeToString(raw), agentID, pin)
			default:
				return clierr.Newf("skill install",
					"can't auto-detect source kind for %q (expected https:// URL or .biuskill / .zip path)", arg)
			}
			if err != nil {
				return clierr.Wrapf("skill install", err, "")
			}
			fmt.Printf("+ %s (skill_id=%s, source=%s)\n",
				sk.Identifier, sk.ID, sk.Source)
			if agentID != "" {
				fmt.Printf("  bound to agent %s%s\n", agentID, ifElse(pin, " (pinned)", ""))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "",
		"Agent UUID to enable the skill on after install")
	cmd.Flags().BoolVar(&pin, "pin", false,
		"Pin the skill on --agent so its body is always inlined into the system prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Resolve the source (URL rewrite or zip parse) and print what would be installed; no server call")
	return cmd
}

// printZipDryRun parses a .biuskill archive locally and prints a
// summary of what the server would extract (file list + content
// hash). Mirrors the skillpack installer-side parse but without the
// runtime registry write — the user gets to see what's about to land
// before any cloud state changes.
func printZipDryRun(path string, raw []byte, agentID string, pin bool) error {
	tmp, err := os.MkdirTemp("", "biu-dryrun-*")
	if err != nil {
		return clierr.Wrapf("skill install", err, "mktemp")
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	res, err := skillpack.Unpack(raw, tmp)
	if err != nil {
		return clierr.Wrapf("skill install", err, "parse %s", path)
	}
	fmt.Printf("dry-run: %s (%d files, sha256=%s)\n",
		path, len(res.Written), res.Sha256[:16])
	for _, f := range res.Written {
		fmt.Printf("  %s\n", f)
	}
	if agentID != "" {
		fmt.Printf("dry-run: would bind to agent %s%s\n",
			agentID, ifElse(pin, " (pinned)", ""))
	}
	return nil
}

func ifElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ─── list ────────────────────────────────────────────────────

func newSkillListCmd(f *rootFlags) *cobra.Command {
	var (
		statusFlag string
		sourceFlag string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List skills available on the cloud (filtered by --status / --source)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := skillClient(f)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			rows, err := c.List(ctx, skillsync.ListOptions{
				Status: statusFlag,
				Source: sourceFlag,
			})
			if err != nil {
				return clierr.Wrapf("skill list", err, "")
			}
			if len(rows) == 0 {
				fmt.Println("(no skills)")
				return nil
			}
			fmt.Printf("%-32s %-12s %-10s %s\n", "IDENTIFIER", "SOURCE", "STATUS", "DESCRIPTION")
			for _, s := range rows {
				fmt.Printf("%-32s %-12s %-10s %s\n",
					trunc(s.Identifier, 32), s.Source, s.Status, trunc(s.Description, 60))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&statusFlag, "status", "",
		"filter by status (active / disabled / staged / staged_org / suspended)")
	cmd.Flags().StringVar(&sourceFlag, "source", "",
		"filter by source (bundled / org / user / marketplace / imported)")
	return cmd
}

// ─── pull ────────────────────────────────────────────────────

func newSkillPullCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Sync cloud skills into ~/.biumind/skills/ (writes SKILL.md per skill)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := skillClient(f)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return clierr.Wrapf("skill pull", err, "user home")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			results, err := skillsync.Pull(ctx, c, home)
			if err != nil {
				return clierr.Wrapf("skill pull", err, "")
			}
			conflicts := 0
			for _, r := range results {
				marker := actionMarker(string(r.Action))
				fmt.Printf("%s %-20s %s\n", marker, r.Identifier, trunc(r.LocalPath, 60))
				if r.Action == skillsync.PullConflict {
					conflicts++
				}
			}
			if conflicts > 0 {
				fmt.Fprintf(os.Stderr,
					"\n%d conflict(s); run `biu skill diff <name>` to inspect, "+
						"then `biu skill push <name>` to upload local or delete the "+
						"local file to accept cloud.\n", conflicts)
				// Exit non-zero so scripts notice.
				return errors.New("pull completed with conflicts")
			}
			return nil
		},
	}
}

// ─── push ────────────────────────────────────────────────────

func newSkillPushCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "push <identifier>",
		Short: "Upload a local SKILL.md to the cloud (create or update)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := skillClient(f)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			res, err := skillsync.Push(ctx, c, home, args[0])
			if err != nil {
				return clierr.Wrapf("skill push", err, args[0])
			}
			fmt.Printf("%s %s (skill_id=%s)\n",
				actionMarker(string(res.Action)), res.Identifier, res.Skill.ID)
			return nil
		},
	}
}

// ─── diff ────────────────────────────────────────────────────

func newSkillDiffCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "diff <identifier>",
		Short: "Compare local SKILL.md hash vs the cloud copy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := skillClient(f)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			d, err := skillsync.Diff(ctx, c, home, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("identifier:  %s\n", d.Identifier)
			fmt.Printf("status:      %s\n", d.Action)
			if d.LocalHash != "" {
				fmt.Printf("local hash:  %s\n", d.LocalHash[:16])
			}
			if d.CloudHash != "" {
				fmt.Printf("cloud hash:  %s\n", d.CloudHash[:16])
			}
			if d.Action == "diverged" {
				return errors.New("diverged — push or delete local to resolve")
			}
			return nil
		},
	}
}

// ─── run (offline expand) ───────────────────────────────────

func newSkillRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <identifier> [args...]",
		Short: "Expand a local SKILL.md body with $ARGS substitution and write to stdout (no LLM)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			reg, err := skills.Load(cwd)
			if err != nil {
				return err
			}
			rs, ok := reg.Lookup(args[0])
			if !ok {
				return clierr.Newf("skill run", "skill %q not found in ~/.biumind/skills/ or <cwd>/.biumind/skills/", args[0])
			}
			expandArgs := strings.Join(args[1:], " ")
			body, err := rs.Run(cmd.Context(), expandArgs)
			if err != nil {
				return err
			}
			fmt.Println(body)
			return nil
		},
	}
}

// ─── toggle (enable / disable) ──────────────────────────────

func newSkillToggleCmd(f *rootFlags, enable bool) *cobra.Command {
	verb := "enable"
	if !enable {
		verb = "disable"
	}
	var (
		agentID string
		pin     bool
	)
	cmd := &cobra.Command{
		Use:   verb + " <skill-id>",
		Short: verb + " a skill on a specific agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentID == "" {
				return clierr.Newf("skill "+verb, "--agent <uuid> required")
			}
			c, err := skillClient(f)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			as, err := c.Toggle(ctx, args[0], skillsync.ToggleRequest{
				AgentID:   agentID,
				IsEnabled: enable,
				Pinned:    enable && pin,
			})
			if err != nil {
				return err
			}
			fmt.Printf("%s skill_id=%s agent=%s enabled=%v pinned=%v\n",
				actionMarker(verb), as.SkillID, as.AgentID, as.IsEnabled, as.Pinned)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "", "Agent UUID to toggle on / off (required)")
	if enable {
		cmd.Flags().BoolVar(&pin, "pin", false, "also pin so the body inlines into every system prompt")
	}
	return cmd
}

// ─── helpers ────────────────────────────────────────────────

// skillClient builds the runtime client from flag → env → config
// in that priority order. Returns a friendly error when no source
// of a runtime URL is found.
func skillClient(f *rootFlags) (*skillsync.Client, error) {
	url := f.runtimeURL
	if url == "" {
		url = os.Getenv("BIUMIND_RUNTIME_URL")
	}
	if url == "" {
		return nil, clierr.Newf("skill",
			"runtime URL unset; pass --runtime-url or set BIUMIND_RUNTIME_URL")
	}
	token := f.token
	if token == "" {
		// Fall back to the same place the engine path looks: model-relay.virtual_key.
		if cfg, _, err := config.Load(f.cfgPath); err == nil {
			token = cfg.Relay.VirtualKey
		}
	}
	return skillsync.New(url, token), nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func actionMarker(a string) string {
	switch a {
	case "created":
		return "+"
	case "updated":
		return "~"
	case "unchanged", "in-sync":
		return "="
	case "conflict", "diverged":
		return "!"
	case "enable":
		return "✓"
	case "disable":
		return "✗"
	}
	return "•"
}
