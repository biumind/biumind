// End-to-end tests for the QueryEngine. We use a fake Provider that
// emits scripted StreamFrame sequences and a tiny Tool implementation
// to exercise the full turn loop without a real LLM.

package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// ─── Fake provider ────────────────────────────────────

// scriptedProvider emits a sequence of frame slices — one per turn.
// The first call to Stream pulls scripts[0], second call scripts[1],
// etc. Useful for testing multi-turn tool loops.
type scriptedProvider struct {
	scripts [][]StreamFrame
	calls   int
}

func (p *scriptedProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamFrame, error) {
	if p.calls >= len(p.scripts) {
		return nil, errors.New("scripted provider out of scripts")
	}
	frames := p.scripts[p.calls]
	p.calls++
	ch := make(chan StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

// erroredProvider always fails Stream up-front.
type erroredProvider struct{ err error }

func (p *erroredProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamFrame, error) {
	return nil, p.err
}

// ─── Fake tools ───────────────────────────────────────

type fakeTool struct {
	name              string
	readOnly          bool
	destructive       bool
	concurrencySafe   bool
	interruptBehavior string
	calls             int
	respond           func(input map[string]any) (*ToolResultPayload, error)
}

func (t *fakeTool) Name() string                            { return t.name }
func (t *fakeTool) Description(_ map[string]any) string     { return "fake " + t.name }
func (t *fakeTool) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (t *fakeTool) IsReadOnly(_ map[string]any) bool        { return t.readOnly }
func (t *fakeTool) IsDestructive(_ map[string]any) bool     { return t.destructive }
func (t *fakeTool) IsConcurrencySafe(_ map[string]any) bool { return t.concurrencySafe }
func (t *fakeTool) InterruptBehavior() string               { return t.interruptBehavior }
func (t *fakeTool) Call(ctx context.Context, input map[string]any, env *ToolEnv) (*ToolResultPayload, error) {
	t.calls++
	if t.respond != nil {
		return t.respond(input)
	}
	return &ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: "ok-" + t.name}},
	}, nil
}

// ─── Helpers ──────────────────────────────────────────

// drainAll reads every event from ch into a slice (channel must close).
func drainAll(ch <-chan Event) []Event {
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

// hasEvent returns true when at least one event matches the predicate.
func hasEvent[T Event](events []Event, pred func(T) bool) bool {
	for _, ev := range events {
		if t, ok := ev.(T); ok {
			if pred == nil || pred(t) {
				return true
			}
		}
	}
	return false
}

// ─── Tests ────────────────────────────────────────────

func TestEngineEndTurnImmediately(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		textTurn("Hello there!"),
	}}
	st := state.New()
	reg := NewRegistry()
	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := eng.Submit(context.Background(), "hi")
	events := drainAll(ch)

	if !hasEvent(events, func(*DoneEvent) bool { return true }) {
		t.Errorf("expected DoneEvent")
	}
	// Should have AssistantMessageEvent containing the text
	var got string
	for _, ev := range events {
		if a, ok := ev.(*AssistantMessageEvent); ok {
			for _, b := range a.Message.Content {
				if b.Type == state.ContentText {
					got = b.Text
				}
			}
		}
	}
	if got != "Hello there!" {
		t.Errorf("assistant text = %q", got)
	}
	// State should have user + assistant messages.
	if got := len(st.Snapshot()); got != 2 {
		t.Errorf("expected 2 messages in state, got %d", got)
	}
}

