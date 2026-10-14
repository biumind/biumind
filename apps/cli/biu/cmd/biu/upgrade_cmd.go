// `biu upgrade` (alias `biu update`) — upgrade biu in-place using
// whatever install method shipped this binary.
//
// Same routing as the REPL's /upgrade run (install.Route):
//
//	homebrew / go install / snap → delegate to the owning package
//	                               manager (`brew upgrade …` etc)
//	client-managed (~/.local/bin) → refuse — the desktop client owns
//	                               that install plane and auto-updates
//	manual / unknown             → self-update: download tar.gz,
//	                               verify SHA-256, atomic swap, keep
//	                               .old for rollback (internal/selfupdate)
//
// Subcommands:
//
//	biu upgrade              — check + install a newer release
//	biu upgrade check        — report the latest release without installing
//	biu upgrade skip [ver]   — stop notifying about a version (no arg =
//	                           the latest version seen by the last check)

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/install"
	"github.com/biumind/biumind/apps/cli/biu/internal/selfupdate"
	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
	"github.com/spf13/cobra"
)

// Test injection points (same pattern as updatecheck.Options.HTTPDo):
// swapping these vars covers every branch without real network or a
// relocated binary.
var (
	upgradeCheckFn    = updatecheck.CheckWithFallback
	upgradeApplyFn    = selfupdate.Apply
	upgradeDetectFn   = install.Detect
	upgradeDelegateFn = install.RunDelegated
)

func newUpgradeCmd(f *rootFlags) *cobra.Command {
	c := &cobra.Command{
		Use:     "upgrade",
		Aliases: []string{"update"},
		Short:   "Upgrade biu to the latest release",
		Long: `Check for a newer biu release and install it.

How the upgrade happens depends on how this binary was installed:
homebrew / go install / snap delegate to their package manager;
a desktop-client-managed install (~/.local/bin) is left to the
client; a manual install self-updates by downloading the verified
release archive and atomically swapping the binary (the previous
binary is kept as biu.old for rollback).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if updatecheck.ShouldSkipVersion(version) {
				return clierr.Newf("upgrade",
					"self-update is not available for this build (%s): dev and desktop-client-managed builds don't self-update",
					version)
			}
			exe, err := os.Executable()
			if err != nil {
				return clierr.Wrapf("upgrade", err, "resolve executable path")
			}
			method, hint := upgradeDetectFn(exe, version)

			switch install.Route(method) {
			case install.ActionRefuse:
				return clierr.Newf("upgrade",
					"this biu was installed by the BiuMind desktop client, which manages its updates — nothing to do")
			case install.ActionDelegate:
				fmt.Fprintf(cmd.ErrOrStderr(), "[biu] delegating to %s: %s\n", method, hint)
				out, err := upgradeDelegateFn(cmd.Context(), hint)
				if out != "" {
					fmt.Fprintln(cmd.OutOrStdout(), out)
				}
				if err != nil {
					return clierr.Wrapf("upgrade", err, "run %q", hint)
				}
				fmt.Fprintln(cmd.ErrOrStderr(),
					"[biu] upgrade ran successfully — restart biu to load the new binary")
				return nil
			}

			// manual / unknown install → self-update.
			res, err := upgradeCheckFn(cmd.Context(), updatecheck.Options{
				CurrentVersion: version,
				ManifestURL:    upgradeManifestURL(f),
			})
			if err != nil {
				return clierr.Wrapf("upgrade", err, "check for updates")
			}
			if !res.Newer {
				fmt.Fprintf(cmd.OutOrStdout(), "already up to date (biu %s, %s)\n",
					res.Current, res.Source)
				return nil
			}
			if res.AssetURL == "" {
				return clierr.WithHint(
					clierr.Newf("upgrade", "no downloadable asset for this platform on %s", res.Source),
					"grab biu "+res.Latest+" from "+res.ReleaseURL)
			}
			if err := upgradeApplyFn(cmd.Context(), selfupdate.Options{
				AssetURL:      res.AssetURL,
				AssetFilename: res.AssetFilename,
				AssetSha256:   res.AssetSha256,
				ChecksumsURL:  res.ChecksumsURL,
			}); err != nil {
				return clierr.Wrapf("upgrade", err, "apply update")
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"biu %s installed (was %s) — restart biu to load it; previous binary kept at %s.old\n",
				res.Latest, res.Current, exe)
			return nil
		},
	}

	c.AddCommand(newUpgradeCheckCmd(f), newUpgradeSkipCmd())
	return c
}

// newUpgradeCheckCmd implements `biu upgrade check`: a live,
// unthrottled check that reports the newest release and refreshes the
// state cache — without installing anything.
func newUpgradeCheckCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check for a newer release without installing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if updatecheck.ShouldSkipVersion(version) {
				return clierr.Newf("upgrade check",
					"update checks are skipped for this build (%s): dev and desktop-client-managed builds don't self-update",
					version)
			}
			res, err := upgradeCheckFn(cmd.Context(), updatecheck.Options{
				CurrentVersion: version,
				ManifestURL:    upgradeManifestURL(f),
			})
			if err != nil {
				return clierr.Wrapf("upgrade check", err, "check for updates")
			}
			_ = updatecheck.RecordCheck(res.Latest)

			out := cmd.OutOrStdout()
			if !res.Newer {
				fmt.Fprintf(out, "up to date (biu %s, %s)\n", res.Current, res.Source)
				return nil
			}
			fmt.Fprintf(out, "biu %s available (current %s)\n", res.Latest, res.Current)
			fmt.Fprintf(out, "  source:  %s\n", res.Source)
			if res.DownloadSource != "" && res.DownloadSource != res.Source {
				fmt.Fprintf(out, "  download via: %s\n", res.DownloadSource)
			}
			fmt.Fprintf(out, "  release: %s\n", res.ReleaseURL)
			fmt.Fprintln(out, "  update:  biu upgrade, or biu upgrade skip to silence this version")
			return nil
		},
	}
}

// newUpgradeSkipCmd implements `biu upgrade skip [version]`. No
// argument → skip the latest version seen by the last check.
func newUpgradeSkipCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skip [version]",
		Short: "Stop notifying about a version (no arg = latest known)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v := ""
			if len(args) > 0 {
				v = strings.TrimSpace(args[0])
			}
			if v == "" {
				state, err := updatecheck.LoadState("")
				if err != nil {
					return clierr.Wrapf("upgrade skip", err, "load state")
				}
				v = state.LatestVersion
				if v == "" {
					return clierr.WithHint(
						clierr.Newf("upgrade skip", "nothing to skip — no version known yet"),
						"run `biu upgrade check` first")
				}
			}
			if err := updatecheck.SkipVersion(v); err != nil {
				return clierr.Wrapf("upgrade skip", err, "save state")
			}
			fmt.Fprintf(cmd.ErrOrStderr(),
				"[biu] biu %s will not be notified about again (a newer release clears this)\n", v)
			return nil
		},
	}
}

// upgradeManifestURL derives the OSS manifest URL from the relay
// endpoint (single-origin addressing), same as the REPL wiring in
// main.go. A broken config must not block an upgrade — fall back to
// GitHub-only.
func upgradeManifestURL(f *rootFlags) string {
	cfg, _, _ := config.Load(f.cfgPath)
	endpoint := firstNonEmpty(f.relayURL, os.Getenv("BIUMIND_MODEL_RELAY_URL"))
	if endpoint == "" && cfg != nil {
		endpoint = cfg.Relay.Endpoint
	}
	return updatecheck.ManifestURLForEndpoint(endpoint)
}
