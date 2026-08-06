// Package planverify watches tool execution against the most-
// recently-approved plan and flags drift — destructive calls that
// don't match anything the model committed to in ExitPlanMode.
//
// It observes PostToolUse, builds an attachment listing drift, and
// attaches it to the next turn so the model sees its own deviation.
//
// Design choices for the OSS port:
//
//   - Pure Go observer (no shell hook). The engine's tool dispatcher
//     calls Verifier.Observe; the engine itself surfaces the drift
//     attachment via the same mechanism used for plan re-injection
//     (compact.Options.Attachments).
//   - Token-based matching: split the plan body into lowercase tokens
//     ≥4 chars; for every destructive tool call, check whether ANY
//     argument token overlaps. Loose by design — false negatives
//     (drift not caught) are cheaper than false positives (model
//     gets nagged about a justified call).
//   - Read-only / safe tools never count as drift even if unmentioned.
//
// Threshold model: every Observe returns whether the call drifted.
// Callers track cumulative count; when it crosses a threshold they
// surface BuildAttachment() into the system prompt.

package planverify

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// Verifier holds the active plan + a tally of drifts observed since
// it was last reset. Safe for concurrent Observe calls.
type Verifier struct {
	mu sync.Mutex

	// plan is the original markdown body. Stored verbatim so the
	// drift attachment can echo the relevant section back.
	plan string

	// tokens is the lowercase, ≥4-char word set of the plan body.
	// Pre-computed at SetPlan time so Observe stays O(args).
	tokens map[string]bool

	// drifts records every flagged call in order.
	drifts []Drift
}

// Drift is one flagged tool call.
type Drift struct {
	Tool string         // e.g. "Bash", "Edit"
	Args map[string]any // raw tool args (path, command, …)
	// Reason is a one-line explanation surfaced to the user / model.
	Reason string
}

// New returns a fresh Verifier with no plan loaded. Drift detection
// is a no-op until SetPlan is called.
func New() *Verifier { return &Verifier{} }

// SetPlan installs (or replaces) the active plan. Empty plan clears
// the verifier — used when the session enters /clear or finishes a
// plan-execution cycle.
func (v *Verifier) SetPlan(plan string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.plan = strings.TrimSpace(plan)
	v.tokens = tokenise(v.plan)
	v.drifts = nil
}

// HasPlan reports whether the verifier is currently active.
func (v *Verifier) HasPlan() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.tokens) > 0
}

// DriftCount returns the number of drifted calls seen since the
// last SetPlan / Reset.
func (v *Verifier) DriftCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.drifts)
}

// Drifts returns a snapshot of every drift recorded so far. The
// returned slice is safe for the caller to mutate.
func (v *Verifier) Drifts() []Drift {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]Drift, len(v.drifts))
	copy(out, v.drifts)
	return out
}

// Reset clears the drift counter without touching the plan. Used
// after surfacing an attachment so the next turn starts fresh.
func (v *Verifier) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.drifts = nil
}

// Observe records one finished tool call. Returns true when the
// call drifted (caller can use the bool to do real-time UI hints).
//
// Tools that are read-only / safe never drift. For destructive
// tools (Edit / Write / Bash / MultiEdit / NotebookEdit) we
// consider the call justified when ANY ≥4-char token from the
// args appears in the plan token set.
func (v *Verifier) Observe(tool string, args map[string]any) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.tokens) == 0 {
		return false
	}
	if !isDestructiveByName(tool) {
		return false
	}
	if v.argsOverlapPlan(args) {
		return false
	}
	d := Drift{
		Tool:   tool,
		Args:   copyArgs(args),
		Reason: explainDrift(tool, args),
	}
	v.drifts = append(v.drifts, d)
	return true
}

// BuildAttachment returns a system-message body suitable for
// prepending to the next turn when drift count exceeds the caller's
// threshold. Empty string when there's nothing to surface.
//
// Format uses a `<plan-drift>` attachment tag so the model
// recognises the drift signal.
func (v *Verifier) BuildAttachment() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.drifts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<plan-drift>\n")
	fmt.Fprintf(&b, "You committed to a plan, but %d tool call(s) ", len(v.drifts))
	b.WriteString("did not match anything the plan promised:\n\n")
	for i, d := range v.drifts {
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, d.Tool, d.Reason)
	}
	b.WriteString("\nIf the deviation was intentional, acknowledge it ")
	b.WriteString("and update the plan via EnterPlanMode → ExitPlanMode. ")
	b.WriteString("Otherwise, return to the plan.\n")
	b.WriteString("</plan-drift>")
	return b.String()
}

