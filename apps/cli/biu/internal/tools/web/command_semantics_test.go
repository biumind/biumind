// Tests for command-aware exit-code interpretation. Two layers:
//
//   - heuristicBaseCommand + splitTopLevelSegments — the parser
//     half. Drives the table at the bottom of the file.
//   - interpretCommandResult — the dispatcher half. Confirms each
//     special-cased command produces the expected (isError, message)
//     pair, and that uncovered commands fall through to default
//     (exit 0 = ok, anything else = error).
//
// Plus a behavioural test through the full BashTool path so the
// integration with bash.go (where the change is observable) doesn't
// silently regress.

package web

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

func TestInterpretCommandResultGrep(t *testing.T) {
	cases := []struct {
		exit       int
		wantErr    bool
		wantNoteIn string // substring expected in note (empty = no note)
	}{
		{0, false, ""},
		{1, false, "No matches found"},
		{2, true, ""},
	}
	for _, c := range cases {
		isErr, note := interpretCommandResult("grep foo *.txt", c.exit, "", "")
		if isErr != c.wantErr {
			t.Errorf("grep exit=%d: isErr=%v want %v", c.exit, isErr, c.wantErr)
		}
		if c.wantNoteIn != "" && !strings.Contains(note, c.wantNoteIn) {
			t.Errorf("grep exit=%d: note %q missing %q", c.exit, note, c.wantNoteIn)
		}
		if c.wantNoteIn == "" && note != "" {
			t.Errorf("grep exit=%d: unexpected note %q", c.exit, note)
		}
	}
}

func TestInterpretCommandResultRg(t *testing.T) {
	// rg shares grep semantics; verify exit=1 doesn't trip IsError.
	isErr, _ := interpretCommandResult("rg foo", 1, "", "")
	if isErr {
		t.Error("rg exit 1 must not be an error")
	}
}

func TestInterpretCommandResultDiff(t *testing.T) {
	for _, exit := range []int{0, 1} {
		isErr, _ := interpretCommandResult("diff a.txt b.txt", exit, "", "")
		if isErr {
			t.Errorf("diff exit %d must not be an error", exit)
		}
	}
	if isErr, _ := interpretCommandResult("diff a b", 2, "", ""); !isErr {
		t.Error("diff exit 2 must be an error")
	}
	_, note := interpretCommandResult("diff a b", 1, "", "")
	if !strings.Contains(note, "Files differ") {
		t.Errorf("diff exit 1 should note 'Files differ'; got %q", note)
	}
}

func TestInterpretCommandResultFind(t *testing.T) {
	for _, exit := range []int{0, 1} {
		isErr, _ := interpretCommandResult("find . -name x", exit, "", "")
		if isErr {
			t.Errorf("find exit %d must not be an error", exit)
		}
	}
	if isErr, _ := interpretCommandResult("find . -name x", 2, "", ""); !isErr {
		t.Error("find exit 2 must be an error")
	}
}

func TestInterpretCommandResultTestAndBracket(t *testing.T) {
	// Both `test` and `[` are POSIX boolean evaluators — exit 1 means
	// the condition was false, not an error.
	for _, cmd := range []string{"test -f /etc/hosts", "[ -f /etc/hosts ]"} {
		isErr, _ := interpretCommandResult(cmd, 1, "", "")
		if isErr {
			t.Errorf("%s exit 1 must not be an error", cmd)
		}
	}
	if isErr, _ := interpretCommandResult("test x = y", 2, "", ""); !isErr {
		t.Error("test exit 2 must be an error")
	}
}

// Default semantics — any command not in the table goes here.
func TestInterpretCommandResultDefault(t *testing.T) {
	if isErr, _ := interpretCommandResult("echo hi", 0, "", ""); isErr {
		t.Error("echo exit 0 must succeed")
	}
	if isErr, _ := interpretCommandResult("cat missing", 1, "", ""); !isErr {
		t.Error("cat exit 1 must be an error (not a special-case command)")
	}
	if isErr, _ := interpretCommandResult("nonexistent-command-xyz", 127, "", ""); !isErr {
		t.Error("default exit 127 must be an error")
	}
}

// Pipelines — sh's $? reports the last segment's exit, so the
// semantic dispatch follows the last command name.
func TestHeuristicBaseCommandPicksLastSegment(t *testing.T) {
	cases := map[string]string{
		"ls | grep foo":           "grep",
		"cat a | rg pat":          "rg",
		"true && diff a b":        "diff",
		"false; test -f x":        "test",
		"a || b || grep z":        "grep",
		"echo hi":                 "echo",
		"":                        "",
		"   ":                     "",
		`grep 'pipe | here' file`: "grep", // pipe inside quotes ignored
		`grep "x|y" file`:         "grep",
		"FOO=bar grep needle":     "grep", // env-var prefix skipped
		"FOO=bar BAZ=qux rg pat":  "rg",
	}
	for in, want := range cases {
		if got := heuristicBaseCommand(in); got != want {
			t.Errorf("heuristicBaseCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

// `ls | grep foo` exit 1 must NOT flip IsError because grep semantics
// kick in even with the leading `ls` segment.
func TestInterpretPipelineUsesLastCommand(t *testing.T) {
	isErr, note := interpretCommandResult("ls | grep foo", 1, "", "")
	if isErr {
		t.Error("piped grep exit 1 must not be an error")
	}
	if !strings.Contains(note, "No matches") {
		t.Errorf("expected grep note; got %q", note)
	}
}

// splitTopLevelSegments — quoting/escape handling.
func TestSplitTopLevelSegmentsRespectsQuotes(t *testing.T) {
	got := splitTopLevelSegments(`echo 'a; b' && echo "c|d"`)
	if len(got) != 2 {
		t.Fatalf("expected 2 segments, got %d (%q)", len(got), got)
	}
	if !strings.Contains(got[0], "'a; b'") {
		t.Errorf("first segment lost quoted contents: %q", got[0])
	}
	if !strings.Contains(got[1], `"c|d"`) {
		t.Errorf("second segment lost quoted contents: %q", got[1])
	}
}

func TestSplitTopLevelSegmentsRespectsEscape(t *testing.T) {
	got := splitTopLevelSegments(`echo a\;b ; echo c`)
	if len(got) != 2 {
		t.Fatalf("expected 2 segments, got %d (%q)", len(got), got)
	}
}

// Behavioural: drive the BashTool through a `grep` that finds nothing
// and confirm IsError stays false. Catches accidental regression of
// the integration in bash.go.
func TestBashToolGrepNoMatchNotError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := BashTool{}
	out, _ := tool.Call(context.Background(), map[string]any{
		// Search for a token guaranteed not to match anywhere in /etc/hosts.
		"command": `grep "zzz-no-match-zzz-${RANDOM}" /etc/hosts`,
	}, &engine.ToolEnv{})
	if out.IsError {
		t.Errorf("grep with no matches must not flip IsError; got %+v", out)
	}
	body := flattenBg(out)
	if !strings.Contains(body, "No matches found") {
		t.Errorf("expected semantic note 'No matches found'; got %q", body)
	}
}

// Behavioural negative case: a real failure (nonexistent command) is
// still an error.
func TestBashToolGenuineFailureStillErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := BashTool{}
	out, _ := tool.Call(context.Background(), map[string]any{
		"command": "nonexistent-command-xyz-12345",
	}, &engine.ToolEnv{})
	if !out.IsError {
		t.Errorf("nonexistent command should remain IsError; got %+v", out)
	}
}
