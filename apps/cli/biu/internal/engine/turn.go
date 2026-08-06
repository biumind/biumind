// Turn loop — the per-Submit work of:
//
//   * draining bg-task / plan-drift attachments
//   * firing UserPromptSubmit hooks (with rewrite/context)
//   * surfacing plan-mode auto-suggestions
//   * iterating provider.Stream → ParseStream → tool batch → repeat
//   * tracking per-turn usage + writing usage logs
//   * emitting RequestStart / AssistantMessage / Done / Error events
//
// Lives in its own file because runSubmit is ~300 lines on its own
// and dominates engine.go's footprint. Keeping it here makes the
// public surface (engine.go) readable as an entry-point file.

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/compact"
	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func (e *QueryEngine) runSubmit(
	ctx context.Context,
	out chan<- Event,
	prompt string,
	attachments []state.ContentBlock,
) {
	turnStarted := time.Now()
	turnUsageIn := 0
	turnUsageOut := 0
	turnCacheRead := 0
	turnCacheWrite := 0
	stopReason := ""

	// Track whether the success-path Stop hook fired. Defer fires
	// StopFailure (P20.55) when the turn returns without a clean
	// Stop — covers provider failures, hook blocks, max-turn
	// exhaustion. Failure source is captured by stopFailureErr.
	var (
		stopFired       bool
		stopFailureErr  error
		stopFailureKind string
	)
	defer func() {
		if stopFired || e.hooks == nil || !e.hooks.Has(hooks.EventStopFailure) {
			return
		}
		payload := map[string]any{
			"session_id": e.agentID,
			"kind":       stopFailureKind,
		}
		if stopFailureErr != nil {
			payload["error"] = stopFailureErr.Error()
		}
		hooks.Run(context.Background(),
			e.hooks.For(hooks.EventStopFailure, ""),
			hooks.EventStopFailure, payload)
	}()

	// ── Plan drift attachment ─────────────────────────────
	// If the previous turn's tool calls drifted from the approved
	// plan above the threshold, surface a `<plan-drift>` system
	// message at the head of this turn's history and reset the
	// counter.
	if e.planVerifier != nil &&
		e.planVerifier.HasPlan() &&
		e.planVerifier.DriftCount() >= e.planDriftThreshold {
		body := e.planVerifier.BuildAttachment()
		if body != "" {
			e.state.AppendMessage(state.Message{
				Role: state.RoleSystem,
				Content: []state.ContentBlock{{
					Type: state.ContentText, Text: body,
				}},
			})
		}
		e.planVerifier.Reset()
	}

	// ── Background-task completion attachment ─────────────
	// Drain whatever finished since the last user turn and surface
	// it as a single system message so the model picks up exit
	// codes + tail output without a polling loop. Mirrors the
	// "you'll be notified when it completes" line from the BashTool
	// prompt — ships a structured payload instead of relying on the
	// model to remember to call BashOutput.
	if e.bgTaskNotifier != nil {
		if completions := e.bgTaskNotifier.PendingCompletions(); len(completions) > 0 {
			body := buildBgTaskAttachment(completions)
			e.state.AppendMessage(state.Message{
				Role: state.RoleSystem,
				Content: []state.ContentBlock{{
					Type: state.ContentText, Text: body,
				}},
			})
		}
	}

	// ── Teammate-completion attachment (P20.53) ───────────
	// Async sub-agents finish in their own goroutines; we surface
	// their final outputs here as a single system message at the
	// head of each user turn. Same shape / ergonomic as bgTaskNotifier
	// — the model sees them like any other tool result.
	if e.asyncAgents != nil {
		if completions := e.asyncAgents.Pending(); len(completions) > 0 {
			body := buildTeammateAttachment(completions)
			e.state.AppendMessage(state.Message{
				Role: state.RoleSystem,
				Content: []state.ContentBlock{{
					Type: state.ContentText, Text: body,
				}},
			})
		}
	}

	// ── UserPromptSubmit hook ─────────────────────────────
	// Fires before the prompt enters state. A blocking hook aborts
	// the turn outright; a non-blocking hook may rewrite the prompt
	// or supply additional context that gets prepended.
	if e.hooks.Has(hooks.EventUserPromptSubmit) {
		entries := e.hooks.For(hooks.EventUserPromptSubmit, "")
		results := hooks.Run(ctx, entries, hooks.EventUserPromptSubmit,
			map[string]any{
				"prompt":     prompt,
				"session_id": e.agentID,
				"cwd":        e.cwd,
			})
		if blocked := hooks.FirstBlocking(results); blocked != nil {
			SafeSend(out, &ErrorEvent{
				Err: fmt.Errorf("UserPromptSubmit hook blocked: %s",
					hookReasonOr(blocked, "no reason")),
				Source: ErrSrcHook, Recoverable: false,
			}, ctx.Done())
			return
		}
		// Apply rewrite/context from the first non-empty hook output.
		for _, r := range results {
			if r.Decision.ReplacePrompt != "" {
				prompt = r.Decision.ReplacePrompt
			}
			if r.Decision.AdditionalContext != "" {
				e.state.AppendMessage(state.Message{
					Role: state.RoleSystem,
					Content: []state.ContentBlock{{Type: state.ContentText,
						Text: r.Decision.AdditionalContext}},
				})
			}
		}
	}

	// ── Plan-mode auto-suggest ─────────────────────────────
	// Heuristic keyword scan over the prompt. When it looks like a
	// large change (e.g. "refactor", "重构") AND the user is not
	// already in plan mode, fold a system note in front of the
	// prompt suggesting `EnterPlanMode`. This promotes planning from
	// a thing users have to remember to a thing the system suggests
	// when likely useful.
	if e.planHinter != nil && e.planHinter.Enabled() &&
		(e.perms == nil || e.perms.Mode() != permissions.ModePlan) {
		if h := e.planHinter.Analyse(prompt); h.Note != "" {
			e.state.AppendMessage(state.Message{
				Role: state.RoleSystem,
				Content: []state.ContentBlock{{
					Type: state.ContentText, Text: h.Note,
				}},
			})
		}
	}

	// Initial user message. Text 永远在前 + attachments(图片等)在后,
	// Anthropic 的 vision 模型对顺序不敏感,但一些 OpenAI Compat 网关
	// 倾向于"先文本后图片"的模板,顺序固定避免兼容性踩坑。
	userContent := make([]state.ContentBlock, 0, 1+len(attachments))
	if prompt != "" || len(attachments) == 0 {
		// prompt 空但有图也允许(纯图问"这是什么?"),不过留一个空 text
		// block 反而会让上游适配器 confused — 仅在没图时保留空 text 兜底。
		userContent = append(userContent, state.ContentBlock{
			Type: state.ContentText, Text: prompt,
		})
	}
	userContent = append(userContent, attachments...)
	e.state.AppendMessage(state.Message{
		Role:    state.RoleUser,
		Content: userContent,
	})

	// ── Deferred-tools attachment (P20.51 Phase 2) ─────────
	// Recomputed per Submit (NOT per turn) so the model sees a
	// consistent list throughout one user turn's tool loop. The
	// attachment names un-selected deferred tools so the model knows
	// to call ToolSearch before invoking them. Empty when nothing is
	// deferred (the common case until MCP servers opt in).
	if att := DeferredAttachment(e.tools, e.selections); att != "" {
		e.state.AppendMessage(state.Message{
			Role: state.RoleSystem,
			Content: []state.ContentBlock{{
				Type: state.ContentText, Text: att,
			}},
		})
	}

	// specs is recomputed each turn so deferred tools unlocked by a
	// ToolSearch call in turn N appear in turn N+1's request. Normally
	// this is a tiny map walk + slice copy; the cost is negligible
	// versus the LLM round trip.
	for turn := 0; turn < e.maxToolTurns; turn++ {
		specs := buildToolSpecs(e.tools, e.selections)
		reason := ReasonUserPrompt
		if turn > 0 {
			reason = ReasonAfterTool
		}
		turnID := fmt.Sprintf("turn-%d", turn)

		// Auto-compact check. Fires if the running input-token count
		// exceeds the configured threshold. Manual /compact calls
		// (engine.Compact()) are not gated by this — they always run.
		if e.compact != nil {
			snap := e.cost.Snapshot()

			// Pre-fire warning emission. Fires once per cycle for each
			// crossed level (info ≈ 50%, urgent ≈ 85%); the engine
			// resets after a successful compact so warnings re-arm
			// for the next cycle.
			if e.compactWarn != nil {
				if level, fire := e.compactWarn.Maybe(snap.InputTokens); fire {
					maxT := e.compactWarn.MaxTokens()
					ratio := 0.0
					if maxT > 0 {
						ratio = float64(snap.InputTokens) / float64(maxT)
					}
					hint := "/compact to summarise now, /clear to reset, or keep going"
					if level == compact.LevelUrgent {
						hint = "auto-compact will fire on the next turn — /compact now to control the summary"
					}
					SafeSend(out, &CompactWarningEvent{
						Level:       level.String(),
						UsedTokens:  snap.InputTokens,
						MaxTokens:   maxT,
						Ratio:       ratio,
						NextActions: hint,
					}, ctx.Done())
				}
			}

			if e.compact.ShouldFire(snap.InputTokens) {
				if err := e.runCompact(ctx, out, "tokens_above_threshold"); err != nil {
					SafeSend(out, &ErrorEvent{
						Err:    fmt.Errorf("auto compact: %w", err),
						Source: ErrSrcCompact, Recoverable: true,
					}, ctx.Done())
					// Compact is best-effort; continue regardless.
				} else {
					reason = ReasonAfterCompact
				}
			}
		}

		SafeSend(out, &RequestStartEvent{
			TurnID: turnID, Model: e.model,
			Timestamp: time.Now().UTC(), Reason: reason,
		}, ctx.Done())

		// Snapshot messages before the call. We pass a copy because
		// the provider may take its time and we don't want it racing
		// with concurrent mutations (none expected in current design,
		// but defensive).
		messages := e.state.Snapshot()

		// Micro compact: dedupe stale Read results + cap oversized
		// tool outputs. Runs in-place on the snapshot so the parent
		// state stays untouched (the LLM doesn't need to "see" the
		// redacted history reflected on disk).
		compact.Apply(messages, compact.Default())

		// Time-based microcompact (MC3): when the gap since the last
		// assistant message is so long the server prompt cache has
		// definitely expired, clear OLD tool result content
		// pre-emptively. Same per-turn-snapshot mutation pattern as
		// micro compact above. Disabled by default; opt in via
		// BIU_TIME_BASED_MC=1.
		if cfg := compact.LoadTimeBasedMCFromEnv(); cfg.Enabled {
			if trigger := compact.EvaluateTimeBasedTrigger(messages, cfg, time.Now()); trigger != nil {
				compact.ApplyTimeBasedMC(messages, trigger)
			}
		}

		// Server-side context management directive (MC4). When env-
		// gated, tells the API to auto-clear old tool uses /
		// thinking blocks at its threshold so we don't bounce off
		// "prompt too long" for cases the API can handle natively.
		// nil disables — every request is unchanged from the
		// pre-MC4 shape.
		//
		// HasThinking stays false until biu grows a ContentThinking
		// content-block type; the strategy-builder skips the
		// thinking edit cleanly when the flag is false.
		apiCtx := compact.BuildAPIContextManagement(compact.APIContextOptions{
			HasThinking: false,
		})

		frames, err := e.provider.Stream(ctx, StreamRequest{
			Model:             e.model,
			System:            e.SystemForTurn(),
			Messages:          messages,
			Tools:             specs,
			MaxTokens:         e.maxTokens,
			ContextManagement: apiCtx,
		})
		if err != nil {
			if isInterrupt(ctx, err) {
				stopFired = true
				emitInterruptedDone(out, turnStarted,
					turnUsageIn, turnUsageOut,
					turnCacheRead, turnCacheWrite)
				return
			}
			SafeSend(out, &ErrorEvent{
				Err:    fmt.Errorf("provider stream: %w", err),
				Source: ErrSrcLLM, Recoverable: false,
			}, ctx.Done())
			return
		}

		assistant, sr, usage, parseErr := ParseStream(ctx, frames, out)
		stopReason = sr

		// Accumulate usage even if parse errored — partial usage may
		// still be useful for the cost bar.
		turnUsageIn += usage.InputTokens
		turnUsageOut += usage.OutputTokens
		turnCacheRead += usage.CacheReadInputTokens
		turnCacheWrite += usage.CacheCreationInputTokens
		e.cost.Add(usage.InputTokens, usage.OutputTokens,
			usage.CacheReadInputTokens, usage.CacheCreationInputTokens)
		// Mirror into AppState for legacy callers that read from there.
		e.state.AddCost(usage.InputTokens, usage.OutputTokens,
			usage.CacheReadInputTokens, 0)

		if parseErr != nil {
			if errors.Is(parseErr, context.Canceled) {
				// User Ctrl-C / Interrupt(): don't store the partial
				// assistant message. If the cancel was a deliberate
				// Interrupt() (cause == ErrInterrupted) emit a clean
				// Done{StopReason:"interrupted"} so embedders can
				// finalise the turn without an Error.
				if isInterrupt(ctx, parseErr) {
					stopFired = true
					emitInterruptedDone(out, turnStarted,
						turnUsageIn, turnUsageOut,
						turnCacheRead, turnCacheWrite)
					return
				}
				SafeSend(out, &ErrorEvent{
					Err: parseErr, Source: ErrSrcLLM, Recoverable: false,
				}, ctx.Done())
				return
			}
			SafeSend(out, &ErrorEvent{
				Err:    fmt.Errorf("parse stream: %w", parseErr),
				Source: ErrSrcLLM, Recoverable: false,
			}, ctx.Done())
			return
		}

		// Persist whatever the LLM produced (even if it's just text).
		assistant.CreatedAt = time.Now().UTC()
		e.state.AppendMessage(assistant)
		SafeSend(out, &AssistantMessageEvent{
			Message: assistant, StopReason: stopReason,
		}, ctx.Done())

		// ── Branch on stop_reason ──────────────────────────────
		switch stopReason {
		case "end_turn", "stop_sequence":
			// Stop hook fires right before the turn finalises. A
			// blocking Stop hook tells us to keep the conversation
			// alive — but we don't yet have a continuation primitive
			// (Phase D). For now we surface the block as a warning
			// and end the turn anyway, matching headless behaviour.
			if e.hooks.Has(hooks.EventStop) {
				results := hooks.Run(ctx,
					e.hooks.For(hooks.EventStop, ""),
					hooks.EventStop,
					map[string]any{"stop_reason": stopReason})
				if msg := hooks.CollectStderr(results); msg != "" {
					SafeSend(out, &ErrorEvent{
						Err:    fmt.Errorf("Stop hook warning: %s", msg),
						Source: ErrSrcHook, Recoverable: true,
					}, ctx.Done())
				}
			}
			elapsed := time.Since(turnStarted)
			// Persist a usage record. Only fires on the top-level
			// agent — sub-agents share the parent's cost tracker so
			// their tokens are already attributed to the parent's
			// session_id. Skipping sub-agent rows keeps the JSONL
			// readable.
			if e.usageLogger != nil && e.agentID == "" {
				snap := e.cost.Snapshot()
				if err := e.usageLogger.Append(cost.UsageRecord{
					SessionID:  e.state.SessionID,
					Model:      snap.Model,
					Input:      turnUsageIn,
					Output:     turnUsageOut,
					CacheRead:  turnCacheRead,
					CacheWrite: turnCacheWrite,
					USD: cost.USD(cost.CostFor(snap.Model),
						turnUsageIn, turnUsageOut,
						turnCacheRead, turnCacheWrite),
					ElapsedMS: elapsed.Milliseconds(),
				}); err != nil {
					// Never fail a turn on logging — just print.
					fmt.Fprintf(stderrOrDevNull(),
						"[biu] usage log: %v\n", err)
				}
			}
			stopFired = true
			SafeSend(out, &DoneEvent{
				StopReason:       stopReason,
				InputTokens:      turnUsageIn,
				OutputTokens:     turnUsageOut,
				CacheReadTokens:  turnCacheRead,
				CacheWriteTokens: turnCacheWrite,
				Elapsed:          elapsed,
			}, ctx.Done())
			return

		case "tool_use":
			calls := callsFromAssistant(assistant)
			if len(calls) == 0 {
				// Defensive: stop_reason said tool_use but no blocks
				// found. Bail out so we don't infinite-loop.
				SafeSend(out, &ErrorEvent{
					Err:    errors.New("stop_reason=tool_use but no tool_use blocks parsed"),
					Source: ErrSrcInternal, Recoverable: false,
				}, ctx.Done())
				return
			}
			outs := e.runBatches(ctx, out, calls)
			// Synthesise tool_result entries for any tool_use that didn't
			// receive an answer because the batch was interrupted mid-way.
			// Anthropic API rejects the next request if a tool_use block
			// has no matching tool_result, so this keeps history replayable
			// after Interrupt(). softError for the tool name surfaces the
			// reason to the model on the next turn (when the user resumes).
			outs = backfillInterruptedToolResults(out, calls, outs, ctx)
			toolResultMsg := buildToolResultMessage(outs)
			toolResultMsg.CreatedAt = time.Now().UTC()
			e.state.AppendMessage(toolResultMsg)
			// If we got here because of Interrupt(), short-circuit before
			// firing another LLM round. State now has assistant + matching
			// tool_results so resuming is safe.
			if isInterrupt(ctx, ctx.Err()) {
				stopFired = true
				emitInterruptedDone(out, turnStarted,
					turnUsageIn, turnUsageOut,
					turnCacheRead, turnCacheWrite)
				return
			}
			// Loop back: next iteration sends the updated message
			// list to the LLM.
			continue

		case "max_tokens":
			// LLM ran out of room mid-response. Force a compact and
			// retry the turn — the next iteration will re-issue with
			// the summarised history.
			if e.compact == nil {
				SafeSend(out, &ErrorEvent{
					Err:    errors.New("hit max_tokens but compact disabled"),
					Source: ErrSrcLLM, Recoverable: false,
				}, ctx.Done())
				return
			}
			if err := e.runCompact(ctx, out, "max_tokens_recovery"); err != nil {
				SafeSend(out, &ErrorEvent{
					Err:    fmt.Errorf("compact after max_tokens: %w", err),
					Source: ErrSrcCompact, Recoverable: false,
				}, ctx.Done())
				return
			}
			// Loop back; next iteration retries with summary in place.
			continue

		default:
			SafeSend(out, &ErrorEvent{
				Err:    fmt.Errorf("unhandled stop_reason %q", stopReason),
				Source: ErrSrcLLM, Recoverable: false,
			}, ctx.Done())
			return
		}
	}

	// Tool turn budget exhausted.
	SafeSend(out, &ErrorEvent{
		Err:    fmt.Errorf("tool turn budget %d exhausted", e.maxToolTurns),
		Source: ErrSrcInternal, Recoverable: false,
	}, ctx.Done())
}

