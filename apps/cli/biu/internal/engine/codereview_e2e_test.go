// End-to-end integration tests for the built-in CodeReview sub-agent.
//
// Mirrors explore_e2e_test.go's structure (full parent → AgentTool →
// spawner → child engine path) but covers CodeReview-specific
// contracts:
//
//   * the agent ships with severity vocabulary (BLOCKER / MAJOR /
//     MINOR / NITPICK / QUESTION / PRAISE) the parent's renderer
//     can rely on
//   * default model is sonnet (reasoning > speed for review work)
//   * deny-list mirrors Explore — no recursive Agent, no edits
//   * the child sees a curated read-only catalog (Read / Glob /
//     Grep / Bash / WebFetch) and nothing else
//   * the parent receives the review tagged "[CodeReview] …"
//
// Lives in `package engine_test` for the same reason as the other
// E2E files: needs `agents` + `tools/orchestration`, which would
// otherwise create import cycles against `engine`.

package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
)

// codeReviewSystemFromRegistry pulls the canonical system prompt the
// built-in registry seeds, used by the captureProvider to identify
// the child stream.
func codeReviewSystemFromRegistry() string {
	r, _ := agents.Load("")
	d, _ := r.Lookup("CodeReview")
	if d == nil {
		return ""
	}
	return d.SystemPrompt
}

// codeReviewCaptureProvider mirrors captureProvider in
// explore_e2e_test.go but discriminates on CodeReview's prompt. We
// keep them as separate types rather than a shared one so changes to
// either agent's prompt don't accidentally route the wrong way.
type codeReviewCaptureProvider struct {
	parentScript [][]engine.StreamFrame
	childScript  []engine.StreamFrame
	parentCalls  int

	gotChildSystem string
	gotChildModel  string
	gotChildSpecs  []engine.ToolSpec
}

func (p *codeReviewCaptureProvider) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	if req.System == codeReviewSystemFromRegistry() {
		p.gotChildSystem = req.System
		p.gotChildModel = req.Model
		p.gotChildSpecs = append([]engine.ToolSpec(nil), req.Tools...)
		ch := make(chan engine.StreamFrame, len(p.childScript))
		for _, f := range p.childScript {
			ch <- f
		}
		close(ch)
		return ch, nil
	}
	idx := p.parentCalls
	if idx >= len(p.parentScript) {
		idx = len(p.parentScript) - 1
	}
	p.parentCalls++
	frames := p.parentScript[idx]
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

// ─── test 1: registry shape ─────────────────────────────

func TestCodeReviewAgent_DefinitionRegistered(t *testing.T) {
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("CodeReview")
	if !ok {
		t.Fatal("CodeReview must be available out of the box")
	}
	if d.Source != "builtin" {
		t.Errorf("Source: want builtin, got %q", d.Source)
	}
	if d.Model != "claude-sonnet-4-6" {
		t.Errorf("default model should be sonnet for reasoning quality; got %q", d.Model)
	}
	// Severity vocabulary the rendering layer depends on must all
	// be present in the system prompt.
	for _, sev := range []string{"BLOCKER", "MAJOR", "MINOR", "NITPICK", "QUESTION", "PRAISE"} {
		if !strings.Contains(d.SystemPrompt, sev) {
			t.Errorf("system prompt missing severity tag %q", sev)
		}
	}
	// Output format markers — these are the literal strings the
	// parent / future tooling parses for. If they drift, tests
	// catch it before users do.
	for _, marker := range []string{"Summary:", "Open questions"} {
		if !strings.Contains(d.SystemPrompt, marker) {
			t.Errorf("system prompt missing output marker %q", marker)
		}
	}
	// Read-only enforcement: prompt must explicitly forbid the write
	// surface. Belt to the deny-list's suspenders.
	for _, banned := range []string{"NO FILE MODIFICATIONS", "STRICTLY PROHIBITED"} {
		if !strings.Contains(d.SystemPrompt, banned) {
			t.Errorf("prompt should forbid writes via %q phrasing", banned)
		}
	}
}

// ─── test 2: FilterTools removes write tools ────────────

func TestCodeReviewAgent_FilterToolsExcludesWrite(t *testing.T) {
	r, _ := agents.Load(t.TempDir())
	d, _ := r.Lookup("CodeReview")
	got := d.FilterTools(fullParentCatalog)
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "ExitPlanMode"} {
		for _, name := range got {
			if name == banned {
				t.Errorf("%s leaked through FilterTools: %v", banned, got)
			}
		}
	}
	for _, want := range []string{"Read", "Glob", "Grep", "Bash", "WebFetch"} {
		found := false
		for _, n := range got {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FilterTools dropped allow-listed %s; got %v", want, got)
		}
	}
}

// ─── test 3: Apply produces a SpawnRequest with sonnet override ──

func TestCodeReviewAgent_DefinitionApplyOverridesParentModel(t *testing.T) {
	r, _ := agents.Load(t.TempDir())
	d, _ := r.Lookup("CodeReview")
	// Parent runs opus; CodeReview must downshift to sonnet so the
	// reviewer matches its design point regardless of the parent.
	got := d.Apply(agents.SpawnRequest{Model: "claude-opus-4-7"})
	if got.Model != "claude-sonnet-4-6" {
		t.Errorf("CodeReview should override parent model with sonnet; got %q", got.Model)
	}
	if string(got.PermissionMode) != "" {
		t.Errorf("permission mode should inherit (empty); got %q", got.PermissionMode)
	}
	if got.System == "" {
		t.Errorf("system prompt must be propagated")
	}
}