func TestEngineSingleToolLoop(t *testing.T) {
	// Turn 1: assistant says "let me check" + tool_use Read({path:"/a"}).
	// Turn 2: assistant says "found it" + end_turn.
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("Reading", "tu_1", "Read", `{"path":"/a"}`),
		textTurn("found it"),
	}}
	st := state.New()
	reg := NewRegistry()
	read := &fakeTool{name: "Read", readOnly: true, concurrencySafe: true}
	reg.Register(read)

	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	ch := eng.Submit(context.Background(), "tell me what's in /a")
	events := drainAll(ch)

	if read.calls != 1 {
		t.Errorf("expected 1 tool call, got %d", read.calls)
	}
	if !hasEvent(events, func(s *ToolUseStartEvent) bool { return s.Name == "Read" }) {
		t.Errorf("missing ToolUseStartEvent")
	}
	if !hasEvent(events, func(r *ToolUseResultEvent) bool { return r.Name == "Read" && !r.Result.IsError }) {
		t.Errorf("missing successful ToolUseResultEvent")
	}
	if !hasEvent(events, func(*DoneEvent) bool { return true }) {
		t.Errorf("missing DoneEvent")
	}
	// State should have user + assistant + tool_result + assistant = 4
	if got := len(st.Snapshot()); got != 4 {
		t.Errorf("messages = %d", got)
	}
}

func TestEngineUnknownToolSoftErrors(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("Trying", "tu_1", "Frobnicate", `{}`),
		textTurn("oh well, sorry"),
	}}
	st := state.New()
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	events := drainAll(eng.Submit(context.Background(), "do something weird"))

	// Engine should report a soft error in the result, not crash the turn.
	if !hasEvent(events, func(r *ToolUseResultEvent) bool {
		return r.Result.IsError && r.Name == "Frobnicate"
	}) {
		t.Errorf("expected soft error result for unknown tool")
	}
	// Loop continues to a clean Done.
	if !hasEvent(events, func(*DoneEvent) bool { return true }) {
		t.Errorf("expected loop to continue past unknown tool")
	}
}

func TestEngineParallelBatch(t *testing.T) {
	// Two safe tools in one assistant turn → parallel.
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		twoToolsTurn(
			"tu_1", "Read", `{"path":"/a"}`,
			"tu_2", "Glob", `{"pattern":"*.go"}`),
		textTurn("done"),
	}}
	st := state.New()
	reg := NewRegistry()
	read := &fakeTool{name: "Read", readOnly: true, concurrencySafe: true}
	glob := &fakeTool{name: "Glob", readOnly: true, concurrencySafe: true}
	reg.Register(read)
	reg.Register(glob)

	eng, _ := New(Options{State: st, Tools: reg, Provider: prov, Model: "test", BypassPermissions: true})
	events := drainAll(eng.Submit(context.Background(), "scan repo"))

	if read.calls != 1 || glob.calls != 1 {
		t.Errorf("each tool should run once: read=%d glob=%d", read.calls, glob.calls)
	}
	// One tool_result message containing two blocks, in order.
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleUser || len(m.Content) != 2 {
			continue
		}
		if m.Content[0].Type == state.ContentToolResult &&
			m.Content[0].ToolResultID == "tu_1" &&
			m.Content[1].ToolResultID == "tu_2" {
			return // pass
		}
	}
	t.Errorf("did not find a tool_result message with both blocks in order: %+v", st.Snapshot())
	_ = events
}

func TestEngineDestructiveToolPermissionAsk(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("running rm", "tu_1", "Bash", `{"command":"rm -rf /tmp/x"}`),
		textTurn("cleanup done"),
	}}
	st := state.New()
	reg := NewRegistry()
	bash := &fakeTool{
		name: "Bash", destructive: true,
	}
	reg.Register(bash)

	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		// Note: BypassPermissions=false → ask.
	})
	ch := eng.Submit(context.Background(), "wipe /tmp/x")

	// Drain in goroutine, intercept PermissionAskEvent and approve.
	doneCh := make(chan struct{})
	var events []Event
	go func() {
		for ev := range ch {
			events = append(events, ev)
			if ask, ok := ev.(*PermissionAskEvent); ok {
				ask.Decision <- PermissionAnswer{Decision: PermAllow}
			}
		}
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("engine hung")
	}

	if bash.calls != 1 {
		t.Errorf("bash should have run after approve; calls=%d", bash.calls)
	}
	if !hasEvent(events, func(*PermissionAskEvent) bool { return true }) {
		t.Errorf("missing PermissionAskEvent")
	}
}

