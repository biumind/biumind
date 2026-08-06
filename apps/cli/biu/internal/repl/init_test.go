// Tests for the /init slash handler. Detector behaviour is locked
// in projectinit/; here we cover the REPL-side flag parsing +
// file-write semantics: don't overwrite without --force, --dry-run
// prints without writing, output mentions detected languages.

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirTmp creates + cd's to a fresh tmp dir AND returns the
// resolved-symlinks form (matches what os.Getwd returns on macOS).
//
// Restores the original cwd via t.Cleanup so subsequent tests in
// the same package don't inherit a deleted directory after
// t.TempDir() removes the fixture — would-be /bin/sh forks would
// emit "getcwd failed" stderr that pollutes other fixtures' output
// buffers.
func chdirTmp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("cleanup: chdir back to %s failed: %v", orig, err)
		}
	})
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestInitFreshDirectoryWritesFile(t *testing.T) {
	cwd := chdirTmp(t)
	if err := os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := model{}
	got := m.handleInit([]string{"/init"})
	if !strings.Contains(got, "wrote BIUMIND.md") {
		t.Errorf("status line missing write ack; got %q", got)
	}
	if !strings.Contains(got, "Go") {
		t.Errorf("status line should name detected language; got %q", got)
	}
	body, err := os.ReadFile(filepath.Join(cwd, "BIUMIND.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"# BIUMIND.md", "## Project type", "go test ./..."} {
		if !strings.Contains(string(body), must) {
			t.Errorf("file missing %q; got:\n%s", must, body)
		}
	}
}

// Bare /init in an existing-file dir refuses to overwrite — saves
// the user from accidentally clobbering hand-written content.
func TestInitRefusesToOverwriteWithoutForce(t *testing.T) {
	cwd := chdirTmp(t)
	keep := []byte("# my hand-written file\n")
	if err := os.WriteFile(filepath.Join(cwd, "BIUMIND.md"), keep, 0o644); err != nil {
		t.Fatal(err)
	}
	m := model{}
	got := m.handleInit([]string{"/init"})
	if !strings.Contains(got, "already exists") {
		t.Errorf("expected refusal; got %q", got)
	}
	body, _ := os.ReadFile(filepath.Join(cwd, "BIUMIND.md"))
	if string(body) != string(keep) {
		t.Errorf("file should be unchanged; got %q", body)
	}
}

// --force overwrites cleanly + the status line says so.
func TestInitForceOverwrites(t *testing.T) {
	cwd := chdirTmp(t)
	_ = os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "BIUMIND.md"), []byte("old"), 0o644)
	m := model{}
	got := m.handleInit([]string{"/init", "--force"})
	if !strings.Contains(got, "overwrote") {
		t.Errorf("status line should say overwrote; got %q", got)
	}
	body, _ := os.ReadFile(filepath.Join(cwd, "BIUMIND.md"))
	if !strings.Contains(string(body), "go test ./...") {
		t.Errorf("file should have been regenerated; got %q", body)
	}
}

// --dry-run never writes the file, even if one doesn't exist.
func TestInitDryRunDoesNotWrite(t *testing.T) {
	cwd := chdirTmp(t)
	_ = os.WriteFile(filepath.Join(cwd, "Cargo.toml"), []byte(""), 0o644)
	m := model{}
	got := m.handleInit([]string{"/init", "--dry-run"})
	if !strings.HasPrefix(got, "/init --dry-run:") {
		t.Errorf("dry-run line missing; got %q", got)
	}
	if !strings.Contains(got, "cargo test") {
		t.Errorf("dry-run output should embed render preview; got %q", got)
	}
	if _, err := os.Stat(filepath.Join(cwd, "BIUMIND.md")); err == nil {
		t.Errorf("dry-run must NOT write the file")
	}
}

// --dry-run on an existing file is also fine (no overwrite).
func TestInitDryRunOnExistingFileNeverWrites(t *testing.T) {
	cwd := chdirTmp(t)
	_ = os.WriteFile(filepath.Join(cwd, "BIUMIND.md"), []byte("keep me\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module x\n"), 0o644)
	m := model{}
	_ = m.handleInit([]string{"/init", "--dry-run"})
	body, _ := os.ReadFile(filepath.Join(cwd, "BIUMIND.md"))
	if string(body) != "keep me\n" {
		t.Errorf("--dry-run must not touch existing file; got %q", body)
	}
}

// Empty cwd with no manifests — the status line is honest: "no
// manifest detected — fill in the placeholders".
func TestInitEmptyDirSurfacesGuidance(t *testing.T) {
	chdirTmp(t)
	m := model{}
	got := m.handleInit([]string{"/init"})
	if !strings.Contains(got, "no manifest detected") {
		t.Errorf("status line should explain empty detection; got %q", got)
	}
}

// Unknown flag path — reject with a useful usage hint.
func TestInitRejectsUnknownFlag(t *testing.T) {
	chdirTmp(t)
	m := model{}
	got := m.handleInit([]string{"/init", "--bogus"})
	if !strings.Contains(got, "unknown flag") {
		t.Errorf("expected unknown-flag rejection; got %q", got)
	}
}

// Short forms work as documented (-f / -n).
func TestInitShortFlagsAreAccepted(t *testing.T) {
	cwd := chdirTmp(t)
	_ = os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "BIUMIND.md"), []byte("old"), 0o644)
	m := model{}
	if got := m.handleInit([]string{"/init", "-f"}); !strings.Contains(got, "overwrote") {
		t.Errorf("`-f` should overwrite; got %q", got)
	}
	// -n preview after -f wrote: no further write but preview shown.
	if got := m.handleInit([]string{"/init", "-n"}); !strings.HasPrefix(got, "/init --dry-run:") {
		t.Errorf("`-n` should dry-run; got %q", got)
	}
}
