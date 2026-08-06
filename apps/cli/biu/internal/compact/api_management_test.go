package compact

import (
	"encoding/json"
	"testing"
)

func TestBuildAPIContextManagement_disabledByDefault(t *testing.T) {
	t.Setenv("USE_API_CLEAR_TOOL_RESULTS", "")
	t.Setenv("USE_API_CLEAR_TOOL_USES", "")
	if got := BuildAPIContextManagement(APIContextOptions{}); got != nil {
		t.Errorf("default should be nil, got %+v", got)
	}
}

func TestBuildAPIContextManagement_thinkingKeepAll(t *testing.T) {
	got := BuildAPIContextManagement(APIContextOptions{HasThinking: true})
	if got == nil || len(got.Edits) != 1 {
		t.Fatalf("want 1 edit, got %+v", got)
	}
	e := got.Edits[0]
	if e.Type != "clear_thinking_20251015" {
		t.Errorf("Type = %q", e.Type)
	}
	if e.Keep != "all" {
		t.Errorf("Keep should be 'all' by default, got %v", e.Keep)
	}
}

func TestBuildAPIContextManagement_thinkingClearAll(t *testing.T) {
	got := BuildAPIContextManagement(APIContextOptions{
		HasThinking:      true,
		ClearAllThinking: true,
	})
	if got == nil {
		t.Fatal("want config")
	}
	keep, ok := got.Edits[0].Keep.(APIThreshold)
	if !ok {
		t.Fatalf("Keep should be APIThreshold when ClearAllThinking, got %T", got.Edits[0].Keep)
	}
	if keep.Value != 1 || keep.Type != "thinking_turns" {
		t.Errorf("Keep = %+v, want {thinking_turns, 1}", keep)
	}
}

func TestBuildAPIContextManagement_thinkingSkippedWhenRedacted(t *testing.T) {
	got := BuildAPIContextManagement(APIContextOptions{
		HasThinking:            true,
		IsRedactThinkingActive: true,
	})
	if got != nil {
		t.Errorf("redact-thinking active → no thinking strategy, got %+v", got)
	}
}

func TestBuildAPIContextManagement_clearToolResults(t *testing.T) {
	t.Setenv("USE_API_CLEAR_TOOL_RESULTS", "1")
	got := BuildAPIContextManagement(APIContextOptions{})
	if got == nil || len(got.Edits) != 1 {
		t.Fatalf("want 1 edit, got %+v", got)
	}
	e := got.Edits[0]
	if e.Type != "clear_tool_uses_20250919" {
		t.Errorf("Type = %q", e.Type)
	}
	if e.Trigger == nil || e.Trigger.Value != APIDefaultMaxInputTokens {
		t.Errorf("Trigger = %+v", e.Trigger)
	}
	if e.ClearAtLeast == nil ||
		e.ClearAtLeast.Value != APIDefaultMaxInputTokens-APIDefaultTargetInputTokens {
		t.Errorf("ClearAtLeast = %+v", e.ClearAtLeast)
	}
	// Tool list contains representative entries.
	want := map[string]bool{}
	for _, n := range []string{"Bash", "Read", "Grep", "WebFetch"} {
		want[n] = true
	}
	for _, n := range e.ClearToolInputs {
		delete(want, n)
	}
	if len(want) > 0 {
		t.Errorf("missing tools: %v (got %v)", want, e.ClearToolInputs)
	}
}

func TestBuildAPIContextManagement_clearToolUses(t *testing.T) {
	t.Setenv("USE_API_CLEAR_TOOL_USES", "1")
	got := BuildAPIContextManagement(APIContextOptions{})
	if got == nil || len(got.Edits) != 1 {
		t.Fatalf("want 1 edit, got %+v", got)
	}
	e := got.Edits[0]
	if len(e.ExcludeTools) == 0 {
		t.Error("clear_tool_uses should set ExcludeTools")
	}
}

func TestBuildAPIContextManagement_envOverrideThresholds(t *testing.T) {
	t.Setenv("USE_API_CLEAR_TOOL_RESULTS", "1")
	t.Setenv("API_MAX_INPUT_TOKENS", "100000")
	t.Setenv("API_TARGET_INPUT_TOKENS", "20000")

	got := BuildAPIContextManagement(APIContextOptions{})
	e := got.Edits[0]
	if e.Trigger.Value != 100_000 {
		t.Errorf("Trigger override = %d, want 100000", e.Trigger.Value)
	}
	if e.ClearAtLeast.Value != 80_000 {
		t.Errorf("ClearAtLeast = %d, want 80000", e.ClearAtLeast.Value)
	}
}

func TestBuildAPIContextManagement_envOverrideInvalidIgnored(t *testing.T) {
	t.Setenv("USE_API_CLEAR_TOOL_RESULTS", "1")
	t.Setenv("API_MAX_INPUT_TOKENS", "abc")
	got := BuildAPIContextManagement(APIContextOptions{})
	if got.Edits[0].Trigger.Value != APIDefaultMaxInputTokens {
		t.Errorf("invalid env should fall back to default; got %d",
			got.Edits[0].Trigger.Value)
	}
}

// Marshalling sanity: the final JSON shape matches what the
// Anthropic API expects (lowercase snake_case fields, `edits`
// array at top level).
func TestAPIContextManagement_jsonShape(t *testing.T) {
	cfg := &APIContextManagementConfig{
		Edits: []APIContextEditStrategy{{
			Type:    "clear_tool_uses_20250919",
			Trigger: &APIThreshold{Type: "input_tokens", Value: 180_000},
		}},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`"edits"`, `"type":"clear_tool_uses_20250919"`,
		`"trigger"`, `"input_tokens"`, `"value":180000`}
	for _, w := range want {
		if !contains(string(raw), w) {
			t.Errorf("JSON missing %q: %s", w, raw)
		}
	}
}

func TestBuildAPIContextManagement_combined(t *testing.T) {
	t.Setenv("USE_API_CLEAR_TOOL_RESULTS", "1")
	t.Setenv("USE_API_CLEAR_TOOL_USES", "1")
	got := BuildAPIContextManagement(APIContextOptions{HasThinking: true})
	if got == nil || len(got.Edits) != 3 {
		t.Errorf("thinking + 2 tool strategies = 3 edits, got %d", len(got.Edits))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
