package promptsuggest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func userMsg(text string) state.Message {
	return state.Message{
		Role: state.RoleUser,
		Content: []state.ContentBlock{
			{Type: state.ContentText, Text: text},
		},
	}
}

// ─── env gate ────────────────────────────────────────────────

func TestIsEnabled(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "")
	if !IsEnabled() {
		t.Error("default should be enabled")
	}
	for _, v := range []string{"0", "false", "no", "off", "FALSE"} {
		t.Setenv("BIU_PROMPT_SUGGEST", v)
		if IsEnabled() {
			t.Errorf("%q should disable", v)
		}
	}
	t.Setenv("BIU_PROMPT_SUGGEST", "1")
	if !IsEnabled() {
		t.Error("'1' should enable")
	}
}

// ─── slash branch ────────────────────────────────────────────

func TestSlashSuggestions_prefixMatch(t *testing.T) {
	cat := []string{"/clear", "/compact", "/cost", "/copy", "/help"}
	got := slashSuggestions("/co", cat)
	names := map[string]bool{}
	for _, s := range got {
		names[s.Body] = true
	}
	for _, want := range []string{"/compact", "/cost", "/copy"} {
		if !names[want] {
			t.Errorf("missing %q in suggestions: %+v", want, names)
		}
	}
	if names["/clear"] || names["/help"] {
		t.Errorf("non-matching shouldn't appear: %+v", names)
	}
}

func TestSlashSuggestions_scoreShape(t *testing.T) {
	cat := []string{"/foo", "/foobar"}
	got := slashSuggestions("/foo", cat)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	// Score formula: 0.7 + 0.3*(q_len/name_len).
	// /foo: 0.7 + 0.3*(4/4) = 1.0
	// /foobar: 0.7 + 0.3*(4/7) ≈ 0.871
	for _, s := range got {
		if s.Body == "/foo" && s.Score != 1.0 {
			t.Errorf("exact match should score 1.0, got %f", s.Score)
		}
		if s.Body == "/foobar" && (s.Score >= 1.0 || s.Score < 0.7) {
			t.Errorf("partial match should be in (0.7, 1.0): got %f", s.Score)
		}
	}
}

// ─── history branch ──────────────────────────────────────────

func TestHistorySuggestions_substringMatch(t *testing.T) {
	hist := []state.Message{
		userMsg("ancient prompt about cats"),
		userMsg("middle ground"),
		userMsg("recent question regarding DOGS"),
	}
	got := historySuggestions("dog", hist)
	if len(got) != 1 {
		t.Errorf("want 1 hit (case-insensitive), got %d", len(got))
	}
	if !strings.Contains(got[0].Body, "DOGS") {
		t.Errorf("hit should contain DOGS: %q", got[0].Body)
	}
}

func TestHistorySuggestions_recencyBoost(t *testing.T) {
	// 12 prompts; only first and last contain "needle". Recent
	// should outrank old.
	var hist []state.Message
	for i := 0; i < 12; i++ {
		hist = append(hist, userMsg("needle in old"))
	}
	hist[len(hist)-1] = userMsg("needle in recent")
	got := historySuggestions("needle", hist)
	if len(got) == 0 {
		t.Fatal("expected hits")
	}
	// Top hit is recent.
	if !strings.Contains(got[0].Body, "recent") {
		t.Errorf("recency boost failed; top = %q", got[0].Body)
	}
}

func TestHistorySuggestions_skipsAssistantTurns(t *testing.T) {
	hist := []state.Message{
		{Role: state.RoleAssistant, Content: []state.ContentBlock{
			{Type: state.ContentText, Text: "the answer is needle in haystack"},
		}},
	}
	got := historySuggestions("needle", hist)
	if len(got) != 0 {
		t.Errorf("assistant turns should be ignored, got %+v", got)
	}
}

// ─── Suggest end-to-end ─────────────────────────────────────

func TestSuggest_emptyInputReturnsNothing(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "")
	if got := Suggest(context.Background(), "", Sources{}); len(got) != 0 {
		t.Errorf("empty input → no suggestions, got %+v", got)
	}
}

func TestSuggest_disabledViaEnv(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "0")
	if got := Suggest(context.Background(), "/co",
		Sources{Slash: []string{"/copy"}}); len(got) != 0 {
		t.Errorf("disabled → no suggestions, got %+v", got)
	}
}

func TestSuggest_combinesSlashAndHistory(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "")
	hist := []state.Message{userMsg("/copy something")}
	got := Suggest(context.Background(), "/co", Sources{
		Slash:   []string{"/copy", "/compact"},
		History: hist,
	})
	if len(got) == 0 {
		t.Fatal("expected suggestions")
	}
	// Highest-scoring should be the slash branch (exact prefix
	// hit, score ≈ 1.0).
	if got[0].Source != "slash" {
		t.Errorf("top should be slash, got %+v", got[0])
	}
}

func TestSuggest_capsAtMax(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "")
	cat := []string{"/c1", "/c2", "/c3", "/c4", "/c5"}
	got := Suggest(context.Background(), "/c", Sources{Slash: cat})
	if len(got) > MaxSuggestions {
		t.Errorf("exceeded cap: %d", len(got))
	}
}

// ─── speculation ────────────────────────────────────────────

type fakeSpec struct {
	calls int
	resp  string
	err   error
}

func (f *fakeSpec) Speculate(ctx context.Context, hist []state.Message, partial string) (string, error) {
	f.calls++
	return f.resp, f.err
}

func TestSpeculation_runsOnlyOnEmpty(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "")
	spec := &fakeSpec{resp: "guessed continuation"}

	// When slash + history yield results, speculation must not fire.
	got := Suggest(context.Background(), "/co", Sources{
		Slash:       []string{"/copy"},
		Speculation: spec,
	})
	if spec.calls != 0 {
		t.Errorf("speculation should NOT fire when other branches hit")
	}
	if len(got) == 0 {
		t.Error("slash branch should have yielded a hit")
	}

	// When no other source matches, speculation IS asked.
	spec2 := &fakeSpec{resp: "guessed thing"}
	got = Suggest(context.Background(), "obscure",
		Sources{Speculation: spec2})
	if spec2.calls != 1 {
		t.Errorf("speculation should fire as fallback, calls = %d", spec2.calls)
	}
	if len(got) != 1 || got[0].Source != "speculation" {
		t.Errorf("speculation result missing: %+v", got)
	}
}

func TestSpeculation_errorIgnored(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "")
	spec := &fakeSpec{err: errors.New("api down")}
	got := Suggest(context.Background(), "obscure",
		Sources{Speculation: spec})
	if len(got) != 0 {
		t.Errorf("speculation error → drop quietly, got %+v", got)
	}
}

func TestSpeculation_emptyResponseIgnored(t *testing.T) {
	t.Setenv("BIU_PROMPT_SUGGEST", "")
	spec := &fakeSpec{resp: "   "}
	got := Suggest(context.Background(), "obscure",
		Sources{Speculation: spec})
	if len(got) != 0 {
		t.Errorf("empty speculation should drop, got %+v", got)
	}
}
