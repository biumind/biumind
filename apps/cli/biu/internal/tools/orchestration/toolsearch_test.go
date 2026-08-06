package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// fakeTool implements engine.Tool for ToolSearch tests. ShouldDefer is
// exposed as a field so individual tests can pin the deferral state
// without touching the full Deferrable interface contract.
type fakeTool struct {
	name        string
	desc        string
	schema      map[string]any
	deferred    bool
	readOnly    bool
	concurrency bool
}

func (f *fakeTool) Name() string                                    { return f.name }
func (f *fakeTool) Description(_ map[string]any) string             { return f.desc }
func (f *fakeTool) InputSchema() map[string]any                     { return f.schema }
func (f *fakeTool) IsReadOnly(_ map[string]any) bool                { return f.readOnly }
func (f *fakeTool) IsDestructive(_ map[string]any) bool             { return false }
func (f *fakeTool) IsConcurrencySafe(_ map[string]any) bool         { return f.concurrency }
func (f *fakeTool) InterruptBehavior() string                       { return "cancel" }
func (f *fakeTool) ShouldDefer() bool                               { return f.deferred }
func (f *fakeTool) Call(_ context.Context, _ map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	return plainResult("ok"), nil
}

func newRegistryWithDeferred() engine.ToolRegistry {
	r := engine.NewRegistry()
	r.Register(&fakeTool{name: "Read", desc: "Read a file", schema: map[string]any{"type": "object"}})
	r.Register(&fakeTool{
		name:     "mcp__github__create_issue",
		desc:     "Create a new issue on a GitHub repository",
		schema:   map[string]any{"type": "object", "properties": map[string]any{"repo": map[string]any{"type": "string"}}},
		deferred: true,
	})
	r.Register(&fakeTool{
		name:     "mcp__github__list_pull_requests",
		desc:     "List open pull requests for a repository",
		schema:   map[string]any{"type": "object"},
		deferred: true,
	})
	r.Register(&fakeTool{
		name:     "mcp__slack__send_message",
		desc:     "Post a message to a Slack channel",
		schema:   map[string]any{"type": "object"},
		deferred: true,
	})
	r.Register(&fakeTool{
		name:     "mcp__notion__create_page",
		desc:     "Create a Notion page in a workspace",
		schema:   map[string]any{"type": "object"},
		deferred: true,
	})
	return r
}

// TestPartitionDeferred locks the engine.PartitionDeferred contract:
// active and deferred slices add up to the registry total, and
// non-Deferrable tools default to active. Lives here (not in engine
// package) because we need a real Deferrable implementation that
// engine_test's stubs don't provide.
func TestPartitionDeferred(t *testing.T) {
	r := newRegistryWithDeferred()
	active, deferred := engine.PartitionDeferred(r)
	if len(active) != 1 || active[0].Name() != "Read" {
		t.Errorf("active = %v, want [Read]", names(active))
	}
	if len(deferred) != 4 {
		t.Errorf("deferred count = %d, want 4: %v", len(deferred), names(deferred))
	}
	for _, d := range deferred {
		if !engine.IsDeferred(d) {
			t.Errorf("partition put non-deferred %q in deferred slice", d.Name())
		}
	}
}

// TestToolSearch_SelectExact — `select:foo,bar` returns those exact
// tools' descriptions+schemas, not a search ranking.
func TestToolSearch_SelectExact(t *testing.T) {
	r := newRegistryWithDeferred()
	out, err := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "select:mcp__github__create_issue,mcp__slack__send_message",
	}, nil)
	if err != nil || out.IsError {
		t.Fatalf("call: %v %+v", err, out)
	}
	text := flatten(out)
	for _, must := range []string{
		"mcp__github__create_issue",
		"Create a new issue",
		"mcp__slack__send_message",
		"Post a message to a Slack channel",
	} {
		if !strings.Contains(text, must) {
			t.Errorf("result missing %q:\n%s", must, text)
		}
	}
	// Non-selected deferred tools must NOT leak.
	if strings.Contains(text, "list_pull_requests") {
		t.Errorf("select must not include unselected tools:\n%s", text)
	}
}

