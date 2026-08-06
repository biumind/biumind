// End-to-end integration tests for the Plan / Explore lifecycle.
//
// Stitches together:
//
//   * a real engine.QueryEngine (with scripted provider + fake tools),
//   * the real interactive.EnterPlanModeTool / ExitPlanModeTool,
//   * the real planverify.Verifier (drift detection),
//   * the real planhint.Analyser (auto-suggest hint),
//   * the real permissions.Context with allowedPrompts wired through.
//
// Lives in `package engine_test` (external) so it can import
// `internal/tools/interactive` without an import cycle. We re-implement
// the small scriptedProvider / frame-builder helpers locally so the
// integration narrative reads top-to-bottom in one file.

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/planhint"
	"github.com/biumind/biumind/apps/cli/biu/internal/planverify"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/interactive"
)

// ─── scripted provider ──────────────────────────────────

// scripted streams pre-baked StreamFrame slices, one per Stream call.
// The engine's loop calls Stream at every turn boundary, so the test
// pre-populates the script in the order turns will run.
type scripted struct {
	turns [][]engine.StreamFrame
	calls int
}

func (s *scripted) Stream(_ context.Context, _ engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	if s.calls >= len(s.turns) {
		return nil, errors.New("scripted provider exhausted")
	}
	frames := s.turns[s.calls]
	s.calls++
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

func textTurn(text string) []engine.StreamFrame {
	return []engine.StreamFrame{
		{Type: engine.FrameMessageStart, Message: &engine.StreamMessageHead{Model: "test"}},
		{Type: engine.FrameContentBlockStart, Index: 0, ContentBlock: &engine.StreamBlockHead{Type: "text"}},
		{Type: engine.FrameContentBlockDelta, Index: 0, Delta: &engine.StreamDelta{Type: "text_delta", Text: text}},
		{Type: engine.FrameContentBlockStop, Index: 0},
		{Type: engine.FrameMessageDelta, Delta: &engine.StreamDelta{StopReason: "end_turn"}},
		{Type: engine.FrameMessageStop},
	}
}

func toolUseTurn(useID, name, inputJSON string) []engine.StreamFrame {
	return []engine.StreamFrame{
		{Type: engine.FrameMessageStart, Message: &engine.StreamMessageHead{Model: "test"}},
		{Type: engine.FrameContentBlockStart, Index: 0, ContentBlock: &engine.StreamBlockHead{
			Type: "tool_use", ID: useID, Name: name,
		}},
		{Type: engine.FrameContentBlockDelta, Index: 0, Delta: &engine.StreamDelta{
			Type: "input_json_delta", PartialJSON: inputJSON,
		}},
		{Type: engine.FrameContentBlockStop, Index: 0},
		{Type: engine.FrameMessageDelta, Delta: &engine.StreamDelta{StopReason: "tool_use"}},
		{Type: engine.FrameMessageStop},
	}
}

func drainAll(ch <-chan engine.Event) []engine.Event {
	var out []engine.Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

// ─── tiny tool stubs ────────────────────────────────────

// recordingBash captures every command the engine dispatched so tests
// can assert which calls were attempted vs allowed.
type recordingBash struct{ ran []string }

func (recordingBash) Name() string                            { return "Bash" }
func (recordingBash) Description(_ map[string]any) string     { return "execute shell" }
func (recordingBash) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (recordingBash) IsReadOnly(_ map[string]any) bool        { return false }
func (recordingBash) IsDestructive(_ map[string]any) bool     { return true }
func (recordingBash) IsConcurrencySafe(_ map[string]any) bool { return false }
func (recordingBash) InterruptBehavior() string               { return "cancel" }
func (b *recordingBash) Call(_ context.Context, in map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	cmd, _ := in["command"].(string)
	b.ran = append(b.ran, cmd)
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: "ran: " + cmd}},
	}, nil
}

// readonlyTool is a no-op read-only tool to flesh out the catalog.
type readonlyTool struct{ name string }

