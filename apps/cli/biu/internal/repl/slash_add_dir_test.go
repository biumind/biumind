// Tests for /add-dir + /remove-dir slash handlers.
//
// We don't drive a full Bubble Tea program — just the handler
// methods on `model`. Engine is a stub built via newTestModel
// (defined in statusline_test.go) so the slash handlers see a real
// permission context.

package repl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleAddDir_MissingPath(t *testing.T) {
	m := newTestModel(t, "test-model")
	got := m.handleAddDir([]string{"/add-dir"})
	if !strings.Contains(got, "missing path") {
		t.Errorf("got %q, want hint about missing path", got)
	}
	if !strings.Contains(got, "usage:") {
		t.Errorf("got %q, want usage hint", got)
	}
}

func TestHandleAddDir_PathNotFound(t *testing.T) {
	m := newTestModel(t, "test-model")
	got := m.handleAddDir([]string{"/add-dir", "/definitely/missing/biumind/xyz"})
	if !strings.Contains(got, "not found") {
		t.Errorf("got %q, want not-found message", got)
	}
}

func TestHandleAddDir_NotADirectory(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, "test-model")
	got := m.handleAddDir([]string{"/add-dir", file})
	if !strings.Contains(got, "not a directory") {
		t.Errorf("got %q, want not-a-dir message", got)
	}
}

func TestHandleAddDir_SuccessSession(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, "test-model")
	got := m.handleAddDir([]string{"/add-dir", target})
	if !strings.Contains(got, "Added") {
		t.Errorf("got %q, want success message", got)
	}
	if !strings.Contains(got, "this session") {
		t.Errorf("session destination should be reflected; got %q", got)
	}
	dirs := m.engine.Permissions().AdditionalDirectoryPaths()
	if len(dirs) != 1 || dirs[0] != target {
		t.Errorf("ctx should hold target; got %+v", dirs)
	}
}

func TestHandleAddDir_SuccessRemember(t *testing.T) {
	// t.Chdir (not os.Chdir) registers a Cleanup that restores the
	// original working directory. Without it this test leaves the
	// process parked inside a t.TempDir that the framework then removes,
	// so every later test in the package inherits a deleted cwd — their
	// /bin/sh forks emit a "getcwd: cannot access parent directories"
	// line into bgtask buffers, which flaked TestTasksOutputDeltaCursor.
	// Cleanup LIFO restores cwd before the tempdir removal runs.
	cwd := t.TempDir()
	t.Chdir(cwd)
	// Target lives in a SIBLING tempdir so it's not "already inside"
	// cwd — we want to exercise the persistence path, not the
	// already-in-working-dir branch.
	target := t.TempDir()
	m := newTestModel(t, "test-model")
	got := m.handleAddDir([]string{"/add-dir", target, "--remember"})
	if !strings.Contains(got, "Added") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got, ".biumind/settings.local.json") {
		t.Errorf("--remember should reference settings.local.json; got %q", got)
	}
	// Verify the file was written.
	settingsPath := filepath.Join(cwd, ".biumind", "settings.local.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings file missing: %v", err)
	}
	var s struct {
		Permissions struct {
			AdditionalDirectories []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(s.Permissions.AdditionalDirectories) != 1 || s.Permissions.AdditionalDirectories[0] != target {
		t.Errorf("file should hold target; got %+v", s.Permissions.AdditionalDirectories)
	}
}

func TestHandleAddDir_AlreadyInside(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	sub := filepath.Join(parent, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, "test-model")
	m.engine.Permissions().AddDirectories("session", []string{parent})

	got := m.handleAddDir([]string{"/add-dir", sub})
	if !strings.Contains(got, "already accessible") {
		t.Errorf("got %q, want already-in-working-dir message", got)
	}
}

func TestHandleRemoveDir_NotRegistered(t *testing.T) {
	m := newTestModel(t, "test-model")
	tmp := t.TempDir()
	got := m.handleRemoveDir([]string{"/remove-dir", tmp})
	if !strings.Contains(got, "not a registered") {
		t.Errorf("got %q, want not-registered message", got)
	}
}

func TestHandleRemoveDir_SessionEntry(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, "test-model")
	m.engine.Permissions().AddDirectories("session", []string{target})

	got := m.handleRemoveDir([]string{"/remove-dir", target})
	if !strings.Contains(got, "Removed") {
		t.Errorf("got %q, want Removed message", got)
	}
	if dirs := m.engine.Permissions().AdditionalDirectoryPaths(); len(dirs) != 0 {
		t.Errorf("dir should be gone; got %+v", dirs)
	}
}

func TestParseAddDirArgs(t *testing.T) {
	cases := []struct {
		in       []string
		wantPath string
		wantRem  bool
		wantErr  bool
	}{
		{[]string{"/add-dir"}, "", false, true},
		{[]string{"/add-dir", "/tmp/foo"}, "/tmp/foo", false, false},
		{[]string{"/add-dir", "/tmp/foo", "--remember"}, "/tmp/foo", true, false},
		{[]string{"/add-dir", "--remember", "/tmp/foo"}, "/tmp/foo", true, false},
		{[]string{"/add-dir", "--save", "/tmp/foo"}, "/tmp/foo", true, false},
		{[]string{"/add-dir", "--persist", "/tmp/foo"}, "/tmp/foo", true, false},
	}
	for _, c := range cases {
		path, rem, err := parseAddDirArgs(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if path != c.wantPath {
			t.Errorf("%v: path=%q want %q", c.in, path, c.wantPath)
		}
		if rem != c.wantRem {
			t.Errorf("%v: remember=%v want %v", c.in, rem, c.wantRem)
		}
	}
}
