package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook runner tests need /bin/sh")
	}
}

func TestRegistryAddAndMatch(t *testing.T) {
	r := NewRegistry()
	r.Add("user", map[string][]json.RawMessage{
		"PreToolUse": {json.RawMessage(`[
			{"matcher":"Bash","hooks":[{"type":"command","command":"echo bash"}]},
			{"matcher":"Edit|Write","hooks":[{"type":"command","command":"echo edit"}]}
		]`)},
		"UserPromptSubmit": {json.RawMessage(`[{"hooks":[{"type":"command","command":"echo prompt"}]}]`)},
	})
	if got := len(r.For(EventPreToolUse, "Bash")); got != 1 {
		t.Errorf("Bash matcher hits = %d", got)
	}
	if got := len(r.For(EventPreToolUse, "Edit")); got != 1 {
		t.Errorf("Edit matcher hits = %d", got)
	}
	if got := len(r.For(EventPreToolUse, "Glob")); got != 0 {
		t.Errorf("Glob shouldn't match Bash|Edit|Write; got %d", got)
	}
	// UserPromptSubmit has no matcher → matches anything.
	if got := len(r.For(EventUserPromptSubmit, "")); got != 1 {
		t.Errorf("empty matcher should match; got %d", got)
	}
}

func TestRunBasicJSONDecision(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	body := "#!/bin/sh\ncat > /dev/null\nprintf '%s' '{\"block\":true,\"reason\":\"nope\"}'"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	r.Add("user", map[string][]json.RawMessage{
		"PreToolUse": {json.RawMessage(`[{"matcher":"Bash","hooks":[{"type":"command","command":"` + script + `"}]}]`)},
	})

	results := Run(context.Background(), r.For(EventPreToolUse, "Bash"),
		EventPreToolUse, map[string]any{"tool_name": "Bash"})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Decision.Block {
		t.Errorf("decision.block expected true: %+v", results[0])
	}
	if results[0].Decision.Reason != "nope" {
		t.Errorf("reason = %q", results[0].Decision.Reason)
	}
	if FirstBlocking(results) == nil {
		t.Errorf("FirstBlocking should return the entry")
	}
}

func TestRunExit2IsBlocking(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "exit2.sh")
	body := "#!/bin/sh\ncat > /dev/null\nprintf 'denied\\n' >&2\nexit 2"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.Add("local", map[string][]json.RawMessage{
		"PreToolUse": {json.RawMessage(`[{"matcher":"Bash","hooks":[{"type":"command","command":"` + script + `"}]}]`)},
	})
	results := Run(context.Background(), r.For(EventPreToolUse, "Bash"),
		EventPreToolUse, map[string]any{"tool_name": "Bash"})
	if len(results) != 1 || results[0].ExitCode != 2 || !results[0].IsBlocking() {
		t.Errorf("exit-2 hook should block: %+v", results)
	}
	if !strings.Contains(CollectStderr(results), "denied") {
		t.Errorf("stderr aggregation missing 'denied'")
	}
}

func TestRunUnsupportedTypeRecorded(t *testing.T) {
	r := NewRegistry()
	r.Add("user", map[string][]json.RawMessage{
		"PreToolUse": {json.RawMessage(`[{"matcher":"Bash","hooks":[{"type":"prompt","prompt":"hi"}]}]`)},
	})
	results := Run(context.Background(), r.For(EventPreToolUse, "Bash"),
		EventPreToolUse, map[string]any{})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected unsupported-type error: %+v", results)
	}
	if !strings.Contains(results[0].Err.Error(), "not supported") {
		t.Errorf("err = %v", results[0].Err)
	}
}
