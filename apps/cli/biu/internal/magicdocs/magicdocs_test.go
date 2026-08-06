package magicdocs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ─── IsMagicDoc ──────────────────────────────────────────────

func TestIsMagicDoc_basicMatch(t *testing.T) {
	got, ok := IsMagicDoc("# MAGIC DOC: Architecture\n\nbody")
	if !ok {
		t.Fatal("expected match")
	}
	if got.Title != "Architecture" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Instructions != "" {
		t.Errorf("Instructions should be empty, got %q", got.Instructions)
	}
}

func TestIsMagicDoc_withInstructions(t *testing.T) {
	got, ok := IsMagicDoc("# MAGIC DOC: Engineering Notes\n*Keep concise. Cite line numbers.*\n\nbody")
	if !ok {
		t.Fatal("expected match")
	}
	if got.Instructions != "Keep concise. Cite line numbers." {
		t.Errorf("Instructions = %q", got.Instructions)
	}
}

func TestIsMagicDoc_notFirstLine(t *testing.T) {
	// Header buried in body should not match.
	if _, ok := IsMagicDoc("intro line\n# MAGIC DOC: lying"); ok {
		t.Error("buried header should not match")
	}
}

func TestIsMagicDoc_caseInsensitive(t *testing.T) {
	got, ok := IsMagicDoc("# magic doc: Lower")
	if !ok || got.Title != "Lower" {
		t.Errorf("case-insensitive failed: %+v ok=%v", got, ok)
	}
}

func TestIsMagicDoc_extraSpaces(t *testing.T) {
	got, ok := IsMagicDoc("#   MAGIC   DOC:    Spaced")
	if !ok || got.Title != "Spaced" {
		t.Errorf("extra spaces should match: %+v", got)
	}
}

func TestIsMagicDoc_notMagic(t *testing.T) {
	cases := []string{
		"# Regular Header",
		"## MAGIC DOC: wrong level",
		"random text",
		"",
	}
	for _, c := range cases {
		if _, ok := IsMagicDoc(c); ok {
			t.Errorf("%q should not match", c)
		}
	}
}

func TestIsMagicDoc_trimsTitleWhitespace(t *testing.T) {
	got, _ := IsMagicDoc("# MAGIC DOC:    Title with trail   ")
	if got.Title != "Title with trail" {
		t.Errorf("Title not trimmed: %q", got.Title)
	}
}

// ─── Tracker ─────────────────────────────────────────────────

func TestTracker_NoteOnlyMagicDocs(t *testing.T) {
	tr := NewTracker()
	if tr.Note("/x/regular.md", "# Regular File") {
		t.Error("non-magic should not register")
	}
	if !tr.Note("/x/magic.md", "# MAGIC DOC: Topic") {
		t.Error("magic doc should register")
	}
	if len(tr.Tracked()) != 1 {
		t.Errorf("tracked = %d, want 1", len(tr.Tracked()))
	}
}

func TestTracker_NoteIdempotent(t *testing.T) {
	tr := NewTracker()
	tr.Note("/p", "# MAGIC DOC: A")
	tr.Note("/p", "# MAGIC DOC: A") // same path again
	if len(tr.Tracked()) != 1 {
		t.Errorf("idempotent re-note should not duplicate")
	}
}

func TestTracker_Forget(t *testing.T) {
	tr := NewTracker()
	tr.Note("/x", "# MAGIC DOC: T")
	tr.Forget("/x")
	if len(tr.Tracked()) != 0 {
		t.Error("forget should remove from set")
	}
}

func TestTracker_NilSafe(t *testing.T) {
	var tr *Tracker
	if tr.Note("/x", "# MAGIC DOC: T") {
		t.Error("nil tracker note should be no-op")
	}
	if got := tr.Tracked(); got != nil {
		t.Error("nil tracker Tracked should return nil")
	}
	tr.Forget("/x") // should not panic
}

// ─── BuildUpdatePrompt ───────────────────────────────────────