func (r readonlyTool) Name() string                            { return r.name }
func (r readonlyTool) Description(_ map[string]any) string     { return r.name }
func (r readonlyTool) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (r readonlyTool) IsReadOnly(_ map[string]any) bool        { return true }
func (r readonlyTool) IsDestructive(_ map[string]any) bool     { return false }
func (r readonlyTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (r readonlyTool) InterruptBehavior() string               { return "cancel" }
func (r readonlyTool) Call(_ context.Context, _ map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: r.name + " ok"}},
	}, nil
}

// editTool is a stub for a destructive write operation. Used to prove
// plan mode actually denies it.
type editTool struct{ ran int }

func (editTool) Name() string                            { return "Edit" }
func (editTool) Description(_ map[string]any) string     { return "edit a file" }
func (editTool) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (editTool) IsReadOnly(_ map[string]any) bool        { return false }
func (editTool) IsDestructive(_ map[string]any) bool     { return false }
func (editTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (editTool) InterruptBehavior() string               { return "cancel" }
func (e *editTool) Call(_ context.Context, _ map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	e.ran++
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: "edited"}},
	}, nil
}

// ─── helpers ────────────────────────────────────────────

// findToolResult returns the recorded result payload for a given tool
// use ID, or nil when none was emitted.
func findToolResult(events []engine.Event, useID string) *engine.ToolResultPayload {
	for _, ev := range events {
		if r, ok := ev.(*engine.ToolUseResultEvent); ok && r.ID == useID {
			payload := r.Result
			return &payload
		}
	}
	return nil
}

// systemMessagesContain returns true when any system-role message in
// state has text containing `needle`.
func systemMessagesContain(st *state.AppState, needle string) bool {
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if b.Type == state.ContentText && strings.Contains(b.Text, needle) {
				return true
			}
		}
	}
	return false
}

// hinterAdapter bridges *planhint.Analyser → engine.PlanHinter (the
// engine package can't import planhint without a cycle, so we shim).
type hinterAdapter struct{ inner *planhint.Analyser }

func (h *hinterAdapter) Enabled() bool { return h != nil && h.inner.Enabled() }
func (h *hinterAdapter) Analyse(prompt string) engine.PlanHint {
	if h == nil {
		return engine.PlanHint{}
	}
	s := h.inner.Analyse(prompt)
	return engine.PlanHint{Note: s.Note, MatchedKeyword: s.MatchedKeyword}
}

