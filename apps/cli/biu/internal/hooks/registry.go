// Hook registry — owns the parsed configuration loaded from
// settings.json and resolves which Commands fire for a given (event,
// matcher) pair.
//
// settings.json shape consumed here (per layer):
//
//   "hooks": {
//     "PreToolUse": [
//       { "matcher": "Bash",       "hooks": [{"type":"command", ...}] },
//       { "matcher": "Edit|Write", "hooks": [...] }
//     ],
//     "UserPromptSubmit": [
//       { "hooks": [...] }    // matcher omitted ⇒ matches everything
//     ]
//   }
//
// Some legacy single-object form is also tolerated:
//
//   "hooks": { "PreToolUse": {"hooks":[{"type":"command", ...}]} }
//
// The Layered settings package normalises both into []json.RawMessage
// per event, which we then unmarshal lazily.

package hooks

import (
	"encoding/json"
	"regexp"
)

// TrustGate is consulted by the registry on every For() / Has()
// call. Implementations return true when the current session is
// allowed to fire shell hooks (typically: cwd is on the trusted-
// directories allow-list). When false, For() returns no entries
// and Has() reports false — the hook firing site short-circuits
// without ever spawning the command.
//
// Injected as an interface so the hooks package stays decoupled from
// biu's trust persistence (and so embedders can stub it for tests).
type TrustGate interface {
	// IsTrustedNow returns the trust state at call time, NOT at
	// registry-construction time. Trust changes mid-session
	// (the user runs `/trust here`) take effect immediately.
	IsTrustedNow() bool
}

// SkipNotifier is an optional stderr / log sink fired when the
// registry refuses to surface hook entries because trust failed.
// Helps users diagnose "why isn't my hook running" without having
// to reach for `/trust` first.
type SkipNotifier func(event Event, count int)

// Registry is an immutable, pre-flattened view: per Event, a flat
// slice of (source, matcher, command) entries. Constructed once at
// session start and read concurrently by the runner.
type Registry struct {
	entries map[Event][]Entry

	// trustGate, when non-nil, gates every For() / Has() lookup.
	// nil = legacy mode (everything fires regardless of trust),
	// matching biu's pre-P20.16 behaviour.
	trustGate TrustGate

	// skipNotifier, when non-nil, fires when trustGate blocks a
	// hook lookup. Callers wire this to a stderr logger or a REPL
	// system note so untrusted-skip events are visible.
	skipNotifier SkipNotifier
}

// Entry is one hook + its provenance + matcher.
type Entry struct {
	Source  string // user | project | local
	Matcher string // empty = match everything
	Command Command
}

// NewRegistry builds an empty registry. Callers should immediately
// Add() per layer before handing to the engine.
func NewRegistry() *Registry { return &Registry{entries: map[Event][]Entry{}} }

// SetTrustGate installs (or replaces) the trust gate. Pass nil to
// disable gating — useful in tests / SDK paths that don't want the
// directory-trust contract. Idempotent: callers can re-install at
// any time.
func (r *Registry) SetTrustGate(g TrustGate) {
	if r == nil {
		return
	}
	r.trustGate = g
}

// SetSkipNotifier installs a callback fired whenever a For() lookup
// returns nil because the trust gate blocked it. Lets the wiring
// layer surface a one-time "hooks suppressed: dir untrusted" note
// without coupling the hooks package to a logger.
func (r *Registry) SetSkipNotifier(fn SkipNotifier) {
	if r == nil {
		return
	}
	r.skipNotifier = fn
}

// MergeJSON ingests a plugin manifest's `hooks` field — a single
// JSON object whose keys are event names and whose values are the
// per-event matcher arrays. Internally normalises into the same
// per-event RawMessage map shape Add() takes and dispatches.
//
// Used by the plugins aggregator (PP3) so a plugin's hooks
// integrate identically to settings.json hooks (same matcher /
// trust-gate / dispatch semantics). Source should be
// "plugin:<name>" for traceability in /plugin show.
//
// Invalid JSON is silently dropped — symmetric with Add() — because
// crashing on bad config from a third-party plugin is a worse UX
// than dropping it and letting the user notice the missing hook.
func (r *Registry) MergeJSON(source string, raw []byte) {
	if r == nil || len(raw) == 0 {
		return
	}
	// Decode into a per-event map so each event's value can be
	// re-encoded as RawMessage and fed to Add(). The intermediate
	// any value preserves whatever shape (array / single object /
	// bare command list) was on disk; addOne() handles all three.
	var perEvent map[string]json.RawMessage
	if err := json.Unmarshal(raw, &perEvent); err != nil {
		return
	}
	normalised := make(map[string][]json.RawMessage, len(perEvent))
	for evt, blob := range perEvent {
		normalised[evt] = []json.RawMessage{blob}
	}
	r.Add(source, normalised)
}

