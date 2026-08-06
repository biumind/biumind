package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestAsyncAgentStore_RecordAndDrain — basic round-trip: Record three
// completions, Pending() returns them sorted by ID, second Pending()
// is empty (drain semantics).
func TestAsyncAgentStore_RecordAndDrain(t *testing.T) {
	s := NewAsyncAgentStore()
	for _, id := range []string{"agent-3", "agent-1", "agent-2"} {
		s.Record(TeammateCompletion{
			Handle: TeammateHandle{ID: id, AgentType: "explore"},
			Output: "result for " + id,
		})
	}
	got := s.Pending()
	if len(got) != 3 {
		t.Fatalf("Pending = %d, want 3", len(got))
	}
	for i, want := range []string{"agent-1", "agent-2", "agent-3"} {
		if got[i].Handle.ID != want {
			t.Errorf("position %d: got %q, want %q", i, got[i].Handle.ID, want)
		}
	}
	if again := s.Pending(); len(again) != 0 {
		t.Errorf("second Pending should be empty after drain; got %d", len(again))
	}
}

// TestAsyncAgentStore_RecordIdempotent — same ID written twice is one
// entry. Defensive against goroutine double-fire (rare but possible
// when a Spawn returns + a panic-recover both write).
func TestAsyncAgentStore_RecordIdempotent(t *testing.T) {
	s := NewAsyncAgentStore()
	s.Record(TeammateCompletion{Handle: TeammateHandle{ID: "a"}, Output: "first"})
	s.Record(TeammateCompletion{Handle: TeammateHandle{ID: "a"}, Output: "second"})
	got := s.Pending()
	if len(got) != 1 || got[0].Output != "second" {
		t.Errorf("dup write should overwrite; got %+v", got)
	}
}

// TestAsyncAgentStore_Active — MarkActive registers a handle; Record
// removes it from Active. Useful for "what's still running?" queries.
func TestAsyncAgentStore_Active(t *testing.T) {
	s := NewAsyncAgentStore()
	h1 := TeammateHandle{ID: "agent-1", AgentType: "explore"}
	h2 := TeammateHandle{ID: "agent-2", AgentType: "general-purpose"}
	s.MarkActive(h1)
	s.MarkActive(h2)
	if active := s.Active(); len(active) != 2 {
		t.Errorf("two active handles expected, got %v", active)
	}
	s.Record(TeammateCompletion{Handle: h1, Output: "done"})
	active := s.Active()
	if len(active) != 1 || active[0].ID != "agent-2" {
		t.Errorf("after Record h1, expected only h2 active; got %v", active)
	}
}

// TestBuildTeammateAttachment_Format — system-prompt block has the
// wrapper tag, lists each completion with handle id + agent type +
// description + output, and renders Err clearly.
func TestBuildTeammateAttachment_Format(t *testing.T) {
	completions := []TeammateCompletion{
		{
			Handle: TeammateHandle{
				ID: "agent-1", AgentType: "explore",
				Description: "Find auth flow",
			},
			Output: "JWT validation lives in pkg/auth/jwt.go",
		},
		{
			Handle: TeammateHandle{ID: "agent-2", AgentType: "general-purpose"},
			Err:    errors.New("budget exceeded after 25 turns"),
		},
		{
			Handle: TeammateHandle{ID: "agent-3"},
			// Output empty — should render "(no output)".
		},
	}
	att := buildTeammateAttachment(completions)
	for _, must := range []string{
		"<teammate-completions>",
		"</teammate-completions>",
		"agent-1 (explore)",
		"Find auth flow",
		"JWT validation lives in pkg/auth/jwt.go",
		"agent-2 (general-purpose)",
		"[failed: budget exceeded",
		"agent-3",
		"(no output)",
	} {
		if !strings.Contains(att, must) {
			t.Errorf("attachment missing %q:\n%s", must, att)
		}
	}
}