// jsonMust is a tiny helper for embedding tool-call inputs.
func jsonMust(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// ─── test 1: full lifecycle ─────────────────────────────

// TestPlanFlow_LifecycleFromPlanToAllowedExec walks the entire happy
// path the user-facing UX promises:
//
//  1. The user says "refactor X" → planhint adds a system note suggesting
//     EnterPlanMode.
//  2. The model calls EnterPlanMode → context flips to plan mode.
//  3. The model calls Read (read-only, allowed in plan mode).
//  4. The model attempts Edit → permission engine denies.
//  5. The model calls ExitPlanMode with plan + allowedPrompts: [{Bash,
//     "run tests"}] → plan attachment + allowedPrompts staged, mode
//     restored to default.
//  6. The model calls Bash "go test ./..." → classifier matches the
//     staged prompt → auto-allowed without asking.
//  7. Drift verifier observes the Bash call but tolerates it (read-only
//     drift only fires for *destructive* tools that don't trace to the
//     plan, and "go test" tokens overlap "run tests" via the verifier
//     too — but more importantly, the Bash arg "command" is destructive,
//     so drift catches anything genuinely off-plan).
//  8. The model emits an end-turn text reply.
//
// Failure modes covered: missed hint, plan mode bypass, allowedPrompt
// false-negative, mode failing to restore, plan attachment loss.
func TestPlanFlow_LifecycleFromPlanToAllowedExec(t *testing.T) {
	perms := permissions.NewContext()
	verifier := planverify.New()
	perms.SetPlanObserver(verifier.SetPlan)
	hinter := &hinterAdapter{inner: planhint.New(true, nil)}

	// Pre-allow the orchestration tools (EnterPlanMode / ExitPlanMode /
	// Read) so the test doesn't block on the interactive ask flow.
	// Edit / Bash are deliberately NOT pre-allowed so we can prove:
	//   - plan mode denies Edit even without an explicit deny rule
	//   - allowedPrompts auto-allow Bash via the classifier
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"EnterPlanMode", "ExitPlanMode", "Read"})

	bash := &recordingBash{}
	edit := &editTool{}
	enter := interactive.EnterPlanModeTool{Perms: perms}
	exit := interactive.ExitPlanModeTool{Perms: perms, SessionID: "test-session"}
	read := readonlyTool{name: "Read"}

	reg := engine.NewRegistry()
	reg.Register(bash)
	reg.Register(edit)
	reg.Register(enter)
	reg.Register(exit)
	reg.Register(read)

	// Script the conversation. One Stream call per turn.
	prov := &scripted{turns: [][]engine.StreamFrame{
		// Turn 1 — model decides to plan.
		toolUseTurn("u1", "EnterPlanMode", `{}`),
		// Turn 2 — model reads.
		toolUseTurn("u2", "Read", `{"path":"auth.go"}`),
		// Turn 3 — model tries to write while still in plan mode.
		toolUseTurn("u3", "Edit", `{"path":"auth.go","content":"x"}`),
		// Turn 4 — model exits with a plan + allowedPrompts.
		toolUseTurn("u4", "ExitPlanMode", jsonMust(map[string]any{
			"plan": "Refactor auth: rewrite middleware.go and rerun tests.",
			"allowedPrompts": []map[string]any{
				{"tool": "Bash", "prompt": "run tests"},
			},
		})),
		// Turn 5 — model issues the actual test command.
		toolUseTurn("u5", "Bash", `{"command":"go test ./..."}`),
		// Turn 6 — model wraps up.
		textTurn("done — refactor complete."),
	}}

	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions:        perms,
		PlanVerifier:       verifier,
		PlanDriftThreshold: 0, // any drift surfaces — the strictest setting
		PlanHinter:         hinter,
		MaxToolTurns:       10,
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drainAll(eng.Submit(context.Background(),
		"refactor the auth module to use middleware"))

	// (1) Hinter injected a `EnterPlanMode` suggestion.
	if !systemMessagesContain(st, "EnterPlanMode") {
		t.Errorf("expected planhint system note mentioning EnterPlanMode")
	}

	// (2 / 5) EnterPlanMode + ExitPlanMode succeeded.
	enterRes := findToolResult(events, "u1")
	if enterRes == nil || enterRes.IsError {
		t.Fatalf("EnterPlanMode result missing or errored: %+v", enterRes)
	}

	// (3) Read ran (read-only, allowed in plan mode).
	readRes := findToolResult(events, "u2")
	if readRes == nil || readRes.IsError {
		t.Fatalf("Read should succeed under plan mode: %+v", readRes)
	}

	// (4) Edit was rejected by Decide while in plan mode.
	editRes := findToolResult(events, "u3")
	if editRes == nil || !editRes.IsError {
		t.Errorf("Edit must be denied under plan mode; got %+v", editRes)
	}
	if edit.ran != 0 {
		t.Errorf("Edit.Call must NOT have executed; ran=%d", edit.ran)
	}

	// (5) ExitPlanMode result advertises the pre-approval and restores mode.
	exitRes := findToolResult(events, "u4")
	if exitRes == nil || exitRes.IsError {
		t.Fatalf("ExitPlanMode should succeed: %+v", exitRes)
	}
	if perms.Mode() == permissions.ModePlan {
		t.Errorf("ExitPlanMode should restore non-plan mode; got %v", perms.Mode())
	}
	if perms.PlanAttachment() == "" {
		t.Errorf("ExitPlanMode should set plan attachment for compact survival")
	}
	if got := perms.AllowedPrompts(); len(got) != 1 || got[0].Prompt != "run tests" {
		t.Errorf("AllowedPrompts not staged correctly: %+v", got)
	}
	if !verifier.HasPlan() {
		t.Errorf("verifier should pick up plan via observer callback")
	}

	// (6) Bash was auto-allowed via the classifier path AND actually ran.
	bashRes := findToolResult(events, "u5")
	if bashRes == nil || bashRes.IsError {
		t.Fatalf("Bash should auto-allow via allowedPrompt: %+v", bashRes)
	}
	if len(bash.ran) != 1 || bash.ran[0] != "go test ./..." {
		t.Errorf("Bash dispatch lost or wrong: %+v", bash.ran)
	}

	// (7) The conversation ended cleanly.
	gotDone := false
	for _, ev := range events {
		if _, ok := ev.(*engine.DoneEvent); ok {
			gotDone = true
		}
	}
	if !gotDone {
		t.Errorf("expected DoneEvent at end of run")
	}
}