// Add accepts the raw map produced by settings.Layered.MergedHooks().
// Source identifies which settings layer contributed this batch and
// gets attached to every Entry for telemetry.
//
// Per-event values can be either:
//
//   - an array of Matcher objects (the canonical form), or
//   - a single Matcher object.
//
// Anything else is silently dropped — the goal is to never crash on
// half-typed config.
func (r *Registry) Add(source string, raw map[string][]json.RawMessage) {
	for evt, blobs := range raw {
		if !IsValid(evt) {
			continue
		}
		for _, blob := range blobs {
			r.addOne(source, Event(evt), blob)
		}
	}
}

func (r *Registry) addOne(source string, evt Event, blob json.RawMessage) {
	// First try the array form: Matcher[].
	var arr []Matcher
	if err := json.Unmarshal(blob, &arr); err == nil {
		for _, m := range arr {
			r.appendMatcher(source, evt, m)
		}
		return
	}
	// Single Matcher object.
	var single Matcher
	if err := json.Unmarshal(blob, &single); err == nil {
		r.appendMatcher(source, evt, single)
		return
	}
	// Bare command list (legacy): { "PreToolUse": [{"type":"command",...}] }
	var cmds []Command
	if err := json.Unmarshal(blob, &cmds); err == nil {
		for _, c := range cmds {
			r.entries[evt] = append(r.entries[evt], Entry{
				Source: source, Command: c,
			})
		}
	}
}

func (r *Registry) appendMatcher(source string, evt Event, m Matcher) {
	for _, c := range m.Hooks {
		if c.Type == "" {
			c.Type = "command"
		}
		r.entries[evt] = append(r.entries[evt], Entry{
			Source: source, Matcher: m.Matcher, Command: c,
		})
	}
}

// For returns every hook entry registered under evt whose matcher
// matches `key`. The key is event-specific:
//
//   - PreToolUse / PostToolUse: pass the tool name.
//   - Notification:             the notification type.
//   - SessionStart:             the source ("startup" | "resume" | "compact").
//   - Others (Stop, UserPromptSubmit, ...): pass "" — every hook with
//     matcher omitted will match.
//
// Matchers support `|` alternation and Go regexp syntax.
//
// Trust gate: when SetTrustGate is wired and the gate reports
// untrusted, this function returns nil — the hook firing site
// receives an empty entry list and short-circuits. Optional
// SkipNotifier fires once per blocked event so the user gets a
// stderr breadcrumb explaining why their hook didn't run.
func (r *Registry) For(evt Event, key string) []Entry {
	if r == nil {
		return nil
	}
	all := r.entries[evt]
	if len(all) == 0 {
		return nil
	}
	if r.trustGate != nil && !r.trustGate.IsTrustedNow() {
		if r.skipNotifier != nil {
			r.skipNotifier(evt, len(all))
		}
		return nil
	}
	out := make([]Entry, 0, len(all))
	for _, e := range all {
		if matcherMatches(e.Matcher, key) {
			out = append(out, e)
		}
	}
	return out
}

// Has reports whether any hooks are registered for the event AND
// the trust gate (if wired) currently allows them. Engine firing
// sites typically check Has() before For(); gating both here keeps
// the gate's effect consistent.
//
// Returns false when:
//   - r is nil
//   - the event has no registered entries
//   - a TrustGate is installed and IsTrustedNow returns false
func (r *Registry) Has(evt Event) bool {
	if r == nil {
		return false
	}
	if len(r.entries[evt]) == 0 {
		return false
	}
	if r.trustGate != nil && !r.trustGate.IsTrustedNow() {
		return false
	}
	return true
}

// matcherMatches implements the matcher mini-language. Empty matcher =
// always-match. `*` = always-match. Otherwise the matcher is treated
// as a regex; `|` works naturally as alternation. Unanchored matches
// are sufficient.
func matcherMatches(matcher, key string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile(matcher)
	if err != nil {
		// Malformed regex — fall back to exact-name match so a typo
		// doesn't silently match every tool.
		return matcher == key
	}
	return re.MatchString(key)
}
