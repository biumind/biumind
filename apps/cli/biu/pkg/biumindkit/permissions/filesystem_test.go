package permissions

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAllWorkingDirectories(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcSession, []string{"/tmp/x", "/tmp/y"})
	c.AddDirectories(SrcLocalSettings, []string{"/srv/repo"})

	got := AllWorkingDirectories(c, "/repo")
	want := []string{"/repo", "/srv/repo", "/tmp/x", "/tmp/y"}
	if !equalStrings(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAllWorkingDirectories_DedupsCwd(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcSession, []string{"/repo", "/tmp/x"})
	got := AllWorkingDirectories(c, "/repo")
	want := []string{"/repo", "/tmp/x"}
	if !equalStrings(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAllWorkingDirectories_EmptyCwd(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcSession, []string{"/tmp/a"})
	got := AllWorkingDirectories(c, "")
	if len(got) != 1 || got[0] != "/tmp/a" {
		t.Errorf("got %+v, want [/tmp/a]", got)
	}
}

func TestAllWorkingDirectories_NoExtras(t *testing.T) {
	c := NewContext()
	got := AllWorkingDirectories(c, "/repo")
	if len(got) != 1 || got[0] != "/repo" {
		t.Errorf("got %+v, want [/repo]", got)
	}
}

func TestPathInWorkingPath_Containment(t *testing.T) {
	cases := []struct {
		path    string
		working string
		want    bool
	}{
		{"/repo/src/main.go", "/repo", true},
		{"/repo", "/repo", true},
		{"/repo/", "/repo", true},
		{"/other/main.go", "/repo", false},
		{"/repo/../escape", "/repo", false},
		{"/repo/sub/file", "/repo/sub", true},
	}
	for _, tc := range cases {
		got := PathInWorkingPath(tc.path, tc.working)
		if got != tc.want {
			t.Errorf("PathInWorkingPath(%q, %q) = %v, want %v",
				tc.path, tc.working, got, tc.want)
		}
	}
}

func TestPathInWorkingPath_CaseInsensitive(t *testing.T) {
	// Defends against macOS/Windows case mutation bypass
	if !PathInWorkingPath("/Repo/Src/Main.go", "/repo") {
		t.Errorf("case mismatch should still be inside")
	}
	if !PathInWorkingPath("/repo/.cLauDe/settings.json", "/repo/.claude") {
		t.Errorf("mixed case .claude/ should still match")
	}
}

func TestPathInWorkingPath_DarwinPrivatePrefix(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("private-prefix normalisation is darwin-only")
	}
	cases := []struct {
		path    string
		working string
		want    bool
	}{
		{"/private/tmp/foo", "/tmp", true},
		{"/tmp/foo", "/private/tmp", true},
		{"/private/var/folders/x/y", "/var/folders", true},
		{"/var/folders/x/y", "/private/var/folders", true},
		// /private/tmp must equal /tmp exactly when paths land there
		{"/private/tmp", "/tmp", true},
	}
	for _, tc := range cases {
		got := PathInWorkingPath(tc.path, tc.working)
		if got != tc.want {
			t.Errorf("PathInWorkingPath(%q,%q)=%v want %v",
				tc.path, tc.working, got, tc.want)
		}
	}
}

func TestPathInWorkingPath_RelativeResolved(t *testing.T) {
	// Working path is relative — should resolve against cwd. Use a
	// path we know exists under any test cwd by checking that an
	// absolutized "." matches its resolved form.
	abs, _ := filepath.Abs(".")
	if !PathInWorkingPath(abs+"/sentinel.txt", ".") {
		t.Errorf("relative working dir should resolve to cwd")
	}
}

func TestPathInWorkingPath_TildeExpansion(t *testing.T) {
	// ~ expansion must work for both args
	if !PathInWorkingPath("~/file.txt", "~") {
		t.Errorf("tilde expansion failed for both args")
	}
}

func TestPathInWorkingPath_ErrorsFailClosed(t *testing.T) {
	if PathInWorkingPath("", "/repo") {
		t.Errorf("empty path should fail closed")
	}
	if PathInWorkingPath("/foo", "") {
		t.Errorf("empty workingPath should fail closed")
	}
}

func TestPathInAllowedWorkingPath(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcSession, []string{"/tmp/proj"})

	// Inside originalCwd
	if !PathInAllowedWorkingPath("/repo/src/x.go", c, "/repo") {
		t.Errorf("path inside cwd must be allowed")
	}
	// Inside additional dir
	if !PathInAllowedWorkingPath("/tmp/proj/x.go", c, "/repo") {
		t.Errorf("path inside extra dir must be allowed")
	}
	// Outside both
	if PathInAllowedWorkingPath("/etc/passwd", c, "/repo") {
		t.Errorf("path outside all working dirs must NOT be allowed")
	}
}

func TestPathInAllowedWorkingPath_NilCtx(t *testing.T) {
	// nil ctx must still work — only originalCwd is checked
	if !PathInAllowedWorkingPath("/repo/x", nil, "/repo") {
		t.Errorf("nil ctx + cwd containment must be allowed")
	}
	if PathInAllowedWorkingPath("/etc/x", nil, "/repo") {
		t.Errorf("nil ctx + outside cwd must NOT be allowed")
	}
}

// ─── helpers ──────────────────────────────────────────

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// silence unused-import warning when the test file lacks a strings.Contains call
var _ = strings.Contains