// TestToolSearch_SelectAlreadyLoaded — selecting a non-deferred tool
// shouldn't error; it should report the tool is already loaded.
// Ensures a harmless no-op on stale model retries.
func TestToolSearch_SelectAlreadyLoaded(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "select:Read",
	}, nil)
	if out.IsError {
		t.Fatalf("should not error: %+v", out)
	}
	text := flatten(out)
	if !strings.Contains(text, "Already-loaded") || !strings.Contains(text, "Read") {
		t.Errorf("expected already-loaded notice: %s", text)
	}
}

// TestToolSearch_SelectMissing — naming a tool the registry doesn't
// know yields a "not found" notice, not an error.
func TestToolSearch_SelectMissing(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "select:does_not_exist",
	}, nil)
	if out.IsError {
		t.Fatalf("missing should soft-succeed: %+v", out)
	}
	text := flatten(out)
	if !strings.Contains(text, "does_not_exist") {
		t.Errorf("expected 'does_not_exist' in result: %s", text)
	}
}

// TestToolSearch_KeywordRanksByName — query "github issue" should
// surface the create_issue tool (multiple name-part hits) above
// list_pull_requests.
func TestToolSearch_KeywordRanksByName(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query":       "github issue",
		"max_results": 3,
	}, nil)
	if out.IsError {
		t.Fatalf("keyword search errored: %+v", out)
	}
	text := flatten(out)
	idxIssue := strings.Index(text, "mcp__github__create_issue")
	idxPR := strings.Index(text, "mcp__github__list_pull_requests")
	if idxIssue < 0 {
		t.Fatalf("create_issue should rank: %s", text)
	}
	// list_pull_requests still matches "github" but should rank lower.
	if idxPR > 0 && idxPR < idxIssue {
		t.Errorf("create_issue should rank above list_pull_requests:\n%s", text)
	}
}

// TestToolSearch_KeywordRequiredTerm — `+slack` filters to slack
// tools only, regardless of how strong the optional terms match elsewhere.
func TestToolSearch_KeywordRequiredTerm(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "+slack message",
	}, nil)
	text := flatten(out)
	if !strings.Contains(text, "mcp__slack__send_message") {
		t.Errorf("slack tool missing: %s", text)
	}
	if strings.Contains(text, "mcp__github") || strings.Contains(text, "mcp__notion") {
		t.Errorf("required term should exclude non-slack tools: %s", text)
	}
}

// TestToolSearch_McpPrefixShortcut — `mcp__github` returns every
// github-namespaced tool without keyword scoring.
func TestToolSearch_McpPrefixShortcut(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "mcp__github",
	}, nil)
	text := flatten(out)
	if !strings.Contains(text, "mcp__github__create_issue") ||
		!strings.Contains(text, "mcp__github__list_pull_requests") {
		t.Errorf("prefix should match both github tools: %s", text)
	}
	if strings.Contains(text, "mcp__slack") {
		t.Errorf("prefix should not match slack: %s", text)
	}
}

// TestToolSearch_NoDeferredTools — empty deferred set returns a
// helpful notice, not an error.
func TestToolSearch_NoDeferredTools(t *testing.T) {
	r := engine.NewRegistry()
	r.Register(&fakeTool{name: "Read"})
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "anything",
	}, nil)
	if out.IsError {
		t.Errorf("empty deferred should not error: %+v", out)
	}
	text := flatten(out)
	if !strings.Contains(text, "No deferred tools") {
		t.Errorf("expected helpful notice: %s", text)
	}
}

// TestToolSearch_KeywordNoMatches — query that hits nothing returns
// the "no matches" sentinel.
func TestToolSearch_KeywordNoMatches(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "xyz_quagmire_floob",
	}, nil)
	text := flatten(out)
	if !strings.Contains(text, "No matching deferred tools") {
		t.Errorf("expected 'no matches' sentinel: %s", text)
	}
}

