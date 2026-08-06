// `biu doctor` — comprehensive self-check that's the first thing
// every user reaches for when something feels broken. Validates
// config + provider reachability + filesystem layout + external
// tool availability + auth backend, prints a colour-tagged
// checklist, and exits non-zero if anything failed.

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
	"github.com/biumind/biumind/apps/cli/biu/internal/secretstore"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/spf13/cobra"
)

func newDoctorCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run a full self-check: config, providers, tools, perms",
		RunE: func(cmd *cobra.Command, args []string) error {
			report := newDoctorReport()

			// ── Config ──────────────────────────────
			cfg, cfgPath, err := config.Load(f.cfgPath)
			if err != nil {
				report.fail("config", err.Error())
				report.print()
				return err
			}
			report.ok("config", or(cfgPath, "(defaults only)"))

			// File mode 0600 check — config holds API keys.
			if cfgPath != "" {
				if st, err := os.Stat(cfgPath); err == nil {
					mode := st.Mode().Perm()
					if mode&0o077 != 0 {
						report.warn("config perm",
							fmt.Sprintf("%v — should be 0600 (chmod 600 %s)", mode, cfgPath))
					} else {
						report.ok("config perm", "0600")
					}
				}
			}

			// ── Mode + provider ─────────────────────
			mode := firstNonEmpty(f.mode, cfg.Default.Mode, string(client.ModeCloud))
			report.ok("mode", mode)
			report.ok("model", cfg.Default.Model)
			report.ok("perm mode", cfg.Permissions.Mode)

			ctx := cmd.Context()
			if client.Mode(mode) == client.ModeDirect {
				if err := checkDirectProvider(ctx, cfg, report); err != nil {
					report.print()
					return err
				}
			} else {
				checkRelayProvider(ctx, f, cfg, report)
			}

			// ── Filesystem layout ───────────────────
			home, _ := os.UserHomeDir()
			for _, dir := range []string{
				filepath.Join(home, ".biu"),
				filepath.Join(home, ".biumind"),
			} {
				if st, err := os.Stat(dir); err == nil {
					mode := st.Mode().Perm()
					if mode&0o022 != 0 {
						report.warn(dir,
							fmt.Sprintf("%v — world/group writable; chmod 755 recommended", mode))
					} else {
						report.ok(dir, "ok")
					}
				} else if os.IsNotExist(err) {
					report.warn(dir, "missing — run `biu init` to create")
				}
			}

			// ── External tool detection ─────────────
			for _, bin := range []string{"git", "rg", "gopls"} {
				if path, err := exec.LookPath(bin); err == nil {
					report.ok(bin, path)
				} else {
					report.warn(bin, "not in PATH (some tools degrade gracefully)")
				}
			}
			// Sandbox tooling — platform specific.
			switch runtime.GOOS {
			case "darwin":
				if path, err := exec.LookPath("sandbox-exec"); err == nil {
					report.ok("sandbox-exec", path)
				} else {
					report.warn("sandbox-exec", "not in PATH — Bash will run unsandboxed")
				}
			case "linux":
				if path, err := exec.LookPath("bwrap"); err == nil {
					report.ok("bwrap", path)
				} else {
					report.warn("bwrap", "not in PATH — Bash will run unsandboxed (apt install bubblewrap)")
				}
			}

			// ── Settings layer ──────────────────────
			cwd, _ := os.Getwd()
			if l, err := clauseSettings.Load(cwd); err == nil {
				if l.UserPath != "" {
					report.ok("settings.user", l.UserPath)
				}
				if l.ProjectPath != "" {
					report.ok("settings.project", l.ProjectPath)
				}
				if l.LocalPath != "" {
					report.ok("settings.local", l.LocalPath)
				}
				// Surface the merged sandbox config so users can
				// confirm the rules they wrote actually got loaded.
				// A typo in the JSON key (e.g. fsReadDeny vs
				// fs_read_deny) silently produces an empty list;
				// `biu doctor` is the place to catch that.
				if sb := l.MergedSandboxConfig(cwd); sb != nil {
					report.ok("sandbox", fmt.Sprintf(
						"read-deny=%d allow-within=%d write-extra=%d deny-within=%d",
						len(sb.FSReadDeny),
						len(sb.FSReadAllowWithinDeny),
						len(sb.FSWriteAllowExtra),
						len(sb.FSWriteDenyWithinAllow)))
				} else {
					report.ok("sandbox", "default policy (no `sandbox` block in settings.json)")
				}
			}

			// ── Auth backend ────────────────────────
			// Prefers OS keychain when available; reports the
			// fallback so users know where their tokens land.
			if store, err := oauth.Open(""); err == nil {
				switch store.Backend() {
				case "file":
					// Suggest migration when the file backend is in
					// use AND the legacy file already has tokens.
					if t, _ := store.Load(); t.AccessToken != "" {
						report.warn("auth backend",
							fmt.Sprintf("file (%s) — run `biu auth migrate` to move into the OS keychain", store.Path()))
					} else {
						report.ok("auth backend", "file (no tokens)")
					}
				default:
					report.ok("auth backend", store.Backend()+" — "+store.Path())
				}
			}

			// ── Agent-plane secrets backend (R6.4) ──
			// device token + X25519 privkey 落 keychain 还是 0600 文件。
			if p, perr := deviceTokenPath(); perr == nil {
				ss := secretstore.Open(keychainServiceName, deviceTokenAccount, p)
				if ss.Backend() == "file" {
					report.ok("agent-plane secrets", "file (no OS keychain) — "+ss.Path())
				} else {
					report.ok("agent-plane secrets", ss.Backend()+" — device token + privkey in OS keychain")
				}
			}

			report.print()
			if report.hasFail() {
				return clierr.WithHint(
					clierr.Newf("doctor", "%d failed check(s)", report.failCount()),
					"fix the ✗ entries above; ! warnings are degraded-but-OK")
			}
			return nil
		},
	}
}

