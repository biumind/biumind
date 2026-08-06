package repl

import (
	"strings"
	"testing"
)

// We don't run gh in tests — too environment-dependent. These tests
// verify the parse-shape logic that lives ahead of the gh shellout.

func TestHandleIssue_unknownFormHint(t *testing.T) {
	got := model{}.handleIssue([]string{"/issue", "garbage"})
	// Either "GitHub CLI not found" (no gh on PATH) or "unknown form".
	// Both are acceptable; we just want a non-empty informative string.
	if got == "" {
		t.Error("handler should always return a hint")
	}
	if !strings.Contains(got, "unknown form") &&
		!strings.Contains(got, "not found") &&
		!strings.Contains(got, "view") {
		t.Errorf("unexpected output: %s", got)
	}
}

func TestHandleIssue_commentMissingArgs(t *testing.T) {
	got := model{}.handleIssue([]string{"/issue", "comment"})
	if !strings.Contains(got, "need <n>") &&
		!strings.Contains(got, "not found") {
		t.Errorf("missing arg error: %s", got)
	}
}

func TestHandleIssue_closeMissingArgs(t *testing.T) {
	got := model{}.handleIssue([]string{"/issue", "close"})
	if !strings.Contains(got, "need <n>") &&
		!strings.Contains(got, "not found") {
		t.Errorf("missing arg error: %s", got)
	}
}

func TestHandlePRComments_nonNumericArg(t *testing.T) {
	got := model{}.handlePRComments([]string{"/pr-comments", "abc"})
	if !strings.Contains(got, "not a PR number") &&
		!strings.Contains(got, "not found") {
		t.Errorf("non-numeric arg should be flagged: %s", got)
	}
}
