// Tests for the bg-task-completion injection — proves the notifier
// hook fires at the head of every user turn and the rendered
// attachment matches the documented shape.

package engine_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// stubNotifier scripts what the engine should pull each time it
// asks for pending completions. We track invocations to lock the
// "drained once per turn" contract.
type stubNotifier struct {
	mu     sync.Mutex
	calls  int
	queues [][]engine.BgTaskCompletion
}

func (s *stubNotifier) PendingCompletions() []engine.BgTaskCompletion {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls > len(s.queues) {
		return nil
	}
	return s.queues[s.calls-1]
}

func newNotifyEngine(t *testing.T, prov engine.Provider, n engine.BgTaskNotifier) (*engine.QueryEngine, *state.AppState) {
	t.Helper()
	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: engine.NewRegistry(), Provider: prov,
		Model:             "test",
		BypassPermissions: true,
		BgTaskNotifier:    n,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eng, st
}

func TestBgTaskNotifyEmptyQueueInjectsNothing(t *testing.T) {
	prov := &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}}
	notifier := &stubNotifier{queues: [][]engine.BgTaskCompletion{nil}}
	eng, st := newNotifyEngine(t, prov, notifier)
	drainAll(eng.Submit(context.Background(), "hello"))

	if notifier.calls != 1 {
		t.Errorf("notifier should be polled once per user turn; got %d", notifier.calls)
	}
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, "bg-task-completed") {
				t.Errorf("empty queue should NOT inject an attachment; got %q", b.Text)
			}
		}
	}
}

func TestBgTaskNotifyInjectsAttachmentOnNextTurn(t *testing.T) {
	prov := &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}}
	notifier := &stubNotifier{
		queues: [][]engine.BgTaskCompletion{
			{
				{
					ID: "bg-1", Command: "go test ./...",
					Status: "done", ExitCode: 0,
					Tail: []string{"PASS", "ok  ./internal/engine  1.2s"},
				},
			},
		},
	}
	eng, st := newNotifyEngine(t, prov, notifier)
	drainAll(eng.Submit(context.Background(), "anything"))

	// The attachment must be a system message landing BEFORE the
	// user message — that's the head-of-turn slot.
	var attachIdx, userIdx = -1, -1
	for i, m := range st.Snapshot() {
		flat := ""
		for _, b := range m.Content {
			flat += b.Text
		}
		if m.Role == state.RoleSystem && strings.Contains(flat, "bg-task-completed") {
			attachIdx = i
		}
		if m.Role == state.RoleUser && strings.Contains(flat, "anything") {
			userIdx = i
		}
	}
	if attachIdx < 0 {
		t.Fatalf("expected bg-task-completed attachment in state; got %d messages", len(st.Snapshot()))
	}
	if userIdx < 0 {
		t.Fatalf("user message should be in state")
	}
	if attachIdx >= userIdx {
		t.Errorf("attachment must precede user message; attachIdx=%d userIdx=%d",
			attachIdx, userIdx)
	}
}

// The attachment payload format is the contract — model behaviour
// depends on the literal `<bg-task-completed …>` markers and the
// `tail (last N line(s)):` header.
func TestBgTaskNotifyAttachmentShape(t *testing.T) {
	prov := &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}}
	notifier := &stubNotifier{
		queues: [][]engine.BgTaskCompletion{
			{
				{
					ID: "bg-1", Command: "make build",
					Status: "failed", ExitCode: 2,
					Tail: []string{"main.go:42: undefined: foo"},
				},
				{
					ID: "bg-2", Command: "tail -f /tmp/log",
					Status: "killed", ExitCode: -1,
					Tail: nil, // no captured output
				},
			},
		},
	}
	eng, st := newNotifyEngine(t, prov, notifier)
	drainAll(eng.Submit(context.Background(), "go"))

	body := flattenSystemMessages(st)
	for _, must := range []string{
		`Background tasks completed since your last turn`,
		`<bg-task-completed id="bg-1" status="failed" exit=2>`,
		`command: make build`,
		`tail (last 1 line(s)):`,
		`main.go:42: undefined: foo`,
		`<bg-task-completed id="bg-2" status="killed" exit=-1>`,
		`(no captured output)`,
		`</bg-task-completed>`,
	} {
		if !strings.Contains(body, must) {
			t.Errorf("attachment missing %q;\nfull body:\n%s", must, body)
		}
	}
}

// Two turns in a row: first turn drains 2 completions; second turn
// must NOT re-show them (Pending() returned nil the second time).
func TestBgTaskNotifyDoesNotRepeatAcrossTurns(t *testing.T) {
	prov := &scripted{turns: [][]engine.StreamFrame{
		textTurn("first reply"),
		textTurn("second reply"),
	}}
	notifier := &stubNotifier{
		queues: [][]engine.BgTaskCompletion{
			// Turn 1: one completion.
			{{ID: "bg-1", Command: "echo a", Status: "done", Tail: []string{"a"}}},
			// Turn 2: nothing.
			nil,
		},
	}
	eng, st := newNotifyEngine(t, prov, notifier)
	drainAll(eng.Submit(context.Background(), "first"))
	drainAll(eng.Submit(context.Background(), "second"))

	count := strings.Count(flattenSystemMessages(st), `id="bg-1"`)
	if count != 1 {
		t.Errorf("bg-1 should appear once across two turns; got %d", count)
	}
	if notifier.calls != 2 {
		t.Errorf("notifier should be polled once per turn; got %d calls", notifier.calls)
	}
}

// Nil notifier is a valid configuration — engine must skip the
// attachment block entirely.
func TestBgTaskNotifyNilSafe(t *testing.T) {
	prov := &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}}
	eng, _ := newNotifyEngine(t, prov, nil)
	drainAll(eng.Submit(context.Background(), "anything"))
	// No assertion on state — the test passes if no panic / nil
	// deref happened during runSubmit.
}

// flattenSystemMessages joins every system-role message in state for
// substring assertions.
func flattenSystemMessages(st *state.AppState) string {
	var b strings.Builder
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, c := range m.Content {
			b.WriteString(c.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
