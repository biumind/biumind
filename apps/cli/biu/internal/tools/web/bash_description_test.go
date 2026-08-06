// Sanity tests for the BashTool Description text. Verifies the
// steering bullets are present so a future cleanup doesn't
// accidentally truncate the prompt back to one line.

package web

import (
	"strings"
	"testing"
)

func TestBashDescriptionSteersAwayFromShellTools(t *testing.T) {
	got := BashTool{}.Description(nil)
	for _, want := range []string{
		// Tool-preference steering — this is the highest-leverage
		// part of the description; without it the model uses bash
		// `find / grep / cat / sed / echo` instead of the dedicated
		// tools and the user loses the permission UX entirely.
		"file search",
		"content search",
		"Read files",
		"Edit files",
		"Write files",
	} {
		// Match case-insensitively — the description text wraps these
		// terms in markdown lists with mixed casing.
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("description missing %q", want)
		}
	}
}

func TestBashDescriptionMentionsParallelism(t *testing.T) {
	got := BashTool{}.Description(nil)
	for _, want := range []string{
		"in parallel",
		"&&",
		"sequentially",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing parallelism guidance %q", want)
		}
	}
}

func TestBashDescriptionMentionsBackgroundAndSleep(t *testing.T) {
	got := BashTool{}.Description(nil)
	for _, want := range []string{
		"run_in_background",
		"sleep",
		// The model should know it gets notified on bg-task completion
		// rather than polling — wording should mention that path.
		"notified",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing %q", want)
		}
	}
}

func TestBashDescriptionMentionsGitSafety(t *testing.T) {
	got := BashTool{}.Description(nil)
	for _, want := range []string{
		"git reset --hard",
		"git push --force",
		"--no-verify",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing git-safety reference %q", want)
		}
	}
}