// ── doctor report helpers ────────────────────────────

type doctorEntry struct {
	level string // ok | warn | fail
	name  string
	body  string
}
type doctorReport struct {
	entries []doctorEntry
}

func newDoctorReport() *doctorReport { return &doctorReport{} }

func (r *doctorReport) ok(name, body string) {
	r.entries = append(r.entries, doctorEntry{"ok", name, body})
}

func (r *doctorReport) warn(name, body string) {
	r.entries = append(r.entries, doctorEntry{"warn", name, body})
}

func (r *doctorReport) fail(name, body string) {
	r.entries = append(r.entries, doctorEntry{"fail", name, body})
}

func (r *doctorReport) hasFail() bool { return r.failCount() > 0 }

func (r *doctorReport) failCount() int {
	n := 0
	for _, e := range r.entries {
		if e.level == "fail" {
			n++
		}
	}
	return n
}

// print emits a colour-tagged checklist to stdout. Levels:
//
//	✓ ok    (green-ish — just bold check)
//	! warn  (yellow)
//	✗ fail  (red)
//
// We use ANSI escapes directly rather than lipgloss to avoid pulling
// the TUI dependency into a 1-shot CLI.
func (r *doctorReport) print() {
	for _, e := range r.entries {
		marker, color := "✓", "\033[32m"
		switch e.level {
		case "warn":
			marker, color = "!", "\033[33m"
		case "fail":
			marker, color = "✗", "\033[31m"
		}
		reset := "\033[0m"
		fmt.Printf("%s%s%s %-20s  %s\n", color, marker, reset, e.name, e.body)
	}
}

// checkDirectProvider validates direct-anthropic config + reaches
// the endpoint. Adds entries to report and returns the first hard
// failure (which the doctor command propagates as its exit code).
func checkDirectProvider(ctx context.Context, cfg *config.Config, r *doctorReport) error {
	providerName := firstNonEmpty(cfg.Default.Provider, "anthropic")
	ps, ok := cfg.Providers[providerName]
	if !ok || ps.APIKey == "" {
		r.fail("provider", "no api_key under [providers."+providerName+"] — run `biu init --mode=direct` or edit ~/.biu/config.toml")
		return clierr.Newf("doctor", "[providers.%s].api_key missing", providerName)
	}
	endpoint := ps.Endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com"
	}
	r.ok("endpoint", endpoint)
	r.ok("api key",
		fmt.Sprintf("%s…%s (len=%d)",
			ps.APIKey[:min4(len(ps.APIKey))],
			ps.APIKey[max0(len(ps.APIKey)-4):], len(ps.APIKey)))
	if err := pingDirect(ctx, endpoint); err != nil {
		r.fail("connectivity", err.Error())
		return err
	}
	r.ok("connectivity", "reachable")
	return nil
}

func checkRelayProvider(ctx context.Context, f *rootFlags, cfg *config.Config, r *doctorReport) {
	relayURL := firstNonEmpty(f.relayURL, os.Getenv("BIUMIND_MODEL_RELAY_URL"), cfg.Relay.Endpoint)
	if relayURL == "" {
		r.warn("model-relay URL", "unset (set [model-relay].endpoint or BIUMIND_MODEL_RELAY_URL)")
		return
	}
	r.ok("model-relay URL", relayURL)
	c := client.New(relayURL, "")
	if err := c.PingHealth(ctx); err != nil {
		r.fail("model-relay healthz", err.Error())
		return
	}
	r.ok("model-relay healthz", "200 OK")
}

// pingDirect probes a direct-Anthropic endpoint by reaching /v1/messages
// — anything < 500 means the host + TLS + reverse proxy are healthy.
// We don't auth or send a body; 401/405/400 all count as "online".
func pingDirect(ctx context.Context, endpoint string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint+"/v1/messages", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%s returned %d", endpoint, resp.StatusCode)
	}
	return nil
}