func TestEngineDestructiveToolDenied(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("running rm", "tu_1", "Bash", `{"command":"rm -rf /"}`),
		textTurn("ack denied"),
	}}
	st := state.New()
	reg := NewRegistry()
	bash := &fakeTool{name: "Bash", destructive: true}
	reg.Register(bash)
	eng, _ := New(Options{State: st, Tools: reg, Provider: prov, Model: "test"})
	ch := eng.Submit(context.Background(), "destroy")

	doneCh := make(chan struct{})
	var events []Event
	go func() {
		for ev := range ch {
			events = append(events, ev)
			if ask, ok := ev.(*PermissionAskEvent); ok {
				ask.Decision <- PermissionAnswer{Decision: PermDeny}
			}
		}
		close(doneCh)
	}()
	<-doneCh

	if bash.calls != 0 {
		t.Errorf("bash should NOT run after deny; calls=%d", bash.calls)
	}
	if !hasEvent(events, func(r *ToolUseResultEvent) bool {
		return r.Result.IsError
	}) {
		t.Errorf("missing soft-error result for denied call")
	}
}

func TestEngineProviderError(t *testing.T) {
	prov := &erroredProvider{err: errors.New("503 service unavailable")}
	st := state.New()
	reg := NewRegistry()
	eng, _ := New(Options{State: st, Tools: reg, Provider: prov, Model: "test"})
	events := drainAll(eng.Submit(context.Background(), "hi"))

	if !hasEvent(events, func(e *ErrorEvent) bool { return e.Source == ErrSrcLLM }) {
		t.Errorf("expected provider ErrorEvent")
	}
}

func TestEngineConcurrentSubmitRejected(t *testing.T) {
	// A provider that blocks until the test signals — lets us hold
	// the inflight lock long enough to make a second call.
	hold := make(chan struct{})
	prov := &blockingProvider{hold: hold,
		next: textTurn("ok")}

	st := state.New()
	reg := NewRegistry()
	eng, _ := New(Options{State: st, Tools: reg, Provider: prov, Model: "test", BypassPermissions: true})

	ch1 := eng.Submit(context.Background(), "first")
	// Give the goroutine a moment to reach inflight.Lock.
	time.Sleep(20 * time.Millisecond)
	ch2 := eng.Submit(context.Background(), "second")

	// Second submission should immediately error.
	saw := false
	for ev := range ch2 {
		if e, ok := ev.(*ErrorEvent); ok && errors.Is(e.Err, ErrConcurrentSubmit) {
			saw = true
		}
	}
	if !saw {
		t.Errorf("expected ErrConcurrentSubmit from second Submit")
	}

	close(hold) // unblock first
	drainAll(ch1)
}

type blockingProvider struct {
	hold <-chan struct{}
	next []StreamFrame
}

func (p *blockingProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamFrame, error) {
	<-p.hold
	ch := make(chan StreamFrame, len(p.next))
	for _, f := range p.next {
		ch <- f
	}
	close(ch)
	return ch, nil
}

// ─── Frame fixtures ───────────────────────────────────

// textTurn is a frame slice for an assistant turn that just says
// `text` then end_turn.
func textTurn(text string) []StreamFrame {
	return []StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "test"}},
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{Type: "text"}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{Type: "text_delta", Text: text}},
		{Type: FrameContentBlockStop, Index: 0},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "end_turn"}},
		{Type: FrameMessageStop},
	}
}

// toolUseTurn is a frame slice for an assistant turn with intro text
// + a single tool_use block + stop_reason=tool_use.
func toolUseTurn(intro, useID, name, inputJSON string) []StreamFrame {
	return []StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "test"}},
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{Type: "text"}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{Type: "text_delta", Text: intro}},
		{Type: FrameContentBlockStop, Index: 0},
		{Type: FrameContentBlockStart, Index: 1, ContentBlock: &StreamBlockHead{
			Type: "tool_use", ID: useID, Name: name,
		}},
		{Type: FrameContentBlockDelta, Index: 1, Delta: &StreamDelta{
			Type: "input_json_delta", PartialJSON: inputJSON,
		}},
		{Type: FrameContentBlockStop, Index: 1},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "tool_use"}},
		{Type: FrameMessageStop},
	}
}