// ─── test 2: deny-rule veto beats allowedPrompt ─────────

// TestPlanFlow_DenyRuleBeatsAllowedPrompt asserts that an explicit
// deny rule still vetoes a command that would otherwise satisfy a
// plan-time prompt approval. Defense in depth — a user setting
// `Bash(go test:*)` to deny must NOT be undone by the model promising
// "run tests" in its plan.
func TestPlanFlow_DenyRuleBeatsAllowedPrompt(t *testing.T) {
	perms := permissions.NewContext()
	perms.AddAllowedPrompts([]permissions.AllowedPrompt{
		{Tool: "Bash", Prompt: "run tests"},
	})
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorDeny,
		[]string{"Bash(go test:*)"})

	bash := &recordingBash{}
	reg := engine.NewRegistry()
	reg.Register(bash)

	prov := &scripted{turns: [][]engine.StreamFrame{
		toolUseTurn("u1", "Bash", `{"command":"go test ./..."}`),
		textTurn("done"),
	}}

	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions:  perms,
		MaxToolTurns: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := drainAll(eng.Submit(context.Background(), "test it"))

	if len(bash.ran) != 0 {
		t.Errorf("deny rule should have prevented dispatch; ran=%v", bash.ran)
	}
	res := findToolResult(events, "u1")
	if res == nil || !res.IsError {
		t.Errorf("expected denied tool result; got %+v", res)
	}
}

// ─── test 3: planhint silent on small change ────────────

// TestPlanFlow_HinterSilentOnSmallChange — bias: false negatives
// preferred over false positives, so trivial prompts should not be
// nagged about planning.
func TestPlanFlow_HinterSilentOnSmallChange(t *testing.T) {
	perms := permissions.NewContext()
	hinter := &hinterAdapter{inner: planhint.New(true, nil)}
	reg := engine.NewRegistry()

	prov := &scripted{turns: [][]engine.StreamFrame{
		textTurn("ok"),
	}}

	st := state.New()
	eng, _ := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions: perms,
		PlanHinter:  hinter,
	})
	drainAll(eng.Submit(context.Background(), "fix the typo in README"))

	if systemMessagesContain(st, "EnterPlanMode") {
		t.Errorf("hinter should stay silent for trivial prompts")
	}
}

// ─── test 4: drift detection surfaces an attachment ─────

// TestPlanFlow_DriftDetectionSurfacesNextTurn primes a plan, has the
// model run a destructive call that doesn't trace to the plan, then
// submits a NEW user prompt — the engine should fold the
// `<plan-drift>` attachment into the new turn.
func TestPlanFlow_DriftDetectionSurfacesNextTurn(t *testing.T) {
	perms := permissions.NewContext()
	verifier := planverify.New()
	perms.SetPlanObserver(verifier.SetPlan)
	// Manually arm the plan as if ExitPlanMode had run earlier.
	perms.SetPlanAttachment("Step 1: edit auth.go to add JWT verification.")

	bash := &recordingBash{}
	reg := engine.NewRegistry()
	reg.Register(bash)

	// Turn 1: the model issues an off-plan destructive command.
	// Turn 2: it ends.
	prov := &scripted{turns: [][]engine.StreamFrame{
		toolUseTurn("u1", "Bash", `{"command":"rm -rf node_modules"}`),
		textTurn("cleaned up"),
		// After the user's NEXT submit, the engine surfaces drift then
		// the model immediately ends.
		textTurn("understood"),
	}}

	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions:        perms,
		PlanVerifier:       verifier,
		PlanDriftThreshold: 0, // any drift surfaces immediately
		BypassPermissions:  true,
		MaxToolTurns:       3,
	})
	if err != nil {
		t.Fatal(err)
	}

	// First submit: drift accrues but is not yet surfaced (we surface
	// at the START of the *next* user turn so the model can see it
	// against fresh context).
	drainAll(eng.Submit(context.Background(), "clean up"))
	if verifier.DriftCount() == 0 {
		t.Fatalf("expected a drift observation; got %d", verifier.DriftCount())
	}

	// Second submit: the engine should append a `<plan-drift>` system
	// message before the new user prompt enters state.
	drainAll(eng.Submit(context.Background(), "now what?"))

	if !systemMessagesContain(st, "plan-drift") &&
		!systemMessagesContain(st, "drift") {
		t.Errorf("expected drift attachment in state after second submit; messages=%+v",
			messageRoles(st))
	}
}

