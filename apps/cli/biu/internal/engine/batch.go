// Tool-call batcher.
//
// Anthropic Messages API can emit multiple tool_use blocks in a single
// assistant turn (e.g. "I'll Read foo.go and Glob *.go in parallel").
// The QueryEngine groups them into batches:
//
//   * Batch = consecutive calls that are all IsConcurrencySafe(input)
//   * Within a batch: parallel execution
//   * Between batches: serial
//
// Why split per assistant turn rather than just running everything
// parallel? Two reasons:
//
//   1. Many tools mutate disk state (Edit, Write). Two Edits to the
//      same file in parallel = data race. IsConcurrencySafe=false on
//      Edit prevents that.
//   2. The LLM relies on result ordering: a tool_use_summary block in
//      the next message must list results in the same order as the
//      original tool_use blocks. We preserve order regardless of how
//      we batched execution.
//
// Algorithm:
//   group = []
//   for each call in turn order:
//     if group empty: group = [call]; continue
//     if call.safe and last.safe: group += call
//     else: flush group; group = [call]
//   flush group
//
//   flush(group):
//     if len == 1: run serial
//     else: run parallel via WaitGroup, preserve result slot order

package engine

import (
	"context"
	"sync"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// runBatches executes every tool_use block in `calls` in the right
// order with the right concurrency, emits all events through `out`,
// and returns the results in original call order so the caller can
// build the next user-turn message deterministically.
func (e *QueryEngine) runBatches(
	ctx context.Context,
	out chan<- Event,
	calls []runnerInput,
) []runnerOutput {
	results := make([]runnerOutput, len(calls))
	if len(calls) == 0 {
		return results
	}

	// Each entry is the index range [start, end) into `calls` that
	// can run in parallel. Single-element groups are also valid.
	batches := groupBySafety(e.tools, calls)

	for _, group := range batches {
		if len(group) == 1 {
			i := group[0]
			results[i] = e.runOne(ctx, out, calls[i])
			continue
		}
		// Parallel batch.
		var wg sync.WaitGroup
		for _, idx := range group {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = e.runOne(ctx, out, calls[i])
			}(idx)
		}
		wg.Wait()
	}
	return results
}

// groupBySafety walks calls in order and emits index groups. A call
// joins the current group only if both it and the group's last call
// are concurrency-safe. Reads tool metadata from the registry; for
// unknown tools we conservatively force single-call groups (so the
// runner's "unknown tool" soft error doesn't get mixed into a parallel
// batch).
func groupBySafety(reg ToolRegistry, calls []runnerInput) [][]int {
	var groups [][]int
	cur := []int{}

	safeAt := func(i int) bool {
		t, ok := reg.Get(calls[i].Name)
		if !ok {
			return false
		}
		return t.IsConcurrencySafe(calls[i].Input)
	}

	for i := range calls {
		if len(cur) == 0 {
			cur = append(cur, i)
			continue
		}
		// To extend the current group: i AND every existing member
		// must be safe. Since we only add safe items to a "safe"
		// group, checking `safeAt(cur[0])` doubles as the
		// group-is-still-parallel signal.
		if safeAt(i) && safeAt(cur[0]) {
			cur = append(cur, i)
			continue
		}
		groups = append(groups, cur)
		cur = []int{i}
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// callsFromAssistant pulls the tool_use blocks out of an assistant
// message in the order they appeared. Blocks with malformed input are
// skipped (the turn loop synthesises soft-error results for them).
func callsFromAssistant(msg state.Message) []runnerInput {
	out := []runnerInput{}
	for _, b := range msg.Content {
		if b.Type != state.ContentToolUse {
			continue
		}
		if b.ToolUseMalformed != "" {
			// Malformed-input blocks are not executed — the turn loop
			// synthesises a soft-error tool_result for them so the model
			// can re-emit the call. Skip real dispatch here.
			continue
		}
		out = append(out, runnerInput{
			UseID: b.ToolUseID,
			Name:  b.ToolUseName,
			Input: b.ToolUseInput,
		})
	}
	return out
}

// malformedBlocks returns the tool_use blocks in msg whose streamed
// input JSON failed to parse (ToolUseMalformed != ""). They are not
// dispatched to real tools; the turn loop synthesises soft-error
// tool_results so the model can re-emit them next round.
func malformedBlocks(msg state.Message) []state.ContentBlock {
	var out []state.ContentBlock
	for _, b := range msg.Content {
		if b.Type == state.ContentToolUse && b.ToolUseMalformed != "" {
			out = append(out, b)
		}
	}
	return out
}

// buildToolResultMessage assembles the next user-turn message from a
// slice of runner outputs. Each result becomes a content block of
// type "tool_result"; the full message goes back to the LLM.
//
// Anthropic requires that every tool_use in the assistant message has
// a matching tool_result in the next user message — order-by-id, not
// by-position — so we use UseID (preserved from the assistant block)
// as the link.
func buildToolResultMessage(outs []runnerOutput) state.Message {
	blocks := make([]state.ContentBlock, 0, len(outs))
	for _, o := range outs {
		blocks = append(blocks, state.ContentBlock{
			Type:              state.ContentToolResult,
			ToolResultID:      o.UseID,
			ToolResultContent: o.Payload.Content,
			ToolResultIsError: o.Payload.IsError,
		})
	}
	return state.Message{
		Role:    state.RoleUser, // tool_results live inside a user turn (per Anthropic spec)
		Content: blocks,
	}
}
