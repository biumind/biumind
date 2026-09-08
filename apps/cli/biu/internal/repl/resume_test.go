// Tests for the /resume session-picker plumbing.
//
// Covers the two private helpers:
//   - buildResumeMenu(dir, project)  → numbered list rendered as a system note
//   - resolveResumeArg(dir, project, arg) → resolves "#N" / "latest" / "<id>"
//
// The slash handler itself is exercised via the menu / resolver
// because hooking the full Bubble Tea Update would require a much
// heavier fixture; the helpers carry all the user-facing logic.

package repl

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

func writeOne(t *testing.T, dir, project, prompt string) string {
	t.Helper()
	w, err := session.Open(dir, project)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Append(session.Event{Type: "user_message", Content: prompt}); err != nil {
		t.Fatal(err)
	}
	return w.Path()
}

func TestBuildResumeMenuEmpty(t *testing.T) {
	got := buildResumeMenu(t.TempDir(), "p1")
	if !strings.Contains(got, "no saved sessions") {
		t.Errorf("expected empty-state message, got %q", got)
	}
}

func TestBuildResumeMenuScopesToProject(t *testing.T) {
	dir := t.TempDir()
	writeOne(t, dir, "p1", "first prompt about auth")
	writeOne(t, dir, "p2", "second prompt about caching")

	got := buildResumeMenu(dir, "p1")
	if !strings.Contains(got, "first prompt about auth") {
		t.Errorf("menu should list the current project's session: %q", got)
	}
	if strings.Contains(got, "second prompt about caching") {
		t.Errorf("menu must not leak other projects' sessions: %q", got)
	}
	if strings.Contains(got, "#2") {
		t.Errorf("project with one session should only number #1: %q", got)
	}
	if !strings.Contains(got, "/resume #<n>") || !strings.Contains(got, "/resume latest") {
		t.Errorf("menu should advertise both pick syntaxes: %q", got)
	}
}

func TestBuildResumeMenuTruncatesLongLists(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 13; i++ {
		writeOne(t, dir, "p", "msg")
	}
	got := buildResumeMenu(dir, "p")
	if !strings.Contains(got, "more") {
		t.Errorf("expected `…(N more)` overflow line: %q", got)
	}
	// We list at most 10.
	if strings.Count(got, "#") > 11 { // 10 numbered + 1 from "/resume #<n>"
		t.Errorf("too many entries listed: %q", got)
	}
}

func TestResolveResumeArgPicksByIndex(t *testing.T) {
	dir := t.TempDir()
	writeOne(t, dir, "p1", "alpha")
	writeOne(t, dir, "p1", "beta")
	writeOne(t, dir, "p2", "other project")

	all, _ := session.ListSessionsIn(dir, "p1")
	if len(all) != 2 {
		t.Fatalf("setup: %d sessions in p1", len(all))
	}

	got1, ok := resolveResumeArg(dir, "p1", "#1")
	if !ok || got1.ID != all[0].ID {
		t.Errorf("#1 should resolve to %s; got %+v", all[0].ID, got1)
	}
	got2, ok := resolveResumeArg(dir, "p1", "#2")
	if !ok || got2.ID != all[1].ID {
		t.Errorf("#2 should resolve to %s; got %+v", all[1].ID, got2)
	}
	// p2's session must not be reachable by index from p1's picker.
	if _, ok := resolveResumeArg(dir, "p1", "#3"); ok {
		t.Errorf("#3 should not resolve with only 2 sessions in p1")
	}
	if _, ok := resolveResumeArg(dir, "p1", "#nope"); ok {
		t.Errorf("#nope (non-numeric) should not resolve")
	}
}

func TestResolveResumeArgLatestAlias(t *testing.T) {
	dir := t.TempDir()
	writeOne(t, dir, "p1", "older")
	writeOne(t, dir, "p1", "newer")

	got, ok := resolveResumeArg(dir, "p1", "latest")
	if !ok {
		t.Fatal("latest should resolve when sessions exist")
	}
	idx1, _ := session.FindByIndexIn(dir, "p1", 1)
	if got.ID != idx1.ID {
		t.Errorf("latest != #1: %s vs %s", got.ID, idx1.ID)
	}
}

func TestResolveResumeArgFindsByIDAcrossProjects(t *testing.T) {
	dir := t.TempDir()
	path := writeOne(t, dir, "p2", "only")
	id := strings.TrimSuffix(filepathBase(path), ".jsonl")
	// Standing in p1, an exact id from p2 still resolves — the picker
	// is scoped, explicit ids are global.
	got, ok := resolveResumeArg(dir, "p1", id)
	if !ok {
		t.Fatalf("exact id %q should resolve across projects", id)
	}
	if got.Path != path {
		t.Errorf("path mismatch: %s vs %s", got.Path, path)
	}
}

func TestResolveResumeArgWhitespaceTolerant(t *testing.T) {
	dir := t.TempDir()
	writeOne(t, dir, "p", "x")
	if _, ok := resolveResumeArg(dir, "p", "  latest  "); !ok {
		t.Errorf("trimmed whitespace should still resolve")
	}
}

// filepathBase is a tiny shim so the test file doesn't have to add
// path/filepath to its imports just for one ID extraction. Kept
// inline because the helper is two lines and trivial.
func filepathBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