// messageRoles is a small debug helper.
func messageRoles(st *state.AppState) []string {
	var out []string
	for _, m := range st.Snapshot() {
		out = append(out, string(m.Role))
	}
	return out
}

// ─── test 5: plan attachment survives compact ───────────

// TestPlanFlow_AttachmentSurvivesCompact — when a session compacts (to
// reclaim context), the approved plan should be re-injected as a
// system message so the model doesn't drift after the summary.
func TestPlanFlow_AttachmentSurvivesCompact(t *testing.T) {
	perms := permissions.NewContext()
	const planBody = "Step 1: rewrite middleware.go using JWT helper."
	perms.SetPlanAttachment(planBody)

	reg := engine.NewRegistry()
	// Provider is asked twice: once for the summary turn, once for the
	// post-compact resume reply.
	prov := &scripted{turns: [][]engine.StreamFrame{
		textTurn("Conversation summary: refactoring auth."),
		textTurn("ok"),
	}}

	st := state.New()
	// Seed some history so compact has something to summarise.
	st.AppendMessage(state.Message{
		Role:    state.RoleUser,
		Content: []state.ContentBlock{{Type: state.ContentText, Text: "earlier turn"}},
	})
	st.AppendMessage(state.Message{
		Role:    state.RoleAssistant,
		Content: []state.ContentBlock{{Type: state.ContentText, Text: "earlier reply"}},
	})

	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions:       perms,
		BypassPermissions: true,
		// CompactMaxTokens default (>0) lets compactor build.
	})
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan engine.Event, 64)
	if err := eng.Compact(context.Background(), out); err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	close(out)
	for range out { // drain
	}

	if !systemMessagesContain(st, planBody) {
		t.Errorf("plan body must be re-injected post-compact; messages=%+v", messageRoles(st))
	}
	if !systemMessagesContain(st, "approved-plan") {
		t.Errorf("expected <approved-plan> wrapper")
	}
}

// ─── test 6: plan-mode permission gate ──────────────────

// TestPlanFlow_PlanModeBlocksWriteThroughDecide — sanity check that
// the same Decide() call the engine uses denies writes when plan
// mode is active. Belt-and-suspenders: even if the system prompt
// fails to dissuade the model, the gate stops the call.
func TestPlanFlow_PlanModeBlocksWriteThroughDecide(t *testing.T) {
	perms := permissions.NewContext()
	perms.EnterPlanMode()

	cases := []struct {
		tool     string
		readOnly bool
		want     permissions.Decision
	}{
		{"Edit", false, permissions.DecideDeny},
		{"Write", false, permissions.DecideDeny},
		{"Read", true, permissions.DecideAllow},
		{"Bash", false, permissions.DecideDeny},
		// Plan-mode transitions exempt — see policy.go isPlanTransition.
		{"ExitPlanMode", false, permissions.DecideAsk},
		{"EnterPlanMode", false, permissions.DecideAsk},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			d, _ := permissions.Decide(perms, permissions.Request{
				Tool:       c.tool,
				IsReadOnly: c.readOnly,
				Args:       map[string]any{"path": "x.go"},
			})
			if d != c.want {
				t.Errorf("plan mode + %s: got %v, want %v", c.tool, d, c.want)
			}
		})
	}
}

// ─── test 7: ExitPlanMode rejected outside plan mode ────

