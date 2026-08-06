// Compact ↔ SessionMemory bridge.
//
// Every successful macro compact captures the "what's still alive"
// subset of the conversation; piping that into SessionMemory's
// Current State section lets the next session resume without
// re-discovering context.
//
// Why this lives in the compact package (rather than in
// sessionmemory): compact owns the summary and chooses what to
// persist. Compact already runs the LLM call and holds the Result;
// pushing into sessionmemory from here keeps the per-summary policy
// (e.g. only update when summary > 200 chars, only on main-thread
// compacts) in one place.
//
// Engine wires this via SessionMemoryWriter — a minimal interface so
// compact stays decoupled from internal/sessionmemory's filesystem
// surface. Tests can substitute a fake writer that captures calls.

package compact

// SessionMemoryWriter is the minimum surface compact needs to push
// summaries into the session memory store. internal/sessionmemory's
// SessionMemory satisfies this via UpdateFromSummary on top.
type SessionMemoryWriter interface {
	// SetCurrentState replaces the "Current State" section body
	// with the supplied text. Implementations should preserve every
	// other section.
	SetCurrentState(body string)

	// Save persists to disk. Returns an error so the engine can
	// surface failures (telemetry / stderr); compact ignores
	// errors here — failing to write session memory should never
	// block a successful compact.
	Save() error

	// Truncate enforces the per-section + total token caps. Called
	// after SetCurrentState to keep the file from growing past
	// SessionMemory's MaxTotalTokens.
	Truncate()
}

// MinSummaryBytesForSessionMemory — sub-this-length summaries are
// usually fragments ("ok", "done") that don't carry useful state;
// skip them to avoid clobbering a richer prior Current State with
// noise.
const MinSummaryBytesForSessionMemory = 200

// PushSummaryToSessionMemory updates writer's Current State with
// the supplied summary text + persists the file. No-op for nil
// writer (engine builds writer at session start; nil means
// "session memory disabled").
//
// Returns an error only when Save fails — caller should log + move
// on; compact must not abort because session memory write failed.
func PushSummaryToSessionMemory(writer SessionMemoryWriter, summary string) error {
	if writer == nil {
		return nil
	}
	if len(summary) < MinSummaryBytesForSessionMemory {
		return nil
	}
	writer.SetCurrentState(summary)
	writer.Truncate()
	return writer.Save()
}