// twoToolsTurn produces two parallel tool_use blocks in a single turn.
func twoToolsTurn(id1, name1, in1, id2, name2, in2 string) []StreamFrame {
	return []StreamFrame{
		{Type: FrameMessageStart, Message: &StreamMessageHead{Model: "test"}},
		{Type: FrameContentBlockStart, Index: 0, ContentBlock: &StreamBlockHead{
			Type: "tool_use", ID: id1, Name: name1,
		}},
		{Type: FrameContentBlockDelta, Index: 0, Delta: &StreamDelta{
			Type: "input_json_delta", PartialJSON: in1,
		}},
		{Type: FrameContentBlockStop, Index: 0},
		{Type: FrameContentBlockStart, Index: 1, ContentBlock: &StreamBlockHead{
			Type: "tool_use", ID: id2, Name: name2,
		}},
		{Type: FrameContentBlockDelta, Index: 1, Delta: &StreamDelta{
			Type: "input_json_delta", PartialJSON: in2,
		}},
		{Type: FrameContentBlockStop, Index: 1},
		{Type: FrameMessageDelta, Delta: &StreamDelta{StopReason: "tool_use"}},
		{Type: FrameMessageStop},
	}
}

// TestMalformedToolInputSelfHeals verifies the glm-truncation bug fix:
// a tool_use whose streamed input JSON is incomplete (upstream cut
// mid-argument) must NOT abort the turn. The engine synthesises a
// soft-error tool_result for the malformed block, loops back, and the
// model re-emits cleanly on the next round. No fatal ErrorEvent escapes.
func TestMalformedToolInputSelfHeals(t *testing.T) {
	// Turn 1: tool_use with truncated input JSON `{"` (the bug scenario).
	// Turn 2: clean text after the model sees the soft-error result.
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("calling tool", "tu_1", "Echo", `{"`),
		textTurn("recovered"),
	}}
	st := state.New()
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	events := drainAll(eng.Submit(context.Background(), "do the thing"))

	// Engine must loop: turn 1 self-healed, turn 2 produced clean text.
	if prov.calls != 2 {
		t.Fatalf("expected provider called 2× (self-heal loop), got %d; events: %v", prov.calls, events)
	}
	// Malformed tool_use must yield a synthesised error tool_result.
	sawErrResult := false
	for _, ev := range events {
		if r, ok := ev.(*ToolUseResultEvent); ok && r.ID == "tu_1" && r.Result.IsError {
			sawErrResult = true
		}
	}
	if !sawErrResult {
		t.Errorf("expected soft-error ToolUseResultEvent for malformed tu_1")
	}
	// No terminal ErrorEvent — recovery is in-band via tool_result.
	for _, ev := range events {
		if e, ok := ev.(*ErrorEvent); ok {
			t.Errorf("malformed self-heal must not emit fatal ErrorEvent: %+v", e)
		}
	}
	// Turn reached a clean end after self-heal.
	if !hasEvent(events, func(d *DoneEvent) bool { return d.StopReason == "end_turn" }) {
		t.Errorf("expected Done{end_turn} after self-heal; events: %v", events)
	}
}

// TestMalformedToolInputPersistsAborts verifies the cost-bomb guard: if
// the model (or a stuck upstream) keeps emitting malformed tool input,
// the engine gives up after maxConsecutiveMalformed turns instead of
// looping until maxToolTurns.
func TestMalformedToolInputPersistsAborts(t *testing.T) {
	// Every turn is malformed — never recovers.
	scripts := make([][]StreamFrame, maxConsecutiveMalformed+2)
	for i := range scripts {
		scripts[i] = toolUseTurn("again", "tu_"+strconv.Itoa(i), "Echo", `{"`)
	}
	prov := &scriptedProvider{scripts: scripts}
	st := state.New()
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	events := drainAll(eng.Submit(context.Background(), "do the thing"))

	// Must abort with a non-recoverable ErrorEvent around the cap, not
	// run all maxToolTurns iterations.
	sawFatal := false
	for _, ev := range events {
		if e, ok := ev.(*ErrorEvent); ok && !e.Recoverable {
			sawFatal = true
		}
	}
	if !sawFatal {
		t.Errorf("expected fatal ErrorEvent after %d consecutive malformed turns; events: %v", maxConsecutiveMalformed, events)
	}
	if prov.calls > maxConsecutiveMalformed+1 {
		t.Errorf("engine ran %d turns, expected abort around %d", prov.calls, maxConsecutiveMalformed+1)
	}
}