// TestPlanFlow_ExitPlanModeRejectedOutsidePlan ensures the tool can't
// be used to silently flip the session into the prePlanMode fallback.
func TestPlanFlow_ExitPlanModeRejectedOutsidePlan(t *testing.T) {
	perms := permissions.NewContext()
	tool := interactive.ExitPlanModeTool{Perms: perms}

	out, _ := tool.Call(context.Background(), map[string]any{
		"plan": "doesn't matter",
	}, nil)
	if !out.IsError {
		t.Fatalf("ExitPlanMode must reject when not in plan mode")
	}
	if !strings.Contains(out.SoftError, "plan mode") {
		t.Errorf("error message should mention plan mode; got %q", out.SoftError)
	}
}

// ─── test 8: classifier semantic match end-to-end ───────

// TestPlanFlow_ClassifierMatchesAndMisses asserts the runner-level
// integration of the classifier — the same Decide() the engine uses
// allows or rejects based on prompt intent. Each case mirrors a real
// scenario:
//
//   - "run tests" + "go test ./..." → allow (intent matches)
//   - "run tests" + "rm -rf /etc"   → ask  (no overlap)
//   - "deploy"    + "kubectl apply" → ask  (no "depl" stem in cmd)
//   - "build"     + "go build ./"   → allow
func TestPlanFlow_ClassifierMatchesAndMisses(t *testing.T) {
	perms := permissions.NewContext()
	perms.AddAllowedPrompts([]permissions.AllowedPrompt{
		{Tool: "Bash", Prompt: "run tests"},
		{Tool: "Bash", Prompt: "build"},
	})

	cases := []struct {
		cmd  string
		want permissions.Decision
	}{
		{"go test ./...", permissions.DecideAllow},
		{"npm test", permissions.DecideAllow},
		{"go build ./cmd/biu", permissions.DecideAllow},
		{"rm -rf /etc", permissions.DecideAsk},
		{"kubectl apply -f .", permissions.DecideAsk},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			d, _ := permissions.Decide(perms, permissions.Request{
				Tool: "Bash",
				Args: map[string]any{"command": c.cmd},
			})
			if d != c.want {
				t.Errorf("decide(%q) = %v; want %v", c.cmd, d, c.want)
			}
		})
	}
}

// ─── test 9: ClearAllowedPrompts wipes approvals ────────

// TestPlanFlow_ClearAllowedPromptsRevokesAccess simulates `/clear` —
// after wiping, a previously-approved prompt no longer auto-allows.
func TestPlanFlow_ClearAllowedPromptsRevokesAccess(t *testing.T) {
	perms := permissions.NewContext()
	perms.AddAllowedPrompts([]permissions.AllowedPrompt{
		{Tool: "Bash", Prompt: "run tests"},
	})
	d, _ := permissions.Decide(perms, permissions.Request{
		Tool: "Bash",
		Args: map[string]any{"command": "go test ./..."},
	})
	if d != permissions.DecideAllow {
		t.Fatalf("setup: expected initial allow; got %v", d)
	}

	perms.ClearAllowedPrompts()

	d, _ = permissions.Decide(perms, permissions.Request{
		Tool: "Bash",
		Args: map[string]any{"command": "go test ./..."},
	})
	if d == permissions.DecideAllow {
		t.Errorf("after Clear, classifier path must not auto-allow; got %v", d)
	}
}

// ─── test 10: plan attachment cleared on empty SetPlanAttachment ──

// TestPlanFlow_EmptyPlanAttachmentClearsObserver — the observer
// callback must fire on clear too, so the verifier drops its plan.
func TestPlanFlow_EmptyPlanAttachmentClearsObserver(t *testing.T) {
	perms := permissions.NewContext()
	verifier := planverify.New()
	perms.SetPlanObserver(verifier.SetPlan)

	perms.SetPlanAttachment("Step 1: do thing")
	if !verifier.HasPlan() {
		t.Fatalf("setup: verifier should have plan after observer fires")
	}
	perms.SetPlanAttachment("")
	if verifier.HasPlan() {
		t.Errorf("verifier should drop plan when attachment is cleared")
	}
}
