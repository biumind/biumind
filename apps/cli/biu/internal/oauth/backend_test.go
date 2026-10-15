package oauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStoreOpenFileBackend — Open returns a file-backed store at the
// override path (file is the only backend).
func TestStoreOpenFileBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend() != "file" {
		t.Errorf("expected file backend; got %q", s.Backend())
	}
	if s.Path() != path {
		t.Errorf("path mismatch: got %q want %q", s.Path(), path)
	}
}

// TestStoreFileRoundTrip — Save → Load → Delete against a temp file,
// including the 0600 permission contract.
func TestStoreFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.Load(); err != nil || got.AccessToken != "" {
		t.Errorf("missing file should load as empty: %+v err=%v", got, err)
	}

	want := Tokens{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Round(time.Second),
		Provider:     "test",
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm=%o want 600", fi.Mode().Perm())
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, want)
	}

	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone after Delete")
	}
	// Double-delete is not an error.
	if err := s.Delete(); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}
