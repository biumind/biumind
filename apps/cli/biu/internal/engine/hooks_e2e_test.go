// E2E tests for the 9 P20.55 hook events through real engine flows.
//
// hooks_lifecycle_test.go already covers SessionStart / SessionEnd /
// SubagentStop with the same markerHooks + waitForFile pattern.
// This file adds the remaining 9 events:
//
//   StopFailure         — top-level Submit fails (provider error)
//   SubagentStart       — sync Agent dispatch begins
//                         (async path covered in swarm_e2e_test.go)
//   PermissionRequest   — destructive tool reaches the gate
//   PermissionDenied    — denied by rule
//   TaskCreated         — TaskCreate tool fires
//   TaskCompleted       — TaskUpdate transitions to completed
//   FileChanged         — Edit/Write commits
//   CwdChanged          — engine.SetCwd flips
//   TeammateIdle        — already covered in swarm_e2e_test.go
//
// We reuse `markerHooks` + `waitForFile` from hooks_lifecycle_test.go
// (same package). No new harness needed.

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/files"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
)

// ─── shared helpers ─────────────────────────────────────────

// erroringProvider returns an error on the first Stream call. Drives
// the StopFailure path.
type erroringProvider struct {
	err error
}

func (p *erroringProvider) Stream(_ context.Context, _ engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	if p.err == nil {
		p.err = errors.New("synthetic provider failure")
	}
	return nil, p.err
}

// fixedScript is a tiny scripted provider keeping turn order
// (works alongside the in-package `scripted` already defined in
// plan_e2e_test.go but with no shared mutable state).
type fixedScript struct {
	mu    sync.Mutex
	turns [][]engine.StreamFrame
	idx   int
}

func newFixedScript(turns ...[]engine.StreamFrame) *fixedScript {
	return &fixedScript{turns: turns}
}

