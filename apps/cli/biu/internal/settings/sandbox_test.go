// Tests for the settings.json sandbox bridge: SandboxSection
// parsing, three-layer union merge, and path expansion. End-to-end
// integration with BashTool happens in cmd/biu/wiring (covered
// indirectly via the sandbox layer's own tests); this file stays
// at the settings package boundary.

package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── SandboxSection parsing ───────────────────────────

func TestSandboxSectionRoundTrip(t *testing.T) {
	body := `{
		"sandbox": {
			"fsReadDeny":             ["~/.ssh", "~/.aws"],
			"fsReadAllowWithinDeny":  ["~/.aws/config"],
			"fsWriteAllowExtra":      ["${PROJECT_ROOT}/build"],
			"fsWriteDenyWithinAllow": ["${PROJECT_ROOT}/build/secret"]
		}
	}`
	var s Settings
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Sandbox == nil {
		t.Fatal("sandbox section missing")
	}
	if len(s.Sandbox.FSReadDeny) != 2 {
		t.Errorf("fsReadDeny: %+v", s.Sandbox.FSReadDeny)
	}
	if s.Sandbox.FSReadAllowWithinDeny[0] != "~/.aws/config" {
		t.Errorf("allowWithinDeny: %+v", s.Sandbox.FSReadAllowWithinDeny)
	}
	if s.Sandbox.FSWriteAllowExtra[0] != "${PROJECT_ROOT}/build" {
		t.Errorf("writeAllowExtra: %+v", s.Sandbox.FSWriteAllowExtra)
	}
}

// ─── Path expansion ──────────────────────────────────

func TestExpandPathTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no user home")
	}
	cases := map[string]string{
		"~":                  home,
		"~/.ssh":             filepath.Join(home, ".ssh"),
		"~/.aws/credentials": filepath.Join(home, ".aws/credentials"),
	}
	for in, want := range cases {
		got := expandPath(in, "")
		if got != want {
			t.Errorf("expandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandPathProjectRoot(t *testing.T) {
	got := expandPath("${PROJECT_ROOT}/build", "/Users/me/repo")
	if got != "/Users/me/repo/build" {
		t.Errorf("got %q", got)
	}
	// When projectRoot is empty the literal stays — caller's
	// rule doesn't apply to this layer (e.g. a user-level rule
	// referencing PROJECT_ROOT shouldn't accidentally land in
	// the cwd of whatever process biu was started from).
	got = expandPath("${PROJECT_ROOT}/build", "")
	if got != "" {
		// `expandPath` requires absolute output; an unresolved
		// PROJECT_ROOT yields a relative-looking path, which is
		// then rejected.
		t.Errorf("unresolved PROJECT_ROOT must yield empty; got %q", got)
	}
}

func TestExpandPathEnvVar(t *testing.T) {
	t.Setenv("BIU_TEST_DIR", "/var/tmp/biu-test")
	got := expandPath("${BIU_TEST_DIR}/cache", "")
	if got != "/var/tmp/biu-test/cache" {
		t.Errorf("got %q", got)
	}
}

func TestExpandPathDropsRelative(t *testing.T) {
	for _, in := range []string{
		"relative/path",
		"./local",
		"../escape",
		"",
		"   ",
		"$NONEXISTENT_VAR/foo", // expands to "/foo"... wait, that's absolute. Let me check.
	} {
		got := expandPath(in, "")
		if in == "$NONEXISTENT_VAR/foo" {
			// os.Expand turns $NONEXISTENT into "" → "/foo" which IS
			// absolute. We accept it; that's the user's responsibility.
			if got != "/foo" {
				t.Errorf("expandPath(%q) unexpected: %q", in, got)
			}
			continue
		}
		if got != "" {
			t.Errorf("expandPath(%q) = %q, want empty", in, got)
		}
	}
}

// ─── MergedSandboxConfig ─────────────────────────────

func TestMergedSandboxConfigUnion(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home")
	}
	l := &Layered{
		User: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"~/.ssh"},
		}},
		Project: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"~/.aws"}, // adds, doesn't replace
		}},
		Local: &Settings{Sandbox: &SandboxSection{
			FSWriteAllowExtra: []string{"/tmp/scratch"},
		}},
	}
	got := l.MergedSandboxConfig("/repo")
	if got == nil {
		t.Fatal("nil result")
	}

	// User's deny + project's deny — both present, expanded.
	wantDenies := []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
	}
	if !equalLists(got.FSReadDeny, wantDenies) {
		t.Errorf("FSReadDeny: got %+v want %+v", got.FSReadDeny, wantDenies)
	}
	// Local's writable extra surfaced.
	if len(got.FSWriteAllowExtra) != 1 || got.FSWriteAllowExtra[0] != "/tmp/scratch" {
		t.Errorf("FSWriteAllowExtra: %+v", got.FSWriteAllowExtra)
	}
}

