// E2E for the general-purpose built-in. Distinct test posture from
// the four specialists because:
//
//   * No tool restriction — must inherit the parent's full catalog.
//   * No model override — must run on whatever the parent picked.
//   * Doesn't have a /verb slash command (deliberate: the parent
//     calls it via Agent[subagent_type=general-purpose] when the
//     specialist set doesn't fit).
//
// The big invariant we test here: the AgentTool's input-schema
// `subagent_type` enum must NOT contain duplicate "general-purpose"
// entries even though the registry now seeds it. A duplicate would
// trip JSON-Schema validators and confuse models that read the enum
// list during tool selection.

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

func generalPurposeSystemFromRegistry() string {
	r, _ := agents.Load("")
	d, _ := r.Lookup("general-purpose")
	if d == nil {
		return ""
	}
	return d.SystemPrompt
}

type gpCaptureProvider struct {
	parentScript [][]engine.StreamFrame
	childScript  []engine.StreamFrame
	parentCalls  int

	gotChildSystem string
	gotChildModel  string
	gotChildSpecs  []engine.ToolSpec
}

func (p *gpCaptureProvider) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	if req.System == generalPurposeSystemFromRegistry() {
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

// ─── test 1: Definition shape ──────────────────────────

func TestGeneralPurposeAgent_DefinitionRegistered(t *testing.T) {
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("general-purpose")
	if !ok {
		t.Fatal("general-purpose must be available out of the box")
	}
	if d.Source != "builtin" {
		t.Errorf("Source: got %q, want builtin", d.Source)
	}
	if len(d.Tools) != 0 || len(d.DisallowedTools) != 0 {
		t.Errorf("general-purpose should be a pass-through (no allow/deny); got allow=%v deny=%v",
			d.Tools, d.DisallowedTools)
	}
	if d.Model != "inherit" {
		t.Errorf("Model: got %q, want inherit", d.Model)
	}
}

// ─── test 2: schema enum dedupe ────────────────────────

// The AgentTool prepends "general-purpose" as a guaranteed-present
// fallback, then appends every registered name. Now that the
// registry has its own "general-purpose" entry, the schema must
// NOT list it twice — the test catches accidental regressions of
// the dedupe in agent.go.
func TestGeneralPurposeAgent_SchemaEnumDedupes(t *testing.T) {
	registry, _ := agents.Load(t.TempDir())
	tool := orchestration.AgentTool{Registry: registry}
	schema := tool.InputSchema()

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	subtype, ok := props["subagent_type"].(map[string]any)
	if !ok {
		t.Fatal("schema missing subagent_type")
	}
	enum, ok := subtype["enum"].([]any)
	if !ok {
		t.Fatal("subagent_type should expose an enum once a registry is wired")
	}

	count := 0
	for _, v := range enum {
		if v == "general-purpose" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("`general-purpose` appears %d times in the enum, want 1; full enum=%v",
			count, enum)
	}
	// Sanity: every other built-in shows up exactly once too.
	for _, want := range []string{"Plan", "Explore", "CodeReview", "Verification"} {
		hits := 0
		for _, v := range enum {
			if v == want {
				hits++
			}
		}
		if hits != 1 {
			t.Errorf("%q appears %d times in enum; want 1", want, hits)
		}
	}
}

// ─── test 3: full E2E dispatch ─────────────────────────

func TestGeneralPurposeAgent_DispatchE2E(t *testing.T) {
	registry, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	prov := &gpCaptureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent",
				`{"subagent_type":"general-purpose","prompt":"investigate where /api/users is wired"}`),
			textTurn("Sub-agent reported its findings."),
		},
		// general-purpose can write — but its prompt says "NEVER
		// create files unless absolutely necessary". For the test we
		// only emit a final text reply.
		childScript: textTurn("- handler at internal/api/users.go:42\n- middleware at internal/api/middleware.go:118"),
	}

	parentReg := engine.NewRegistry()
	// `registerCatalog` shares one stub across every "write-like"
	// name (it always reports Name()="Edit"), so we only register
	// "Edit" here — the value of this test is proving Edit is NOT
	// filtered out for general-purpose, which is the structural
	// difference from CodeReview / Verification / Explore.
	registerCatalog(parentReg, []string{
		"Read", "Glob", "Grep", "Bash", "WebFetch", "Edit",
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
	events := drainAll(eng.Submit(context.Background(), "research"))

	if prov.gotChildSystem == "" {
		t.Fatalf("child stream never invoked; events=%d", len(events))
	}
	// inherit means the child runs the parent's model — opus here.
	// If a future commit ever hardcodes a default for general-purpose
	// the test catches it.
	if prov.gotChildModel != "claude-opus-4-7" {
		t.Errorf("child model should inherit parent; got %q", prov.gotChildModel)
	}

	// Catalog must include EVERYTHING from the parent — Edit, Agent,
	// and the read-only research tools alike. This is the structural
	// difference from the specialists, which strip writes + Agent
	// via deny-lists.
	gotNames := map[string]bool{}
	for _, s := range prov.gotChildSpecs {
		gotNames[s.Name] = true
	}
	for _, must := range []string{"Read", "Glob", "Grep", "Bash", "WebFetch", "Edit", "Agent"} {
		if !gotNames[must] {
			t.Errorf("child catalog missing %q; got keys=%v", must, keysOf(gotNames))
		}
	}

	// Parent saw the tagged report.
	tagged := false
	for _, ev := range events {
		if r, ok := ev.(*engine.ToolUseResultEvent); ok && r.ID == "u1" {
			for _, b := range r.Result.Content {
				if strings.Contains(b.Text, "[general-purpose]") &&
					strings.Contains(b.Text, "internal/api/users.go:42") {
					tagged = true
				}
			}
		}
	}
	if !tagged {
		t.Errorf("expected `[general-purpose] …` tag with citation")
	}
}

// ─── test 4: user override ─────────────────────────────

func TestGeneralPurposeAgent_UserOverrideWins(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	body := "---\nname: general-purpose\ndescription: Custom general-purpose for our team\n---\nUse our internal lookup table at //foo/MAP.md.\n"
	dir := homeDir + "/.biumind/agents"
	if err := writeUserAgent(dir, "general-purpose.md", body); err != nil {
		t.Fatal(err)
	}
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := r.Lookup("general-purpose")
	if d.Source != "user" {
		t.Errorf("user override should win; got source=%q", d.Source)
	}
	if !strings.Contains(d.SystemPrompt, "MAP.md") {
		t.Errorf("user prompt body should win")
	}
}
