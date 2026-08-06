package repl

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

func TestFormatProgressStdoutLine(t *testing.T) {
	got := formatProgress(engine.ProgressData{
		"kind": "stdout", "line": "hello world",
	})
	if !strings.Contains(got, "hello world") {
		t.Errorf("expected line text in output: %q", got)
	}
	if strings.HasPrefix(strings.TrimLeft(got, " "), "!") {
		t.Errorf("stdout should not get the stderr `!` prefix: %q", got)
	}
}

func TestFormatProgressStderrPrefixed(t *testing.T) {
	got := formatProgress(engine.ProgressData{
		"kind": "stderr", "line": "warn: x",
	})
	if !strings.Contains(got, "! warn: x") {
		t.Errorf("stderr should carry `!` marker: %q", got)
	}
}

func TestFormatProgressMatchedCount(t *testing.T) {
	got := formatProgress(engine.ProgressData{
		"kind": "grep_progress", "matched": 42,
	})
	if !strings.Contains(got, "matched: 42") {
		t.Errorf("matched count missing: %q", got)
	}
}

func TestFormatProgressEmpty(t *testing.T) {
	if got := formatProgress(nil); got != "" {
		t.Errorf("nil should yield empty: %q", got)
	}
	if got := formatProgress(engine.ProgressData{"kind": "stdout"}); got != "" {
		t.Errorf("missing line should yield empty: %q", got)
	}
}

func TestProgressRingBufferCap(t *testing.T) {
	r := toolRow{}
	for i := 0; i < ProgressLineCap*3; i++ {
		line := formatProgress(engine.ProgressData{
			"kind": "stdout", "line": "x",
		})
		r.ProgressLines = append(r.ProgressLines, line)
		if len(r.ProgressLines) > ProgressLineCap {
			r.ProgressLines = r.ProgressLines[len(r.ProgressLines)-ProgressLineCap:]
		}
	}
	if len(r.ProgressLines) != ProgressLineCap {
		t.Errorf("ring buffer should cap at %d; got %d",
			ProgressLineCap, len(r.ProgressLines))
	}
}

func TestRenderToolRowsShowsProgressForRunning(t *testing.T) {
	rows := []toolRow{{
		ID: "u1", Name: "Bash", Phase: "running",
		Input:         map[string]any{"command": "ls"},
		ProgressLines: []string{"    line1", "    line2"},
	}}
	got := renderToolRows(rows)
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") {
		t.Errorf("running row should render progress: %q", got)
	}
}

func TestRenderToolRowsHidesProgressForDone(t *testing.T) {
	rows := []toolRow{{
		ID: "u1", Name: "Bash", Phase: "done",
		ProgressLines: []string{"    leftover"},
	}}
	got := renderToolRows(rows)
	if strings.Contains(got, "leftover") {
		t.Errorf("done row should not show progress: %q", got)
	}
}
