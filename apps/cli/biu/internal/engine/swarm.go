// Async-agent swarm primitives (P20.53), kept at minimal scope: a
// parent engine can fire-and-forget a sub-agent goroutine and pick up
// the result at the start of its next user turn — no polling, no
// manual inbox read.
//
// Design:
//
//   1. AsyncAgentStore is the parent-side inbox. The spawner writes
//      a TeammateCompletion into it when an async sub-agent reaches
//      DoneEvent.
//   2. QueryEngine consults Store.Pending() at the head of every
//      user-turn (like bgTaskNotifier does) and renders the
//      completions as a system attachment so the model sees them as
//      naturally as a tool result.
//   3. Tools that want to spawn async work go through
//      AgentSpawner.SpawnAsync — a non-blocking method that returns
//      a handle struct synchronously.
//
// What this file does NOT do:
//
//   - TeamCreate / TeamDelete (group abstraction). Future commit can
//     layer those on top by routing teammates into a shared store
//     keyed by team name.
//   - SendMessage to a still-running teammate. Today the message
//     queue is one-way (teammate → parent on completion). Two-way
//     follow-up requires a per-teammate prompt channel + mid-turn
//     interrupt support, which is its own lift.

package engine

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
)

// TeammateHandle is what SpawnAsync returns to the calling tool. The
// tool surfaces the ID to the model so the model can refer to the
// teammate by handle in subsequent prose ("agent-3 is researching X").
type TeammateHandle struct {
	ID          string
	AgentType   string
	Description string
	Started     time.Time
}

// TeammateCompletion is what the parent engine reads back from the
// inbox once a teammate finishes. Lean on purpose — only what the
// system attachment needs.
type TeammateCompletion struct {
	Handle  TeammateHandle
	Output  string // the teammate's final assistant text, or "" on error
	Err     error  // non-nil ⇒ teammate crashed / hit budget / was cancelled
	Stopped time.Time
}

// AsyncAgentStore is the interface the engine consumes. The default
// implementation is the in-memory NewAsyncAgentStore; mockable so
// tests can plug their own.
type AsyncAgentStore interface {
	// Record stores a completion. Idempotent on Handle.ID — re-Record
	// of the same id replaces the prior entry (defensive: a goroutine
	// that double-fires shouldn't double-render the attachment).
	Record(c TeammateCompletion)

	// Pending returns + drains every recorded completion. Engine calls
	// it at user-turn head; subsequent calls without new Records
	// return nil so the system attachment doesn't repeat.
	Pending() []TeammateCompletion

	// Active returns the still-running handles (snapshot). Used by the
	// model-facing diagnostic so the user can ask "what's running?"
	// without waiting for a completion.
	Active() []TeammateHandle

	// MarkActive registers a handle as in-flight. Spawner calls this
	// before launching the goroutine so a Pending()-drain after the
	// task starts but before it finishes still reports it via Active.
	MarkActive(h TeammateHandle)
}

// asyncAgentStore is the in-memory implementation.
type asyncAgentStore struct {
	mu      sync.Mutex
	pending map[string]TeammateCompletion
	active  map[string]TeammateHandle
}

// NewAsyncAgentStore returns an empty in-memory store.
func NewAsyncAgentStore() AsyncAgentStore {
	return &asyncAgentStore{
		pending: map[string]TeammateCompletion{},
		active:  map[string]TeammateHandle{},
	}
}

func (s *asyncAgentStore) MarkActive(h TeammateHandle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[h.ID] = h
}

func (s *asyncAgentStore) Record(c TeammateCompletion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[c.Handle.ID] = c
	delete(s.active, c.Handle.ID)
}

func (s *asyncAgentStore) Pending() []TeammateCompletion {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	out := make([]TeammateCompletion, 0, len(s.pending))
	for _, c := range s.pending {
		out = append(out, c)
	}
	s.pending = map[string]TeammateCompletion{}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Handle.ID < out[j].Handle.ID
	})
	return out
}