// buildBgTaskAttachment formats a slice of completed bg tasks as the
// system note that gets injected into the next turn. Stable shape —
// the model parses these by line shape, so changes here rotate user
// expectations.
//
// Layout per task:
//
//	<bg-task-completed id="bg-N" status="done" exit="0">
//	command: <verbatim>
//	tail (last K lines):
//	  <line>
//	  ...
//	</bg-task-completed>
//
// Multiple tasks → repeated blocks separated by a blank line. Caller
// guarantees `done` is non-empty; we panic-skip the empty case here
// rather than print a no-op header.
func buildBgTaskAttachment(done []BgTaskCompletion) string {
	var b strings.Builder
	b.WriteString("Background tasks completed since your last turn. ")
	b.WriteString("This is a notification, not a command — review and ")
	b.WriteString("decide whether to act. Output past the tail can be ")
	b.WriteString("fetched with BashOutput{task_id:\"<id>\"} until it ")
	b.WriteString("falls off the buffer cap.\n\n")
	for i, t := range done {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "<bg-task-completed id=%q status=%q exit=%d>\n",
			t.ID, t.Status, t.ExitCode)
		fmt.Fprintf(&b, "command: %s\n", t.Command)
		if len(t.Tail) > 0 {
			fmt.Fprintf(&b, "tail (last %d line(s)):\n", len(t.Tail))
			for _, line := range t.Tail {
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteByte('\n')
			}
		} else {
			b.WriteString("(no captured output)\n")
		}
		fmt.Fprintf(&b, "</bg-task-completed>\n")
	}
	return b.String()
}

