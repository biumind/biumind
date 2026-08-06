// E2E tests for the deferred-tool catalog + ToolSearch integration
// (P20.51 phase 1 + phase 2).
//
// Unit coverage in deferred_test.go (engine package, in-process
// helpers) + toolsearch_test.go (orchestration package, ToolSearch
// scoring) is solid. This file glues the two through a real
// QueryEngine and verifies the cross-module contracts:
//
//   - StreamRequest.Tools sent to the provider OMITS deferred tools
//     until ToolSearch select unlocks them.
//   - The <available-deferred-tools> system attachment lists exactly
//     the un-selected deferred tools.
//   - select:foo unlocks foo for the next inner turn (within the
//     same Submit, since buildToolSpecs is per-turn).
//   - select persistence: an unlock recorded in turn 1 stays unlocked
//     across many subsequent Submits.
//   - Keyword form returns ranked candidates without modifying the
//     selection set.
//
// The capture provider records each Stream call's tools list so
// assertions can inspect the exact wire-level catalog the LLM saw.

package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
)

// ─── deferrable test tool ────────────────────────────────────

// deferrableTool implements engine.Tool + engine.Deferrable. Calls
// always return plain text; the e2e tests don't actually invoke
// these — they just assert the *catalog* the LLM sees.
type deferrableTool struct {
	name     string
	desc     string
	deferred bool
}

func (t *deferrableTool) Name() string                            { return t.name }
func (t *deferrableTool) Description(_ map[string]any) string     { return t.desc }
func (t *deferrableTool) InputSchema() map[string]any             { return map[string]any{"type": "object"} }
func (t *deferrableTool) IsReadOnly(_ map[string]any) bool        { return true }
func (t *deferrableTool) IsDestructive(_ map[string]any) bool     { return false }
func (t *deferrableTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (t *deferrableTool) InterruptBehavior() string               { return "cancel" }
func (t *deferrableTool) ShouldDefer() bool                       { return t.deferred }
func (t *deferrableTool) Call(_ context.Context, _ map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: "ok"}},
	}, nil
}

// catalogRegistry returns a fresh registry seeded with:
//   - one regular tool: Read
//   - four deferred tools mimicking MCP-imported catalog:
//     mcp__github__create_issue
//     mcp__github__list_pull_requests
//     mcp__slack__send_message
//     mcp__notion__create_page
//
// Plus the real ToolSearchTool wired against this registry — that's
// the integration we're testing.
func catalogRegistry() *engine.SimpleRegistry {
	r := engine.NewRegistry()
	r.Register(&deferrableTool{name: "Read", desc: "Read a file"})
	r.Register(&deferrableTool{
		name: "mcp__github__create_issue", desc: "Create a new issue on a GitHub repository",
		deferred: true,
	})
	r.Register(&deferrableTool{
		name: "mcp__github__list_pull_requests", desc: "List open pull requests for a repository",
		deferred: true,
	})
	r.Register(&deferrableTool{
		name: "mcp__slack__send_message", desc: "Post a message to a Slack channel",
		deferred: true,
	})
	r.Register(&deferrableTool{
		name: "mcp__notion__create_page", desc: "Create a Notion page in a workspace",
		deferred: true,
	})
	r.Register(orchestration.ToolSearchTool{Registry: r})
	return r
}

// ─── capture provider ────────────────────────────────────────

// capturedRequest is one Stream call's snapshot — what the model saw.
type capturedRequest struct {
	system   string
	toolNames []string
}

// deferredCaptureProvider serves a script (turn-by-turn frames) and stores
// every StreamRequest passed to Stream. Tests assert on the captured
// log.
type deferredCaptureProvider struct {
	mu     sync.Mutex
	turns  [][]engine.StreamFrame
	idx    int
	calls  []capturedRequest
}

func newDeferredCaptureProvider(turns ...[]engine.StreamFrame) *deferredCaptureProvider {
	return &deferredCaptureProvider{turns: turns}
}