// argsOverlapPlan reports whether any extracted token from args is
// also in the plan token set.
func (v *Verifier) argsOverlapPlan(args map[string]any) bool {
	for _, tok := range tokensFromArgs(args) {
		if v.tokens[tok] {
			return true
		}
	}
	return false
}

// ─── Helpers ──────────────────────────────────────────

// tokenise splits text into lowercase ≥4-char alphanumeric tokens.
// Also adds basename-style tokens for any path-like substring so
// `internal/engine/loop.go` produces { "internal", "engine", "loop",
// "loop.go" } — gives the matcher better hit rate against tool args
// that pass paths.
func tokenise(text string) map[string]bool {
	out := map[string]bool{}
	if text == "" {
		return out
	}
	addTok := func(t string) {
		// Strip surrounding punctuation we kept for path-aware splitting
		// — `caching.` becomes `caching`, `setup_db,` becomes `setup_db`.
		t = strings.Trim(t, "._-/\\")
		t = strings.ToLower(t)
		if len(t) >= 4 {
			out[t] = true
		}
	}
	// indexParts splits a path-like word into useful sub-tokens.
	// Recursive: for each `/`-separated piece we also try dot-split
	// so `internal/engine/loop.go` → "internal", "engine", "loop",
	// "go", plus the original "loop.go".
	var indexParts func(string)
	indexParts = func(raw string) {
		addTok(raw)
		if strings.ContainsAny(raw, "/\\") {
			for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
				return r == '/' || r == '\\'
			}) {
				indexParts(part)
			}
			return
		}
		// Atomic piece — try dot-split for `loop.go` style names.
		if i := strings.LastIndex(raw, "."); i > 0 && i < len(raw)-1 {
			addTok(raw[:i])
			addTok(raw[i+1:])
		}
	}
	for _, raw := range splitWords(text) {
		indexParts(raw)
	}
	return out
}

// splitWords splits on anything that isn't a letter/digit. Underscores
// and dots are kept inside tokens so identifiers like `setup_db` and
// path-like fragments survive intact for tokenise() to refine.
func splitWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		switch r {
		case '_', '.', '-', '/', '\\':
			return false
		}
		return true
	})
}

// tokensFromArgs pulls plausible "what is this call about" tokens
// from a tool's args:
//   - `path` / `file_path` → base + parent + extension
//   - `command` (Bash) → first 2 words + every long word
//   - any other string field is split via splitWords
func tokensFromArgs(args map[string]any) []string {
	var out []string
	add := func(s string) {
		for _, raw := range splitWords(s) {
			t := strings.ToLower(raw)
			if len(t) >= 4 {
				out = append(out, t)
			}
		}
	}

	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			continue
		}
		switch k {
		case "path", "file_path", "filename":
			out = append(out, strings.ToLower(filepath.Base(s)))
			parent := filepath.Base(filepath.Dir(s))
			if parent != "." && parent != "/" {
				out = append(out, strings.ToLower(parent))
			}
			if i := strings.LastIndex(s, "."); i > 0 && i < len(s)-1 {
				out = append(out, strings.ToLower(s[i+1:]))
			}
			add(s)
		case "command":
			// Bash: every long word is a hint. The command verb is
			// already covered by add().
			add(s)
		default:
			add(s)
		}
	}
	return out
}

// isDestructiveByName mirrors the engine's IsDestructive flag for
// the canonical built-in tools. We keep the check here rather than
// importing engine to avoid a cycle (engine → planverify is the
// future direction).
func isDestructiveByName(name string) bool {
	switch name {
	case "Edit", "edit",
		"Write", "write",
		"MultiEdit", "multi_edit",
		"NotebookEdit", "notebook_edit",
		"Bash", "bash":
		return true
	}
	return false
}

// explainDrift renders a one-line reason. Uses the most informative
// arg field per tool: `path` for editors, `command` for Bash.
func explainDrift(tool string, args map[string]any) string {
	if cmd, ok := args["command"].(string); ok && cmd != "" {
		if len(cmd) > 80 {
			cmd = cmd[:79] + "…"
		}
		return tool + " " + cmd + " (not in plan)"
	}
	for _, key := range []string{"file_path", "path", "filename"} {
		if v, ok := args[key].(string); ok && v != "" {
			return tool + " " + v + " (not in plan)"
		}
	}
	return tool + " (not in plan)"
}

// copyArgs is a defensive shallow clone so the recorded Drift
// doesn't share state with the engine's args map.
func copyArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}
