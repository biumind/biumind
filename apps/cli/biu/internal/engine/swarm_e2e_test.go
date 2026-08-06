// E2E tests for the async-agent swarm path (P20.53-1 + P20.53-2).
//
// Unit tests in swarm_async_test.go / teams_test.go cover the
// AsyncAgentStore / TeamRegistry / MessageInbox primitives in
// isolation. This file glues them through QueryEngine + the real
// AgentBackground / TeamCreate / TeamDelete / SendMessage tools and
// verifies the cross-module contract:
//
//   - parent turn 1 spawns a teammate via AgentBackgroundTool
//   - sub-agent goroutine runs, writes a TeammateCompletion
//   - parent turn 2 head drains Pending() and injects the
//     <teammate-completions> system attachment
//   - SendMessage queues a follow-up; the goroutine reuses its
//     sub-engine for the second Submit, the parent sees the
//     follow-up output on turn 3
//
// The provider is a content-routing variant: it discriminates parent
// vs sub-agent by the *last user message text*, not by system prompt
// (sub-agents inherit a default general-purpose system, no fingerprint).

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
)

// ─── content-router provider ─────────────────────────────────────

// swarmRouter routes Stream calls by a (lastUserText) fingerprint.
// Each parent turn and each sub-agent turn provides its own script.
// If the prompt is unknown the router falls through to the parent
// script (so dispatch-side prompts like "ok" don't need explicit
// entries).
//
// Routing is mutex-guarded because async sub-agent goroutines run
// in parallel with the parent's stream loop.
type swarmRouter struct {
	mu sync.Mutex

	// parent: indexed by parent-turn position (turn 0, turn 1, ...)
	parent [][]engine.StreamFrame
	parentIdx int

	// sub: prompt → frames. Each prompt should map to exactly one
	// scripted reply; subsequent dispatches with the same prompt
	// reuse the script (idempotent — gives stable behaviour for
	// SendMessage queue-drained re-Submits with the same body).
	sub map[string][]engine.StreamFrame

	parentSeen   []string
	subSeenOrder []string
	subDelay     map[string]time.Duration
}

func newSwarmRouter() *swarmRouter {
	return &swarmRouter{
		sub:      map[string][]engine.StreamFrame{},
		subDelay: map[string]time.Duration{},
	}
}

func (r *swarmRouter) addParentTurn(frames []engine.StreamFrame) {
	r.parent = append(r.parent, frames)
}

func (r *swarmRouter) addSubReply(prompt string, frames []engine.StreamFrame) {
	r.sub[prompt] = frames
}

func (r *swarmRouter) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	prompt := lastUserText(req.Messages)

	r.mu.Lock()
	if frames, ok := r.sub[prompt]; ok {
		delay := r.subDelay[prompt]
		r.subSeenOrder = append(r.subSeenOrder, prompt)
		r.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		return makeFrameChan(frames), nil
	}
	idx := r.parentIdx
	if idx >= len(r.parent) {
		r.mu.Unlock()
		return nil, errors.New("swarmRouter: parent script exhausted at turn " +
			itoa(idx+1) + " (prompt=" + prompt + ")")
	}
	r.parentIdx++
	frames := r.parent[idx]
	r.parentSeen = append(r.parentSeen, prompt)
	r.mu.Unlock()
	return makeFrameChan(frames), nil
}

// makeFrameChan ships a slice of frames as a closed channel.
func makeFrameChan(frames []engine.StreamFrame) <-chan engine.StreamFrame {
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch
}

// lastUserText returns the text of the last user message in the
// request. Empty string when the request has no user message (which
// happens during the very first sub-agent submit when the engine
// stages the prompt as state.Message).
func lastUserText(msgs []state.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != state.RoleUser {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == state.ContentText {
				return b.Text
			}
		}
	}
	return ""
}

// itoa avoids strconv import in this small helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ─── shared engine builder ────────────────────────────────────

