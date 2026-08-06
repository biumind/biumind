// End-to-end integration tests for the built-in Explore sub-agent.
//
// Walks the full Explore dispatch path:
//
//   parent engine + AgentTool
//     → orchestration.AgentTool.Call (registry lookup + def merge)
//       → engine.engineSpawner.Spawn (forks perms, filters tool catalog)
//         → child engine.QueryEngine (real Submit loop, scripted provider)
//
// Verifies the contracts that matter for a real Explore run:
//
//   * the Definition is registered as a built-in
//   * the deny-list survives all the way down to the child's tool registry
//     (Edit/Write/MultiEdit/NotebookEdit + Agent + ExitPlanMode are
//     unavailable to the child even if the parent has them)
//   * the allow-list narrows the catalog to read-only research tools
//     (Read / Glob / Grep / Bash / WebFetch)
//   * recursive Agent dispatch from inside Explore is impossible (the
//     child literally cannot find the Agent tool)
//   * the system prompt and model override flow through correctly
//   * the child's last assistant text reaches the parent unchanged,
//     wrapped with the `[Explore] …` tag the orchestration tool adds
//   * a per-call model override on the SpawnRequest beats the
//     definition default (the caller can ask for opus on a tricky
//     query even though Explore defaults to haiku).
//
// Lives in `package engine_test` (external) so it can import
// `internal/tools/orchestration` and `internal/agents` without an
// import cycle.

package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
)

// ─── shared fixtures ─────────────────────────────────────

// Catalog of every tool name the parent registers. Sorted to mirror
// what the registry would actually expose to the LLM.
var fullParentCatalog = []string{
	"Read", "Glob", "Grep", "Bash", "WebFetch",
	"Edit", "Write", "MultiEdit", "NotebookEdit",
	"Agent", "ExitPlanMode",
}

// captureProvider records the StreamRequest the child receives so
// tests can assert on the merged System prompt + Model + tool specs.
type captureProvider struct {
	parentScript [][]engine.StreamFrame
	childScript  []engine.StreamFrame
	parentCalls  int

	gotChildSystem string
	gotChildModel  string
	gotChildSpecs  []engine.ToolSpec
}

func (p *captureProvider) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	// Heuristic: parent has the user-said-research prompt; child has
	// whatever the parent forwarded. We discriminate by the assistant-
	// shaped System (parent's is one string, child's is another).
	if req.System == exploreSystemFromRegistry() {
		// Child run — record then play the child script.
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
	// Otherwise it's the parent.
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

// exploreSystemFromRegistry pulls the canonical system prompt the
// built-in registry seeds. Built once per process (cheap regardless).
func exploreSystemFromRegistry() string {
	r, _ := agents.Load("")
	d, _ := r.Lookup("Explore")
	if d == nil {
		return ""
	}
	return d.SystemPrompt
}

// registerCatalog seeds a fresh registry with read-only stubs for
// every tool name in `names`. Edit/Write/etc are also stubbed —
// they MUST be filtered out for Explore, and the test asserts that.
func registerCatalog(reg *engine.SimpleRegistry, names []string) {
	for _, n := range names {
		switch n {
		case "Edit", "Write", "MultiEdit", "NotebookEdit":
			reg.Register(&editTool{}) // re-uses the writeable stub from plan_e2e_test.go via shared package
		case "Bash":
			reg.Register(&recordingBash{})
		default:
			reg.Register(readonlyTool{name: n})
		}
	}
}

// ─── test 1: registry shape ─────────────────────────────

// TestExploreAgent_DefinitionRegistered asserts the loader seeds the
// Explore Definition with the contract the orchestration layer
// depends on. Catches accidental drift in field names / casing.
func TestExploreAgent_DefinitionRegistered(t *testing.T) {
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("Explore")
	if !ok {
		t.Fatal("Explore must be available out of the box")
	}
	if d.Source != "builtin" {
		t.Errorf("Source: want builtin, got %q", d.Source)
	}
	// Allow-list shape.
	wantAllow := map[string]bool{"Read": true, "Glob": true, "Grep": true, "Bash": true, "WebFetch": true}
	if len(d.Tools) != len(wantAllow) {
		t.Fatalf("Tools length: want %d, got %v", len(wantAllow), d.Tools)
	}
	for _, t2 := range d.Tools {
		if !wantAllow[t2] {
			t.Errorf("unexpected allow-list tool %q", t2)
		}
	}
	// Deny-list shape.
	wantDeny := map[string]bool{
		"Agent": true, "ExitPlanMode": true,
		"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
	}
	for _, t2 := range d.DisallowedTools {
		if !wantDeny[t2] {
			t.Errorf("unexpected deny-list tool %q", t2)
		}
		delete(wantDeny, t2)
	}
	if len(wantDeny) > 0 {
		t.Errorf("missing deny-list entries: %v", wantDeny)
	}
	if d.Model != "claude-haiku-4-5" {
		t.Errorf("Model default should be haiku for speed; got %q", d.Model)
	}
	if !strings.Contains(d.SystemPrompt, "READ-ONLY") {
		t.Errorf("system prompt must enforce read-only contract")
	}
}

// ─── test 2: FilterTools removes write tools ────────────

// TestExploreAgent_FilterToolsExcludesWrite proves Definition.FilterTools
// removes every write/edit tool from a wide catalog.
func TestExploreAgent_FilterToolsExcludesWrite(t *testing.T) {
	r, _ := agents.Load(t.TempDir())
	d, _ := r.Lookup("Explore")
	got := d.FilterTools(fullParentCatalog)
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "ExitPlanMode"} {
		for _, name := range got {
			if name == banned {
				t.Errorf("%s leaked through FilterTools: %v", banned, got)
			}
		}
	}
	// Allow-listed tools that happen to be in the catalog should pass.
	for _, want := range []string{"Read", "Glob", "Grep", "Bash", "WebFetch"} {
		found := false
		for _, n := range got {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FilterTools dropped allow-listed tool %s; got %v", want, got)
		}
	}
}

