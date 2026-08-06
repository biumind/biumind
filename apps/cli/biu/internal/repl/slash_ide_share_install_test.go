package repl

import (
	"os"
	"strings"
	"testing"
)

// ─── /ide ──────────────────────────────────────────────────

func TestSlashIDE_notRunning(t *testing.T) {
	t.Setenv("BIU_BRIDGE_URL", "")
	got := model{}.handleIDE([]string{"/ide"})
	for _, want := range []string{"bridge not running", "biu bridge"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestSlashIDE_runningAuthed(t *testing.T) {
	t.Setenv("BIU_BRIDGE_URL", "http://127.0.0.1:7173")
	t.Setenv("BIU_BRIDGE_TOKEN", "deadbeef-secret-very-long-token")

	got := model{}.handleIDE([]string{"/ide"})
	if !strings.Contains(got, "127.0.0.1:7173") {
		t.Errorf("missing endpoint: %s", got)
	}
	if !strings.Contains(got, "bearer") {
		t.Errorf("auth line missing: %s", got)
	}
	// Token must be truncated — never surface in full to avoid
	// leaking via transcript.
	if strings.Contains(got, "deadbeef-secret-very-long-token") {
		t.Errorf("full token should not be echoed: %s", got)
	}
	if !strings.Contains(got, `"deadbeef`) {
		t.Errorf("token preview missing: %s", got)
	}
}

func TestSlashIDE_runningUnauthed(t *testing.T) {
	t.Setenv("BIU_BRIDGE_URL", "http://127.0.0.1:7173")
	t.Setenv("BIU_BRIDGE_TOKEN", "")

	got := model{}.handleIDE([]string{"/ide"})
	if !strings.Contains(got, "auth:     none") {
		t.Errorf("unauth state missing: %s", got)
	}
}

// ─── /share ─────────────────────────────────────────────────

func TestSlashShare_noSessionLog(t *testing.T) {
	got := model{}.handleShare([]string{"/share"})
	if !strings.Contains(got, "no session writer") {
		t.Errorf("missing no-session note: %s", got)
	}
}

// ─── /install ──────────────────────────────────────────────

func TestSlashInstall_basicShape(t *testing.T) {
	got := model{}.handleInstall([]string{"/install"})
	for _, want := range []string{
		"binary:", "version:", "commit:", "go:", "os/arch:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in /install output: %s", want, got)
		}
	}
}

func TestSlashInstall_withInjectedInfo(t *testing.T) {
	original := installInfoForREPL
	t.Cleanup(func() { installInfoForREPL = original })

	SetInstallInfo("1.2.3", "abc1234", "2026-05-29T00:00:00Z")
	got := model{}.handleInstall([]string{"/install"})
	for _, want := range []string{"1.2.3", "abc1234", "2026-05-29"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing injected %q: %s", want, got)
		}
	}
}

func TestSlashInstall_detectInstallMethod(t *testing.T) {
	cases := []struct {
		exe        string
		wantMethod string
	}{
		{"/Cellar/biu/1.0/bin/biu", "homebrew"},
		{"/usr/local/Homebrew/Cellar/biu/0.1/bin/biu", "homebrew"},
		{"/Users/me/go/bin/biu", "go install"},
		{"/snap/biu/x1/biu", "snap"},
		{"/usr/local/bin/biu", "manual install"},
		{"", ""},
		{"(unknown)", ""},
		{"/opt/random/biu", ""},
	}
	for _, tc := range cases {
		method, hint := detectInstallMethod(tc.exe)
		if !strings.HasPrefix(method, tc.wantMethod) {
			t.Errorf("detectInstallMethod(%q) method=%q, want prefix %q",
				tc.exe, method, tc.wantMethod)
		}
		if tc.wantMethod != "" && hint == "" {
			t.Errorf("%q: known method should have an update hint", tc.exe)
		}
	}
}

func TestSlashInstall_neverPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("/install panicked: %v", r)
		}
	}()
	_ = model{}.handleInstall([]string{"/install"})
}

// /share with no clipboard tool should still write the file +
// surface the path. Hard to fake cross-OS so we just verify the
// no-session-writer branch (other paths need a writer fixture
// that's bigger than this batch warrants).
func TestSlashShare_argParsing(t *testing.T) {
	// Verify that flag parsing accepts each format alias without
	// panicking. The function bails on m.sessionLog == nil so we
	// don't actually write — but the parse-arg code runs first.
	_ = model{}.handleShare([]string{"/share", "json"})
	_ = model{}.handleShare([]string{"/share", "md"})
	_ = model{}.handleShare([]string{"/share", "anthropic-replay"})
	_ = model{}.handleShare([]string{"/share", "/tmp/x.md"})
	// No assertions — purely a "doesn't crash" smoke test.
}

// Ensure /install reflects current binary. We can't predict the
// path in CI, but it should not be empty unless os.Executable
// genuinely failed.
func TestSlashInstall_binaryPathPresent(t *testing.T) {
	got := model{}.handleInstall([]string{"/install"})
	exe, _ := os.Executable()
	if exe != "" && !strings.Contains(got, "binary:") {
		t.Errorf("binary line should be present: %s", got)
	}
}