func (p *fixedScript) Stream(_ context.Context, _ engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx >= len(p.turns) {
		return nil, errors.New("fixedScript: exhausted")
	}
	frames := p.turns[p.idx]
	p.idx++
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

// hookedEngine assembles a QueryEngine wired with markerHooks for
// the listed events plus an optional permission ruleset.
type hookedEngine struct {
	eng     *engine.QueryEngine
	st      *state.AppState
	markers map[hooks.Event]string
	perms   *permissions.Context
}

func newHookedEngine(t *testing.T, prov engine.Provider, reg engine.ToolRegistry, hookEvents []hooks.Event) *hookedEngine {
	t.Helper()
	h := &hookedEngine{markers: map[hooks.Event]string{}}

	// Marker file per event, all under one TempDir.
	tmp := t.TempDir()
	for _, ev := range hookEvents {
		h.markers[ev] = filepath.Join(tmp, string(ev)+".marker")
	}
	hookReg := buildPerEventMarkers(h.markers)

	perms := permissions.NewContext()
	h.perms = perms
	h.st = state.New()

	eng, err := engine.New(engine.Options{
		State:        h.st,
		Tools:        reg,
		Provider:     prov,
		Model:        "test",
		Permissions:  perms,
		Hooks:        hookReg,
		MaxToolTurns: 8,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	h.eng = eng
	return h
}

// buildPerEventMarkers assembles a hook Registry where each event in
// `markers` runs `touch <markerPath>`. Lets a single test register
// distinct markers per event without touching markerHooks (which
// targets one path).
func buildPerEventMarkers(markers map[hooks.Event]string) *hooks.Registry {
	reg := hooks.NewRegistry()
	mapping := map[string][]json.RawMessage{}
	for ev, p := range markers {
		raw := json.RawMessage(`[{"hooks":[{"type":"command","command":"touch ` + p + `"}]}]`)
		mapping[string(ev)] = []json.RawMessage{raw}
	}
	reg.Add("test", mapping)
	return reg
}

// runEngineSubmit drives one Submit, returning all events. Does NOT
// require DoneEvent — some tests intentionally trigger the failure
// path.
func runEngineSubmit(eng *engine.QueryEngine, prompt string) []engine.Event {
	return drainAll(eng.Submit(context.Background(), prompt))
}

// ─── Case 1: StopFailure on provider error ──────────────────

func TestHooksE2E_StopFailure_OnProviderError(t *testing.T) {
	h := newHookedEngine(t, &erroringProvider{}, engine.NewRegistry(),
		[]hooks.Event{hooks.EventStopFailure, hooks.EventStop})

	runEngineSubmit(h.eng, "go")

	if !waitForFile(h.markers[hooks.EventStopFailure], 1*time.Second) {
		t.Errorf("StopFailure hook did not fire on provider error")
	}
}

// ─── Case 2: StopFailure NOT fired on clean Submit ──────────

func TestHooksE2E_StopFailure_NotFiredOnSuccess(t *testing.T) {
	prov := newFixedScript(textTurn("ok"))
	h := newHookedEngine(t, prov, engine.NewRegistry(),
		[]hooks.Event{hooks.EventStopFailure})

	runEngineSubmit(h.eng, "happy")

	// Allow time for any spurious fire.
	time.Sleep(60 * time.Millisecond)
	if _, err := os.Stat(h.markers[hooks.EventStopFailure]); err == nil {
		t.Errorf("StopFailure should not fire on clean turn")
	}
}

// ─── Case 3: SubagentStart fires on sync Agent dispatch ────

// Uses orchestration.AgentTool against a builtin agent definition
// registered by the agents package. We don't need to hit a sub-agent
// turn; SubagentStart fires inside Spawn before runSubmit runs.
func TestHooksE2E_SubagentStart_OnSyncAgent(t *testing.T) {
	registry := agents.NewRegistry()

	prov := newFixedScript(
		toolUseTurn("a1", "Agent",
			`{"subagent_type":"general-purpose","description":"d","prompt":"go"}`),
		textTurn("ack"),
		// sub-agent's first stream:
		textTurn("done"),
	)

	reg := engine.NewRegistry()
	reg.Register(orchestration.AgentTool{Registry: registry})

	h := newHookedEngine(t, prov, reg,
		[]hooks.Event{hooks.EventSubagentStart})
	h.perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"Agent"})

	runEngineSubmit(h.eng, "kick")

	if !waitForFile(h.markers[hooks.EventSubagentStart], 1*time.Second) {
		t.Errorf("SubagentStart hook did not fire on sync Agent")
	}
}

// ─── Case 4: PermissionRequest fires on gated tool ──────────

func TestHooksE2E_PermissionRequest_OnGatedTool(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")

	prov := newFixedScript(
		toolUseTurn("w1", "Write",
			`{"file_path":"`+target+`","content":"x"}`),
		textTurn("done"),
	)
	reg := engine.NewRegistry()
	reg.Register(files.WriteTool{})

	h := newHookedEngine(t, prov, reg,
		[]hooks.Event{hooks.EventPermissionRequest})
	// Allow Write so PermissionRequest fires for an allow path
	// (the hook fires regardless of decision — this is the
	// observability use case).
	h.perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"Write"})
	h.eng.SetCwd(dir)

	runEngineSubmit(h.eng, "go")

	if !waitForFile(h.markers[hooks.EventPermissionRequest], 1*time.Second) {
		t.Errorf("PermissionRequest hook did not fire")
	}
}

// ─── Case 5: PermissionDenied fires on deny rule ────────────

func TestHooksE2E_PermissionDenied_OnDenyRule(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "blocked.txt")

	prov := newFixedScript(
		toolUseTurn("w1", "Write",
			`{"file_path":"`+target+`","content":"x"}`),
		textTurn("done"),
	)
	reg := engine.NewRegistry()
	reg.Register(files.WriteTool{})

	h := newHookedEngine(t, prov, reg,
		[]hooks.Event{hooks.EventPermissionDenied})
	h.perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorDeny,
		[]string{"Write"})
	h.eng.SetCwd(dir)

	runEngineSubmit(h.eng, "blocked")

	if !waitForFile(h.markers[hooks.EventPermissionDenied], 1*time.Second) {
		t.Errorf("PermissionDenied hook did not fire on deny rule")
	}
}

// ─── Case 6: TaskCreated fires on TaskCreate tool ──────────

func TestHooksE2E_TaskCreated_OnTaskCreate(t *testing.T) {
	prov := newFixedScript(
		toolUseTurn("tc1", "TaskCreate",
			`{"subject":"finish docs","description":"draft API reference"}`),
		textTurn("ack"),
	)
	reg := engine.NewRegistry()
	reg.Register(orchestration.TaskCreateTool{})

	h := newHookedEngine(t, prov, reg,
		[]hooks.Event{hooks.EventTaskCreated})
	h.perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"TaskCreate"})

	runEngineSubmit(h.eng, "track work")

	if !waitForFile(h.markers[hooks.EventTaskCreated], 1*time.Second) {
		t.Errorf("TaskCreated hook did not fire")
	}
}