// Layer order matters: user denies come first (security baseline),
// then project additions, then local. The merger doesn't sort —
// it preserves the configured order so the rendered sandbox
// profile reads top-down the way the user authored it.
func TestMergedSandboxConfigPreservesLayerOrder(t *testing.T) {
	l := &Layered{
		User: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"/u/a", "/u/b"},
		}},
		Project: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"/p/x"},
		}},
		Local: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"/l/n"},
		}},
	}
	got := l.MergedSandboxConfig("")
	want := []string{"/u/a", "/u/b", "/p/x", "/l/n"}
	if !equalLists(got.FSReadDeny, want) {
		t.Errorf("order: got %+v want %+v", got.FSReadDeny, want)
	}
}

// Project trying to "remove" a user-layer deny by writing the same
// list with the entry omitted does NOT remove it — merge is
// union-only. This is the security-critical invariant the bridge
// guarantees: a malicious project's settings.json can't loosen the
// user's baseline.
func TestMergedSandboxConfigProjectCannotShadowUserDeny(t *testing.T) {
	l := &Layered{
		User: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"/Users/me/.ssh"},
		}},
		Project: &Settings{Sandbox: &SandboxSection{
			// Empty list — the malicious project's "attempt to
			// override". User's entry must persist.
			FSReadDeny: []string{},
		}},
	}
	got := l.MergedSandboxConfig("")
	if len(got.FSReadDeny) != 1 || got.FSReadDeny[0] != "/Users/me/.ssh" {
		t.Errorf("user deny was overridable: %+v", got.FSReadDeny)
	}
}

func TestMergedSandboxConfigDeduplicatesAcrossLayers(t *testing.T) {
	l := &Layered{
		User: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"/Users/me/.ssh"},
		}},
		Project: &Settings{Sandbox: &SandboxSection{
			FSReadDeny: []string{"/Users/me/.ssh", "/Users/me/.aws"},
		}},
	}
	got := l.MergedSandboxConfig("")
	want := []string{"/Users/me/.ssh", "/Users/me/.aws"}
	if !equalLists(got.FSReadDeny, want) {
		t.Errorf("dedup: got %+v want %+v", got.FSReadDeny, want)
	}
}

// All-nil layered → nil result so the caller knows there's nothing
// to surface. (vs. `&SandboxConfig{}` which would suggest "configured
// but empty").
func TestMergedSandboxConfigNilWhenNothingSet(t *testing.T) {
	l := &Layered{User: &Settings{}, Project: &Settings{}, Local: &Settings{}}
	if got := l.MergedSandboxConfig(""); got != nil {
		t.Errorf("expected nil; got %+v", got)
	}
	if got := (*Layered)(nil).MergedSandboxConfig(""); got != nil {
		t.Errorf("nil receiver should yield nil; got %+v", got)
	}
}

// ─── Helpers ──────────────────────────────────────────

func equalLists(a, b []string) bool {
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
