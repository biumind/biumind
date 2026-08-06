// Tests for the /remember slash command's parser + handler. The
// parser is the more interesting half — covering all four flag
// shapes plus rejection paths — so it gets the bulk of the cases.
//
// The handler tests use t.TempDir() + t.Setenv("HOME", …) to keep
// every write inside the test sandbox.

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/memory"
)

func TestParseRememberArgsDefaultsToUser(t *testing.T) {
	mt, body, err := parseRememberArgs("user wants chinese replies")
	if err != nil {
		t.Fatal(err)
	}
	if mt != memory.TypeUser {
		t.Errorf("default type should be user; got %q", mt)
	}
	if body != "user wants chinese replies" {
		t.Errorf("body lost: %q", body)
	}
}

func TestParseRememberArgsShortFlag(t *testing.T) {
	mt, body, err := parseRememberArgs("-t feedback don't mock the database")
	if err != nil {
		t.Fatal(err)
	}
	if mt != memory.TypeFeedback {
		t.Errorf("type should be feedback; got %q", mt)
	}
	if body != "don't mock the database" {
		t.Errorf("body wrong: %q", body)
	}
}

func TestParseRememberArgsLongFlag(t *testing.T) {
	mt, body, err := parseRememberArgs("--type project compliance deadline 2026-03-05")
	if err != nil {
		t.Fatal(err)
	}
	if mt != memory.TypeProject {
		t.Errorf("type should be project; got %q", mt)
	}
	if body != "compliance deadline 2026-03-05" {
		t.Errorf("body wrong: %q", body)
	}
}

func TestParseRememberArgsLongFlagEquals(t *testing.T) {
	mt, body, err := parseRememberArgs("--type=reference INGEST = pipeline tracker")
	if err != nil {
		t.Fatal(err)
	}
	if mt != memory.TypeReference {
		t.Errorf("type should be reference; got %q", mt)
	}
	if body != "INGEST = pipeline tracker" {
		t.Errorf("body wrong: %q", body)
	}
}

func TestParseRememberArgsRejectsBadType(t *testing.T) {
	_, _, err := parseRememberArgs("-t feedbcak typo wins")
	if err == nil {
		t.Error("typo should fail")
	}
}

func TestParseRememberArgsRejectsEmptyBody(t *testing.T) {
	if _, _, err := parseRememberArgs("-t user"); err == nil {
		t.Error("missing body after flag should fail")
	}
	if _, _, err := parseRememberArgs("-t user   "); err == nil {
		t.Error("whitespace-only body should fail")
	}
}

// End-to-end: handleRemember writes a real file into a temp HOME and
// returns a status line that names the type + filename.
func TestHandleRememberWritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &model{} // no engine — exercises the persist-but-don't-reload branch

	got := m.handleRemember("user wants Chinese replies for non-code")
	if !strings.HasPrefix(got, "/remember: saved user memory") {
		t.Errorf("status line shape wrong: %q", got)
	}
	dir := filepath.Join(home, ".biumind", "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	memCount := 0
	hasIndex := false
	for _, e := range entries {
		if e.Name() == "MEMORY.md" {
			hasIndex = true
			continue
		}
		if strings.HasSuffix(e.Name(), ".md") {
			memCount++
		}
	}
	if memCount != 1 {
		t.Errorf("want 1 memory file, got %d", memCount)
	}
	if !hasIndex {
		t.Errorf("MEMORY.md should be created")
	}
}

func TestHandleRememberHonoursTypeFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &model{}
	got := m.handleRemember("-t feedback don't mock the DB in integration tests")
	if !strings.Contains(got, "saved feedback memory") {
		t.Errorf("status line should report feedback type; got %q", got)
	}
	// File should be type-prefixed.
	entries, _ := os.ReadDir(filepath.Join(home, ".biumind", "memory"))
	feedbackHits := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "feedback-") {
			feedbackHits++
		}
	}
	if feedbackHits != 1 {
		t.Errorf("want 1 feedback-prefixed file, got %d", feedbackHits)
	}
}

func TestHandleRememberEmptyBodyShowsUsage(t *testing.T) {
	m := &model{}
	got := m.handleRemember("")
	if !strings.HasPrefix(got, "usage:") {
		t.Errorf("empty input should print usage; got %q", got)
	}
}

func TestHandleRememberInvalidTypeReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &model{}
	got := m.handleRemember("-t feedbcak typo")
	if !strings.Contains(got, "invalid type") {
		t.Errorf("expected typed error; got %q", got)
	}
}
