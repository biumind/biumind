// E2E for the Verification built-in. Verifies the dispatch shape +
// that the verdict vocabulary survives the round-trip back to the
// parent unchanged. Mirrors codereview_e2e_test.go's structure
// because the agents are sister types — same deny-list, same
// orchestration path, different intent.
//
// Particular care here: Verification is the agent most likely to be
// invoked by automation (CI bots, "auto-verify on commit"), so the
// VERDICT line MUST flow back verbatim. If a future change ever
// summarises sub-agent output the test catches it.

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

// verificationSystemFromRegistry pulls the canonical system prompt
// the registry seeds. Used by capture providers to identify the
// Verification child stream.
func verificationSystemFromRegistry() string {
	r, _ := agents.Load("")
	d, _ := r.Lookup("Verification")
	if d == nil {
		return ""
	}
	return d.SystemPrompt
}

// verificationCaptureProvider routes parent vs child by checking the
// system prompt against the Verification fingerprint.
type verificationCaptureProvider struct {
	parentScript [][]engine.StreamFrame
	childScript  []engine.StreamFrame
	parentCalls  int

	gotChildSystem string
	gotChildModel  string
	gotChildSpecs  []engine.ToolSpec
}

func (p *verificationCaptureProvider) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	if req.System == verificationSystemFromRegistry() {
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

func TestVerificationAgent_DefinitionRegistered(t *testing.T) {
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("Verification")
	if !ok {
		t.Fatal("Verification must be available out of the box")
	}
	if d.Source != "builtin" {
		t.Errorf("Source: got %q, want builtin", d.Source)
	}
	if d.Model != "claude-sonnet-4-6" {
		t.Errorf("default model should be sonnet; got %q", d.Model)
	}
	// The agent's contract is that it runs commands AND can spawn
	// long-running probes via background tasks.
	for _, want := range []string{"Bash", "BashOutput", "KillBash", "Read", "Glob", "Grep", "WebFetch"} {
		found := false
		for _, t2 := range d.Tools {
			if t2 == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Verification missing allow-listed %q; got %v", want, d.Tools)
		}
	}
	for _, marker := range []string{"VERDICT: PASS", "VERDICT: FAIL", "VERDICT: PARTIAL", "RATIONALIZATIONS"} {
		if !strings.Contains(d.SystemPrompt, marker) {
			t.Errorf("system prompt missing marker %q", marker)
		}
	}
}

// ─── test 2: deny-list shape ────────────────────────────

func TestVerificationAgent_FilterToolsExcludesWrites(t *testing.T) {
	r, _ := agents.Load(t.TempDir())
	d, _ := r.Lookup("Verification")
	got := d.FilterTools(fullParentCatalog)
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "ExitPlanMode"} {
		for _, n := range got {
			if n == banned {
				t.Errorf("%s leaked through FilterTools: %v", banned, got)
			}
		}
	}
	for _, want := range []string{"Bash", "Read", "Glob", "Grep", "WebFetch"} {
		found := false
		for _, n := range got {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FilterTools dropped %q; got %v", want, got)
		}
	}
}

// ─── test 3: full E2E dispatch via AgentTool ────────────

func TestVerificationAgent_DispatchVerdictRoundTrip(t *testing.T) {
	registry, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Realistic verification reply ending with the verdict line the
	// parent + tooling parse for. If the orchestration tagging
	// modifies the reply this test catches it.
	childReply := "### Check: build succeeds\n" +
		"**Command run:**\n  go build ./...\n" +
		"**Output observed:**\n  (clean exit)\n" +
		"**Result: PASS**\n\n" +
		"### Check: race detector finds nothing\n" +
		"**Command run:**\n  go test -race ./internal/...\n" +
		"**Output observed:**\n  ok  ./internal/...\n" +
		"**Result: PASS**\n\n" +
		"VERDICT: PASS"

	prov := &verificationCaptureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent", `{"subagent_type":"Verification","prompt":"verify the auth changes"}`),
			textTurn("Verifier reported PASS."),
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
	events := drainAll(eng.Submit(context.Background(), "verify"))

	if prov.gotChildSystem == "" {
		t.Fatalf("child stream never invoked; events=%d", len(events))
	}
	// Parent runs opus; Verification's definition must downshift to
	// sonnet — same posture as CodeReview, locked here so a future
	// "use parent model" tweak doesn't silently regress quality.
	if prov.gotChildModel != "claude-sonnet-4-6" {
		t.Errorf("child model: want sonnet, got %q", prov.gotChildModel)
	}

	// Catalog must include the bg-task partners — without them
	// Verification can't run a dev server in background and probe it.
	gotNames := map[string]bool{}
	for _, s := range prov.gotChildSpecs {
		gotNames[s.Name] = true
	}
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "ExitPlanMode"} {
		if gotNames[banned] {
			t.Errorf("child catalog leaked %s; got %v", banned, keysOf(gotNames))
		}
	}

	// VERDICT line must reach the parent intact — automation depends
	// on it.
	taggedFound, verdictSurvived := false, false
	for _, ev := range events {
		if r, ok := ev.(*engine.ToolUseResultEvent); ok && r.ID == "u1" {
			for _, b := range r.Result.Content {
				if strings.Contains(b.Text, "[Verification]") {
					taggedFound = true
				}
				if strings.Contains(b.Text, "VERDICT: PASS") {
					verdictSurvived = true
				}
			}
		}
	}
	if !taggedFound {
		t.Errorf("expected `[Verification] …` orchestration tag")
	}
	if !verdictSurvived {
		t.Errorf("VERDICT line must not be summarised away by the orchestration layer")
	}
}

// ─── test 4: recursive Agent dispatch blocked ──────────

func TestVerificationAgent_CannotRecursivelySpawnAgents(t *testing.T) {
	registry, _ := agents.Load(t.TempDir())

	prov := &verificationCaptureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent", `{"subagent_type":"Verification","prompt":"verify"}`),
			textTurn("done"),
		},
		childScript: append(
			toolUseTurn("c1", "Agent", `{"subagent_type":"Verification","prompt":"recurse"}`),
			textTurn("VERDICT: PASS")...,
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
	drainAll(eng.Submit(context.Background(), "verify"))

	for _, s := range prov.gotChildSpecs {
		if s.Name == "Agent" {
			t.Errorf("Agent leaked into Verification's catalog — recursive spawn possible")
		}
	}
}

// ─── test 5: user override wins ─────────────────────────

func TestVerificationAgent_UserOverrideReplacesBuiltin(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	body := "---\nname: Verification\ndescription: Custom verification with a project-specific harness\n---\nUse our internal harness via `make verify`.\n"
	dir := homeDir + "/.biumind/agents"
	if err := writeUserAgent(dir, "verification.md", body); err != nil {
		t.Fatal(err)
	}
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := r.Lookup("Verification")
	if d.Source != "user" {
		t.Errorf("user override should win; got source=%q", d.Source)
	}
	if !strings.Contains(d.SystemPrompt, "make verify") {
		t.Errorf("user prompt body should win")
	}
}
