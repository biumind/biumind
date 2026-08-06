// Hook runner — fires every matching command hook in parallel,
// collects results, and surfaces the first blocking outcome.
//
// Wire contract per command:
//
//   stdin:  one JSON object (event-specific shape) + newline
//   stdout: optional JSON; parsed into Decision
//   stderr: forwarded to caller (also logged)
//   exitCode 2 ⇒ block; other non-zero ⇒ warn-only
//
// The runner is intentionally simple — no streaming progress, no
// cancellation propagation beyond the parent context.

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout caps a single hook command. Individual commands can
// override via Command.Timeout (seconds).
const DefaultTimeout = 60 * time.Second

// Run fires every entry in the slice with the supplied stdin payload.
// Results are returned in the same order as `entries`. Errors from a
// single hook (timeout, missing shell) become Result.Err — the runner
// never returns a top-level error.
//
// Hooks run concurrently because there's no ordering guarantee; we
// do, however, preserve input order in the output slice.
func Run(
	parent context.Context,
	entries []Entry,
	event Event,
	stdin map[string]any,
) []Result {
	if len(entries) == 0 {
		return nil
	}
	payload, err := json.Marshal(stdin)
	if err != nil {
		// Single-shot failure: record once per entry so callers can see
		// which event failed.
		out := make([]Result, len(entries))
		for i, e := range entries {
			out[i] = Result{
				Source: e.Source, Event: event,
				Command: e.Command.Command,
				Err:     fmt.Errorf("marshal stdin: %w", err),
			}
		}
		return out
	}

	results := make([]Result, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func(idx int, entry Entry) {
			defer wg.Done()
			results[idx] = runOne(parent, entry, event, payload)
		}(i, e)
	}
	wg.Wait()
	return results
}

func runOne(parent context.Context, entry Entry, event Event, stdin []byte) Result {
	// Internal handlers go through a separate dispatch — no
	// subprocess, no shell. See internal_handlers.go for the
	// registry and the rationale.
	if entry.Command.Type == "internal" {
		return runInternal(parent, entry, event, stdin)
	}

	r := Result{Source: entry.Source, Event: event, Command: entry.Command.Command}

	// Skip non-command hooks for now (P0 scope). Record + move on so
	// settings round-trip cleanly.
	if entry.Command.Type != "" && entry.Command.Type != "command" {
		r.Err = fmt.Errorf("hook type %q not supported in this build", entry.Command.Type)
		return r
	}
	if entry.Command.Command == "" {
		r.Err = fmt.Errorf("empty hook command")
		return r
	}

	timeout := DefaultTimeout
	if entry.Command.Timeout > 0 {
		timeout = time.Duration(entry.Command.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	shell := entry.Command.Shell
	if shell == "" {
		shell = "sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", entry.Command.Command)
	cmd.Stdin = bytes.NewReader(append(stdin, '\n'))
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	r.Elapsed = time.Since(start).Round(time.Millisecond).String()
	r.Stdout = stdoutBuf.String()
	r.Stderr = stderrBuf.String()

	if ctx.Err() == context.DeadlineExceeded {
		r.Err = fmt.Errorf("hook timeout after %s", timeout)
		return r
	}
	if err != nil {
		// exec.ExitError holds the non-zero code; treat any non-2
		// non-zero exit as a warning, not a runner-level error.
		if exitErr, ok := err.(*exec.ExitError); ok {
			r.ExitCode = exitErr.ExitCode()
		} else {
			r.Err = err
			return r
		}
	}

	// Try to parse stdout as JSON Decision. Plain-text stdout is fine —
	// just ignored when not JSON.
	if trimmed := strings.TrimSpace(r.Stdout); trimmed != "" &&
		(strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		_ = json.Unmarshal([]byte(trimmed), &r.Decision)
	}
	return r
}

// FirstBlocking returns the first Result that should halt the
// in-flight operation, or nil if none do. Callers use this to decide
// whether to abort a tool call / replace a prompt.
func FirstBlocking(rs []Result) *Result {
	for i := range rs {
		if rs[i].IsBlocking() {
			return &rs[i]
		}
	}
	return nil
}

// CollectStderr concatenates non-empty stderr from results into one
// human-readable string, prefixed by the hook source. Used to render
// warnings into the assistant transcript.
func CollectStderr(rs []Result) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Stderr == "" && r.Err == nil {
			continue
		}
		b.WriteString("[hook ")
		b.WriteString(r.Source)
		b.WriteString(":")
		b.WriteString(string(r.Event))
		b.WriteString("] ")
		if r.Err != nil {
			b.WriteString(r.Err.Error())
		} else {
			b.WriteString(strings.TrimSpace(r.Stderr))
		}
		b.WriteString("\n")
	}
	return b.String()
}