// TestToolSearch_EmptyQuery — empty / whitespace query is a soft error.
func TestToolSearch_EmptyQuery(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "   ",
	}, nil)
	if !out.IsError {
		t.Errorf("empty query should soft-error")
	}
}

// TestToolSearch_NilRegistry — defensive: nil registry → soft error.
func TestToolSearch_NilRegistry(t *testing.T) {
	out, _ := ToolSearchTool{Registry: nil}.Call(context.Background(), map[string]any{
		"query": "anything",
	}, nil)
	if !out.IsError {
		t.Errorf("nil registry should soft-error")
	}
}

// TestToolSearch_MaxResultsHonoured — keyword search returns at most
// max_results matches even when more would qualify.
func TestToolSearch_MaxResultsHonoured(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query":       "mcp",
		"max_results": 2,
	}, nil)
	text := flatten(out)
	matches := strings.Count(text, "## mcp__")
	if matches > 2 {
		t.Errorf("max_results=2 violated; got %d in:\n%s", matches, text)
	}
}

// TestToolSearch_RecordsSelections_Select — `select:foo,bar` adds
// matched names to env.Selections so the engine's next-turn catalog
// build picks them up.
func TestToolSearch_RecordsSelections_Select(t *testing.T) {
	r := newRegistryWithDeferred()
	sel := engine.NewDeferredSelection()
	env := &engine.ToolEnv{Selections: sel}
	_, _ = ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "select:mcp__github__create_issue,mcp__slack__send_message",
	}, env)
	got := sel.Names()
	if len(got) != 2 ||
		!stringInSlice(got, "mcp__github__create_issue") ||
		!stringInSlice(got, "mcp__slack__send_message") {
		t.Errorf("Selections after select: = %v, want both names recorded", got)
	}
}

// TestToolSearch_RecordsSelections_Keyword — keyword matches also
// land in Selections, so the model can immediately invoke any tool
// the search surfaced without a second select: round-trip.
func TestToolSearch_RecordsSelections_Keyword(t *testing.T) {
	r := newRegistryWithDeferred()
	sel := engine.NewDeferredSelection()
	env := &engine.ToolEnv{Selections: sel}
	_, _ = ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "github issue",
	}, env)
	if !sel.Has("mcp__github__create_issue") {
		t.Errorf("keyword match should select create_issue; have %v", sel.Names())
	}
}

// TestToolSearch_NilSelections_NoCrash — env.Selections == nil is
// the test-harness path; the tool must work and just not record.
func TestToolSearch_NilSelections_NoCrash(t *testing.T) {
	r := newRegistryWithDeferred()
	out, _ := ToolSearchTool{Registry: r}.Call(context.Background(), map[string]any{
		"query": "select:mcp__github__create_issue",
	}, &engine.ToolEnv{Selections: nil})
	if out.IsError {
		t.Errorf("nil Selections should not error")
	}
}

func stringInSlice(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestParseToolName — internal helper: MCP names split on __ and _,
// CamelCase tools split on case boundaries.
func TestParseToolName(t *testing.T) {
	cases := []struct {
		in        string
		wantParts []string
		wantMcp   bool
	}{
		{"mcp__github__create_issue", []string{"github", "create", "issue"}, true},
		{"mcp__slack__send_message", []string{"slack", "send", "message"}, true},
		{"WebFetch", []string{"web", "fetch"}, false},
		{"Read", []string{"read"}, false},
		{"NotebookEdit", []string{"notebook", "edit"}, false},
		{"snake_case_tool", []string{"snake", "case", "tool"}, false},
	}
	for _, tc := range cases {
		got := parseToolName(tc.in)
		if got.isMcp != tc.wantMcp {
			t.Errorf("%q: isMcp=%v want %v", tc.in, got.isMcp, tc.wantMcp)
		}
		if !sameSlice(got.parts, tc.wantParts) {
			t.Errorf("%q: parts=%v want %v", tc.in, got.parts, tc.wantParts)
		}
	}
}

func names(tools []engine.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name()
	}
	return out
}

func sameSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