func (s *asyncAgentStore) Active() []TeammateHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active) == 0 {
		return nil
	}
	out := make([]TeammateHandle, 0, len(s.active))
	for _, h := range s.active {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AsyncSpawner is the optional sub-interface AgentSpawner can
// implement to support fire-and-forget spawning. Tools type-assert
// at call time: an engine without AsyncSpawner support hands the
// async tool a clean error rather than blocking forever.
type AsyncSpawner interface {
	SpawnAsync(ctx context.Context, req AgentSpawnRequest) (TeammateHandle, error)
}

// SpawnAsync on the standard engineSpawner: launches the synchronous
// Spawn in a background goroutine, registers the handle with the
// parent's AsyncAgentStore (so Active reports the in-flight task),
// and returns the handle synchronously.
//
// The goroutine carries its OWN context derived from context.Background
// — the calling tool's ctx (typically the engine's per-call ctx)
// would otherwise cancel the teammate the moment SpawnAsync returns.
// The teammate gets to run until it completes naturally OR the engine
// is closed; cleanup-on-engine-shutdown lands when the swarm work
// gets a `Close()` API.
func (s *engineSpawner) SpawnAsync(_ context.Context, req AgentSpawnRequest) (TeammateHandle, error) {
	if s == nil || s.parent == nil || s.parent.asyncAgents == nil {
		return TeammateHandle{}, ErrAsyncUnavailable
	}
	h := TeammateHandle{
		ID:          nextAgentID(),
		AgentType:   req.AgentType,
		Description: req.Description,
		Started:     time.Now(),
	}
	s.parent.asyncAgents.MarkActive(h)

	// Fire SubagentStart (P20.55) for async dispatch too — observers
	// shouldn't have to care whether the parent went sync (Spawn) or
	// async (SpawnAsync). Fired before goroutine launch so the hook
	// runs while the calling tool's stack is still on it; the actual
	// teammate runs on context.Background regardless.
	if s.parent.hooks != nil && s.parent.hooks.Has(hooks.EventSubagentStart) {
		hooks.Run(context.Background(),
			s.parent.hooks.For(hooks.EventSubagentStart, req.AgentType),
			hooks.EventSubagentStart,
			map[string]any{
				"session_id":  s.parent.agentID,
				"agent_id":    h.ID,
				"agent_type":  req.AgentType,
				"description": req.Description,
				"prompt":      req.Prompt,
				"async":       true,
			})
	}

	go func() {
		// context.Background detaches the teammate's lifetime from the
		// calling tool's per-invocation ctx. The user's Ctrl-C still
		// cancels the parent engine; the teammate then sees its parent
		// become unreachable on next API call and exits.
		bgCtx := context.Background()
		// Build a single sub-engine + reuse it across follow-up
		// Submits queued via SendMessage (P20.53-2). The first iteration
		// runs the original prompt; each subsequent iteration pulls
		// the next queued message off the inbox and re-Submits.
		sub, subErr := s.newSubEngine(req, h.ID)
		c := TeammateCompletion{Handle: h, Stopped: time.Now()}
		if subErr != nil {
			c.Err = subErr
			s.parent.asyncAgents.Record(c)
			return
		}

		nextPrompt := req.Prompt
		nextSender := "user"
		for {
			out := s.runSubmit(bgCtx, sub, nextPrompt, nextSender)
			c.Output = out.Output
			c.Err = out.Err
			c.Stopped = time.Now()

			// Drain one queued message; if there's none, the teammate
			// goes idle and we record completion.
			depth := 0
			if s.parent.teamMessages != nil {
				depth = s.parent.teamMessages.Depth(h.ID)
			}

			// Fire TeammateIdle (P20.55) before potentially picking up
			// the next queued message — observers see the idle state
			// and the queue depth at that moment.
			s.fireTeammateIdleHook(bgCtx, h, out.Output, depth)

			if s.parent.teamMessages == nil {
				break
			}
			msg, ok := s.parent.teamMessages.Dequeue(h.ID)
			if !ok {
				break
			}
			nextPrompt = msg.Body
			nextSender = msg.From
			if nextSender == "" {
				nextSender = "team-lead"
			}
		}
		s.parent.asyncAgents.Record(c)
	}()

	return h, nil
}

// ErrAsyncUnavailable is returned by SpawnAsync when the parent
// engine wasn't built with an AsyncAgentStore. Tools that depend on
// async spawn surface this to the model as a soft error so it falls
// back to the synchronous Agent tool.
var ErrAsyncUnavailable = asyncUnavailable{}

type asyncUnavailable struct{}

func (asyncUnavailable) Error() string {
	return "async agent spawning is not enabled on this engine — use Agent for synchronous delegation"
}

// fireTeammateIdleHook fires the TeammateIdle hook (P20.55) at the
// boundary where a teammate finishes one Submit and may pick up
// another queued message. nil-safe — no hook registry, no fire.
func (s *engineSpawner) fireTeammateIdleHook(ctx context.Context, h TeammateHandle, output string, queueDepth int) {
	if s == nil || s.parent == nil || s.parent.hooks == nil {
		return
	}
	if !s.parent.hooks.Has(hooks.EventTeammateIdle) {
		return
	}
	hooks.Run(ctx,
		s.parent.hooks.For(hooks.EventTeammateIdle, h.AgentType),
		hooks.EventTeammateIdle,
		map[string]any{
			"session_id":  s.parent.agentID,
			"handle_id":   h.ID,
			"agent_type":  h.AgentType,
			"description": h.Description,
			"output":      output,
			"queue_depth": queueDepth,
		})
}

// buildTeammateAttachment renders the system-prompt block for one
// turn's worth of completed teammates. Empty completions list ⇒ "".
func buildTeammateAttachment(completions []TeammateCompletion) string {
	if len(completions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<teammate-completions>\n")
	b.WriteString("These background sub-agents finished while you were busy. ")
	b.WriteString("Their final outputs are below — fold them into your reasoning ")
	b.WriteString("as if they had just answered.\n\n")
	for _, c := range completions {
		b.WriteString("### ")
		b.WriteString(c.Handle.ID)
		if c.Handle.AgentType != "" {
			b.WriteString(" (")
			b.WriteString(c.Handle.AgentType)
			b.WriteString(")")
		}
		if c.Handle.Description != "" {
			b.WriteString(" — ")
			b.WriteString(c.Handle.Description)
		}
		b.WriteByte('\n')
		if c.Err != nil {
			b.WriteString("[failed: ")
			b.WriteString(c.Err.Error())
			b.WriteString("]\n")
		} else if c.Output == "" {
			b.WriteString("(no output)\n")
		} else {
			b.WriteString(strings.TrimSpace(c.Output))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString("</teammate-completions>")
	return b.String()
}
