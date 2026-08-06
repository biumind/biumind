// Full-lifecycle integration tests covering the cross-agent chain
// the user-facing slash commands compose into:
//
//   /ultraplan  →  Plan sub-agent designs the work + allowedPrompts
//   ExitPlanMode → plan persisted, batch approvals staged
//   Bash        →  auto-allowed via classifier
//   /review     →  CodeReview sub-agent critiques the diff
//   <fix>       →  parent applies a follow-up Bash, still inside the
//                  same allowedPrompt scope
//   /review     →  second CodeReview pass returns clean
//
// Earlier files (plan_e2e_test.go, explore_e2e_test.go, codereview_
// e2e_test.go) lock single-agent behaviour. This file proves the
// pieces compose end-to-end:
//
//   * Two distinct sub-agents (Plan + CodeReview) dispatched in
//     order from the same parent session.
//   * State carried across sub-agent boundaries (plan attachment,
//     allowedPrompts, drift verifier).
//   * Parent state stays clean — sub-agent internals don't leak.
//   * Multiple ExitPlanMode calls dedupe their allowedPrompts.
//   * Drift detection surfaces across sub-agent dispatches.
//
// The provider is a small router: it routes each Stream call to the
// right script based on a fingerprint substring of req.System. Each
// agent's system prompt has a distinctive opening line that's the
// reliable discriminator.

package engine_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/planverify"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/interactive"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
)

// ─── routerProvider: parent + Plan + CodeReview multiplexer ───

// routerProvider hands out the right scripted frames depending on
// which engine is asking. The discriminator is the System prompt's
// distinctive opening line. Routing is concurrency-safe because the
// engine streams turns sequentially even when multiple Agent calls
// fan out — we still serialise here for safety.
type routerProvider struct {
	mu sync.Mutex

	parent [][]engine.StreamFrame
	plan   [][]engine.StreamFrame
	review [][]engine.StreamFrame

	parentCalls int
	planCalls   int
	reviewCalls int

	// gotChildSystems records every distinct child system prompt
	// the spawner actually pushed; tests assert on this to confirm
	// each agent's definition flowed through.
	gotChildSystems []string
}

func (r *routerProvider) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Fingerprint match against the agent definitions' opening lines
	// (locked into builtins.go — TestExploreAgent_DefinitionRegistered
	// + sister tests fail loudly if either drifts, so this stays
	// stable).
	switch {
	case strings.Contains(req.System, "software architect and planning specialist"):
		r.gotChildSystems = append(r.gotChildSystems, "Plan")
		return next(&r.planCalls, r.plan)
	case strings.Contains(req.System, "senior code reviewer"):
		r.gotChildSystems = append(r.gotChildSystems, "CodeReview")
		return next(&r.reviewCalls, r.review)
	default:
		return next(&r.parentCalls, r.parent)
	}
}