// TestBuildTeammateAttachment_Empty — empty completion list ⇒ empty
// string so turn.go can short-circuit without injecting a noisy
// system message.
func TestBuildTeammateAttachment_Empty(t *testing.T) {
	if got := buildTeammateAttachment(nil); got != "" {
		t.Errorf("empty input should yield \"\"; got %q", got)
	}
}

// TestSpawnAsync_NilParent — defensive coverage. The async spawner
// without a parent or store returns ErrAsyncUnavailable, never
// blocks, never panics.
func TestSpawnAsync_NilParent(t *testing.T) {
	s := &engineSpawner{} // parent nil
	_, err := s.SpawnAsync(context.Background(), AgentSpawnRequest{Prompt: "x"})
	if err == nil {
		t.Errorf("nil parent should fail with ErrAsyncUnavailable")
	}
	if err != ErrAsyncUnavailable {
		t.Errorf("expected ErrAsyncUnavailable, got %v", err)
	}
}

// TestSpawnAsync_RecordsHandleViaActive — the spawner registers the
// handle synchronously (before launching the goroutine) so a
// Pending()-poll right after SpawnAsync sees the work in Active.
//
// This test wires an artificial spawner whose Spawn() blocks on a
// gate so we can assert "still running" before letting it complete.
func TestSpawnAsync_RecordsHandleViaActive(t *testing.T) {
	store := NewAsyncAgentStore()
	parent := &QueryEngine{asyncAgents: store}
	// Hand-roll a spawner that uses our store but doesn't actually
	// run a sub-engine — Spawn blocks on the gate so we can observe
	// the in-flight state.
	gate := make(chan struct{})
	s := &fakeAsyncSpawner{
		store: store,
		spawn: func() (*AgentSpawnResult, error) {
			<-gate
			return &AgentSpawnResult{Output: "result"}, nil
		},
		parent: parent,
	}

	h, err := s.SpawnAsync(context.Background(), AgentSpawnRequest{
		AgentType: "explore", Description: "task", Prompt: "do",
	})
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	if h.ID == "" {
		t.Fatal("handle ID should be set")
	}
	// Active should report it.
	active := store.Active()
	if len(active) != 1 || active[0].ID != h.ID {
		t.Errorf("Active = %+v, want [%s]", active, h.ID)
	}
	// Pending should be empty (still running).
	if pend := store.Pending(); len(pend) != 0 {
		t.Errorf("Pending should be empty while running; got %v", pend)
	}

	close(gate)
	// Wait briefly for the goroutine to record completion.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pend := store.Pending(); len(pend) > 0 {
			if pend[0].Output != "result" {
				t.Errorf("got Output=%q, want 'result'", pend[0].Output)
			}
			if len(store.Active()) != 0 {
				t.Errorf("Active should drain on Record")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("teammate never recorded completion")
}

// fakeAsyncSpawner mimics engineSpawner.SpawnAsync without booting a
// sub-engine, so we can exercise the inbox plumbing in isolation.
type fakeAsyncSpawner struct {
	store  AsyncAgentStore
	spawn  func() (*AgentSpawnResult, error)
	parent *QueryEngine
}

func (s *fakeAsyncSpawner) Spawn(_ context.Context, _ AgentSpawnRequest) (*AgentSpawnResult, error) {
	return s.spawn()
}

func (s *fakeAsyncSpawner) SpawnAsync(_ context.Context, req AgentSpawnRequest) (TeammateHandle, error) {
	if s.parent == nil || s.parent.asyncAgents == nil {
		return TeammateHandle{}, ErrAsyncUnavailable
	}
	h := TeammateHandle{
		ID: nextAgentID(), AgentType: req.AgentType,
		Description: req.Description, Started: time.Now(),
	}
	s.store.MarkActive(h)
	go func() {
		res, err := s.spawn()
		c := TeammateCompletion{Handle: h, Stopped: time.Now()}
		if err != nil {
			c.Err = err
		} else if res != nil {
			c.Output = res.Output
		}
		s.store.Record(c)
	}()
	return h, nil
}

