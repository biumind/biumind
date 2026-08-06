// Layer H — sandbox (P20.47 v2). 10 scenarios that prove the
// macOS / Linux / off sandbox layer enforces what settings.sandbox
// asks it to enforce, and that per-call Bash flags
// (dangerously_disable_sandbox, allow_network) override correctly.
//
// All cases use the SDK plumbing (LoadProjectSettings: Auto +
// settings.json fixture). The sandbox FS rules now flow through the
// SDK because cmd/biumindkit/sdk.go was patched to wire
// MergedSandboxConfig() into webtools.Register options.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func init() {
	// H1 — outside-cwd write blocked by default.
	// Default sandbox: cwd is writable, /tmp is writable, but nothing else.
	// We point the model at a deliberately disallowed target.
	register(scenario{
		name: "H1.outside-cwd-write-blocked",
		prompt: "Use Bash to run `echo blocked > /usr/local/biu-h1-target`. " +
			"If the write is blocked by sandbox, reply with literally H1-BLOCKED and stop.",
		timeout: 45 * time.Second,
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "H1-BLOCKED") && !strings.Contains(up, "BLOCKED") &&
				!strings.Contains(up, "DENIED") && !strings.Contains(up, "PERMITTED") {
				return fmt.Errorf("expected blocked ack, got %q", s)
			}
			return nil
		},
	})

	// H2 — inside-cwd write succeeds.
	register(scenario{
		name: "H2.cwd-write-allowed",
		prompt: "Use Bash to run `echo HELLO > inside.txt`, then `cat inside.txt`. " +
			"Reply with the file's contents.",
		wantTools: []string{"Bash"},
		timeout:   45 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(s, "HELLO") {
				return fmt.Errorf("expected HELLO in reply, got %q", s)
			}
			return nil
		},
	})

	// H3 — FSReadDeny via settings: pretend ~/.fake-secrets is the
	// credentials root we want masked. We seed a file there and
	// configure the deny rule, then have the model attempt to read
	// it. The SDK's MergedSandboxConfig should land FSReadDeny on
	// BashTool (post-patch) so the cat fails.
	register(scenario{
		name: "H3.fs-read-deny",
		prompt: "Use Bash to `cat $HOME/.fake-secrets/key`. If reading is denied " +
			"by sandbox, reply with literally H3-DENIED and stop.",
		timeout:      45 * time.Second,
		loadSettings: true,
		prep: func(dir string) error {
			home, _ := os.UserHomeDir()
			secretDir := filepath.Join(home, ".fake-secrets")
			if err := os.MkdirAll(secretDir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(secretDir, "key"),
				[]byte("super-secret\n"), 0o600); err != nil {
				return err
			}
			return seedSettingsJSON(dir, map[string]any{
				"sandbox": map[string]any{
					"fsReadDeny": []string{secretDir},
				},
			})
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "H3-DENIED") && !strings.Contains(up, "DENIED") &&
				!strings.Contains(up, "BLOCKED") {
				return fmt.Errorf("expected denial ack, got %q", s)
			}
			// Defensive: reply must NOT contain the actual secret value.
			if strings.Contains(s, "super-secret") {
				return fmt.Errorf("REGRESSION: secret leaked into reply: %q", s)
			}
			return nil
		},
	})

	// H4 — FSReadAllowWithinDeny carveout: the deny block masks
	// ~/.fake-mixed but we re-allow ~/.fake-mixed/public, so
	// reading it must succeed.
	register(scenario{
		name:         "H4.fs-read-allow-within-deny",
		prompt:       "Use Bash to `cat $HOME/.fake-mixed/public`. Reply with the contents.",
		wantTools:    []string{"Bash"},
		timeout:      45 * time.Second,
		loadSettings: true,
		prep: func(dir string) error {
			home, _ := os.UserHomeDir()
			root := filepath.Join(home, ".fake-mixed")
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(root, "public"),
				[]byte("PUBLIC-DATA\n"), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(root, "private"),
				[]byte("PRIVATE-DATA\n"), 0o600); err != nil {
				return err
			}
			return seedSettingsJSON(dir, map[string]any{
				"sandbox": map[string]any{
					"fsReadDeny":            []string{root},
					"fsReadAllowWithinDeny": []string{filepath.Join(root, "public")},
				},
			})
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "PUBLIC-DATA") {
				return fmt.Errorf("expected PUBLIC-DATA in reply, got %q", s)
			}
			if strings.Contains(s, "PRIVATE-DATA") {
				return fmt.Errorf("REGRESSION: private data leaked: %q", s)
			}
			return nil
		},
	})

	// H5 — FSWriteAllowExtra: a sibling output dir outside cwd.
	register(scenario{
		name: "H5.fs-write-allow-extra",
		prompt: "Use Bash to `echo OK > /tmp/biu-h5-extra/output.txt && cat /tmp/biu-h5-extra/output.txt`. " +
			"Reply with the file's contents.",
		wantTools:    []string{"Bash"},
		timeout:      45 * time.Second,
		loadSettings: true,
		prep: func(dir string) error {
			extra := "/tmp/biu-h5-extra"
			_ = os.RemoveAll(extra)
			if err := os.MkdirAll(extra, 0o755); err != nil {
				return err
			}
			return seedSettingsJSON(dir, map[string]any{
				"sandbox": map[string]any{
					"fsWriteAllowExtra": []string{extra},
				},
			})
		},
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "OK") {
				return fmt.Errorf("expected OK in reply, got %q", s)
			}
			return nil
		},
	})

	// H6 — FSWriteDenyWithinAllow: a hole inside an otherwise-allowed
	// extra root. Writes to the carved-out path are denied.
	register(scenario{
		name: "H6.fs-write-deny-within-allow",
		prompt: "Use Bash to `echo blocked > /tmp/biu-h6-extra/forbidden/sub.txt`. " +
			"If the sandbox blocks the write, reply with literally H6-BLOCKED and stop.",
		timeout:      45 * time.Second,
		loadSettings: true,
		prep: func(dir string) error {
			extra := "/tmp/biu-h6-extra"
			forbidden := filepath.Join(extra, "forbidden")
			_ = os.RemoveAll(extra)
			if err := os.MkdirAll(forbidden, 0o755); err != nil {
				return err
			}
			return seedSettingsJSON(dir, map[string]any{
				"sandbox": map[string]any{
					"fsWriteAllowExtra":      []string{extra},
					"fsWriteDenyWithinAllow": []string{forbidden},
				},
			})
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "H6-BLOCKED") && !strings.Contains(up, "BLOCKED") &&
				!strings.Contains(up, "DENIED") {
				return fmt.Errorf("expected block ack, got %q", s)
			}
			return nil
		},
	})

	// H7 — allow_network=false (default): outbound curl times out / fails.
	// We DON'T set allow_network in the prompt; defaults are network-blocked.
	register(scenario{
		name: "H7.network-blocked-default",
		prompt: "Use Bash to run `curl -sS --max-time 3 https://example.com`. " +
			"If the network is blocked or the request fails, reply with literally H7-BLOCKED and stop.",
		timeout: 45 * time.Second,
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "H7-BLOCKED") && !strings.Contains(up, "BLOCKED") &&
				!strings.Contains(up, "DENIED") && !strings.Contains(up, "FAIL") {
				return fmt.Errorf("expected network-block ack, got %q", s)
			}
			return nil
		},
		// macOS: sandbox-exec actually denies the syscall. Linux:
		// bwrap unshare-net achieves the same. Skip on platforms
		// where neither is wired (none right now, but future-proof).
		skipReason: func() string {
			if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
				return "sandbox network policy not wired on " + runtime.GOOS
			}
			return ""
		},
	})

	// H8 — allow_network=true per call: model opts in via Bash input.
	register(scenario{
		name: "H8.network-allowed-per-call",
		prompt: "Use Bash with allow_network=true to run `curl -sS --max-time 5 https://example.com | head -1`. " +
			"Reply with the first line of the HTML.",
		wantTools: []string{"Bash"},
		timeout:   60 * time.Second,
		assertText: func(s string) error {
			// example.com returns "<!doctype html>" on the first line.
			low := strings.ToLower(s)
			if !strings.Contains(low, "doctype") && !strings.Contains(low, "html") {
				return fmt.Errorf("expected HTML-ish first line, got %q", s)
			}
			return nil
		},
		skipReason: func() string {
			if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
				return "sandbox network policy not wired on " + runtime.GOOS
			}
			return ""
		},
	})

	// H9 — dangerously_disable_sandbox=true: per-call escape hatch.
	// Already exercised by Layer D's D19; here we use a different
	// outside-cwd target so the scenarios don't collide.
	register(scenario{
		name: "H9.disable-sandbox-per-call",
		prompt: "Use Bash with dangerously_disable_sandbox=true to run " +
			"`echo DISABLED-OK > /tmp/biu-h9-marker && cat /tmp/biu-h9-marker`. " +
			"Reply with whatever was inside.",
		wantTools: []string{"Bash"},
		timeout:   45 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(s, "DISABLED-OK") {
				return fmt.Errorf("expected DISABLED-OK in reply, got %q", s)
			}
			return nil
		},
	})

	// H10 — /dev/null redirect works (regression for the bug
	// P20.47a uncovered: sandbox-exec previously didn't allow
	// writes to /dev/null, breaking `2>/dev/null` patterns).
	register(scenario{
		name: "H10.dev-null-redirect-works",
		prompt: "Use Bash to run `ls *.go 2>/dev/null | wc -l | tr -d ' '`. " +
			"Reply with just the number of .go files.",
		wantTools: []string{"Bash"},
		timeout:   30 * time.Second,
		prep: func(dir string) error {
			for _, n := range []string{"a.go", "b.go", "c.go", "d.go"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("package x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "4") {
				return fmt.Errorf("expected count 4, got %q (likely /dev/null regression)", s)
			}
			return nil
		},
	})
	_ = time.Second // anchor the time import; some platforms strip it
}