// next pulls the next scripted turn out of `script`, advancing the
// counter. When the script runs out we return an error so the test
// fails loudly instead of hanging.
func next(counter *int, script [][]engine.StreamFrame) (<-chan engine.StreamFrame, error) {
	idx := *counter
	if idx >= len(script) {
		return nil, fmt.Errorf("router: script exhausted at call #%d", idx+1)
	}
	*counter++
	frames := script[idx]
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

// buildLifecycleParentReg seeds the parent registry with the real
// AgentTool + plan-mode interactive tools + recording stubs so the
// dispatch / permission paths run end-to-end.
func buildLifecycleParentReg(t *testing.T, perms *interactive.PermissionsAccessor, registry *agents.Registry, bash *recordingBash) *engine.SimpleRegistry {
	t.Helper()
	parentReg := engine.NewRegistry()
	parentReg.Register(bash)
	parentReg.Register(interactive.EnterPlanModeTool{Perms: *perms})
	parentReg.Register(interactive.ExitPlanModeTool{Perms: *perms, SessionID: "lifecycle-e2e"})
	parentReg.Register(readonlyTool{name: "Read"})
	parentReg.Register(orchestration.AgentTool{Registry: registry})
	return parentReg
}

// ─── test 1: Plan → exec → CodeReview happy path ───────

// TestE2E_PlanReviewHappyPath walks the canonical lifecycle:
//
//  1. Parent dispatches Agent[subagent_type=Plan]
//  2. Plan sub-agent calls ExitPlanMode (plan + allowedPrompts)
//  3. Plan returns; parent issues Bash matching the prompt
//  4. Parent dispatches Agent[subagent_type=CodeReview]
//  5. CodeReview returns severity-tagged feedback
//  6. Parent emits final text
//
// Asserts on every contract the chain depends on: each child stream
// fires once, plan attachment + allowedPrompts persist after the
// Plan child returns, Bash auto-allows via classifier, review tagged
// with `[CodeReview]`, ordering preserved.
func TestE2E_PlanReviewHappyPath(t *testing.T) {
	perms := permissions.NewContext()
	verifier := planverify.New()
	perms.SetPlanObserver(verifier.SetPlan)

	registry, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	bash := &recordingBash{}
	pa := interactive.PermissionsAccessor(perms)
	parentReg := buildLifecycleParentReg(t, &pa, registry, bash)

	// Plan sub-agent's "internal" turn calls ExitPlanMode against the
	// PARENT's perms context (forked perms for the child carries the
	// same interactive tool setup). The child's tool_use lands at
	// the parent's ExitPlanModeTool because the spawner shares the
	// parent's tool registry — exactly what /ultraplan does in prod.
	prov := &routerProvider{
		parent: [][]engine.StreamFrame{
			// Turn 1: dispatch Plan agent.
			toolUseTurn("p1", "Agent",
				`{"subagent_type":"Plan","prompt":"design oauth login implementation"}`),
			// Turn 2: parent runs the test command, classifier allows.
			toolUseTurn("p2", "Bash", `{"command":"go test ./..."}`),
			// Turn 3: dispatch CodeReview agent.
			toolUseTurn("p3", "Agent",
				`{"subagent_type":"CodeReview","prompt":"review the oauth changes"}`),
			// Turn 4: end with final text using the review.
			textTurn("Review came back clean — feature ready to ship."),
		},
		plan: [][]engine.StreamFrame{
			// Plan's internal step: call ExitPlanMode with the
			// finished plan + an allowedPrompt covering test runs.
			toolUseTurn("c1", "ExitPlanMode", jsonMust(map[string]any{
				"plan": "Implement OAuth login: add /auth/oauth handler, wire JWT verifier, run tests.",
				"allowedPrompts": []map[string]any{
					{"tool": "Bash", "prompt": "run tests"},
				},
			})),
			// Final assistant text the parent will receive.
			textTurn("Plan ready: 3 steps. ExitPlanMode called."),
		},
		review: [][]engine.StreamFrame{
			// CodeReview's single output.
			textTurn("**Summary:** 0 BLOCKERs, 0 MAJORs.\n\nNo issues found beyond NITPICK level."),
		},
	}

	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: parentReg, Provider: prov, Model: "claude-opus-4-7",
		Permissions:        perms,
		PlanVerifier:       verifier,
		PlanDriftThreshold: 1,
		MaxToolTurns:       12,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Parent must run in plan mode for ExitPlanMode (called inside
	// the child) to satisfy the "must be in plan mode" guard. The
	// spawner forks perms by *copy* of rules but DOES NOT copy mode
	// when no override is set — so we call EnterPlanMode here so
	// the child's forked context inherits ModePlan.
	perms.EnterPlanMode()
	// Pre-allow the orchestration tools the chain dispatches
	// (Agent, Read, ExitPlanMode, EnterPlanMode). Plan-mode exempts
	// the plan-transition pair from the read-only gate, but Decide
	// still falls through to DecideAsk without an allow rule — and
	// without an AskUser the test would block. Bash is deliberately
	// off this list so we can prove allowedPrompts auto-allows it.
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"Agent", "Read", "ExitPlanMode", "EnterPlanMode"})

	events := drainAll(eng.Submit(context.Background(),
		"/ultraplan implement oauth login"))

	// (a) Each child ran. The Plan script is 2 turns (tool_use →
	// text), CodeReview is 1 turn — so the per-agent Stream counts
	// match the script lengths exactly. We assert on the script
	// length rather than "exactly N spawns" because counting spawns
	// would require instrumenting the AgentTool, which would
	// duplicate behaviour that's already locked in dispatch tests.
	if prov.planCalls != 2 {
		t.Errorf("Plan child stream count %d != script length 2", prov.planCalls)
	}
	if prov.reviewCalls != 1 {
		t.Errorf("CodeReview child stream count %d != script length 1", prov.reviewCalls)
	}
	if !contains(prov.gotChildSystems, "Plan") || !contains(prov.gotChildSystems, "CodeReview") {
		t.Errorf("expected both Plan and CodeReview prompts seen by router; got %v",
			prov.gotChildSystems)
	}

	// (b) Plan agent ran ExitPlanMode → plan attachment + allowedPrompts staged.
	if perms.PlanAttachment() == "" {
		t.Errorf("plan attachment should persist after Plan agent's ExitPlanMode")
	}
	if !verifier.HasPlan() {
		t.Errorf("verifier should have picked up the plan")
	}
	if got := perms.AllowedPrompts(); len(got) != 1 || got[0].Prompt != "run tests" {
		t.Errorf("allowedPrompts not staged correctly: %+v", got)
	}

	// (c) Bash auto-allowed via the classifier — actually executed.
	if len(bash.ran) != 1 || bash.ran[0] != "go test ./..." {
		t.Errorf("Bash dispatch lost: %+v", bash.ran)
	}

	// (d) Review came back tagged.
	taggedReview := false
	for _, ev := range events {
		if r, ok := ev.(*engine.ToolUseResultEvent); ok && r.ID == "p3" {
			for _, b := range r.Result.Content {
				if strings.Contains(b.Text, "[CodeReview]") &&
					strings.Contains(b.Text, "0 BLOCKERs") {
					taggedReview = true
				}
			}
		}
	}
	if !taggedReview {
		t.Errorf("expected `[CodeReview] …` review with summary")
	}

	// (e) DoneEvent at end.
	if !hasDone(events) {
		t.Errorf("expected DoneEvent")
	}
}

