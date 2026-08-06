package repl

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// Drift detector: the embedded copy must match the canonical
// RELEASE_NOTES.md at the biu CLI root. If a release-notes update
// landed in the canonical file but the embed wasn't refreshed, this
// fires loudly so the slash output doesn't silently lag the source
// of truth.
//
// The path is relative to this test file (internal/repl/) so it
// works from `go test` regardless of cwd.
func TestReleaseNotes_embedMatchesCanonical(t *testing.T) {
	canonical, err := os.ReadFile("../../RELEASE_NOTES.md")
	if err != nil {
		t.Skipf("RELEASE_NOTES.md not present at expected path: %v", err)
	}
	embedHash := sha256Sum([]byte(releaseNotesBody))
	canonicalHash := sha256Sum(canonical)
	if embedHash != canonicalHash {
		t.Errorf("release_notes_embed.md is stale; refresh with:\n"+
			"  cp apps/cli/biu/RELEASE_NOTES.md apps/cli/biu/internal/repl/release_notes_embed.md\n"+
			"embed sha256:    %s\ncanonical sha256: %s",
			embedHash, canonicalHash)
	}
}

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// /release-notes (no args) tails the most recent N lines.
func TestSlashReleaseNotes_defaultTails(t *testing.T) {
	if releaseNotesBody == "" {
		t.Skip("notes empty in this build")
	}
	out := model{}.handleReleaseNotes([]string{"/release-notes"})
	if len(out) == 0 {
		t.Fatal("empty output")
	}
	// When the file has more than DefaultReleaseNotesTail lines,
	// the output should mention "showing last N of M lines".
	totalLines := strings.Count(releaseNotesBody, "\n") + 1
	if totalLines > DefaultReleaseNotesTail {
		if !strings.Contains(out, "showing last") {
			t.Errorf("missing tail hint when notes are long: %d lines", totalLines)
		}
	}
}

func TestSlashReleaseNotes_full(t *testing.T) {
	out := model{}.handleReleaseNotes([]string{"/release-notes", "full"})
	if len(out) < len(releaseNotesBody)/2 {
		t.Errorf("`full` output suspiciously short: %d bytes", len(out))
	}
}

func TestSlashReleaseNotes_grep(t *testing.T) {
	if !strings.Contains(strings.ToLower(releaseNotesBody), "p20") {
		t.Skip("no P20 anchor to grep for")
	}
	out := model{}.handleReleaseNotes([]string{"/release-notes", "P20"})
	if !strings.Contains(out, "match") {
		t.Errorf("output should contain match count: %s", out)
	}
	if !strings.Contains(out, "»") {
		t.Errorf("matched lines should be marked with »: %s", out[:200])
	}
}

func TestSlashReleaseNotes_emptyGrep(t *testing.T) {
	out := model{}.handleReleaseNotes([]string{"/release-notes",
		"this-string-definitely-not-in-notes-zzzz"})
	if !strings.Contains(out, "no matches") {
		t.Errorf("missing no-match line: %s", out)
	}
}