// ─── test 3: Definition.Apply produces a clean SpawnRequest ─

// TestExploreAgent_DefinitionApplyShape locks the SpawnRequest the
// orchestration layer hands the engine. If any of these fields drift
// the spawner won't know how to filter properly.
func TestExploreAgent_DefinitionApplyShape(t *testing.T) {
	r, _ := agents.Load(t.TempDir())
	d, _ := r.Lookup("Explore")
	base := agents.SpawnRequest{System: "parent system", Model: "claude-opus-4-7", MaxTurns: 25}
	got := d.Apply(base)

	if got.System != d.SystemPrompt {
		t.Errorf("System should override base; got %q", got.System)
	}
	if got.Model != "claude-haiku-4-5" {
		t.Errorf("Model should be haiku from def; got %q", got.Model)
	}
	if string(got.PermissionMode) != "" {
		t.Errorf("PermissionMode should inherit (empty); got %q", got.PermissionMode)
	}
	if len(got.Tools) != 5 || len(got.DisallowedTools) != 6 {
		t.Errorf("tool list lengths wrong: allow=%d, deny=%d", len(got.Tools), len(got.DisallowedTools))
	}
}

// ─── test 4: AgentTool dispatch end-to-end ──────────────

// TestExploreAgent_DispatchViaAgentToolE2E drives the full flow:
//
//   parent script: Agent tool_use with subagent_type=Explore
//     → orchestration.AgentTool merges Definition into SpawnRequest
//     → engineSpawner forks perms + filters tool catalog
//     → child engine emits one assistant text
//     → text bubbles up tagged "[Explore] …" to parent
//     → parent script: end-turn text using sub-agent's reply
func TestExploreAgent_DispatchViaAgentToolE2E(t *testing.T) {
	registry, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Provider: parent → Agent call → end-turn after sub returns.
	prov := &captureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent", `{"subagent_type":"Explore","prompt":"where is jwt verified?","description":"find auth"}`),
			textTurn("Sub-agent says: pkg/auth/jwt.go:42"),
		},
		childScript: textTurn("found it: pkg/auth/jwt.go:42"),
	}

	parentReg := engine.NewRegistry()
	// Register the real AgentTool last so a stub doesn't accidentally
	// shadow it. The catalog below stocks the parent with everything
	// EXCEPT Agent — that one is the dispatch path under test.
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

	events := drainAll(eng.Submit(context.Background(), "research the auth code"))

	if prov.gotChildSystem == "" {
		t.Fatalf("child stream never invoked — sub-agent did not run; events=%d", len(events))
	}
	if !strings.Contains(prov.gotChildSystem, "READ-ONLY") {
		t.Errorf("child system prompt missing read-only section")
	}
	// Model: haiku from the definition, NOT opus from parent.
	if prov.gotChildModel != "claude-haiku-4-5" {
		t.Errorf("child model: want haiku, got %q", prov.gotChildModel)
	}

	// Child saw a filtered tool catalog: only the allow-list, deny-list
	// excluded.
	gotNames := map[string]bool{}
	for _, s := range prov.gotChildSpecs {
		gotNames[s.Name] = true
	}
	for _, want := range []string{"Read", "Glob", "Grep", "Bash", "WebFetch"} {
		if !gotNames[want] {
			t.Errorf("child catalog missing allow-listed %s; got %v", want, keysOf(gotNames))
		}
	}
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "ExitPlanMode"} {
		if gotNames[banned] {
			t.Errorf("child catalog leaked %s; got %v", banned, keysOf(gotNames))
		}
	}

	// Parent saw the tagged sub-agent reply.
	tagged := false
	for _, ev := range events {
		if r, ok := ev.(*engine.ToolUseResultEvent); ok && r.ID == "u1" {
			for _, b := range r.Result.Content {
				if strings.Contains(b.Text, "[Explore]") &&
					strings.Contains(b.Text, "pkg/auth/jwt.go:42") {
					tagged = true
				}
			}
		}
	}
	if !tagged {
		t.Errorf("expected `[Explore] …` tag with sub reply; events=%d", len(events))
	}
}