// ─── test 2: review → fix → review iteration ────────────

// TestE2E_ReviewFixReviewLoop covers the ergonomics of the
// "review found something, fix it, re-review" workflow. Two
// distinct CodeReview dispatches in the same parent session, with
// a Bash patch in between that's allowed by the plan-time approval.
func TestE2E_ReviewFixReviewLoop(t *testing.T) {
	perms := permissions.NewContext()
	// Pre-stage a plan + allowedPrompts as if /ultraplan had just
	// completed. This is the realistic state after the plan agent
	// returns, freeing this test to focus on the review loop itself.
	perms.SetPlanAttachment("Implement OAuth login")
	perms.AddAllowedPrompts([]permissions.AllowedPrompt{
		{Tool: "Bash", Prompt: "fix issues"},
	})
	registry, _ := agents.Load(t.TempDir())

	bash := &recordingBash{}
	parentReg := engine.NewRegistry()
	parentReg.Register(bash)
	parentReg.Register(orchestration.AgentTool{Registry: registry})

	prov := &routerProvider{
		parent: [][]engine.StreamFrame{
			// Turn 1: first review.
			toolUseTurn("u1", "Agent",
				`{"subagent_type":"CodeReview","prompt":"first pass"}`),
			// Turn 2: fix the BLOCKER via Bash.
			toolUseTurn("u2", "Bash",
				`{"command":"sed -i s/old/new/ fix issues in auth.go"}`),
			// Turn 3: second review after the fix.
			toolUseTurn("u3", "Agent",
				`{"subagent_type":"CodeReview","prompt":"verify the fix"}`),
			// Turn 4: ship.
			textTurn("Both reviews complete; shipped."),
		},
		review: [][]engine.StreamFrame{
			// First pass returns a BLOCKER.
			textTurn("**Summary:** 1 BLOCKER, 0 MAJORs.\n\n" +
				"### auth.go\n\n" +
				"- **[BLOCKER] auth.go:12** — wrong variable name.\n"),
			// Second pass returns clean.
			textTurn("**Summary:** 0 BLOCKERs, 0 MAJORs.\n\nNo issues found."),
		},
	}

	st := state.New()
	eng, _ := engine.New(engine.Options{
		State: st, Tools: parentReg, Provider: prov, Model: "test",
		Permissions:  perms,
		MaxToolTurns: 10,
	})
	events := drainAll(eng.Submit(context.Background(),
		"/review then fix then /review"))

	if prov.reviewCalls != 2 {
		t.Fatalf("CodeReview should run twice; got %d", prov.reviewCalls)
	}
	if len(bash.ran) != 1 {
		t.Errorf("Bash should have run once for the fix; got %v", bash.ran)
	}

	// Both review payloads must have arrived back to parent in order:
	// first BLOCKER, then clean.
	var u1Text, u3Text string
	for _, ev := range events {
		if r, ok := ev.(*engine.ToolUseResultEvent); ok {
			text := ""
			for _, b := range r.Result.Content {
				text += b.Text
			}
			switch r.ID {
			case "u1":
				u1Text = text
			case "u3":
				u3Text = text
			}
		}
	}
	if !strings.Contains(u1Text, "1 BLOCKER") {
		t.Errorf("first review should report the BLOCKER; got %q", u1Text)
	}
	if !strings.Contains(u3Text, "0 BLOCKERs") {
		t.Errorf("second review should be clean; got %q", u3Text)
	}
}

