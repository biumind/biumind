// Internal hook handlers — Go functions registered by name, invoked
// when a hook config has `"type": "internal"` instead of "command".
//
// Why: bundled plugins (PP8b/c/d) ship inside the biu binary and
// shouldn't depend on python3 / bash being installed on the user's
// machine. With `type=command`, hookify-style plugins fork a Python
// interpreter; with `type=internal`, the same logic runs as a Go
// function with no subprocess, no extra interpreter, and microsecond
// dispatch instead of millisecond fork.
//
// Use case: an output-style plugin that injects "explanatory mode"
// system text on SessionStart wants to do exactly one thing — emit
// a JSON Decision pointing at hardcoded text. As a shell command it
// requires sh + cat + heredoc + JSON escaping; as an internal handler
// it's literally `return Decision{AdditionalContext: text}`.
//
// Trust model: handlers are compiled into biu, so they're as trusted
// as biu itself. The trust gate (settings.json trustedDirectories)
// still gates internal hooks the same as shell hooks — a malicious
// plugin in an untrusted dir cannot fire an internal handler. This
// keeps the shell-hook security baseline intact even though no
// subprocess is involved.
//
// API: bundled plugins call RegisterInternal in their package init
// to claim a name. The name is what config files reference under
// `"handler": "<name>"`. Names are dot-separated by convention,
// "<plugin>:<event>", but the registry is type-string-keyed and
// agnostic to the format.

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// InternalHandler is a registered Go function that fulfils a hook
// firing. Receives the same JSON payload subprocess hooks read from
// stdin, returns a Decision the runner converts into a Result.
//
// Errors from the handler become Result.Err (warn-level). Handlers
// that want to BLOCK the operation must set Decision.Block=true;
// returning a non-nil error is treated as a transient failure, not
// a deliberate veto.
type InternalHandler func(ctx context.Context, payload []byte) (Decision, error)

// internalRegistry is the package-level registry. Read-mostly: heavy
// reads under RLock, occasional writes (init-time RegisterInternal +
// test cleanup ResetInternal) under full Lock. sync.Map would also
// work but RWMutex makes the read path identical to the registry's
// other lookups.
var (
	internalRegistry   = map[string]InternalHandler{}
	internalRegistryMu sync.RWMutex
)

// RegisterInternal claims a name for an internal handler. Idempotent
// on identical (name, function-identity) but rejects collisions on
// distinct functions to make a duplicate registration loud — bundled
// plugins shouldn't ever conflict, and a real conflict is a sign of
// a wiring bug.
//
// Conventional naming: "<plugin>:<event>", e.g. "hookify:pretool" or
// "explanatory:session-start". Avoid bare names so a future "/plugin
// disable" can prefix-match without parsing.
func RegisterInternal(name string, h InternalHandler) {
	if name == "" || h == nil {
		return
	}
	internalRegistryMu.Lock()
	defer internalRegistryMu.Unlock()
	internalRegistry[name] = h
}

// LookupInternal returns the registered handler for name, or nil
// when missing. The runner calls this on every internal-typed hook
// invocation; nil → the hook returns an Err result describing the
// missing handler so the user can debug typos in config.
func LookupInternal(name string) InternalHandler {
	internalRegistryMu.RLock()
	defer internalRegistryMu.RUnlock()
	return internalRegistry[name]
}

// ResetInternal is a test-only helper: clears the registry. Real
// usage should never need this — handlers register at init() and
// stay for process lifetime. Exported (capital R) so cross-package
// tests can use it; unexported in production paths because callers
// have no business mutating registry state at runtime.
func ResetInternal() {
	internalRegistryMu.Lock()
	defer internalRegistryMu.Unlock()
	internalRegistry = map[string]InternalHandler{}
}

// runInternal is the runner's branch for `type=internal` hooks.
// Mirrors runOne's control flow: timeout via context, JSON decision
// passthrough, Result populated for telemetry parity with command-
// based hooks.
//
// Lives here (rather than in runner.go) so the internal-handler API
// surface stays in one file and callers reading runner.go aren't
// bothered by reflection-style lookups.
func runInternal(parent context.Context, entry Entry, event Event, payload []byte) Result {
	r := Result{Source: entry.Source, Event: event, Command: entry.Command.Handler}
	if entry.Command.Handler == "" {
		r.Err = fmt.Errorf("internal hook missing handler name")
		return r
	}
	h := LookupInternal(entry.Command.Handler)
	if h == nil {
		r.Err = fmt.Errorf("internal hook handler %q not registered", entry.Command.Handler)
		return r
	}

	timeout := DefaultTimeout
	if entry.Command.Timeout > 0 {
		timeout = time.Duration(entry.Command.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	start := time.Now()
	dec, err := h(ctx, payload)
	r.Elapsed = time.Since(start).Round(time.Millisecond).String()
	if ctx.Err() == context.DeadlineExceeded {
		r.Err = fmt.Errorf("internal hook timeout after %s", timeout)
		return r
	}
	if err != nil {
		// Non-blocking warning, same posture as a non-2 non-zero exit
		// from a command hook.
		r.Err = err
		return r
	}
	r.Decision = dec
	// Round-trip the decision into Stdout for log parity with
	// command hooks — the engine pipeline reads Decision directly,
	// but operators inspecting the runner's telemetry expect a
	// human-readable Stdout column.
	if !decisionIsZero(dec) {
		if buf, err := json.Marshal(dec); err == nil {
			r.Stdout = string(buf)
		}
	}
	return r
}

// decisionIsZero reports whether a Decision is its zero value.
// Decision contains a map field, so it can't be ==-compared directly;
// this open-coded check covers every field the runner inspects.
func decisionIsZero(d Decision) bool {
	return !d.Block &&
		d.Reason == "" &&
		d.AdditionalContext == "" &&
		d.ReplacePrompt == "" &&
		len(d.HookSpecificOutput) == 0
}