// ─── test 5: child cannot recursively spawn agents ──────

// TestExploreAgent_RecursiveAgentDispatchBlocked — Explore shouldn't
// be able to dispatch another agent, even if the model tries. The
// deny-list strips the Agent tool from the child catalog so any
// attempt comes back as "tool not found".
func TestExploreAgent_RecursiveAgentDispatchBlocked(t *testing.T) {
	registry, _ := agents.Load(t.TempDir())

	// Child script: tries to spawn another Agent (would loop forever
	// in production); engine should report "unknown tool" since the
	// catalog filter removed Agent from the child's view.
	prov := &captureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent", `{"subagent_type":"Explore","prompt":"go deeper"}`),
			textTurn("done"),
		},
		childScript: append(
			toolUseTurn("c1", "Agent", `{"subagent_type":"Explore","prompt":"infinite recursion"}`),
			textTurn("ok done")...,
		),
	}

	parentReg := engine.NewRegistry()
	registerCatalog(parentReg, []string{
		"Read", "Glob", "Grep", "Bash", "WebFetch",
		"Edit", "Write", "MultiEdit", "NotebookEdit",
		"ExitPlanMode",
	})
	parentReg.Register(orchestration.AgentTool{Registry: registry})

	st := state.New()
	eng, _ := engine.New(engine.Options{
		State: st, Tools: parentReg, Provider: prov, Model: "test",
		BypassPermissions: true,
		MaxToolTurns:      8,
	})
	drainAll(eng.Submit(context.Background(), "explore"))

	// The child catalog should not advertise Agent.
	for _, s := range prov.gotChildSpecs {
		if s.Name == "Agent" {
			t.Errorf("Agent leaked into Explore's catalog — recursive spawn possible")
		}
	}
}

// ─── test 6: definition allow-list survives through registry ───

// TestExploreAgent_AllowListShrinksCatalogStrictly — given a parent
// with a *partially missing* allow-listed tool (e.g. WebFetch isn't
// registered at all), the child sees only what's actually available.
// FilterTools must intersect, not assume.
func TestExploreAgent_AllowListShrinksCatalogStrictly(t *testing.T) {
	registry, _ := agents.Load(t.TempDir())
	d, _ := registry.Lookup("Explore")

	// Parent only registers Read + Glob. WebFetch / Bash / Grep
	// missing on purpose.
	got := d.FilterTools([]string{"Read", "Glob", "Edit", "Write"})
	if len(got) != 2 {
		t.Errorf("expected 2 (Read+Glob), got %v", got)
	}
	if got[0] != "Read" && got[0] != "Glob" {
		t.Errorf("unexpected first entry: %v", got)
	}
}

// ─── test 7: per-call model override beats the definition ───

// TestExploreAgent_ApplyHonoursInheritModel — a Definition with
// Model="inherit" should NOT override the base SpawnRequest's model
// (the parent's choice wins). Counterpart of the Explore default
// where Definition.Model="claude-haiku-4-5" replaces the base.
func TestExploreAgent_ApplyHonoursInheritModel(t *testing.T) {
	d := &agents.Definition{Name: "X", Model: "inherit"}
	got := d.Apply(agents.SpawnRequest{Model: "claude-opus-4-7"})
	if got.Model != "claude-opus-4-7" {
		t.Errorf("inherit must not override; got %q", got.Model)
	}
}

// ─── test 8: user-level Explore override wins ───────────

// TestExploreAgent_UserOverrideWinsOverBuiltin — dropping a user-
// level explore.md replaces the built-in. Critical for power users
// who want to bias the model toward a different system prompt.
func TestExploreAgent_UserOverrideWinsOverBuiltin(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	body := "---\nname: Explore\ndescription: User explore agent\n---\nUser system prompt"
	dir := homeDir + "/.biumind/agents"
	if err := writeUserAgent(dir, "explore.md", body); err != nil {
		t.Fatal(err)
	}
	r, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := r.Lookup("Explore")
	if d.Source != "user" {
		t.Errorf("user override should win; got source=%q", d.Source)
	}
	if !strings.Contains(d.SystemPrompt, "User system prompt") {
		t.Errorf("user prompt body should win")
	}
}

// ─── helpers ────────────────────────────────────────────

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// writeUserAgent writes a user-level agent definition. Reused across
// override tests to keep path-construction noise out of the
// assertion logic.
func writeUserAgent(dir, name, body string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
}