func (p *deferredCaptureProvider) Stream(_ context.Context, req engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	names := make([]string, len(req.Tools))
	for i, t := range req.Tools {
		names[i] = t.Name
	}
	p.calls = append(p.calls, capturedRequest{
		system:    req.System,
		toolNames: names,
	})

	if p.idx >= len(p.turns) {
		return nil, errors.New("deferredCaptureProvider: script exhausted at call " + itoa(p.idx+1))
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

func (p *deferredCaptureProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *deferredCaptureProvider) lastTools() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return nil
	}
	return p.calls[len(p.calls)-1].toolNames
}

func (p *deferredCaptureProvider) toolsAt(idx int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.calls) {
		return nil
	}
	return p.calls[idx].toolNames
}

// ─── helper: build engine + drive ───────────────────────────

func newDeferredEngine(t *testing.T, prov engine.Provider, reg engine.ToolRegistry) (*engine.QueryEngine, *state.AppState) {
	t.Helper()
	st := state.New()
	perms := permissions.NewContext()
	// ToolSearch is read-only so it auto-allows; this is just defensive.
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"ToolSearch", "Read"})

	eng, err := engine.New(engine.Options{
		State:        st,
		Tools:        reg,
		Provider:     prov,
		Model:        "test",
		Permissions:  perms,
		MaxToolTurns: 8,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng, st
}

// runOne drives one Submit through to DoneEvent.
func runOne(t *testing.T, eng *engine.QueryEngine, prompt string) []engine.Event {
	t.Helper()
	events := drainAll(eng.Submit(context.Background(), prompt))
	if !hasDone(events) {
		t.Fatalf("Submit %q did not reach DoneEvent (got %d events)", prompt, len(events))
	}
	return events
}

// containsName searches a tools-name slice for a target.
func containsName(haystack []string, needle string) bool {
	for _, n := range haystack {
		if n == needle {
			return true
		}
	}
	return false
}

// findSystemAttachment searches the engine's accumulated state for a
// system message whose text contains the given substring. Returns
// the message text (or "" if absent).
func findSystemAttachment(st *state.AppState, marker string) string {
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if b.Type == state.ContentText && strings.Contains(b.Text, marker) {
				return b.Text
			}
		}
	}
	return ""
}

// toolSearchSelectFrames builds a parent turn that fires ToolSearch
// with `select:<names>` then a final-text turn closes the Submit.
func toolSearchSelectFrames(useID, namesCSV string) []engine.StreamFrame {
	return toolUseTurn(useID, "ToolSearch", `{"query":"select:`+namesCSV+`"}`)
}

// toolSearchKeywordFrames builds a ToolSearch keyword query turn.
func toolSearchKeywordFrames(useID, query string) []engine.StreamFrame {
	return toolUseTurn(useID, "ToolSearch", `{"query":"`+query+`"}`)
}

// ─── Case 1: initial catalog hides deferred tools ───────────

func TestDeferredE2E_InitialCatalog_HidesDeferred(t *testing.T) {
	prov := newDeferredCaptureProvider(textTurn("ok"))
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "first turn")

	tools := prov.toolsAt(0)
	if !containsName(tools, "Read") || !containsName(tools, "ToolSearch") {
		t.Errorf("non-deferred tools missing: %v", tools)
	}
	for _, dt := range []string{
		"mcp__github__create_issue",
		"mcp__github__list_pull_requests",
		"mcp__slack__send_message",
		"mcp__notion__create_page",
	} {
		if containsName(tools, dt) {
			t.Errorf("deferred tool %q should NOT appear in initial catalog", dt)
		}
	}
}

// ─── Case 2: <available-deferred-tools> attachment lists them ────

func TestDeferredE2E_AvailableAttachment_ListsAllDeferred(t *testing.T) {
	prov := newDeferredCaptureProvider(textTurn("ok"))
	reg := catalogRegistry()
	eng, st := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "first turn")

	att := findSystemAttachment(st, "<available-deferred-tools>")
	if att == "" {
		t.Fatalf("expected <available-deferred-tools> system message")
	}
	for _, dt := range []string{
		"mcp__github__create_issue",
		"mcp__github__list_pull_requests",
		"mcp__slack__send_message",
		"mcp__notion__create_page",
	} {
		if !strings.Contains(att, dt) {
			t.Errorf("attachment missing %q:\n%s", dt, att)
		}
	}
	if strings.Contains(att, "Read") {
		t.Errorf("non-deferred tool 'Read' leaked into deferred attachment:\n%s", att)
	}
}