// isInterrupt reports whether the given error came from a deliberate
// Agent.Interrupt() (cause == ErrInterrupted) as opposed to a parent
// timeout / regular cancel. Returns false for nil err / nil ctx / no
// cause attached. Used by the runSubmit exit paths to decide between
// emitting an Error vs. a clean DoneEvent{StopReason:"interrupted"}.
//
// Two ways the engine sees an interrupt land:
//
//   - ParseStream / tool runners observe ctx.Err() == Canceled. The
//     cause is reachable via context.Cause(ctx) and equals ErrInterrupted.
//   - Go 1.21+ net/http surfaces the *cause* directly through
//     `httpClient.Do(req)` — so the err returned by provider.Stream
//     wraps ErrInterrupted instead of context.Canceled. We accept
//     either form so the dispatch is independent of which adapter
//     shape an HTTP version chose.
func isInterrupt(ctx context.Context, err error) bool {
	if ctx == nil {
		return false
	}
	// err carries ErrInterrupted directly (http.Do path on Go 1.21+).
	if err != nil && errors.Is(err, ErrInterrupted) {
		return true
	}
	// err is some non-cancel error (real network / HTTP failure).
	if err != nil && !errors.Is(err, context.Canceled) {
		return false
	}
	// nil err + ctx not canceled → nothing to dispatch on.
	if err == nil && ctx.Err() == nil {
		return false
	}
	return errors.Is(context.Cause(ctx), ErrInterrupted)
}

