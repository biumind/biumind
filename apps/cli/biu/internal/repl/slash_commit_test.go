package repl

import (
	"strings"
	"testing"
)

func TestParseCommitFlags_dryRun(t *testing.T) {
	f := parseCommitFlags([]string{"/commit", "--dry-run"})
	if !f.dryRun {
		t.Error("dryRun not set")
	}
}

func TestParseCommitFlags_noStage(t *testing.T) {
	f := parseCommitFlags([]string{"/commit", "--no-stage"})
	if !f.noStage {
		t.Error("noStage not set")
	}
}

func TestParseCommitFlags_messageQuoted(t *testing.T) {
	f := parseCommitFlags([]string{"/commit", "-m", "\"fix:", "boom\""})
	if f.message != "fix: boom" {
		t.Errorf("message = %q, want 'fix: boom'", f.message)
	}
}

func TestParseCommitFlags_messageUnquoted(t *testing.T) {
	f := parseCommitFlags([]string{"/commit", "-m", "fix:", "x"})
	if f.message != "fix: x" {
		t.Errorf("message = %q", f.message)
	}
}

func TestParseCommitFlags_messageLong(t *testing.T) {
	f := parseCommitFlags([]string{"/commit", "--message", "feat:", "y"})
	if f.message != "feat: y" {
		t.Errorf("message = %q", f.message)
	}
}

func TestParseCommitFlags_messageStopsParsing(t *testing.T) {
	// -m consumes everything after; --dry-run after it should be part
	// of the message text, not a flag.
	f := parseCommitFlags([]string{"/commit", "-m", "fix", "--dry-run"})
	if f.dryRun {
		t.Error("--dry-run after -m should be message text, not a flag")
	}
	if !strings.Contains(f.message, "--dry-run") {
		t.Errorf("message should include --dry-run: %q", f.message)
	}
}

func TestParseCommitFlags_unknown(t *testing.T) {
	// Unknown flags are silently ignored — user gets default behaviour.
	f := parseCommitFlags([]string{"/commit", "--bogus"})
	if f.dryRun || f.noStage || f.message != "" {
		t.Errorf("bogus flag should not flip anything: %+v", f)
	}
}

func TestHandleCommit_noProvider(t *testing.T) {
	got := model{}.handleCommit([]string{"/commit"})
	if !strings.Contains(got, "provider/model not wired") {
		t.Errorf("missing wire-error: %s", got)
	}
}