// ─── test 3: drift detection across sub-agent boundary ──

// TestE2E_DriftSurvivesSubagentDispatch — drift accrued in turn N
// must surface as a system attachment on turn N+1, even when turn N
// included a sub-agent dispatch (the sub-agent runs in its own
// AppState; drift state lives on the parent's verifier, so this
// confirms the wiring doesn't accidentally reset).
func TestE2E_DriftSurvivesSubagentDispatch(t *testing.T) {
	perms := permissions.NewContext()
	verifier := planverify.New()
	perms.SetPlanObserver(verifier.SetPlan)
	perms.SetPlanAttachment("Implement JWT verification in auth/jwt.go.")

	registry, _ := agents.Load(t.TempDir())

	bash := &recordingBash{}
	parentReg := engine.NewRegistry()
	parentReg.Register(bash)
	parentReg.Register(orchestration.AgentTool{Registry: registry})

	prov := &routerProvider{
		parent: [][]engine.StreamFrame{
			// Turn 1: drifted destructive call (rm against unrelated path).
			toolUseTurn("u1", "Bash",
				`{"command":"rm -rf node_modules"}`),
			// Turn 2: dispatch CodeReview (drift state should NOT leak
			// into the child's context).
			toolUseTurn("u2", "Agent",
				`{"subagent_type":"CodeReview","prompt":"review"}`),
			textTurn("done"),
			// After SECOND user submit, drift should surface as a
			// system message and the model just acks.
			textTurn("understood, will stay on plan"),
		},
		review: [][]engine.StreamFrame{
			textTurn("**Summary:** review complete."),
		},
	}

	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: parentReg, Provider: prov, Model: "test",
		Permissions:        perms,
		PlanVerifier:       verifier,
		PlanDriftThreshold: 1,
		BypassPermissions:  true,
		MaxToolTurns:       10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// First submit: drift accrues.
	drainAll(eng.Submit(context.Background(), "do work"))
	if verifier.DriftCount() == 0 {
		t.Fatal("expected drift observation from the rm -rf call")
	}

	// Sub-agent ran inside that turn — confirm it didn't reset drift.
	if prov.reviewCalls != 1 {
		t.Errorf("CodeReview should have dispatched; got %d calls", prov.reviewCalls)
	}

	// Second submit: drift attachment should fold in.
	drainAll(eng.Submit(context.Background(), "now what?"))
	if !findSystemText(st, "drift") && !findSystemText(st, "plan-drift") {
		t.Errorf("drift attachment missing after second submit; messages=%+v",
			messageRolesShort(st))
	}
}

// ─── test 4: parent state stays clean ───────────────────