// emitInterruptedDone delivers the synthetic DoneEvent that closes a
// turn aborted by Agent.Interrupt(). Uses a non-`SafeSend` send because
// SafeSend bails when ctx.Done() is already closed (which it is, by
// definition, on this path) — that would drop the event and leave the
// embedder hanging without a stop_reason. The receiver (biumindkit /
// engine.Submit goroutine) keeps draining `out` until it closes, so
// the blocking send is well-defined.
func emitInterruptedDone(
	out chan<- Event,
	turnStarted time.Time,
	in, outTok, cRead, cWrite int,
) {
	out <- &DoneEvent{
		StopReason:       "interrupted",
		InputTokens:      in,
		OutputTokens:     outTok,
		CacheReadTokens:  cRead,
		CacheWriteTokens: cWrite,
		Elapsed:          time.Since(turnStarted),
	}
}

// backfillInterruptedToolResults inspects (calls, outs) and, for any
// tool_use that runBatches skipped because ctx was canceled before the
// runner reached it, synthesises a soft-error tool_result so the
// assistant message + user message stay paired. Anthropic rejects the
// next /v1/messages request if a tool_use block lacks its tool_result.
//
// runBatches always returns a slot per call (`make([]runnerOutput,
// len(calls))`), but a slot can be the zero value when `runOne` was
// never invoked for that index — that happens when ctx canceled before
// the batch group reached it. We detect zero-value slots by an empty
// UseID and fill them in.
//
// The synthetic payload is `is_error: true` with a stable text body so
// downstream tooling (UI, hooks) can recognise it. Emits a regular
// ToolUseResultEvent so subscribers see the synthetic result on their
// stream, just like a real one.
func backfillInterruptedToolResults(
	out chan<- Event,
	calls []runnerInput,
	outs []runnerOutput,
	ctx context.Context,
) []runnerOutput {
	for i, c := range calls {
		if outs[i].UseID != "" {
			continue
		}
		payload := softError(c.Name, "interrupted by user before tool execution")
		outs[i] = runnerOutput{
			UseID:   c.UseID,
			Name:    c.Name,
			Payload: payload,
		}
		// Use SafeSend here: receiver is still draining (see
		// emitInterruptedDone comment) but defer to the existing
		// pattern for non-DoneEvent emissions in canceled state.
		SafeSend(out, &ToolUseResultEvent{
			ID: c.UseID, Name: c.Name, Result: payload,
		}, nil)
	}
	return outs
}