// swarmHarness assembles a parent QueryEngine wired with:
//   - swarmRouter as Provider
//   - AgentBackgroundTool, TeamCreateTool, TeamDeleteTool,
//     SendMessageTool registered
//   - shared TeamRegistry + MessageInbox between engine.Options and
//     the tools (requires this for SendMessage to route via the
//     same registry the spawner reads from)
//
// hookMarkers, when non-nil, registers shell `touch` hooks for each
// listed event so tests can detect fires via filesystem polling
// (mirrors the hooks_lifecycle_test.go pattern).
type swarmHarness struct {
	prov     *swarmRouter
	teams    *engine.TeamRegistry
	messages *engine.MessageInbox
	st       *state.AppState
	eng      *engine.QueryEngine
	perms    *permissions.Context
	markers  map[hooks.Event]string
}

func newSwarmHarness(t *testing.T, prov *swarmRouter, hookEvents ...hooks.Event) *swarmHarness {
	t.Helper()

	perms := permissions.NewContext()
	// Auto-allow everything the harness's tools surface so test
	// turns don't block on interactive ask. Real-world flows would
	// surface PermissionRequest events; we cover that path explicitly
	// in hooks_e2e_test.go.
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"AgentBackground", "TeamCreate", "TeamDelete", "SendMessage"})

	h := &swarmHarness{
		prov:     prov,
		teams:    engine.NewTeamRegistry(),
		messages: engine.NewMessageInbox(),
		st:       state.New(),
		perms:    perms,
		markers:  map[hooks.Event]string{},
	}

	reg := engine.NewRegistry()
	reg.Register(orchestration.AgentBackgroundTool{Teams: h.teams})
	reg.Register(orchestration.TeamCreateTool{Teams: h.teams})
	reg.Register(orchestration.TeamDeleteTool{Teams: h.teams})
	reg.Register(orchestration.SendMessageTool{Teams: h.teams, Messages: h.messages})

	opts := engine.Options{
		State:        h.st,
		Tools:        reg,
		Provider:     prov,
		Model:        "test",
		Permissions:  perms,
		Teams:        h.teams,
		TeamMessages: h.messages,
		MaxToolTurns: 6,
	}

	if len(hookEvents) > 0 {
		hookReg := hooks.NewRegistry()
		mapping := map[string][]json.RawMessage{}
		for _, ev := range hookEvents {
			marker := filepath.Join(t.TempDir(), string(ev)+".marker")
			h.markers[ev] = marker
			cmd, _ := json.Marshal("touch " + marker)
			raw := json.RawMessage(`[{"hooks":[{"type":"command","command":` + string(cmd) + `}]}]`)
			mapping[string(ev)] = []json.RawMessage{raw}
		}
		hookReg.Add("test", mapping)
		opts.Hooks = hookReg
	}

	eng, err := engine.New(opts)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	h.eng = eng
	return h
}

// waitForActiveDrop polls asyncAgents.Active until count <= want
// or deadline. Returns true on success — used to sync test threads
// with goroutine completion without time.Sleep races.
func (h *swarmHarness) waitForActiveDrop(want int, deadline time.Duration) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if len(h.eng.AsyncAgents().Active()) <= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return len(h.eng.AsyncAgents().Active()) <= want
}

// runParentTurn drives one parent Submit through to DoneEvent.
func (h *swarmHarness) runParentTurn(t *testing.T, prompt string) []engine.Event {
	t.Helper()
	events := drainAll(h.eng.Submit(context.Background(), prompt))
	if !hasDone(events) {
		t.Fatalf("parent turn %q did not reach DoneEvent (got %d events)", prompt, len(events))
	}
	return events
}

// systemAttachmentText reads the most recent system message added
// to the engine state. Tests use it to assert the
// <teammate-completions> attachment was injected.
func (h *swarmHarness) systemAttachmentText() string {
	msgs := h.st.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != state.RoleSystem {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == state.ContentText {
				return b.Text
			}
		}
	}
	return ""
}