// ─── SystemForTurn (Working directories injection) ────

func TestSystemForTurn_NoExtras_ReturnsBase(t *testing.T) {
	st := state.New()
	st.OriginalCwd = "/repo"
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: &scriptedProvider{},
		Model: "test", System: "Base prompt", BypassPermissions: true,
	})
	got := eng.SystemForTurn()
	if got != "Base prompt" {
		t.Errorf("no extras → base only; got %q", got)
	}
}

func TestSystemForTurn_WithExtras_AppendsBlock(t *testing.T) {
	st := state.New()
	st.OriginalCwd = "/repo"
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: &scriptedProvider{},
		Model: "test", System: "Base prompt", BypassPermissions: true,
	})
	eng.Permissions().AddDirectories("session", []string{"/tmp/proj"})
	got := eng.SystemForTurn()
	if !strings.Contains(got, "Base prompt") {
		t.Errorf("missing base: %q", got)
	}
	if !strings.Contains(got, "Working directories") {
		t.Errorf("missing dirs block: %q", got)
	}
	if !strings.Contains(got, "/tmp/proj") {
		t.Errorf("missing extra dir: %q", got)
	}
	if !strings.Contains(got, "/repo") {
		t.Errorf("missing originalCwd: %q", got)
	}
}

func TestSystemForTurn_DynamicAddPropagates(t *testing.T) {
	st := state.New()
	st.OriginalCwd = "/repo"
	reg := NewRegistry()
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: &scriptedProvider{},
		Model: "test", BypassPermissions: true,
	})
	if got := eng.SystemForTurn(); strings.Contains(got, "Working directories") {
		t.Errorf("empty ctx → no dirs block; got %q", got)
	}
	eng.Permissions().AddDirectories("session", []string{"/tmp/late"})
	if got := eng.SystemForTurn(); !strings.Contains(got, "/tmp/late") {
		t.Errorf("late add not visible; got %q", got)
	}
}

// ─── Sub-agent ctx inheritance ─────────────────────────

func TestForkPerms_InheritsDirsAndCwd(t *testing.T) {
	parent := permissions.NewContext()
	parent.SetOriginalCwd("/repo")
	parent.AddDirectories(permissions.SrcSession, []string{"/tmp/extra"})

	child := forkPerms(parent, permissions.ModeDefault)
	if got := child.OriginalCwd(); got != "/repo" {
		t.Errorf("child must inherit OriginalCwd; got %q", got)
	}
	dirs := child.AdditionalDirectoryPaths()
	if len(dirs) != 1 || dirs[0] != "/tmp/extra" {
		t.Errorf("child must inherit additional dirs; got %+v", dirs)
	}
	src, ok := child.DirectorySource("/tmp/extra")
	if !ok || src != permissions.SrcSession {
		t.Errorf("child should preserve source; got %s ok=%v", src, ok)
	}
}

func TestForkPerms_SharesRuntimeViaPointerForUnchangedMode(t *testing.T) {
	// Direct Spawn (without PermissionMode override) shares the
	// parent's ctx pointer outright (s.parent.perms). The child
	// sees parent's /add-dir live. Verified by writing through one
	// pointer, reading through the other.
	parent := permissions.NewContext()
	child := parent // simulating what Spawn does without PermissionMode
	parent.AddDirectories(permissions.SrcSession, []string{"/tmp/late"})
	if got := child.AdditionalDirectoryPaths(); len(got) != 1 || got[0] != "/tmp/late" {
		t.Errorf("shared pointer must see late add; got %+v", got)
	}
}

