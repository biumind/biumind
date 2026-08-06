// Post-compact cleanup callback registry.
//
// The problem it solves: macro compact replaces the message history
// with a summary, but several subsystems hold caches keyed off
// message IDs / file hashes / approval records that no longer
// match. If we don't clear those caches, post-compact tool calls
// see stale data:
//
//   - file freshness cache thinks "the model already Read foo.go" but
//     the actual Read result is gone, so a later Edit hits an
//     unfresh file and surprises the user.
//   - memory file cache holds the BIUMIND.md content the summary
//     already absorbed; system prompt assembly may double-count.
//   - classifier / approval caches keyed off the prior turn's tool
//     calls drift after compact.
//
// Pattern: subsystems register a callback at init() time via
// RegisterPostCleanup; the engine calls RunPostCleanup after every
// successful compact. Each callback is independent — failures in
// one don't stop the others.
//
// Scope: callbacks declare whether they touch main-thread state or
// are sub-agent-safe. RunPostCleanup(scope) skips the main-only
// callbacks when called from a sub-agent compact; running
// main-thread cleanup from a sub-agent has caused real production
// bugs (corrupted getUserContext memoisation).

package compact

import (
	"sync"
)

// CleanupScope discriminates the calling context. The engine
// derives the scope from the run's agent ID — see compact_run.go's
// scope helper.
type CleanupScope int

const (
	// ScopeMain — the main REPL / CLI invocation. Every callback
	// fires.
	ScopeMain CleanupScope = iota

	// ScopeSubagent — a child agent (Agent tool) is compacting its
	// own history. Module-level caches in the parent process must
	// NOT be cleared — they belong to the parent thread's state.
	// Only callbacks marked SubagentSafe fire.
	ScopeSubagent
)

// CleanupFunc is one registered cleanup. The fn receives the scope
// so it can fan out further (e.g. resetting only the agent-id-keyed
// portion of a cache).
type CleanupFunc func(scope CleanupScope)

// cleanupEntry keeps the callback alongside metadata for telemetry
// + scope filtering.
type cleanupEntry struct {
	Name          string
	SubagentSafe  bool
	Fn            CleanupFunc
}

var (
	cleanupMu       sync.RWMutex
	cleanupRegistry []cleanupEntry
)

// CleanupOptions tunes the registration. Default zero values are
// the conservative choice: not subagent-safe (main only).
type CleanupOptions struct {
	// Name identifies the callback in telemetry / debug logs.
	// Conventionally "<package>:<purpose>", e.g. "files:freshness".
	Name string

	// SubagentSafe — when true, the callback fires for both main
	// and subagent compacts. Set this only when the callback's
	// effect is genuinely scoped (e.g. it clears a per-agent map
	// using the supplied scope). Unsafe-by-default makes the
	// blast-radius small.
	SubagentSafe bool
}

// RegisterPostCleanup adds a callback to the registry. Idempotent
// is NOT enforced — calling twice with the same name produces two
// entries that both fire. Subsystems should register exactly once
// at init(); double-registration is a wiring bug worth surfacing.
//
// Empty name / nil fn are silently dropped — defensive against
// init-order edge cases where a sub-package is imported but its
// cleanup target hasn't initialised yet.
func RegisterPostCleanup(opt CleanupOptions, fn CleanupFunc) {
	if opt.Name == "" || fn == nil {
		return
	}
	cleanupMu.Lock()
	defer cleanupMu.Unlock()
	cleanupRegistry = append(cleanupRegistry, cleanupEntry{
		Name:         opt.Name,
		SubagentSafe: opt.SubagentSafe,
		Fn:           fn,
	})
}

// RunPostCleanup fans out to every registered callback whose scope
// permits firing. Iteration order matches registration order so
// callbacks with implicit dependencies (rare; document in the name
// when it happens) stay deterministic.
//
// Each callback runs in its own panic recovery so a buggy
// subsystem doesn't take down the engine.
func RunPostCleanup(scope CleanupScope) {
	cleanupMu.RLock()
	entries := append([]cleanupEntry(nil), cleanupRegistry...)
	cleanupMu.RUnlock()

	for _, e := range entries {
		if scope == ScopeSubagent && !e.SubagentSafe {
			continue
		}
		runOneCleanup(e, scope)
	}
}

// runOneCleanup wraps one callback in a panic recovery. The recover
// path silently swallows — the engine must continue regardless.
// Subsystems that want loud failure should log inside the callback.
func runOneCleanup(e cleanupEntry, scope CleanupScope) {
	defer func() {
		_ = recover()
	}()
	e.Fn(scope)
}

// RegisteredCleanupNames returns the set of registered callback
// names — exposed for /plugin doctor / `biu doctor` style
// diagnostics so users can verify their cache invalidations are
// actually wired.
func RegisteredCleanupNames() []string {
	cleanupMu.RLock()
	defer cleanupMu.RUnlock()
	names := make([]string, len(cleanupRegistry))
	for i, e := range cleanupRegistry {
		names[i] = e.Name
	}
	return names
}

// resetPostCleanup is a test-only helper. Lowercase to keep
// production code from accidentally clearing the registered set.
func resetPostCleanup() {
	cleanupMu.Lock()
	defer cleanupMu.Unlock()
	cleanupRegistry = nil
}
