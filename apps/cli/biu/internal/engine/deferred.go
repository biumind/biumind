// Deferred tool support — opt-in mechanism for hiding tools from the
// LLM's initial catalog so they can be loaded on demand via the
// ToolSearchTool, via a `shouldDefer` / `isDeferredTool` partition.
//
// Why defer? Big MCP servers (Slack, GitHub, Notion) ship 30-100+ tools
// each. Sending all their JSONSchemas in every system prompt burns
// thousands of cached tokens and pushes context-sensitive routing past
// what the model can attend to. Deferring is the inversion: send only
// the tool *names* up front, and let the model fetch full schemas on
// demand for the small subset it actually wants to call this turn.
//
// Phase 1 (this file): the interface + partition helper. The engine
// will start filtering specs against this in Phase 2; until then a
// tool implementing `Deferrable` is a no-op.

package engine

import (
	"sort"
	"strings"
	"sync"
)

// Deferrable is an optional sub-interface a Tool can implement to
// signal that its schema should NOT appear in the initial tool
// catalogue. Mirrors `Warner` in shape: implementations cost nothing
// when not present (we type-assert before calling).
//
// Returning true is a *recommendation* — Phase 2 will couple it with
// a feature gate and may override (e.g. "always load" for tools the
// model needs in turn 1, regardless of count).
type Deferrable interface {
	// ShouldDefer reports whether this tool is a candidate for deferred
	// loading. Implementations typically return a constant; MCP-imported
	// tools may decide based on their parent server's tool count.
	ShouldDefer() bool
}

// IsDeferred returns true when the tool wants to be deferred. A tool
// that doesn't implement Deferrable is treated as non-deferred (the
// safe default: "show in initial catalog").
func IsDeferred(t Tool) bool {
	if d, ok := t.(Deferrable); ok {
		return d.ShouldDefer()
	}
	return false
}

// PartitionDeferred splits a registry's tools into (active, deferred).
// Order within each slice is deterministic (registry insertion order
// preserved by SimpleRegistry's iteration; for other registries the
// only contract is that all active precede all deferred).
func PartitionDeferred(reg ToolRegistry) (active, deferred []Tool) {
	for _, t := range reg.List() {
		if IsDeferred(t) {
			deferred = append(deferred, t)
		} else {
			active = append(active, t)
		}
	}
	return active, deferred
}

// DeferredSelection tracks which deferred-tool names the model has
// unlocked via ToolSearch in the current run. It's the bridge between
// the search tool's "select these tools" outcome and the per-turn
// catalog filter — once a tool's name lands here, its full JSONSchema
// rejoins the wire-level catalog on the next provider request.
//
// Concurrency: ToolSearchTool may run in parallel with other read-only
// tools when the LLM batches calls, so Add/Has are mutex-protected.
// Reads dominate (every turn calls Has for every deferred tool); we
// take the cheap RWMutex hit rather than the trickier atomic.Value
// dance with map snapshots.
type DeferredSelection struct {
	mu    sync.RWMutex
	names map[string]struct{}
}

// NewDeferredSelection returns an empty selection set.
func NewDeferredSelection() *DeferredSelection {
	return &DeferredSelection{names: map[string]struct{}{}}
}

// Add marks one or more deferred tools as unlocked. Idempotent. nil
// receiver is a no-op so test harnesses without a Selection plumbed
// through don't have to construct one.
func (s *DeferredSelection) Add(names ...string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.names == nil {
		s.names = map[string]struct{}{}
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		s.names[n] = struct{}{}
	}
}

// Has reports whether the named tool has been unlocked. nil receiver
// always returns false (no selections recorded).
func (s *DeferredSelection) Has(name string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.names[name]
	return ok
}

// Names returns a sorted snapshot of the unlocked set. Useful for
// diagnostics and tests.
func (s *DeferredSelection) Names() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// FilterDeferredCatalog produces the wire-level tool list for one
// turn's StreamRequest. Active tools are always included; deferred
// tools are included ONLY when the selection set has unlocked them.
//
// This is where the context-window saving lands: every deferred tool
// that hasn't been searched-and-selected yet is invisible to the LLM
// at the schema level (its name still surfaces in the
// available-deferred-tools attachment so the model knows it exists).
func FilterDeferredCatalog(reg ToolRegistry, sel *DeferredSelection) []Tool {
	if reg == nil {
		return nil
	}
	out := make([]Tool, 0)
	for _, t := range reg.List() {
		if IsDeferred(t) && !sel.Has(t.Name()) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// DeferredAttachment formats the system-prompt block listing deferred
// tools the model hasn't yet unlocked. Returns "" when there's nothing
// to surface (no deferred tools exist, or the model has already
// unlocked them all).
//
// Format mirrors the <available-deferred-tools> block: one tool name
// per line, no schemas. Descriptions are intentionally omitted —
// keeping the block tight is the whole point of deferring.
func DeferredAttachment(reg ToolRegistry, sel *DeferredSelection) string {
	_, deferred := PartitionDeferred(reg)
	var pending []string
	for _, t := range deferred {
		if sel.Has(t.Name()) {
			continue
		}
		pending = append(pending, t.Name())
	}
	if len(pending) == 0 {
		return ""
	}
	sort.Strings(pending)
	var b strings.Builder
	b.WriteString("<available-deferred-tools>\n")
	b.WriteString("These tools exist but their JSONSchema is not preloaded. Call the\n")
	b.WriteString("ToolSearch tool with `select:NAME` to fetch a schema before invoking.\n\n")
	for _, n := range pending {
		b.WriteString(n)
		b.WriteByte('\n')
	}
	b.WriteString("</available-deferred-tools>")
	return b.String()
}
