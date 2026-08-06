package trust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// freshHome returns a TempDir + sets HOME to it. Every test that
// touches Load / Trust / Save uses this so the on-disk file is
// hermetic. Also unsets BIU_TRUST so envBypass doesn't poison the
// IsTrusted assertions.
func freshHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvBypass, "")
	return home
}

func TestLoadEmptyHomeReturnsInMemoryStore(t *testing.T) {
	s, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("empty home should yield empty list; got %v", got)
	}
	// Trust should still work but Save is a no-op.
	if _, err := s.Trust("/tmp/x"); err != nil {
		t.Errorf("Trust on in-memory store should succeed; got %v", err)
	}
}

func TestLoadFreshHomeIsEmpty(t *testing.T) {
	home := freshHome(t)
	s, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("fresh home: got %v, want []", got)
	}
	// File should not be created until first Save.
	if _, err := os.Stat(filepath.Join(home, ".biumind", FileName)); err == nil {
		t.Errorf("Load should not pre-create the trust file")
	}
}

func TestTrustPersistsAndReloads(t *testing.T) {
	home := freshHome(t)
	s, _ := Load(home)
	target := filepath.Join(home, "myproj")
	got, err := s.Trust(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("Trust returned %q, want %q", got, target)
	}
	// Re-load to confirm persistence round-trip.
	reloaded, _ := Load(home)
	if got := reloaded.List(); len(got) != 1 || got[0] != target {
		t.Errorf("after reload: got %v, want [%s]", got, target)
	}
}

// IsTrusted accepts any descendant of a trusted ancestor — that's
// the whole point of the persisted entry being a project root.
func TestIsTrustedHonoursAncestors(t *testing.T) {
	home := freshHome(t)
	s, _ := Load(home)
	root := filepath.Join(home, "myproj")
	if _, err := s.Trust(root); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{
		root,
		filepath.Join(root, "src"),
		filepath.Join(root, "src/deep/nested"),
	} {
		if !s.IsTrusted(sub) {
			t.Errorf("IsTrusted(%q) should be true under trusted root", sub)
		}
	}
	// Sibling directory must NOT be trusted just because it
	// shares a parent.
	sibling := filepath.Join(home, "other")
	if s.IsTrusted(sibling) {
		t.Errorf("sibling dir %q should not be trusted", sibling)
	}
}

// IsTrusted must NOT match a string-prefix without the path
// separator boundary — `/foo/bar` ≠ `/foo/barley`.
func TestIsTrustedRequiresPathSeparator(t *testing.T) {
	home := freshHome(t)
	s, _ := Load(home)
	_, _ = s.Trust(filepath.Join(home, "bar"))
	imposter := filepath.Join(home, "barley")
	if s.IsTrusted(imposter) {
		t.Errorf("trust must not match adjacent name %q", imposter)
	}
}

func TestUntrustRemovesEntry(t *testing.T) {
	home := freshHome(t)
	s, _ := Load(home)
	target := filepath.Join(home, "p")
	_, _ = s.Trust(target)
	if !s.IsTrusted(target) {
		t.Fatal("setup: should be trusted")
	}
	if err := s.Untrust(target); err != nil {
		t.Fatal(err)
	}
	if s.IsTrusted(target) {
		t.Errorf("should not be trusted after Untrust")
	}
	// Idempotent — second Untrust is a no-op.
	if err := s.Untrust(target); err != nil {
		t.Errorf("repeat Untrust should not error; got %v", err)
	}
}

// Session grants exist only in memory. List() must not include
// them; SessionList() does.
func TestTrustForSessionIsNotPersisted(t *testing.T) {
	home := freshHome(t)
	s, _ := Load(home)
	target := filepath.Join(home, "ephemeral")
	if _, err := s.TrustForSession(target); err != nil {
		t.Fatal(err)
	}
	if !s.IsTrusted(target) {
		t.Errorf("session grant should pass IsTrusted")
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("List() should not include session grants; got %v", got)
	}
	if got := s.SessionList(); len(got) != 1 || got[0] != target {
		t.Errorf("SessionList(): got %v want [%s]", got, target)
	}
	// Reloading from disk discards the session grant — it was
	// never written.
	reloaded, _ := Load(home)
	if reloaded.IsTrusted(target) {
		t.Errorf("reloaded store should not see session grant")
	}
}

// BIU_TRUST=1 short-circuits IsTrusted to true everywhere. Test
// runs the bypass branch with an empty store so we know the result
// can only come from the env var.
func TestEnvBypassTrustsEverything(t *testing.T) {
	freshHome(t)
	t.Setenv(EnvBypass, "1")
	s, _ := Load("")
	if !s.IsTrusted("/this/does/not/exist") {
		t.Errorf("BIU_TRUST=1 should trust everything")
	}
}

func TestEnvBypassIgnoresFalseyValues(t *testing.T) {
	freshHome(t)
	for _, v := range []string{"", "0", "false", "no", "garbage"} {
		t.Setenv(EnvBypass, v)
		s, _ := Load("")
		if s.IsTrusted("/some/random/path") {
			t.Errorf("BIU_TRUST=%q should NOT trust everything", v)
		}
	}
}

// A leftover ~/.claude/trust.json (from a former Claude Code
// install) must NOT be loaded — biumind reads only `.biumind/` so
// stale lists don't silently extend the trust gate.
func TestLoadIgnoresClaudeTrustFile(t *testing.T) {
	home := freshHome(t)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := "/Users/me/stale-proj"
	doc := fileShape{Trusted: []string{target}}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(filepath.Join(claudeDir, FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if s.IsTrusted(target) {
		t.Errorf(".claude/trust.json must be ignored; List=%v", s.List())
	}
	// First Save still lands in .biumind/.
	if _, err := s.Trust(filepath.Join(home, "newproj")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".biumind", FileName)); err != nil {
		t.Errorf("Save should land in .biumind/: %v", err)
	}
}

// Persisted writes survive a partial filesystem failure. We can't
// easily fake a write error in unit tests but we can confirm the
// atomic-rename invariant: after a successful Trust the file is
// valid JSON parseable back into the same list.
func TestPersistedFileIsValidJSON(t *testing.T) {
	home := freshHome(t)
	s, _ := Load(home)
	for _, p := range []string{
		filepath.Join(home, "a"),
		filepath.Join(home, "b"),
	} {
		if _, err := s.Trust(p); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(home, ".biumind", FileName))
	if err != nil {
		t.Fatal(err)
	}
	var doc fileShape
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
	if len(doc.Trusted) != 2 {
		t.Errorf("file has %d entries, want 2", len(doc.Trusted))
	}
	// Sorted on disk for human-friendly diffs.
	if !strings.HasSuffix(doc.Trusted[0], "/a") || !strings.HasSuffix(doc.Trusted[1], "/b") {
		t.Errorf("entries not sorted: %v", doc.Trusted)
	}
}

// normaliseList must dedup + canonicalise — guards against a hand-
// edited trust.json with mixed slashes / dups silently breaking
// IsTrusted's exact-match.
func TestLoadNormalisesHandEditedFile(t *testing.T) {
	home := freshHome(t)
	dir := filepath.Join(home, ".biumind")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"trusted":["/a/b/","/a/b","/c"]}`)
	if err := os.WriteFile(filepath.Join(dir, FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(home)
	got := s.List()
	if len(got) != 2 {
		t.Errorf("dedupe failed: got %v, want 2 entries (/a/b, /c)", got)
	}
}
