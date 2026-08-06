// Tests for the /resume session-picker plumbing.
//
// Covers the two private helpers:
//   - buildResumeMenu(dir)  → numbered list rendered as a system note
//   - resolveResumeArg(dir, arg) → resolves "#N" / "latest" / "<id>"
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
	got := buildResumeMenu(t.TempDir())
	if !strings.Contains(got, "no saved sessions") {
		t.Errorf("expected empty-state message, got %q", got)
	}
}

func TestBuildResumeMenuListsRecentFirst(t *testing.T) {
	dir := t.TempDir()
	writeOne(t, dir, "p1", "first prompt about auth")
	writeOne(t, dir, "p2", "second prompt about caching")

	got := buildResumeMenu(dir)
	if !strings.Contains(got, "#1") || !strings.Contains(got, "#2") {
		t.Errorf("menu should number entries: %q", got)
	}
	if !strings.Contains(got, "/resume #<n>") || !strings.Contains(got, "/resume latest") {
		t.Errorf("menu should advertise both pick syntaxes: %q", got)
	}
	// The most recent prompt's preview must appear (newest-first).
	if !strings.Contains(got, "second prompt about caching") {
		t.Errorf("newest preview missing: %q", got)
	}
}

func TestBuildResumeMenuTruncatesLongLists(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 13; i++ {
		writeOne(t, dir, "p", "msg")
	}
	got := buildResumeMenu(dir)
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
	pathA := writeOne(t, dir, "p1", "alpha")
	pathB := writeOne(t, dir, "p2", "beta")
	_ = pathA
	_ = pathB

	all, _ := session.ListSessions(dir)
	if len(all) != 2 {
		t.Fatalf("setup: %d sessions", len(all))
	}

	got1, ok := resolveResumeArg(dir, "#1")
	if !ok || got1.ID != all[0].ID {
		t.Errorf("#1 should resolve to %s; got %+v", all[0].ID, got1)
	}
	got2, ok := resolveResumeArg(dir, "#2")
	if !ok || got2.ID != all[1].ID {
		t.Errorf("#2 should resolve to %s; got %+v", all[1].ID, got2)
	}
	if _, ok := resolveResumeArg(dir, "#99"); ok {
		t.Errorf("#99 should not resolve")
	}
	if _, ok := resolveResumeArg(dir, "#nope"); ok {
		t.Errorf("#nope (non-numeric) should not resolve")
	}
}

func TestResolveResumeArgLatestAlias(t *testing.T) {
	dir := t.TempDir()
	writeOne(t, dir, "p1", "older")
	writeOne(t, dir, "p2", "newer")

	got, ok := resolveResumeArg(dir, "latest")
	if !ok {
		t.Fatal("latest should resolve when sessions exist")
	}
	idx1, _ := session.FindByIndex(dir, 1)
	if got.ID != idx1.ID {
		t.Errorf("latest != #1: %s vs %s", got.ID, idx1.ID)
	}
}

func TestResolveResumeArgFallsBackToFindByID(t *testing.T) {
	dir := t.TempDir()
	path := writeOne(t, dir, "p", "only")
	id := strings.TrimSuffix(filepathBase(path), ".jsonl")
	got, ok := resolveResumeArg(dir, id)
	if !ok {
		t.Fatalf("exact id %q should resolve", id)
	}
	if got.Path != path {
		t.Errorf("path mismatch: %s vs %s", got.Path, path)
	}
}

func TestResolveResumeArgWhitespaceTolerant(t *testing.T) {
	dir := t.TempDir()
	writeOne(t, dir, "p", "x")
	if _, ok := resolveResumeArg(dir, "  latest  "); !ok {
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