func TestBuildUpdatePrompt_includesAllParts(t *testing.T) {
	got := BuildUpdatePrompt("/x.md",
		DocInfo{Title: "Arch", Instructions: "Be concise"},
		"body")
	for _, want := range []string{
		"/x.md", `"Arch"`, "Be concise",
		"# MAGIC DOC:", "Output rules", "body",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got[:200])
		}
	}
}

// ─── UpdateAll integration ──────────────────────────────────

type fakeUpdater struct {
	calls int
	resp  string
	err   error
}

func (f *fakeUpdater) Update(ctx context.Context, msgs []state.Message, inst string) (string, error) {
	f.calls++
	return f.resp, f.err
}

func TestUpdateAll_writesUpdatedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	original := "# MAGIC DOC: T\n*be brief*\n\nold body"
	_ = os.WriteFile(path, []byte(original), 0o644)

	tr := NewTracker()
	tr.Note(path, original)

	updated := "# MAGIC DOC: T\n*be brief*\n\nnew body with insight"
	u := &fakeUpdater{resp: updated}

	count := UpdateAll(context.Background(), tr, u, nil)
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	got, _ := os.ReadFile(path)
	if string(got) != updated {
		t.Errorf("disk content not updated:\n%s", got)
	}
}

func TestUpdateAll_skipsWhenContentUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	body := "# MAGIC DOC: T\n\nbody"
	_ = os.WriteFile(path, []byte(body), 0o644)

	tr := NewTracker()
	tr.Note(path, body)
	u := &fakeUpdater{resp: body} // same body back

	stBefore, _ := os.Stat(path)
	UpdateAll(context.Background(), tr, u, nil)
	stAfter, _ := os.Stat(path)

	if !stBefore.ModTime().Equal(stAfter.ModTime()) {
		t.Error("unchanged content should not bump mtime")
	}
}

func TestUpdateAll_refusesToStripMagicHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	body := "# MAGIC DOC: T\n\nbody"
	_ = os.WriteFile(path, []byte(body), 0o644)
	tr := NewTracker()
	tr.Note(path, body)

	// Updater returns body without the magic header — must be
	// rejected to prevent silent detach.
	u := &fakeUpdater{resp: "# Just A Heading\n\nupdated body"}
	UpdateAll(context.Background(), tr, u, nil)

	got, _ := os.ReadFile(path)
	if string(got) != body {
		t.Errorf("write should have been refused; got %q", got)
	}
}

func TestUpdateAll_skipsMissingFile(t *testing.T) {
	tr := NewTracker()
	tr.Note("/no/such/file.md", "# MAGIC DOC: T")
	u := &fakeUpdater{resp: "# MAGIC DOC: T\nnew"}
	count := UpdateAll(context.Background(), tr, u, nil)
	if count != 0 {
		t.Errorf("missing file should be skipped, count = %d", count)
	}
}

func TestUpdateAll_skipsTooLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.md")
	body := "# MAGIC DOC: T\n\n" + strings.Repeat("x", MaxUpdateBytes+1)
	_ = os.WriteFile(path, []byte(body), 0o644)
	tr := NewTracker()
	tr.Note(path, body)
	u := &fakeUpdater{resp: "# MAGIC DOC: T\nshort"}

	UpdateAll(context.Background(), tr, u, nil)
	if u.calls != 0 {
		t.Error("oversize file should not call updater")
	}
}

func TestUpdateAll_updaterErrorSilent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	body := "# MAGIC DOC: T\n\noriginal"
	_ = os.WriteFile(path, []byte(body), 0o644)
	tr := NewTracker()
	tr.Note(path, body)
	u := &fakeUpdater{err: errors.New("api down")}

	count := UpdateAll(context.Background(), tr, u, nil)
	if count != 0 {
		t.Error("error should not count as success")
	}
	got, _ := os.ReadFile(path)
	if string(got) != body {
		t.Error("file should be untouched on error")
	}
}

func TestUpdateAll_nilSafety(t *testing.T) {
	if got := UpdateAll(context.Background(), nil, &fakeUpdater{}, nil); got != 0 {
		t.Error("nil tracker should be no-op")
	}
	if got := UpdateAll(context.Background(), NewTracker(), nil, nil); got != 0 {
		t.Error("nil updater should be no-op")
	}
}