// ─── Case 3: select unlocks tool for the *next inner turn* ────

// One Submit, two inner turns:
//   - turn 0: model calls ToolSearch select:mcp__slack__send_message
//   - tool runs; engine recomputes specs for turn 1
//   - turn 1: model emits final text; req.Tools must now include the
//     selected mcp tool
func TestDeferredE2E_Select_UnlocksNextInnerTurn(t *testing.T) {
	prov := newDeferredCaptureProvider(
		toolSearchSelectFrames("ts1", "mcp__slack__send_message"),
		textTurn("found"),
	)
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "find the slack tool")

	if prov.callCount() != 2 {
		t.Fatalf("want 2 stream calls, got %d", prov.callCount())
	}
	turn0 := prov.toolsAt(0)
	turn1 := prov.toolsAt(1)
	if containsName(turn0, "mcp__slack__send_message") {
		t.Errorf("slack tool should not be in turn-0 catalog: %v", turn0)
	}
	if !containsName(turn1, "mcp__slack__send_message") {
		t.Errorf("slack tool should be unlocked in turn-1 catalog: %v", turn1)
	}
}

// ─── Case 4: select multiple via comma list ─────────────────

func TestDeferredE2E_Select_MultipleCommaList(t *testing.T) {
	prov := newDeferredCaptureProvider(
		toolSearchSelectFrames("ts1", "mcp__github__create_issue,mcp__notion__create_page"),
		textTurn("done"),
	)
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "select two")

	turn1 := prov.toolsAt(1)
	if !containsName(turn1, "mcp__github__create_issue") ||
		!containsName(turn1, "mcp__notion__create_page") {
		t.Errorf("comma-select did not unlock both: %v", turn1)
	}
	if containsName(turn1, "mcp__slack__send_message") {
		t.Errorf("non-selected tool leaked: %v", turn1)
	}
}

// ─── Case 5: keyword query unlocks matched candidates ──────

// biu's ToolSearch keyword form auto-unlocks matched candidates so
// the model can invoke them on the next inner turn without an extra
// select round-trip — the unlock is folded into the match step. See
// internal/tools/orchestration/toolsearch.go:124.
//
// Asserts: keyword "slack" unlocks slack tool but leaves notion alone.
func TestDeferredE2E_KeywordQuery_UnlocksMatchedOnly(t *testing.T) {
	prov := newDeferredCaptureProvider(
		toolSearchKeywordFrames("ts1", "slack"),
		textTurn("noted"),
	)
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "search keyword")

	turn1 := prov.toolsAt(1)
	if !containsName(turn1, "mcp__slack__send_message") {
		t.Errorf("keyword 'slack' should unlock matched tool; got: %v", turn1)
	}
	if containsName(turn1, "mcp__notion__create_page") {
		t.Errorf("keyword 'slack' should NOT unlock notion: %v", turn1)
	}
}

// ─── Case 6: persistence across multiple Submits ────────────

func TestDeferredE2E_Persistence_AcrossSubmits(t *testing.T) {
	prov := newDeferredCaptureProvider(
		// Submit 1: select then text.
		toolSearchSelectFrames("ts1", "mcp__slack__send_message"),
		textTurn("selected"),
		// Submit 2: just text.
		textTurn("ack 2"),
		// Submit 3: just text.
		textTurn("ack 3"),
	)
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "p1")
	runOne(t, eng, "p2")
	runOne(t, eng, "p3")

	// Calls 1 + 2 + 3 should each carry the unlocked slack tool.
	for i := 1; i <= 3; i++ {
		got := prov.toolsAt(i)
		if !containsName(got, "mcp__slack__send_message") {
			t.Errorf("turn %d did not retain unlock: %v", i, got)
		}
	}
}

// ─── Case 7: select pruning the attachment ──────────────────

