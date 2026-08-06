//go:build integration

// Layer B LLM-driven CLI cases (B8 / B9 / B10).
//
// All tests in this file gate on RequireRealAPI — they're skipped
// unless ANTHROPIC_API_KEY is set in the test environment. When set,
// each test seeds ~/.biu/config.toml with a `mode = "direct"` block
// that carries the same key so biu's wiring layer accepts it.

package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/test/integration/harness"
)

// TestB8_HeadlessText runs a single-turn `biu --headless --prompt`
// invocation. Expectations: exit 0, stdout carries the assistant
// reply text. We force a deterministic single-token answer so the
// case stays cheap and stable across model swaps.
func TestB8_HeadlessText(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb,
		Args: []string{
			"--mode=direct",
			"--headless",
			"--no-log",
			"--prompt", "Reply with literally the single word PONG.",
		},
		Timeout: 60 * time.Second,
	})
	out := r.CombinedOK(t)
	if !strings.Contains(strings.ToUpper(out), "PONG") {
		t.Errorf("expected PONG in headless reply\nstdout: %q\nstderr: %q", out, r.Stderr)
	}
}

// TestB9_HeadlessJSONL drives `--headless --json` and asserts every
// stdout line is valid JSON. The AG-UI-style stream must contain at
// minimum one RUN_STARTED, one TEXT_MESSAGE_CONTENT, and one
// RUN_FINISHED — that's the contract IDE / SDK consumers depend on.
func TestB9_HeadlessJSONL(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb,
		Args: []string{
			"--mode=direct",
			"--headless",
			"--json",
			"--no-log",
			"--prompt", "Reply with literally OK.",
		},
		Timeout: 60 * time.Second,
	})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", r.ExitCode, r.Stderr, r.Stdout)
	}

	required := map[string]bool{
		"RUN_STARTED":          false,
		"TEXT_MESSAGE_START":   false,
		"TEXT_MESSAGE_CONTENT": false,
		"TEXT_MESSAGE_END":     false,
		"RUN_FINISHED":         false,
	}
	lineNo := 0
	for _, raw := range strings.Split(strings.TrimSpace(r.Stdout), "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		lineNo++
		var ev map[string]any
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", lineNo, err, raw)
		}
		typ, _ := ev["type"].(string)
		if _, want := required[typ]; want {
			required[typ] = true
		}
	}
	for typ, seen := range required {
		if !seen {
			t.Errorf("AG-UI stream missing required event type %q\nstdout:\n%s",
				typ, r.Stdout)
		}
	}
}

// TestB10_HeadlessPermissionDeny exercises `--permission-policy=deny`.
// We prompt the model to delete a file (a destructive op the engine
// must permission-check); with the deny policy the tool call should
// return a denial and the model must back off cleanly without
// looping. We assert exit 0 (deny isn't a fatal error) plus a
// recognisable acknowledgement string in the reply.
func TestB10_HeadlessPermissionDeny(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)

	// Give the model a concrete artifact to "delete" so the prompt
	// resolves to a real Bash invocation rather than vague chitchat.
	sb.WriteFile(t, "scratch.txt", []byte("delete me\n"))

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb,
		Args: []string{
			"--mode=direct",
			"--headless",
			"--no-log",
			"--permission-policy=deny",
			"--prompt", "Delete the file scratch.txt in the current directory using `rm`. " +
				"If the tool call is denied or fails, reply with literally 'DENIED-OK' on its own line and stop.",
		},
		Timeout: 90 * time.Second,
	})
	out := r.CombinedOK(t)
	upper := strings.ToUpper(out)
	if !strings.Contains(upper, "DENIED") && !strings.Contains(upper, "DENY") {
		t.Errorf("expected 'DENIED' or 'DENY' acknowledgement in reply\nstdout: %s\nstderr: %s",
			out, r.Stderr)
	}
}
