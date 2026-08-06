package permissions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDirectoryForWorkspace_Empty(t *testing.T) {
	r := ValidateDirectoryForWorkspace("", NewContext(), "/repo")
	if r.Kind != DirValidEmpty {
		t.Errorf("kind = %s, want %s", r.Kind, DirValidEmpty)
	}
	if !strings.Contains(r.HelpMessage(), "provide") {
		t.Errorf("help: %q", r.HelpMessage())
	}
}

func TestValidateDirectoryForWorkspace_PathNotFound(t *testing.T) {
	r := ValidateDirectoryForWorkspace("/definitely/does/not/exist/biumind", NewContext(), "/repo")
	if r.Kind != DirValidPathNotFound {
		t.Errorf("kind = %s, want %s", r.Kind, DirValidPathNotFound)
	}
	if !strings.Contains(r.HelpMessage(), "not found") {
		t.Errorf("help: %q", r.HelpMessage())
	}
}

func TestValidateDirectoryForWorkspace_NotADirectory(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	r := ValidateDirectoryForWorkspace(file, NewContext(), "/repo")
	if r.Kind != DirValidNotADirectory {
		t.Errorf("kind = %s, want %s", r.Kind, DirValidNotADirectory)
	}
	if !strings.Contains(r.HelpMessage(), "not a directory") {
		t.Errorf("help: %q", r.HelpMessage())
	}
	if !strings.Contains(r.HelpMessage(), tmp) {
		t.Errorf("help should suggest parent dir; got %q", r.HelpMessage())
	}
}

func TestValidateDirectoryForWorkspace_AlreadyInside(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	r := ValidateDirectoryForWorkspace(sub, NewContext(), tmp)
	if r.Kind != DirValidAlreadyInWorkingDir {
		t.Errorf("kind = %s, want %s; r=%+v", r.Kind, DirValidAlreadyInWorkingDir, r)
	}
	// Match against canonicalized working dir (filepath.EvalSymlinks may
	// resolve /tmp differently on macOS) — just check that the input
	// path's parent is reported as the existing working dir.
	if r.ExistingWorkingDir == "" {
		t.Errorf("ExistingWorkingDir should be set; got %+v", r)
	}
	if !strings.Contains(r.HelpMessage(), "already accessible") {
		t.Errorf("help: %q", r.HelpMessage())
	}
}

func TestValidateDirectoryForWorkspace_Success(t *testing.T) {
	tmp := t.TempDir()
	other := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cwd := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	r := ValidateDirectoryForWorkspace(other, NewContext(), cwd)
	if r.Kind != DirValidSuccess {
		t.Fatalf("kind = %s, want %s; r=%+v", r.Kind, DirValidSuccess, r)
	}
	if r.AbsolutePath == "" {
		t.Errorf("AbsolutePath should be set on success")
	}
}

func TestValidateDirectoryForWorkspace_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	r := ValidateDirectoryForWorkspace("~", NewContext(), "/repo")
	if r.Kind != DirValidSuccess {
		t.Fatalf("home should validate; r=%+v", r)
	}
	if r.AbsolutePath == "~" {
		t.Errorf("AbsolutePath should be expanded; got %q", r.AbsolutePath)
	}
}

func TestValidateDirectoryForWorkspace_AlreadyInExtraDir(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	sub := filepath.Join(parent, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	c := NewContext()
	c.AddDirectories(SrcSession, []string{parent})
	r := ValidateDirectoryForWorkspace(sub, c, "/some/cwd")
	if r.Kind != DirValidAlreadyInWorkingDir {
		t.Errorf("kind = %s want %s", r.Kind, DirValidAlreadyInWorkingDir)
	}
}
