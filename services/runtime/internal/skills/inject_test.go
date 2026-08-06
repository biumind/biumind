package skills

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_AllThreeTiers(t *testing.T) {
	loaded := &LoadedSkills{
		Pinned: []*Skill{
			{Name: "code-review", Identifier: "code-review",
				Description: "PR auto-review", Content: "## Body\nDo X."},
		},
		AutoAttach: []*Skill{
			{Name: "go-style", Identifier: "go-style",
				Description: "Go conventions", Content: "Use snake_case for env vars."},
		},
		Available: []*Skill{
			{Name: "weekly-report", Identifier: "weekly-report",
				Description: "Generate weekly report"},
			{Name: "rust-refactor", Identifier: "rust-refactor",
				Description: "Refactor Rust"},
		},
	}
	out := BuildSystemPrompt(loaded)

	for _, want := range []string{
		"# Pinned skills",
		"# Auto-attached skills",
		"# Available skills",
		"<available_skills>",
		"<skill name=\"weekly-report\"",
		"do NOT call skill.activate",
		"Do X.",
		"snake_case",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestBuildSystemPrompt_EmptyLoadedSkillsYieldsEmpty(t *testing.T) {
	if got := BuildSystemPrompt(nil); got != "" {
		t.Errorf("nil → %q, want empty", got)
	}
	if got := BuildSystemPrompt(&LoadedSkills{}); got != "" {
		t.Errorf("empty → %q, want empty", got)
	}
}

func TestBuildSystemPrompt_OnlyAvailable(t *testing.T) {
	loaded := &LoadedSkills{
		Available: []*Skill{{Name: "x", Identifier: "x", Description: "y"}},
	}
	out := BuildSystemPrompt(loaded)
	if strings.Contains(out, "Pinned skills") || strings.Contains(out, "Auto-attached") {
		t.Errorf("unwanted sections in:\n%s", out)
	}
	if !strings.Contains(out, "<available_skills>") {
		t.Error("missing available_skills wrap")
	}
}

func TestBuildSystemPrompt_DeterministicAcrossCalls(t *testing.T) {
	// Prompt-cache hits depend on byte-for-byte stability across
	// turns. Two calls with the same input must produce identical
	// output (no map iteration, no time.Now, no random IDs).
	in := &LoadedSkills{
		Pinned: []*Skill{
			{Name: "p", Identifier: "p", Content: "body"},
		},
		Available: []*Skill{
			{Name: "a", Identifier: "a", Description: "first"},
			{Name: "b", Identifier: "b", Description: "second"},
		},
	}
	a := BuildSystemPrompt(in)
	b := BuildSystemPrompt(in)
	if a != b {
		t.Errorf("system prompt not byte-stable:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

func TestBuildSelectedBlock_WrapsBodies(t *testing.T) {
	skills := []*Skill{
		{Name: "code-review", Identifier: "code-review",
			Content: "Review the diff carefully."},
		{Name: "rust-refactor", Identifier: "rust-refactor",
			Content: "Plan ownership changes first."},
	}
	out := BuildSelectedBlock(skills)
	for _, want := range []string{
		"<selected_skill_context>",
		"<selected_skills>",
		"<skill identifier=\"code-review\"",
		"Review the diff carefully.",
		"Plan ownership changes first.",
		"</selected_skill_context>",
		"do NOT call skill.activate",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestBuildSelectedBlock_EmptyInputYieldsEmpty(t *testing.T) {
	if got := BuildSelectedBlock(nil); got != "" {
		t.Errorf("nil → %q", got)
	}
	if got := BuildSelectedBlock([]*Skill{}); got != "" {
		t.Errorf("empty → %q", got)
	}
}

func TestBuildSelectedBlock_AllContentlessIsEmpty(t *testing.T) {
	// If the user @-mentioned only skills that have no body (e.g.
	// resource-only refs), there's nothing to inject — return empty
	// rather than emit a noisy half-block.
	out := BuildSelectedBlock([]*Skill{
		{Name: "x", Identifier: "x"},
		{Name: "y", Identifier: "y"},
	})
	if out != "" {
		t.Errorf("contentless skills should yield empty; got:\n%s", out)
	}
}

func TestEscapeXML(t *testing.T) {
	cases := map[string]string{
		`a&b`:                   `a&amp;b`,
		`<tag>`:                 `&lt;tag&gt;`,
		`he said "hi"`:          `he said &quot;hi&quot;`,
		`safe-name_no-escape.1`: `safe-name_no-escape.1`,
	}
	for in, want := range cases {
		if got := escapeXML(in); got != want {
			t.Errorf("escapeXML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSelectedBlock_DedupesByID(t *testing.T) {
	// Pinned + explicit @-mention of the same skill is a normal
	// pattern in chat — caller may not have deduped. The block must
	// emit the body exactly once; otherwise the model sees the same
	// instruction twice and burns tokens.
	s := &Skill{ID: "skill_x", Identifier: "x", Name: "X", Content: "BODY-ONCE"}
	out := BuildSelectedBlock([]*Skill{s, s, s})
	if out == "" {
		t.Fatal("expected non-empty block")
	}
	if got := strings.Count(out, "BODY-ONCE"); got != 1 {
		t.Errorf("body should appear exactly once; got %d times in %q",
			got, out)
	}
}

func TestBuildSelectedBlock_DedupePreservesFirstOccurrenceOrder(t *testing.T) {
	a := &Skill{ID: "a", Identifier: "a", Name: "A", Content: "AAA"}
	b := &Skill{ID: "b", Identifier: "b", Name: "B", Content: "BBB"}
	out := BuildSelectedBlock([]*Skill{a, b, a, b})
	idxA := strings.Index(out, "AAA")
	idxB := strings.Index(out, "BBB")
	if idxA < 0 || idxB < 0 {
		t.Fatal("both skills should appear")
	}
	if idxA > idxB {
		t.Errorf("dedup should preserve first-call order; A at %d, B at %d",
			idxA, idxB)
	}
}

func TestBuildSelectedBlock_NoIDSkipsDedup(t *testing.T) {
	// Edge case — skills assembled in tests sometimes lack IDs. We
	// should still emit them rather than treating empty-ID as a
	// dedup key.
	a := &Skill{Identifier: "a", Name: "A", Content: "AAA"}
	b := &Skill{Identifier: "b", Name: "B", Content: "BBB"}
	out := BuildSelectedBlock([]*Skill{a, b})
	if !strings.Contains(out, "AAA") || !strings.Contains(out, "BBB") {
		t.Errorf("ID-less skills should still appear; got %q", out)
	}
}

// ─── BuildSystemPrompt token budget ───────────────────────────

func TestBuildSystemPrompt_BudgetDemotesAutoAttach(t *testing.T) {
	// Two auto_attach skills with chunky bodies. Budget set so only
	// the first fits — the second should demote into the
	// <available_skills> list.
	bigBody := strings.Repeat("x", 600)
	a := &Skill{ID: "a", Identifier: "a", Name: "A", Description: "first", Content: bigBody}
	b := &Skill{ID: "b", Identifier: "b", Name: "B", Description: "second", Content: bigBody}
	loaded := &LoadedSkills{AutoAttach: []*Skill{a, b}}

	out := BuildSystemPromptWithBudget(loaded, 1000)

	// First skill body present in pre-loaded section.
	if !strings.Contains(out, "## A") {
		t.Errorf("first auto-attach should still be inline; got %q", out)
	}
	// Second skill body absent (demoted).
	if strings.Contains(out, bigBody) && strings.Count(out, bigBody) > 1 {
		t.Errorf("second auto-attach body should have been demoted")
	}
	// Second skill surfaces in <available_skills>.
	if !strings.Contains(out, `identifier="b"`) {
		t.Errorf("demoted skill must remain discoverable; got %q", out)
	}
}

func TestBuildSystemPrompt_BudgetZeroIsLegacyBehaviour(t *testing.T) {
	bigBody := strings.Repeat("y", 5000)
	loaded := &LoadedSkills{
		AutoAttach: []*Skill{
			{ID: "a", Identifier: "a", Name: "A", Content: bigBody},
			{ID: "b", Identifier: "b", Name: "B", Content: bigBody},
		},
	}
	out := BuildSystemPromptWithBudget(loaded, 0) // disabled
	// Both bodies present — no demotion.
	if strings.Count(out, bigBody) != 2 {
		t.Errorf("budget=0 should keep both bodies inline; got %d copies",
			strings.Count(out, bigBody))
	}
}

func TestBuildSystemPrompt_PinnedAlwaysEmitted(t *testing.T) {
	// Pinned bodies are sacred — even when their total size blows the
	// budget. Demotion only applies to AutoAttach.
	pinned := &Skill{
		ID: "p", Identifier: "p", Name: "P", Content: strings.Repeat("z", 4000),
	}
	out := BuildSystemPromptWithBudget(&LoadedSkills{Pinned: []*Skill{pinned}}, 500)
	if !strings.Contains(out, "# Pinned skills") {
		t.Errorf("pinned section must always emit")
	}
	if !strings.Contains(out, strings.Repeat("z", 4000)) {
		t.Errorf("pinned body must be preserved even past budget")
	}
}
