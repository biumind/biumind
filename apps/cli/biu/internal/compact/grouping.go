// API-round grouping — split a message slice at API-round boundaries.
//
// An API round is one assistant response and every tool_result that
// resolves the tool_uses in that response. The boundary fires at
// the START of a new assistant message — whose Message.ID differs
// from the prior assistant.
//
// Why grouping matters: reactive operations (compact a single round,
// drop a round's tool results) need to operate on coherent units.
// User-turn boundaries are too coarse for SDK / agentic workloads
// where a single human prompt drives many rounds. API-round
// boundaries are fine-grained yet API-safe (the API contract pairs
// every tool_use with its tool_result before the next assistant
// turn, so groups are always tool-paired).
//
// Failure mode: if message IDs are missing (resume from a corrupt
// jsonl), the gate stays open and every message lands in one
// group. That's a degenerate case the caller should detect — we
// don't try to repair here.

package compact

import "github.com/biumind/biumind/apps/cli/biu/internal/state"

// GroupByAPIRound splits messages at assistant-id transitions. Each
// returned group represents one API round: the assistant's response
// followed by the user-side messages (tool_results, user prompts)
// that arrive before the NEXT distinct assistant.
//
// Empty input returns nil. Messages with empty IDs (legacy data)
// are kept in the current group — a missing ID doesn't open a new
// boundary, which preserves any subsequent matched IDs as the
// canonical split points.
func GroupByAPIRound(messages []state.Message) [][]state.Message {
	if len(messages) == 0 {
		return nil
	}
	var groups [][]state.Message
	var current []state.Message
	var lastAssistantID string

	for _, m := range messages {
		isNewRound := m.Role == state.RoleAssistant &&
			m.ID != "" &&
			m.ID != lastAssistantID &&
			len(current) > 0

		if isNewRound {
			groups = append(groups, current)
			current = []state.Message{m}
		} else {
			current = append(current, m)
		}
		if m.Role == state.RoleAssistant && m.ID != "" {
			lastAssistantID = m.ID
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}
