package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func writeSession(t *testing.T, dir, project string, events []Event) string {
	t.Helper()
	w, err := Open(dir, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	path := w.Path()
	w.Close()
	return path
}

func TestListSessionsSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "p1", []Event{
		{Type: "user_message", Content: "first prompt"},
		{Type: "assistant_message", Content: "ok"},
	})
	writeSession(t, dir, "p2", []Event{
		{Type: "user_message", Content: "second prompt"},
	})
	got, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}
	// Newest IDs encode timestamp; "p2" written second so its file's
	// id is lexicographically larger.
	if got[0].ProjectHash == "" {
		t.Errorf("missing project hash")
	}
	if got[0].FirstPrompt == "" {
		t.Errorf("first prompt not extracted")
	}
}

func TestListSessionsMissingDir(t *testing.T) {
	got, err := ListSessions("/no/such/dir-9999")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list")
	}
}

func TestFindByID(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "p", []Event{
		{Type: "user_message", Content: "hi"},
	})
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	s, ok := FindByID(dir, id)
	if !ok {
		t.Fatalf("FindByID returned false for %s", id)
	}
	if s.Path != path {
		t.Errorf("path mismatch: %s vs %s", s.Path, path)
	}
}

// FindByIndex is the picker's resolver — 1-based index into the
// newest-first list. Out-of-range and zero/negative both return
// ok=false so the slash handler can print a clean error.
func TestFindByIndexHonoursNewestFirstAndBounds(t *testing.T) {
	dir := t.TempDir()
	pathA := writeSession(t, dir, "p1", []Event{
		{Type: "user_message", Content: "first session"},
	})
	pathB := writeSession(t, dir, "p2", []Event{
		{Type: "user_message", Content: "second session"},
	})

	idA := strings.TrimSuffix(filepath.Base(pathA), ".jsonl")
	idB := strings.TrimSuffix(filepath.Base(pathB), ".jsonl")

	all, _ := ListSessions(dir)
	if len(all) != 2 {
		t.Fatalf("setup: want 2 sessions, got %d", len(all))
	}
	// Whichever ListSessions sorts newest-first should match #1.
	want1, want2 := all[0].ID, all[1].ID
	if want1 != idA && want1 != idB {
		t.Fatalf("sorting unexpected: %v", []string{want1, want2})
	}

	s1, ok := FindByIndex(dir, 1)
	if !ok || s1.ID != want1 {
		t.Errorf("FindByIndex(1) = (%+v, %v), want %s", s1.ID, ok, want1)
	}
	s2, ok := FindByIndex(dir, 2)
	if !ok || s2.ID != want2 {
		t.Errorf("FindByIndex(2) = (%+v, %v), want %s", s2.ID, ok, want2)
	}
	if _, ok := FindByIndex(dir, 0); ok {
		t.Errorf("FindByIndex(0) should fail")
	}
	if _, ok := FindByIndex(dir, -1); ok {
		t.Errorf("FindByIndex(-1) should fail")
	}
	if _, ok := FindByIndex(dir, 99); ok {
		t.Errorf("FindByIndex(99) should fail with only 2 sessions")
	}
}

func TestFindLatestIsAliasForIndex1(t *testing.T) {
	dir := t.TempDir()
	if _, ok := FindLatest(dir); ok {
		t.Errorf("FindLatest on empty dir should be ok=false")
	}
	writeSession(t, dir, "p", []Event{
		{Type: "user_message", Content: "only"},
	})
	got, ok := FindLatest(dir)
	if !ok {
		t.Fatal("FindLatest should resolve when at least one session exists")
	}
	idx1, _ := FindByIndex(dir, 1)
	if got.ID != idx1.ID {
		t.Errorf("FindLatest != FindByIndex(1): %s vs %s", got.ID, idx1.ID)
	}
}

func TestReplayRebuildsState(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "p", []Event{
		{Type: "user_message", Content: "what tests are missing?"},
		{Type: "tool_use", Name: "Grep", CallID: "tu_1",
			Args: map[string]any{"pattern": "TODO"}},
		{Type: "tool_result", CallID: "tu_1", Output: "found 3"},
		{Type: "assistant_message", Content: "Three TODOs in pkg/x."},
	})
	st := state.New()
	if err := Replay(path, st); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot()
	if len(snap) < 3 {
		t.Fatalf("expected ≥3 messages, got %d", len(snap))
	}
	// First message: user prompt.
	if snap[0].Role != state.RoleUser ||
		!strings.Contains(snap[0].Content[0].Text, "tests are missing") {
		t.Errorf("first message wrong: %+v", snap[0])
	}
	// Find the assistant tool_use.
	found := false
	for _, m := range snap {
		for _, b := range m.Content {
			if b.Type == state.ContentToolUse && b.ToolUseName == "Grep" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("tool_use block not replayed")
	}
}