// ─── test 4: full E2E dispatch via AgentTool ────────────

func TestCodeReviewAgent_DispatchViaAgentToolE2E(t *testing.T) {
	registry, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Realistic child reply mirrors the documented output format so
	// downstream parsing tests can rely on this shape.
	childReply := "**Summary:** 1 BLOCKER, 0 MAJORs.\n\n" +
		"### auth/jwt.go\n\n" +
		"- **[BLOCKER] auth/jwt.go:42** — secret key read from env at " +
		"call time but never validated. Empty BIU_JWT_SECRET silently " +
		"signs tokens with `\"\"` — every attacker forges valid JWTs.\n"

	prov := &codeReviewCaptureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent", `{"subagent_type":"CodeReview","prompt":"review the auth changes","description":"review JWT diff"}`),
			textTurn("Sub-agent flagged a BLOCKER in auth/jwt.go"),
		},
		childScript: textTurn(childReply),
	}

	parentReg := engine.NewRegistry()
	registerCatalog(parentReg, []string{
		"Read", "Glob", "Grep", "Bash", "WebFetch",
		"Edit", "Write", "MultiEdit", "NotebookEdit",
		"ExitPlanMode",
	})
	parentReg.Register(orchestration.AgentTool{Registry: registry})

	st := state.New()
	eng, err := engine.New(engine.Options{
		State: st, Tools: parentReg, Provider: prov, Model: "claude-opus-4-7",
		BypassPermissions: true,
		MaxToolTurns:      6,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := drainAll(eng.Submit(context.Background(), "review please"))

	if prov.gotChildSystem == "" {
		t.Fatalf("child stream never invoked; events=%d", len(events))
	}
	if !strings.Contains(prov.gotChildSystem, "BLOCKER") {
		t.Errorf("child got unexpected system prompt; first chars=%q",
			prov.gotChildSystem[:min(160, len(prov.gotChildSystem))])
	}
	if prov.gotChildModel != "claude-sonnet-4-6" {
		t.Errorf("child model: want sonnet, got %q", prov.gotChildModel)
	}

	// Child's tool catalog must be filtered.
	gotNames := map[string]bool{}
	for _, s := range prov.gotChildSpecs {
		gotNames[s.Name] = true
	}
	for _, want := range []string{"Read", "Glob", "Grep", "Bash", "WebFetch"} {
		if !gotNames[want] {
			t.Errorf("child catalog missing allow-listed %s; got keys=%v", want, keysOf(gotNames))
		}
	}
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "ExitPlanMode"} {
		if gotNames[banned] {
			t.Errorf("child catalog leaked %s; got keys=%v", banned, keysOf(gotNames))
		}
	}

	// Parent saw the tagged review with the BLOCKER finding intact.
	taggedFound, blockerSurvived := false, false
	for _, ev := range events {
		if r, ok := ev.(*engine.ToolUseResultEvent); ok && r.ID == "u1" {
			for _, b := range r.Result.Content {
				if strings.Contains(b.Text, "[CodeReview]") {
					taggedFound = true
				}
				if strings.Contains(b.Text, "BLOCKER") &&
					strings.Contains(b.Text, "auth/jwt.go:42") {
					blockerSurvived = true
				}
			}
		}
	}
	if !taggedFound {
		t.Errorf("expected `[CodeReview] …` tag from orchestration layer")
	}
	if !blockerSurvived {
		t.Errorf("expected BLOCKER finding text to flow back to parent")
	}
}

// ─── test 5: recursive spawn blocked ────────────────────

func TestCodeReviewAgent_CannotRecursivelySpawnAgents(t *testing.T) {
	registry, _ := agents.Load(t.TempDir())

	prov := &codeReviewCaptureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent", `{"subagent_type":"CodeReview","prompt":"review"}`),
			textTurn("done"),
		},
		childScript: append(
			toolUseTurn("c1", "Agent", `{"subagent_type":"CodeReview","prompt":"deeper recursion"}`),
			textTurn("ok done")...,
		),
	}

	parentReg := engine.NewRegistry()
	registerCatalog(parentReg, []string{"Read", "Glob", "Grep", "Bash", "WebFetch"})
	parentReg.Register(orchestration.AgentTool{Registry: registry})

	st := state.New()
	eng, _ := engine.New(engine.Options{
		State: st, Tools: parentReg, Provider: prov, Model: "test",
		BypassPermissions: true,
		MaxToolTurns:      6,
	})
	drainAll(eng.Submit(context.Background(), "review"))

	for _, s := range prov.gotChildSpecs {
		if s.Name == "Agent" {
			t.Errorf("Agent leaked into CodeReview's catalog — recursive spawn possible")
		}
	}
}

// ─── test 6: user override wins ─────────────────────────

func TestCodeReviewAgent_UserOverrideReplacesBuiltin(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	body := "---\nname: CodeReview\ndescription: Custom review for our team's style guide\n---\nUse our internal style guide at //foo/STYLE.md.\n"
	dir := homeDir + "/.biumind/agents"
	if err := writeUserAgent(dir, "codereview.md", body); err != nil {
		t.Fatal(err)
	}
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := r.Lookup("CodeReview")
	if d.Source != "user" {
		t.Errorf("user override should win; got source=%q", d.Source)
	}
	if !strings.Contains(d.SystemPrompt, "Use our internal style guide") {
		t.Errorf("user prompt body should win")
	}
}

// min is a tiny helper so the file doesn't need to import the math
// package; Go 1.21+ has builtin min, but keeping a defensive version
// keeps the test buildable on older toolchains.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