// ─── Permission ask suggestions ────────────────────────

func TestGenerateAskSuggestions_WorkingDirReason(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.SetOriginalCwd("/repo")

	in := runnerInput{
		Name:  "Read",
		Input: map[string]any{"file_path": "/etc/hosts"},
	}
	reason := permissions.Reason{
		Kind:   "workingDir",
		Detail: "outside working dirs",
	}
	got := generateAskSuggestions(in, reason, ctx)
	if len(got) != 1 {
		t.Fatalf("expected 1 suggestion, got %d: %+v", len(got), got)
	}
	if got[0].HotKey != "w" {
		t.Errorf("HotKey = %q, want w", got[0].HotKey)
	}
	if !strings.Contains(got[0].Label, "etc") {
		t.Errorf("Label should reference parent dir; got %q", got[0].Label)
	}
}

func TestGenerateAskSuggestions_NonWorkingDirReason_ReturnsNil(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.SetOriginalCwd("/repo")

	in := runnerInput{
		Name:  "Bash",
		Input: map[string]any{"command": "rm -rf /"},
	}
	reason := permissions.Reason{Kind: "default", Detail: "first use"}
	got := generateAskSuggestions(in, reason, ctx)
	if got != nil {
		t.Errorf("non-workingDir reason must yield nil; got %+v", got)
	}
}

func TestGenerateAskSuggestions_ParentAlreadyCovered_ReturnsNil(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.SetOriginalCwd("/repo")
	// Pretend /tmp/scratch is already a working dir.
	ctx.AddDirectories("session", []string{"/tmp/scratch"})

	in := runnerInput{
		Name:  "Read",
		Input: map[string]any{"file_path": "/tmp/scratch/x.go"},
	}
	reason := permissions.Reason{Kind: "workingDir"}
	got := generateAskSuggestions(in, reason, ctx)
	if got != nil {
		t.Errorf("parent already covered should yield nil; got %+v", got)
	}
}

func TestEngineWorkingDirAskEmitsSuggestion(t *testing.T) {
	// Read on a path outside cwd → ask with a "Allow + add dir"
	// suggestion. Simulate user picking the suggestion, verify the
	// dir lands in ctx after approval.
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("reading", "tu_1", "Read", `{"file_path":"/tmp/outside/x.go"}`),
		textTurn("got it"),
	}}
	st := state.New()
	st.OriginalCwd = "/repo"
	reg := NewRegistry()
	read := &fakeTool{name: "Read", readOnly: true, concurrencySafe: true}
	reg.Register(read)

	pCtx := permissions.NewContext()
	pCtx.SetOriginalCwd("/repo")
	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions: pCtx,
	})

	doneCh := make(chan struct{})
	var sawSuggestion bool
	go func() {
		for ev := range eng.Submit(context.Background(), "read it") {
			if ask, ok := ev.(*PermissionAskEvent); ok {
				if len(ask.Suggestions) == 1 && ask.Suggestions[0].HotKey == "w" {
					sawSuggestion = true
					// Pick the suggestion via AppliedUpdates.
					ask.Decision <- PermissionAnswer{
						Decision: PermAllow,
						AppliedUpdates: []sdkproto.PermissionUpdate{
							ask.Suggestions[0].Update,
						},
					}
				} else {
					ask.Decision <- PermissionAnswer{Decision: PermAllow}
				}
			}
		}
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("engine hung")
	}

	if !sawSuggestion {
		t.Fatalf("expected workingDir suggestion in PermissionAskEvent")
	}
	if read.calls != 1 {
		t.Errorf("Read should have run after approve; calls=%d", read.calls)
	}
	// The applied suggestion adds /tmp/outside (parent of x.go) to ctx.
	dirs := pCtx.AdditionalDirectoryPaths()
	if len(dirs) != 1 || dirs[0] != "/tmp/outside" {
		t.Errorf("expected /tmp/outside in ctx after suggestion; got %+v", dirs)
	}
}