// TestE2E_SubagentMessagesDoNotPolluteParent — sub-agents own their
// AppState (per spawner.go). The parent's transcript must contain
// only its own user prompt, its own tool_use blocks, and the
// orchestration tool's tagged result string. The sub-agent's
// internal tool turns (Read / Glob / Grep round-trips) must NOT
// appear in the parent.
func TestE2E_SubagentMessagesDoNotPolluteParent(t *testing.T) {
	registry, _ := agents.Load(t.TempDir())

	parentReg := engine.NewRegistry()
	registerCatalog(parentReg, []string{"Read", "Glob", "Grep", "Bash", "WebFetch"})
	parentReg.Register(orchestration.AgentTool{Registry: registry})

	prov := &routerProvider{
		parent: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent",
				`{"subagent_type":"CodeReview","prompt":"review"}`),
			textTurn("Sub-agent reported: clean."),
		},
		review: [][]engine.StreamFrame{
			// Multiple internal turns — the secret-sauce text the
			// child uses internally must stay inside the child's
			// state.
			toolUseTurn("c1", "Read", `{"path":"auth.go"}`),
			toolUseTurn("c2", "Grep", `{"pattern":"jwt"}`),
			textTurn("**Summary:** clean."),
		},
	}

	st := state.New()
	eng, _ := engine.New(engine.Options{
		State: st, Tools: parentReg, Provider: prov, Model: "test",
		BypassPermissions: true,
		MaxToolTurns:      8,
	})
	drainAll(eng.Submit(context.Background(), "review please"))

	// Concatenate all parent messages into one big string; secrets
	// from child-only turns must not be in there.
	all := flattenAll(st)
	for _, leak := range []string{
		`{"path":"auth.go"}`,
		`{"pattern":"jwt"}`,
	} {
		if strings.Contains(all, leak) {
			t.Errorf("child internal arg leaked into parent state: %q\nfull: %s", leak, all)
		}
	}
	// What SHOULD be in there: parent's own dispatch + the tagged
	// sub-agent reply.
	if !strings.Contains(all, "[CodeReview]") {
		t.Errorf("tagged sub-agent reply missing from parent")
	}
	if !strings.Contains(all, "Summary:** clean") {
		t.Errorf("sub-agent's final text missing from parent")
	}
}

// ─── test 5: allowedPrompts dedupe across multiple ExitPlanMode ─

// TestE2E_AllowedPromptsDedupeAcrossMultiplePlans — same session,
// two ExitPlanMode calls (think /ultraplan run twice as the user
// refines the spec). Identical {Tool, Prompt} pairs collapse into
// one staged approval; net-new pairs accumulate.
func TestE2E_AllowedPromptsDedupeAcrossMultiplePlans(t *testing.T) {
	perms := permissions.NewContext()

	tool := interactive.ExitPlanModeTool{Perms: perms}

	// First plan stages two prompts.
	perms.EnterPlanMode()
	out, _ := tool.Call(context.Background(), map[string]any{
		"plan": "round 1",
		"allowedPrompts": []any{
			map[string]any{"tool": "Bash", "prompt": "run tests"},
			map[string]any{"tool": "Bash", "prompt": "run linter"},
		},
	}, nil)
	if out.IsError {
		t.Fatalf("first ExitPlanMode failed: %+v", out)
	}

	// Second plan re-stages "run tests" (dup) + "run build" (new).
	perms.EnterPlanMode()
	out, _ = tool.Call(context.Background(), map[string]any{
		"plan": "round 2",
		"allowedPrompts": []any{
			map[string]any{"tool": "Bash", "prompt": "run tests"},
			map[string]any{"tool": "Bash", "prompt": "run build"},
		},
	}, nil)
	if out.IsError {
		t.Fatalf("second ExitPlanMode failed: %+v", out)
	}

	got := perms.AllowedPrompts()
	if len(got) != 3 {
		t.Errorf("expected 3 distinct prompts (tests, linter, build); got %d: %+v",
			len(got), got)
	}
	prompts := map[string]bool{}
	for _, p := range got {
		prompts[p.Prompt] = true
	}
	for _, want := range []string{"run tests", "run linter", "run build"} {
		if !prompts[want] {
			t.Errorf("expected staged prompt %q; got set=%v", want, prompts)
		}
	}
}

// ─── helpers ────────────────────────────────────────────

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func hasDone(events []engine.Event) bool {
	for _, ev := range events {
		if _, ok := ev.(*engine.DoneEvent); ok {
			return true
		}
	}
	return false
}

func findSystemText(st *state.AppState, needle string) bool {
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, needle) {
				return true
			}
		}
	}
	return false
}

func flattenAll(st *state.AppState) string {
	var b strings.Builder
	for _, m := range st.Snapshot() {
		fmt.Fprintf(&b, "[%s] ", m.Role)
		for _, c := range m.Content {
			b.WriteString(c.Text)
			if c.Type == state.ContentToolUse {
				// Tool-use args go through ToolUseInput, not Text.
				for k, v := range c.ToolUseInput {
					fmt.Fprintf(&b, " %s=%v", k, v)
				}
			}
			if c.Type == state.ContentToolResult {
				for _, rb := range c.ToolResultContent {
					b.WriteString(rb.Text)
				}
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func messageRolesShort(st *state.AppState) []string {
	var out []string
	for _, m := range st.Snapshot() {
		out = append(out, string(m.Role))
	}
	return out
}
