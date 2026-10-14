package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect_table(t *testing.T) {
	home := func(t *testing.T) string {
		t.Helper()
		h, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		return h
	}
	localBin := filepath.Join(home(t), ".local/bin", "biu")
	cases := []struct {
		exe     string
		version string
		want    Method
	}{
		{"/Cellar/biu/1.0/bin/biu", "0.1.0", MethodHomebrew},
		{"/usr/local/Homebrew/Cellar/biu/0.1/bin/biu", "0.1.0", MethodHomebrew},
		{"/Users/me/go/bin/biu", "0.1.0", MethodGoInstall},
		{"/snap/biu/x1/biu", "0.1.0", MethodSnap},
		{"/usr/local/bin/biu", "0.1.0", MethodManual},
		// ~/.local/bin is version-dependent: the desktop client and dev
		// builds carry skip-eligible versions and stay client-managed;
		// a clean release version there is a manual install.
		{localBin, "1.2.3+clientmanaged", MethodClientManaged},
		{localBin, "0.1.0-dev", MethodClientManaged},
		{localBin, "", MethodClientManaged},
		{localBin, "0.1.3", MethodUnknown},
		{"", "0.1.0", MethodUnknown},
		{"(unknown)", "0.1.0", MethodUnknown},
		{"/opt/random/biu", "0.1.0", MethodUnknown},
	}
	for _, tc := range cases {
		method, hint := Detect(tc.exe, tc.version)
		if method != tc.want {
			t.Errorf("Detect(%q, %q) = %v, want %v", tc.exe, tc.version, method, tc.want)
		}
		if tc.want != MethodUnknown && hint == "" {
			t.Errorf("%q: known method should have an update hint", tc.exe)
		}
	}
}

// The method labels are user-visible in /install and /upgrade output —
// lock them down so a refactor can't silently change the UX.
func TestMethod_String(t *testing.T) {
	cases := map[Method]string{
		MethodHomebrew:      "homebrew",
		MethodGoInstall:     "go install",
		MethodClientManaged: "client-managed (~/.local/bin)",
		MethodManual:        "manual install (/usr/local/bin)",
		MethodSnap:          "snap",
		MethodUnknown:       "",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("Method(%d).String() = %q, want %q", int(m), got, want)
		}
	}
}

func TestRoute(t *testing.T) {
	cases := map[Method]Action{
		MethodHomebrew:      ActionDelegate,
		MethodGoInstall:     ActionDelegate,
		MethodSnap:          ActionDelegate,
		MethodClientManaged: ActionRefuse,
		MethodManual:        ActionSelfUpdate,
		MethodUnknown:       ActionSelfUpdate,
	}
	for m, want := range cases {
		if got := Route(m); got != want {
			t.Errorf("Route(%v) = %v, want %v", m, got, want)
		}
	}
}

func TestRunDelegated_rejectsShellMetachars(t *testing.T) {
	cases := []string{
		"echo hi; rm -rf /",
		"brew upgrade biu | tee /tmp/x",
		"go install foo > /dev/null",
		"`echo bad`",
	}
	for _, c := range cases {
		_, err := RunDelegated(context.Background(), c)
		if err == nil || !strings.Contains(err.Error(), "metachars") {
			t.Errorf("metachar %q should be rejected, got err=%v", c, err)
		}
	}
}

func TestRunDelegated_emptyErrors(t *testing.T) {
	_, err := RunDelegated(context.Background(), "   ")
	if err == nil {
		t.Error("empty command should error")
	}
}

func TestInfo_defaultAndSet(t *testing.T) {
	// Default placeholders (no SetInfo wiring) — the REPL slashes must
	// never crash when a test didn't set up the wiring.
	info := Current()
	if info.Version == "" || info.Commit == "" || info.BuildDate == "" {
		t.Errorf("default info should be non-empty placeholders: %+v", info)
	}

	SetInfo("1.2.3", "abc1234", "2026-05-29T00:00:00Z")
	t.Cleanup(func() { SetInfo("0.1.0-dev", "none", "unknown") })
	info = Current()
	if info.Version != "1.2.3" || info.Commit != "abc1234" || info.BuildDate != "2026-05-29T00:00:00Z" {
		t.Errorf("SetInfo not reflected: %+v", info)
	}
}
