package compact

import (
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func msg(id string, role state.MessageRole) state.Message {
	return state.Message{ID: id, Role: role}
}

func TestGroupByAPIRound_empty(t *testing.T) {
	if got := GroupByAPIRound(nil); got != nil {
		t.Errorf("empty input should return nil, got %v", got)
	}
}

func TestGroupByAPIRound_singleRound(t *testing.T) {
	// Boundary fires AT the start of every distinct assistant. A
	// user-only prefix before the first assistant is its own group;
	// the assistant + its tool_results form the second group.
	in := []state.Message{
		msg("u1", state.RoleUser),
		msg("a1", state.RoleAssistant),
		msg("r1", state.RoleUser), // tool_result
	}
	got := GroupByAPIRound(in)
	if len(got) != 2 {
		t.Fatalf("want 2 groups (user prefix + 1 assistant round), got %d: %v", len(got), got)
	}
	if len(got[0]) != 1 || got[0][0].ID != "u1" {
		t.Errorf("first group should be [u1], got %v", got[0])
	}
	if len(got[1]) != 2 || got[1][0].ID != "a1" {
		t.Errorf("second group should be [a1, r1], got %v", got[1])
	}
}

func TestGroupByAPIRound_twoAssistantBoundary(t *testing.T) {
	in := []state.Message{
		msg("u1", state.RoleUser),
		msg("a1", state.RoleAssistant),
		msg("r1", state.RoleUser),
		msg("a2", state.RoleAssistant), // new assistant id → boundary
		msg("r2", state.RoleUser),
	}
	got := GroupByAPIRound(in)
	// 3 groups: [u1], [a1, r1], [a2, r2]
	if len(got) != 3 {
		t.Fatalf("want 3 groups, got %d: %+v", len(got), got)
	}
	if len(got[0]) != 1 || len(got[1]) != 2 || len(got[2]) != 2 {
		t.Errorf("group sizes = %d/%d/%d, want 1/2/2",
			len(got[0]), len(got[1]), len(got[2]))
	}
}

// Streaming chunks share an assistant ID. Within an assistant ID
// boundaries should NOT fire — they'd split tool_uses from their
// results. The leading user prefix is its own group; everything
// else stays merged because the id never changes.
func TestGroupByAPIRound_sharedAssistantIDStaysTogether(t *testing.T) {
	in := []state.Message{
		msg("u", state.RoleUser),
		msg("a1", state.RoleAssistant), // first chunk → opens round
		msg("a1", state.RoleAssistant), // same id → no boundary
		msg("r", state.RoleUser),
		msg("a1", state.RoleAssistant), // still same id
	}
	got := GroupByAPIRound(in)
	if len(got) != 2 {
		t.Errorf("want 2 groups ([u], [a1*4 + r]), got %d", len(got))
	}
	if len(got[1]) != 4 {
		t.Errorf("assistant round should hold 4 messages (3 a1 chunks + r), got %d",
			len(got[1]))
	}
}

// Missing IDs are kept with the current group — we don't open
// boundaries we can't verify.
func TestGroupByAPIRound_missingIDsCoalesce(t *testing.T) {
	in := []state.Message{
		msg("u", state.RoleUser),
		msg("", state.RoleAssistant), // legacy / corrupt — no id; coalesces with current
		msg("r", state.RoleUser),
		msg("a1", state.RoleAssistant), // first id'd assistant → opens boundary
		msg("r1", state.RoleUser),
	}
	got := GroupByAPIRound(in)
	// First "a1" opens a boundary because current ([u, "", r]) is
	// non-empty. So 2 groups.
	if len(got) != 2 {
		t.Errorf("want 2 groups, got %d", len(got))
	}
}

// Non-assistant messages (system, user) never open boundaries — only
// the FIRST distinct assistant after a non-empty current group does.
// So [system, user, assistant] yields 2 groups: [s, u], [a].
func TestGroupByAPIRound_systemMessagesNeverOpenBoundary(t *testing.T) {
	in := []state.Message{
		msg("s", state.RoleSystem),
		msg("u", state.RoleUser),
		msg("a", state.RoleAssistant),
	}
	got := GroupByAPIRound(in)
	if len(got) != 2 {
		t.Errorf("want 2 groups ([s,u] + [a]), got %d", len(got))
	}
	if len(got[0]) != 2 || len(got[1]) != 1 {
		t.Errorf("group sizes = %d/%d, want 2/1", len(got[0]), len(got[1]))
	}
}
