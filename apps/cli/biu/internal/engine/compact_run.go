// Compact cycle execution — the macro-compact path that swaps the
// running message list for an LLM-generated summary. Two entry
// points:
//
//	runCompact — internal, wired into runSubmit's auto-compact +
//	             max_tokens recovery branches.
//	Compact    — public, the REPL's /compact slash invokes this.
//
// PreCompact / PostCompact hooks fire on each cycle. Token counts
// before/after are surfaced via CompactStartEvent / CompactDoneEvent
// so the UI can render a "summarised X tokens → Y" badge.

package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/internal/compact"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// runCompact executes one macro-compact cycle: fires PreCompact hooks,
// asks the model to summarise, swaps state.Messages out for the
// summary, fires PostCompact hooks, and emits Compact{Start,Done}
// events.
//
// Returns an error only when the compact provider itself fails. Hook
// failures are surfaced as warnings inside the event stream so the
// engine can still continue.
func (e *QueryEngine) runCompact(ctx context.Context, out chan<- Event, reason string) error {
	if e.compact == nil {
		return errors.New("compact: not configured")
	}
	before := e.state.Snapshot()
	usage := e.cost.Snapshot()
	SafeSend(out, &CompactStartEvent{
		Reason: reason, TokensBefore: usage.InputTokens,
	}, ctx.Done())

	// PreCompact hook — may inject extra summary instructions, may
	// block (we honour both).
	if e.hooks.Has(hooks.EventPreCompact) {
		results := hooks.Run(ctx,
			e.hooks.For(hooks.EventPreCompact, ""),
			hooks.EventPreCompact,
			map[string]any{"reason": reason, "message_count": len(before)})
		if blocked := hooks.FirstBlocking(results); blocked != nil {
			return fmt.Errorf("PreCompact hook blocked: %s",
				hookReasonOr(blocked, "no reason"))
		}
	}

	res, err := e.compact.Run(ctx, before)
	if err != nil {
		return err
	}
	e.state.ResetMessages(res.Replaced)

	if e.hooks.Has(hooks.EventPostCompact) {
		_ = hooks.Run(ctx,
			e.hooks.For(hooks.EventPostCompact, ""),
			hooks.EventPostCompact,
			map[string]any{
				"summary":        res.Summary,
				"original_count": res.OriginalCount,
				"new_count":      res.NewCount,
				"reason":         reason,
			})
	}

	tokensAfter := approxTokenCount(res.Replaced)
	SafeSend(out, &CompactDoneEvent{
		TokensBefore: usage.InputTokens,
		TokensAfter:  tokensAfter,
		TokensSaved:  usage.InputTokens - tokensAfter,
	}, ctx.Done())

	// MC6: push summary into SessionMemory's Current State section
	// for cross-compact + cross-restart continuity. nil writer or
	// short summaries are no-ops by contract. Errors surface as
	// recoverable events — failing to write session memory must
	// never abort a successful compact.
	if e.sessionMem != nil {
		if err := compact.PushSummaryToSessionMemory(e.sessionMem, res.Summary); err != nil {
			SafeSend(out, &ErrorEvent{
				Err:         fmt.Errorf("session memory write: %w", err),
				Source:      ErrSrcCompact,
				Recoverable: true,
			}, ctx.Done())
		}
	}

	// Re-arm warning levels for the next cycle, with the post-compact
	// token total as the new watermark. cost.Tracker is monotonic
	// across the whole session; without the watermark, every turn
	// after the first compact would re-fire warnings on cumulative
	// totals that no longer reflect actual context size.
	if e.compactWarn != nil {
		e.compactWarn.Reset(usage.InputTokens)
	}

	// Per-engine state cleanup. AppState is engine-scoped so we
	// invalidate it directly rather than via the package-level
	// registry (which is for module globals). The file snapshot
	// ledger MUST clear: the message history that referenced those
	// Reads is now a summary, and Edit's freshness check would
	// otherwise let the model edit files it only "remembered"
	// reading before compact.
	if e.state != nil {
		e.state.ClearFiles()
	}

	// Fan out package-level cleanup callbacks. Subsystems hold
	// caches at module scope (memory file cache, classifier
	// approvals, etc) that the engine can't reach directly. Each
	// subsystem registers via compact.RegisterPostCleanup at init
	// time. Scope is derived from the engine's agent ID: subagent
	// compacts skip main-thread-only callbacks to avoid clobbering
	// the parent thread's cache state.
	scope := compact.ScopeMain
	if e.agentID != "" && e.agentID != "main" {
		scope = compact.ScopeSubagent
	}
	compact.RunPostCleanup(scope)
	return nil
}

// Compact runs the macro-compact cycle synchronously, ignoring the
// auto-trigger threshold. The REPL's /compact slash invokes this.
// Events are emitted on `out`; pass a buffered channel.
func (e *QueryEngine) Compact(ctx context.Context, out chan<- Event) error {
	return e.runCompact(ctx, out, "manual")
}

// approxTokenCount is a rough heuristic — char count ÷ 4 — used only
// to populate the CompactDoneEvent. Good enough for the UI; cost
// accounting comes from the provider's usage frames.
func approxTokenCount(msgs []state.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			n += len(b.Text)
		}
	}
	return n / 4
}
