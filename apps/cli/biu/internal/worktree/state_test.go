package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	wantCurrent := filepath.Join(root, "wt")
	if err := os.MkdirAll(wantCurrent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(State{
		SessionID: "abc-123",
		Previous:  root,
		Current:   wantCurrent,
		Branch:    "biu/x",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Current != wantCurrent || got.Branch != "biu/x" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not auto-populated")
	}
}

func TestVerifyAndResumeRemovesGhost(t *testing.T) {
	root := t.TempDir()
	s, _ := NewStore(root)
	ghost := filepath.Join(root, "deleted-after-save")
	_ = os.MkdirAll(ghost, 0o755)
	_ = s.Save(State{
		SessionID: "ghost-1", Previous: root, Current: ghost,
	})
	// Remove the dir externally — simulating a manual rm or git
	// worktree remove that bypassed biu.
	_ = os.RemoveAll(ghost)

	st, ok, err := s.VerifyAndResume("ghost-1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("expected ghost detection; got %+v", st)
	}
	// Sidecar should be gone.
	if _, err := os.Stat(s.pathFor("ghost-1")); !os.IsNotExist(err) {
		t.Errorf("ghost sidecar should have been deleted")
	}
}

func TestSaveRequiresSessionID(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := s.Save(State{}); err == nil {
		t.Errorf("missing session id should error")
	}
}

func TestDeleteMissingFileOK(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := s.Delete("nonexistent"); err != nil {
		t.Errorf("delete of missing file should be ok; got %v", err)
	}
}

func TestListSkipsGhosts(t *testing.T) {
	root := t.TempDir()
	s, _ := NewStore(root)
	live := filepath.Join(root, "live-wt")
	_ = os.MkdirAll(live, 0o755)
	_ = s.Save(State{SessionID: "live", Previous: root, Current: live})
	dead := filepath.Join(root, "dead-wt")
	_ = os.MkdirAll(dead, 0o755)
	_ = s.Save(State{SessionID: "dead", Previous: root, Current: dead})
	_ = os.RemoveAll(dead)

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "live" {
		t.Errorf("List should return only live entries; got %+v", got)
	}
}