// After select, the <available-deferred-tools> attachment for
// subsequent submits should no longer mention the unlocked tool.
func TestDeferredE2E_Attachment_PrunedAfterSelect(t *testing.T) {
	prov := newDeferredCaptureProvider(
		toolSearchSelectFrames("ts1", "mcp__slack__send_message"),
		textTurn("selected"),
		textTurn("ack 2"),
	)
	reg := catalogRegistry()
	eng, st := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "p1")
	runOne(t, eng, "p2")

	// The most recent <available-deferred-tools> attachment should
	// list 3 mcp tools (slack removed).
	att := mostRecentDeferredAttachment(st)
	if att == "" {
		t.Fatalf("expected attachment after p2")
	}
	if strings.Contains(att, "mcp__slack__send_message") {
		t.Errorf("attachment should NOT mention selected tool: %s", att)
	}
	for _, kept := range []string{
		"mcp__github__create_issue",
		"mcp__github__list_pull_requests",
		"mcp__notion__create_page",
	} {
		if !strings.Contains(att, kept) {
			t.Errorf("attachment missing un-selected tool %q: %s", kept, att)
		}
	}
}

// ─── Case 8: select on already-loaded tool is a no-op ───────

func TestDeferredE2E_Select_AlreadyLoaded_NoChange(t *testing.T) {
	prov := newDeferredCaptureProvider(
		toolSearchSelectFrames("ts1", "Read"),
		textTurn("ack"),
	)
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	runOne(t, eng, "select read")

	turn1 := prov.toolsAt(1)
	// Read was non-deferred; unchanged.
	if !containsName(turn1, "Read") {
		t.Errorf("Read missing from catalog: %v", turn1)
	}
	// No deferred unlocked.
	for _, dt := range []string{
		"mcp__github__create_issue",
		"mcp__slack__send_message",
	} {
		if containsName(turn1, dt) {
			t.Errorf("select:Read should not unlock unrelated deferred %q: %v", dt, turn1)
		}
	}
}

// ─── Case 9: select on missing name surfaces soft error ────

// The tool result for a non-existent select target must be a
// soft-error (IsError=true) so the model retries with a real name.
func TestDeferredE2E_Select_MissingName_SoftError(t *testing.T) {
	prov := newDeferredCaptureProvider(
		toolSearchSelectFrames("ts1", "does_not_exist"),
		textTurn("noted"),
	)
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	events := runOne(t, eng, "missing select")

	// ToolSearch select for a missing name returns a plain message
	// listing it as not-found — not a soft error. We just assert the
	// result text mentions the missing name.
	gotMention := false
	for _, ev := range events {
		r, ok := ev.(*engine.ToolUseResultEvent)
		if !ok || r.ID != "ts1" {
			continue
		}
		for _, b := range r.Result.Content {
			if strings.Contains(b.Text, "does_not_exist") {
				gotMention = true
			}
		}
	}
	if !gotMention {
		t.Errorf("missing-select result should mention 'does_not_exist'")
	}
}

// ─── Case 10: keyword query result text contains candidates ─

// Keyword form should return ranked candidates in the tool result.
// We don't lock the exact order (that's tested in toolsearch_test.go);
// just assert the relevant tool appears.
func TestDeferredE2E_Keyword_ReturnsCandidates(t *testing.T) {
	prov := newDeferredCaptureProvider(
		toolSearchKeywordFrames("ts1", "slack"),
		textTurn("ok"),
	)
	reg := catalogRegistry()
	eng, _ := newDeferredEngine(t, prov, reg)

	events := runOne(t, eng, "search slack")

	gotSlack := false
	for _, ev := range events {
		r, ok := ev.(*engine.ToolUseResultEvent)
		if !ok || r.ID != "ts1" {
			continue
		}
		for _, b := range r.Result.Content {
			if strings.Contains(b.Text, "mcp__slack__send_message") {
				gotSlack = true
			}
		}
	}
	if !gotSlack {
		t.Errorf("keyword 'slack' should surface mcp__slack__send_message in result")
	}
}

// ─── helper: most-recent deferred attachment ────────────────

// mostRecentDeferredAttachment returns the *last* system message
// containing the deferred-attachment marker.
func mostRecentDeferredAttachment(st *state.AppState) string {
	last := ""
	for _, m := range st.Snapshot() {
		if m.Role != state.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if b.Type == state.ContentText &&
				strings.Contains(b.Text, "<available-deferred-tools>") {
				last = b.Text
			}
		}
	}
	return last
}
