package chat

import (
	"testing"

	"github.com/google/uuid"
)

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }

func TestPickLatestSiblingsEmpty(t *testing.T) {
	if got := pickLatestSiblings(nil); got != nil && len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
	if got := pickLatestSiblings([]*Message{}); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestPickLatestSiblingsKeepsAllWithoutSiblings(t *testing.T) {
	u1 := uuid.New()
	a1 := uuid.New()
	history := []*Message{
		{ID: u1, Role: RoleUser, Position: 1},
		{ID: a1, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 2},
	}
	got := pickLatestSiblings(history)
	if len(got) != 2 {
		t.Errorf("expected 2 messages, got %d", len(got))
	}
}

func TestPickLatestSiblingsDropsSuperseded(t *testing.T) {
	u1 := uuid.New()
	a1 := uuid.New()
	a2 := uuid.New()
	a3 := uuid.New()
	// Two regenerations of the same user message — only a3 (highest
	// position) survives.
	history := []*Message{
		{ID: u1, Role: RoleUser, Position: 1},
		{ID: a1, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 2},
		{ID: a2, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 3},
		{ID: a3, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 4},
	}
	got := pickLatestSiblings(history)
	if len(got) != 2 {
		t.Errorf("expected 2 messages (user + latest assistant), got %d", len(got))
	}
	if got[1].ID != a3 {
		t.Errorf("expected latest=a3, got %v", got[1].ID)
	}
}

func TestPickLatestSiblingsAcrossMultipleParents(t *testing.T) {
	u1, u2 := uuid.New(), uuid.New()
	a1a, a1b := uuid.New(), uuid.New()
	a2 := uuid.New()
	history := []*Message{
		{ID: u1, Role: RoleUser, Position: 1},
		{ID: a1a, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 2},
		{ID: a1b, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 3},
		{ID: u2, Role: RoleUser, Position: 4},
		{ID: a2, Role: RoleAssistant, ParentID: ptrUUID(u2), Position: 5},
	}
	got := pickLatestSiblings(history)
	// Expect u1, a1b, u2, a2 — a1a is dropped as superseded.
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	for _, m := range got {
		if m.ID == a1a {
			t.Errorf("a1a should have been dropped")
		}
	}
}

func TestPickLatestSiblingsKeepsAssistantsWithoutParent(t *testing.T) {
	// Legacy/seed messages with no parent_id pass through untouched —
	// pre-P1.2 assistants will be missing parent_id in production
	// data.
	a1 := uuid.New()
	a2 := uuid.New()
	history := []*Message{
		{ID: a1, Role: RoleAssistant, Position: 1},
		{ID: a2, Role: RoleAssistant, Position: 2},
	}
	got := pickLatestSiblings(history)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d", len(got))
	}
}

func TestTrimAfterParentEndsAtUserMessage(t *testing.T) {
	u1 := uuid.New()
	a1 := uuid.New()
	history := []*Message{
		{ID: u1, Role: RoleUser, Position: 1},
		{ID: a1, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 2},
	}
	got := trimAfterParent(history, u1)
	if len(got) != 1 || got[0].ID != u1 {
		t.Errorf("expected only user msg, got %+v", got)
	}
}

func TestTrimAfterParentMissingNoop(t *testing.T) {
	u1 := uuid.New()
	missing := uuid.New()
	history := []*Message{
		{ID: u1, Role: RoleUser, Position: 1},
	}
	got := trimAfterParent(history, missing)
	if len(got) != 1 {
		t.Errorf("missing parent should be no-op, got %+v", got)
	}
}

func TestTrimAfterParentLeavesEarlierTurnsIntact(t *testing.T) {
	u1, u2 := uuid.New(), uuid.New()
	a1, a2 := uuid.New(), uuid.New()
	history := []*Message{
		{ID: u1, Role: RoleUser, Position: 1},
		{ID: a1, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 2},
		{ID: u2, Role: RoleUser, Position: 3},
		{ID: a2, Role: RoleAssistant, ParentID: ptrUUID(u2), Position: 4},
	}
	// Regenerating for u2 → keep u1 + a1 (prior turn) + u2.
	got := trimAfterParent(history, u2)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[2].ID != u2 {
		t.Errorf("last msg should be u2, got %v", got[2].ID)
	}
}

func TestPickLatestSiblingsPreservesOrder(t *testing.T) {
	u1 := uuid.New()
	a1 := uuid.New()
	tool := uuid.New()
	a2 := uuid.New()
	history := []*Message{
		{ID: u1, Role: RoleUser, Position: 1},
		{ID: a1, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 2},
		{ID: tool, Role: RoleTool, Position: 3},
		{ID: a2, Role: RoleAssistant, ParentID: ptrUUID(u1), Position: 4},
	}
	got := pickLatestSiblings(history)
	// Order: u1, tool, a2 (a1 dropped, tool keeps position).
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d: %+v", len(got), got)
	}
	if got[0].ID != u1 || got[1].ID != tool || got[2].ID != a2 {
		t.Errorf("order wrong: %+v", got)
	}
}