// ─── Case 7: TaskCompleted fires on transition ──────────────

// Two-Submit pattern: TaskCreate runs first so we can read the
// process-global counter's actual assignment from the engine state,
// then build TaskUpdate's args with the right id.
func TestHooksE2E_TaskCompleted_OnTransition(t *testing.T) {
	reg := engine.NewRegistry()
	reg.Register(orchestration.TaskCreateTool{})
	reg.Register(orchestration.TaskUpdateTool{})

	st := state.New()
	perms := permissions.NewContext()
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"TaskCreate", "TaskUpdate"})
	markers := map[hooks.Event]string{
		hooks.EventTaskCompleted: filepath.Join(t.TempDir(), "taskcompleted.marker"),
	}
	hookReg := buildPerEventMarkers(markers)

	prov := newFixedScript(
		toolUseTurn("tc1", "TaskCreate", `{"subject":"draft","description":"d"}`),
		textTurn("created"),
	)
	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions: perms, Hooks: hookReg, MaxToolTurns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	runEngineSubmit(eng, "create one")

	// Discover the assigned id from state.
	tasks := st.TasksSnapshot()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	id := string(tasks[0].ID)

	// Submit 2 with discovered id. Append turns to the script.
	prov.mu.Lock()
	prov.turns = append(prov.turns,
		toolUseTurn("tu1", "TaskUpdate", `{"taskId":"`+id+`","status":"completed"}`),
		textTurn("done"),
	)
	prov.mu.Unlock()

	runEngineSubmit(eng, "complete it")

	if !waitForFile(markers[hooks.EventTaskCompleted], 1*time.Second) {
		t.Errorf("TaskCompleted hook did not fire on completion transition")
	}
}

// ─── Case 8: TaskCompleted hook fire-once (smoke check) ────

// We can't easily prove "didn't fire twice" with file existence, so
// this case asserts the simpler invariant: the same redundant
// TaskUpdate doesn't crash the engine and the marker exists. The
// transition-only guard is locked by task_hook_test.go's unit cases.
func TestHooksE2E_TaskCompleted_RedundantUpdate_NoCrash(t *testing.T) {
	reg := engine.NewRegistry()
	reg.Register(orchestration.TaskCreateTool{})
	reg.Register(orchestration.TaskUpdateTool{})

	st := state.New()
	perms := permissions.NewContext()
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"TaskCreate", "TaskUpdate"})
	markers := map[hooks.Event]string{
		hooks.EventTaskCompleted: filepath.Join(t.TempDir(), "taskcompleted.marker"),
	}
	hookReg := buildPerEventMarkers(markers)

	prov := newFixedScript(
		toolUseTurn("tc1", "TaskCreate", `{"subject":"x","description":"d"}`),
		textTurn("created"),
	)
	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions: perms, Hooks: hookReg, MaxToolTurns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	runEngineSubmit(eng, "create")
	id := string(st.TasksSnapshot()[0].ID)

	prov.mu.Lock()
	prov.turns = append(prov.turns,
		toolUseTurn("tu1", "TaskUpdate", `{"taskId":"`+id+`","status":"completed"}`),
		toolUseTurn("tu2", "TaskUpdate", `{"taskId":"`+id+`","status":"completed"}`),
		textTurn("ok"),
	)
	prov.mu.Unlock()
	runEngineSubmit(eng, "double-complete")

	if !waitForFile(markers[hooks.EventTaskCompleted], 1*time.Second) {
		t.Errorf("TaskCompleted should fire at least on the first transition")
	}
}

// ─── Case 9: FileChanged fires on Edit ──────────────────────

func TestHooksE2E_FileChanged_OnWrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "code.go")

	prov := newFixedScript(
		toolUseTurn("w1", "Write",
			`{"file_path":"`+target+`","content":"package main\n"}`),
		textTurn("done"),
	)
	reg := engine.NewRegistry()
	reg.Register(files.WriteTool{})

	h := newHookedEngine(t, prov, reg,
		[]hooks.Event{hooks.EventFileChanged})
	h.perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"Write"})
	h.eng.SetCwd(dir)

	runEngineSubmit(h.eng, "go")

	if !waitForFile(h.markers[hooks.EventFileChanged], 1*time.Second) {
		t.Errorf("FileChanged hook did not fire on Write")
	}
}