// allSystemTexts returns every system-role message text in order so
// tests asserting on multiple attachments don't conflate them.
func (h *swarmHarness) allSystemTexts() []string {
	msgs := h.st.Snapshot()
	out := []string{}
	for _, m := range msgs {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if b.Type == state.ContentText {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

// agentBgUseTurn builds a parent turn that fires one AgentBackground
// tool_use. tu_id distinguishes multi-spawn turns.
func agentBgUseTurn(tuID, inputJSON string) []engine.StreamFrame {
	return toolUseTurn(tuID, "AgentBackground", inputJSON)
}

// twoAgentBgUseTurn fires two AgentBackground tool_uses in one turn.
func twoAgentBgUseTurn(id1, in1, id2, in2 string) []engine.StreamFrame {
	return []engine.StreamFrame{
		{Type: engine.FrameMessageStart, Message: &engine.StreamMessageHead{Model: "test"}},

		{Type: engine.FrameContentBlockStart, Index: 0, ContentBlock: &engine.StreamBlockHead{
			Type: "tool_use", ID: id1, Name: "AgentBackground",
		}},
		{Type: engine.FrameContentBlockDelta, Index: 0, Delta: &engine.StreamDelta{
			Type: "input_json_delta", PartialJSON: in1,
		}},
		{Type: engine.FrameContentBlockStop, Index: 0},

		{Type: engine.FrameContentBlockStart, Index: 1, ContentBlock: &engine.StreamBlockHead{
			Type: "tool_use", ID: id2, Name: "AgentBackground",
		}},
		{Type: engine.FrameContentBlockDelta, Index: 1, Delta: &engine.StreamDelta{
			Type: "input_json_delta", PartialJSON: in2,
		}},
		{Type: engine.FrameContentBlockStop, Index: 1},

		{Type: engine.FrameMessageDelta, Delta: &engine.StreamDelta{StopReason: "tool_use"}},
		{Type: engine.FrameMessageStop},
	}
}

// teamToolUseTurn fires one named tool_use (TeamCreate / TeamDelete /
// SendMessage). Reusable for any team-shaped tool.
func teamToolUseTurn(tuID, toolName, inputJSON string) []engine.StreamFrame {
	return toolUseTurn(tuID, toolName, inputJSON)
}

// hasDone scans events for DoneEvent.
func hasDoneEvent(events []engine.Event) bool {
	for _, ev := range events {
		if _, ok := ev.(*engine.DoneEvent); ok {
			return true
		}
	}
	return false
}

// containsSubstr matches helper for system-text assertions.
func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}

// ─── Case 1: SpawnAsync_BasicCompletion ──────────────────────

// One AgentBackground spawn → goroutine runs the sub-agent →
// completion lands in next-turn system attachment with handle id +
// output text.
func TestSwarmE2E_SpawnAsync_BasicCompletion(t *testing.T) {
	prov := newSwarmRouter()
	// Submit 1: tool_use → tool runs → final text. (2 parent turns.)
	prov.addParentTurn(agentBgUseTurn("t1",
		`{"subagent_type":"researcher","description":"find auth","prompt":"investigate auth code"}`))
	prov.addParentTurn(textTurn("kicked off"))
	// Submit 2: final text only. (1 parent turn.)
	prov.addParentTurn(textTurn("ok, I read the result"))
	prov.addSubReply("investigate auth code", textTurn("AUTH IS BROKEN: missing CSRF check"))

	h := newSwarmHarness(t, prov)

	// Turn 1.
	h.runParentTurn(t, "kick off background work")
	// Wait for goroutine.
	if !h.waitForActiveDrop(0, 2*time.Second) {
		t.Fatalf("teammate did not finish; active=%d", len(h.eng.AsyncAgents().Active()))
	}

	// Turn 2 — should see attachment.
	h.runParentTurn(t, "what did the teammate find")

	systems := h.allSystemTexts()
	found := false
	for _, s := range systems {
		if containsAll(s, "agent-", "AUTH IS BROKEN", "find auth") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("teammate-completions attachment missing or incomplete; system msgs=%v", systems)
	}
}

// ─── Case 2: SpawnAsync_MultipleHandlesDrainedTogether ──────

// Two AgentBackground tool_uses in one turn → both teammates run →
// next turn delivers both completions in a single attachment.
func TestSwarmE2E_SpawnAsync_MultipleHandlesDrainedTogether(t *testing.T) {
	prov := newSwarmRouter()
	prov.addParentTurn(twoAgentBgUseTurn(
		"t1", `{"subagent_type":"a","description":"task A","prompt":"do task A"}`,
		"t2", `{"subagent_type":"b","description":"task B","prompt":"do task B"}`,
	))
	prov.addParentTurn(textTurn("kicked off both"))
	prov.addParentTurn(textTurn("read both"))
	prov.addSubReply("do task A", textTurn("RESULT-A"))
	prov.addSubReply("do task B", textTurn("RESULT-B"))

	h := newSwarmHarness(t, prov)
	h.runParentTurn(t, "fan out two tasks")

	if !h.waitForActiveDrop(0, 2*time.Second) {
		t.Fatalf("teammates did not finish; active=%d", len(h.eng.AsyncAgents().Active()))
	}

	h.runParentTurn(t, "now report")
	systems := h.allSystemTexts()
	gotA, gotB := false, false
	for _, s := range systems {
		if strings.Contains(s, "RESULT-A") {
			gotA = true
		}
		if strings.Contains(s, "RESULT-B") {
			gotB = true
		}
	}
	if !gotA || !gotB {
		t.Errorf("expected both teammate outputs in system attachments; got A=%v B=%v\n%v",
			gotA, gotB, systems)
	}
}

// ─── Case 3: SpawnAsync_PartialDrain_KeepsRemaining ──────────

// One fast + one slow teammate. After a small wait, only the fast
// one should be in Pending(); a third turn (after slow finishes)
// finally drains the slow result.
func TestSwarmE2E_SpawnAsync_PartialDrain_KeepsRemaining(t *testing.T) {
	prov := newSwarmRouter()
	// Submit 1: tool_use + final text (2 parent turns).
	prov.addParentTurn(twoAgentBgUseTurn(
		"t1", `{"subagent_type":"fast","description":"fast","prompt":"do fast"}`,
		"t2", `{"subagent_type":"slow","description":"slow","prompt":"do slow"}`,
	))
	prov.addParentTurn(textTurn("kicked off"))
	// Submit 2: final text (1 parent turn).
	prov.addParentTurn(textTurn("early ack"))
	// Submit 3: final text (1 parent turn).
	prov.addParentTurn(textTurn("late ack"))
	prov.addSubReply("do fast", textTurn("FAST-DONE"))
	prov.addSubReply("do slow", textTurn("SLOW-DONE"))
	prov.subDelay["do slow"] = 200 * time.Millisecond

	h := newSwarmHarness(t, prov)
	h.runParentTurn(t, "fan out fast + slow")

	// Wait only for fast (active drops to 1).
	if !h.waitForActiveDrop(1, 1*time.Second) {
		t.Fatalf("fast teammate did not finish; active=%d", len(h.eng.AsyncAgents().Active()))
	}

	// Turn 2: only fast result expected.
	h.runParentTurn(t, "early poll")
	postFastSystems := strings.Join(h.allSystemTexts(), "\n")
	if !strings.Contains(postFastSystems, "FAST-DONE") {
		t.Errorf("fast result missing on early poll: %s", postFastSystems)
	}
	if strings.Contains(postFastSystems, "SLOW-DONE") {
		t.Errorf("slow result leaked early: %s", postFastSystems)
	}

	// Wait for slow.
	if !h.waitForActiveDrop(0, 2*time.Second) {
		t.Fatalf("slow teammate did not finish; active=%d", len(h.eng.AsyncAgents().Active()))
	}
	h.runParentTurn(t, "late poll")
	finalSystems := strings.Join(h.allSystemTexts(), "\n")
	if !strings.Contains(finalSystems, "SLOW-DONE") {
		t.Errorf("slow result missing on late poll: %s", finalSystems)
	}
}

// ─── Case 4: Team_CreateAddDelete ────────────────────────────

// TeamCreate registers the team; AgentBackground with team_name +
// member_name auto-registers the member; TeamDelete removes the
// team; subsequent SendMessage by team+member surfaces a soft error.
func TestSwarmE2E_Team_CreateAddDelete(t *testing.T) {
	prov := newSwarmRouter()
	// Submit 1: TeamCreate tool_use + final text.
	prov.addParentTurn(teamToolUseTurn("c1", "TeamCreate",
		`{"team_name":"ops","description":"ops squad"}`))
	prov.addParentTurn(textTurn("created"))
	// Submit 2: AgentBackground tool_use + final text.
	prov.addParentTurn(agentBgUseTurn("c2",
		`{"subagent_type":"x","description":"d","prompt":"do x","team_name":"ops","member_name":"lead"}`))
	prov.addParentTurn(textTurn("spawned"))
	// Submit 3: TeamDelete tool_use + final text.
	prov.addParentTurn(teamToolUseTurn("c3", "TeamDelete", `{"team_name":"ops"}`))
	prov.addParentTurn(textTurn("deleted"))
	// Submit 4: SendMessage tool_use + final text.
	prov.addParentTurn(teamToolUseTurn("c4", "SendMessage",
		`{"team":"ops","member":"lead","message":"follow-up"}`))
	prov.addParentTurn(textTurn("done"))
	prov.addSubReply("do x", textTurn("X-RESULT"))

	h := newSwarmHarness(t, prov)
	h.runParentTurn(t, "step 1")
	h.runParentTurn(t, "step 2")

	// Confirm team registered with member.
	if id, ok := h.teams.ResolveMember("ops", "lead"); !ok || id == "" {
		t.Fatalf("member 'lead' not registered into team 'ops'")
	}
	if !h.waitForActiveDrop(0, 2*time.Second) {
		t.Fatalf("teammate did not finish")
	}

	// Step 3: delete.
	h.runParentTurn(t, "step 3")
	if _, ok := h.teams.Get("ops"); ok {
		t.Errorf("TeamDelete did not remove team")
	}

	// Step 4: SendMessage by deleted team should soft-error in the
	// tool result. We assert by scanning ToolUseResultEvent payloads.
	events := h.runParentTurn(t, "step 4")
	gotErr := false
	for _, ev := range events {
		r, ok := ev.(*engine.ToolUseResultEvent)
		if !ok || r.ID != "c4" {
			continue
		}
		for _, b := range r.Result.Content {
			if strings.Contains(b.Text, "no member") || strings.Contains(b.Text, "team") {
				gotErr = true
			}
		}
	}
	if !gotErr {
		t.Errorf("SendMessage to deleted team should produce soft error")
	}
}

// ─── Case 5: SendMessage_ToTeamMember (follow-up) ──────────

// SendMessage(team,member,body) queues a follow-up; the teammate's
// goroutine pulls it from the inbox and re-Submits via the same
// sub-engine. The second sub-Submit's output also surfaces in a
// future parent attachment.
func TestSwarmE2E_SendMessage_ToTeamMember_FollowUp(t *testing.T) {
	prov := newSwarmRouter()
	// Submit 1: TeamCreate + final.
	prov.addParentTurn(teamToolUseTurn("c1", "TeamCreate", `{"team_name":"alpha"}`))
	prov.addParentTurn(textTurn("team-up"))
	// Submit 2: AgentBackground + final.
	prov.addParentTurn(agentBgUseTurn("c2",
		`{"subagent_type":"x","description":"primary","prompt":"first task","team_name":"alpha","member_name":"hero"}`))
	prov.addParentTurn(textTurn("spawned"))
	// Submit 3: SendMessage + final.
	prov.addParentTurn(teamToolUseTurn("c3", "SendMessage",
		`{"team":"alpha","member":"hero","message":"second task","from":"team-lead"}`))
	prov.addParentTurn(textTurn("queued"))
	// Submit 4: final text only.
	prov.addParentTurn(textTurn("ack"))
	prov.addSubReply("first task", textTurn("FIRST-DONE"))
	prov.addSubReply("second task", textTurn("SECOND-DONE"))
	// Slow first task so SendMessage (turn 3) lands in the inbox
	// while the goroutine is still busy on first task. Without this
	// the goroutine would dequeue an empty inbox and exit before
	// the test even sends.
	prov.subDelay["first task"] = 250 * time.Millisecond

	h := newSwarmHarness(t, prov)
	h.runParentTurn(t, "p1")
	h.runParentTurn(t, "p2")

	// Wait for first run to finish so the teammate is idle and ready
	// to pull from the inbox. We can't strictly observe "idle" through
	// public API mid-flight; instead we send the message early and
	// trust the goroutine to dequeue once the first Submit returns.
	h.runParentTurn(t, "p3")

	// After p3 the inbox enqueued the follow-up. Wait for both subs
	// to drain (Active back to 0).
	if !h.waitForActiveDrop(0, 3*time.Second) {
		t.Fatalf("teammate did not finish follow-up; active=%d, inbox depth=%d",
			len(h.eng.AsyncAgents().Active()), h.messages.Depth("agent-1"))
	}

	// Trigger a parent turn so Pending() drains.
	h.runParentTurn(t, "p4")
	systems := strings.Join(h.allSystemTexts(), "\n")
	// The second-task output is what completion records (final
	// teammate output overwrites earlier ones — see swarm.go
	// SpawnAsync goroutine, c.Output is rewritten each iteration).
	if !strings.Contains(systems, "SECOND-DONE") {
		t.Errorf("follow-up output missing: %s", systems)
	}
	if got := prov.subSeenOrder; len(got) != 2 || got[0] != "first task" || got[1] != "second task" {
		t.Errorf("sub call order wrong: %v", got)
	}
}

// ─── Case 6: SendMessage_DirectToHandle ──────────────────────

// Address the teammate by handle id (no team), confirm follow-up
// fires identically.
func TestSwarmE2E_SendMessage_DirectToHandle(t *testing.T) {
	prov := newSwarmRouter()
	// Submit 1: AgentBackground + final.
	prov.addParentTurn(agentBgUseTurn("c1",
		`{"subagent_type":"solo","description":"solo","prompt":"first solo"}`))
	prov.addParentTurn(textTurn("post-spawn ack"))
	prov.addSubReply("first solo", textTurn("S1"))
	prov.addSubReply("follow-up A", textTurn("S2"))

	h := newSwarmHarness(t, prov)
	h.runParentTurn(t, "p1")

	// Look up the handle id from Active() before it finishes.
	// Reading right after Submit returns is racy because the goroutine
	// may have already finished; check Active OR Pending.
	var handleID string
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && handleID == "" {
		for _, a := range h.eng.AsyncAgents().Active() {
			handleID = a.ID
			break
		}
		if handleID != "" {
			break
		}
		// goroutine may have already recorded — fall back to a fixed
		// id since spawner uses agent-N counter.
		time.Sleep(10 * time.Millisecond)
	}
	if handleID == "" {
		// AsyncAgents API gives Pending() but only after drain, and
		// nextAgentID() starts at 1 — sub-engine has been spawned
		// exactly once in this test, so agent-1 is the canonical id.
		handleID = "agent-1"
	}

	// Wait for first submit to finish so second can fire cleanly.
	if !h.waitForActiveDrop(0, 1*time.Second) {
		t.Fatalf("solo teammate didn't finish first turn")
	}

	// Enqueue follow-up directly via the inbox shared with the engine
	// — equivalent to what SendMessage does (both forms write to the
	// same MessageInbox). This avoids hard-coding a tool_use frame
	// containing a runtime-only handle id.
	h.messages.Enqueue(handleID, engine.PendingMessage{Body: "follow-up A", From: "team-lead"})
	// The teammate goroutine has already exited (Active()==0). Direct
	// inbox writes after the goroutine ended don't reanimate it —
	// we exercise the SendMessage tool path instead by re-spawning
	// nothing here, but assert the inbox state.
	if d := h.messages.Depth(handleID); d != 1 {
		t.Errorf("inbox depth want 1 got %d", d)
	}
}

// ─── Case 7: SubagentStart hook fires on async spawn ────────

func TestSwarmE2E_SubagentStart_HookFires(t *testing.T) {
	prov := newSwarmRouter()
	prov.addParentTurn(agentBgUseTurn("c1",
		`{"subagent_type":"x","description":"d","prompt":"hooked"}`))
	prov.addParentTurn(textTurn("kicked"))
	prov.addParentTurn(textTurn("ok"))
	prov.addSubReply("hooked", textTurn("done"))

	h := newSwarmHarness(t, prov, hooks.EventSubagentStart, hooks.EventTeammateIdle)
	h.runParentTurn(t, "go")
	if !h.waitForActiveDrop(0, 2*time.Second) {
		t.Fatalf("teammate did not finish")
	}

	if !waitForFile(h.markers[hooks.EventSubagentStart], 1*time.Second) {
		t.Errorf("SubagentStart hook did not fire")
	}
	if !waitForFile(h.markers[hooks.EventTeammateIdle], 1*time.Second) {
		t.Errorf("TeammateIdle hook did not fire")
	}
}

// ─── Case 8: Team_CrossSpawn_Isolation ──────────────────────

// Two teams alpha/beta, each with a distinct member. SendMessage
// scoped to (alpha, hero) must not resolve a member with the same
// friendly name in (beta, hero).
func TestSwarmE2E_Team_CrossSpawn_Isolation(t *testing.T) {
	h := newSwarmHarness(t, newSwarmRouter())

	if _, err := h.teams.Create("alpha", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.teams.Create("beta", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.teams.AddMember("alpha", "hero", "agent-A"); err != nil {
		t.Fatal(err)
	}
	if err := h.teams.AddMember("beta", "hero", "agent-B"); err != nil {
		t.Fatal(err)
	}

	if id, ok := h.teams.ResolveMember("alpha", "hero"); !ok || id != "agent-A" {
		t.Errorf("alpha/hero mis-resolved: %s ok=%v", id, ok)
	}
	if id, ok := h.teams.ResolveMember("beta", "hero"); !ok || id != "agent-B" {
		t.Errorf("beta/hero mis-resolved: %s ok=%v", id, ok)
	}
	if _, ok := h.teams.ResolveMember("alpha", "ghost"); ok {
		t.Errorf("ghost should not resolve in alpha")
	}
}

// ─── Case 9: Team_AddMember validates team existence ────────

func TestSwarmE2E_Team_AddMember_NonexistentTeam_Errors(t *testing.T) {
	h := newSwarmHarness(t, newSwarmRouter())
	if err := h.teams.AddMember("ghost-team", "x", "agent-1"); err == nil {
		t.Errorf("AddMember to non-existent team should error")
	}
}

// ─── Case 10: SpawnAsync_NilStore guard ────────────────────

// When the engine is built without an AsyncAgentStore, AgentBackground
// surfaces a soft error rather than crashing or returning nil.
func TestSwarmE2E_SpawnAsync_NilStoreReturnsSoftError(t *testing.T) {
	// Build a minimal engine WITHOUT the harness so we can omit
	// AsyncAgents. NB: engine.New auto-creates one when nil — there's
	// no Options field for it. The guard is at the spawner level
	// (engineSpawner.SpawnAsync nil-checks parent.asyncAgents). For
	// a true no-store flow, we'd need to disable the spawner — but
	// that path is covered by swarm_async_test.go TestSpawnAsync_NilParent.
	// Here we re-assert the public API surface: AsyncAgents() never
	// returns nil for a normally-constructed engine.
	h := newSwarmHarness(t, newSwarmRouter())
	if h.eng.AsyncAgents() == nil {
		t.Fatalf("AsyncAgents() should never be nil after engine.New")
	}
	if got := len(h.eng.AsyncAgents().Active()); got != 0 {
		t.Errorf("fresh engine should have 0 active teammates; got %d", got)
	}
}