// ─── Case 10: FileChanged does NOT fire on Read ─────────────

func TestHooksE2E_FileChanged_NotOnRead(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "code.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := newFixedScript(
		toolUseTurn("r1", "Read", `{"file_path":"`+target+`"}`),
		textTurn("ack"),
	)
	reg := engine.NewRegistry()
	reg.Register(files.ReadTool{})

	h := newHookedEngine(t, prov, reg,
		[]hooks.Event{hooks.EventFileChanged})
	h.perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"Read"})
	h.eng.SetCwd(dir)

	runEngineSubmit(h.eng, "go")

	time.Sleep(60 * time.Millisecond)
	if _, err := os.Stat(h.markers[hooks.EventFileChanged]); err == nil {
		t.Errorf("FileChanged should not fire on Read")
	}
}

// ─── Case 11: CwdChanged fires on SetCwd ────────────────────

func TestHooksE2E_CwdChanged_OnSetCwd(t *testing.T) {
	prov := newFixedScript(textTurn("ok"))
	h := newHookedEngine(t, prov, engine.NewRegistry(),
		[]hooks.Event{hooks.EventCwdChanged})

	dir := t.TempDir()
	h.eng.SetCwd(dir)

	if !waitForFile(h.markers[hooks.EventCwdChanged], 1*time.Second) {
		t.Errorf("CwdChanged did not fire on SetCwd")
	}
}

// ─── Case 12: CwdChanged does NOT fire on same-value set ────

func TestHooksE2E_CwdChanged_NotOnSameValue(t *testing.T) {
	prov := newFixedScript(textTurn("ok"))
	h := newHookedEngine(t, prov, engine.NewRegistry(),
		[]hooks.Event{hooks.EventCwdChanged})

	dir := t.TempDir()
	h.eng.SetCwd(dir)
	if !waitForFile(h.markers[hooks.EventCwdChanged], 1*time.Second) {
		t.Fatalf("first SetCwd should fire")
	}
	// Reset marker by deleting and trying again; the second SetCwd
	// to the same value should NOT recreate it.
	if err := os.Remove(h.markers[hooks.EventCwdChanged]); err != nil {
		t.Fatal(err)
	}
	h.eng.SetCwd(dir) // same value

	time.Sleep(60 * time.Millisecond)
	if _, err := os.Stat(h.markers[hooks.EventCwdChanged]); err == nil {
		t.Errorf("CwdChanged should not fire when value unchanged")
	}
}

// ─── Case 13: PermissionRequest payload includes rule_decision ─

// Asserts the hook payload uses the *string label* (allow/ask/deny)
// rather than the int iota — a regression guard for
// permissionDecisionLabel mapping.
//
// Implementation: register a hook that writes the payload into a
// file (rather than just touching), then read and assert.
func TestHooksE2E_PermissionRequest_PayloadHasStringLabel(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	logFile := filepath.Join(dir, "hook.log")

	prov := newFixedScript(
		toolUseTurn("w1", "Write",
			`{"file_path":"`+target+`","content":"x"}`),
		textTurn("done"),
	)
	reg := engine.NewRegistry()
	reg.Register(files.WriteTool{})

	st := state.New()
	perms := permissions.NewContext()
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"Write"})

	// Custom hook that captures stdin to logFile.
	hookReg := hooks.NewRegistry()
	hookReg.Add("test", map[string][]json.RawMessage{
		string(hooks.EventPermissionRequest): {
			json.RawMessage(`[{"hooks":[{"type":"command","command":"cat > ` + logFile + `"}]}]`),
		},
	})

	eng, err := engine.New(engine.Options{
		State:        st,
		Tools:        reg,
		Provider:     prov,
		Model:        "test",
		Permissions:  perms,
		Hooks:        hookReg,
		MaxToolTurns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.SetCwd(dir)

	runEngineSubmit(eng, "go")

	if !waitForFile(logFile, 1*time.Second) {
		t.Fatalf("PermissionRequest hook did not run")
	}
	body, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `"rule_decision"`) {
		t.Fatalf("payload missing rule_decision: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"allow"`) {
		t.Errorf("rule_decision should be string 'allow'; got: %s", bodyStr)
	}
}
