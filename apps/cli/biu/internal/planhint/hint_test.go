package planhint

import (
	"strings"
	"testing"
)

func TestAnalyseTriggersOnRefactor(t *testing.T) {
	a := New(true, nil)
	got := a.Analyse("refactor the auth module to use middleware")
	if got.Note == "" {
		t.Fatal("expected suggestion for `refactor`; got none")
	}
	if got.MatchedKeyword != "refactor" {
		t.Errorf("expected match=refactor; got %q", got.MatchedKeyword)
	}
	if !strings.Contains(got.Note, "EnterPlanMode") {
		t.Errorf("note should advertise EnterPlanMode; got %q", got.Note)
	}
}

func TestAnalyseChineseTriggers(t *testing.T) {
	a := New(true, nil)
	for _, prompt := range []string{
		"重构权限系统",
		"重写一遍权限模块",
		"整套都改一下",
	} {
		t.Run(prompt, func(t *testing.T) {
			got := a.Analyse(prompt)
			if got.Note == "" {
				t.Errorf("expected suggestion for %q", prompt)
			}
		})
	}
}

func TestAnalyseSilentForSmallChange(t *testing.T) {
	a := New(true, nil)
	for _, prompt := range []string{
		"fix the typo in README",
		"add a console.log to debug",
		"什么是 plan mode？",
	} {
		t.Run(prompt, func(t *testing.T) {
			if got := a.Analyse(prompt); got.Note != "" {
				t.Errorf("unexpected suggestion for %q: %s", prompt, got.Note)
			}
		})
	}
}

func TestAnalyseDisabledShortCircuits(t *testing.T) {
	a := New(false, nil)
	if got := a.Analyse("refactor everything"); got.Note != "" {
		t.Errorf("disabled analyser must stay silent; got %q", got.Note)
	}
	if a.Enabled() {
		t.Errorf("Enabled should report false")
	}
}

func TestAnalyseRespectsCustomKeywords(t *testing.T) {
	// A team-specific override list (e.g. internal jargon).
	a := New(true, []string{"deepclean", " 大手术 "}) // spaces should be trimmed
	if got := a.Analyse("Time to deepclean the deps"); got.Note == "" {
		t.Errorf("custom keyword should match")
	}
	if got := a.Analyse("做个大手术"); got.Note == "" {
		t.Errorf("custom Chinese keyword should match")
	}
	if got := a.Analyse("refactor"); got.Note != "" {
		t.Errorf("default `refactor` should NOT match when custom list is set")
	}
}

func TestNilAnalyserSafe(t *testing.T) {
	var a *Analyser
	if got := a.Analyse("refactor"); got.Note != "" {
		t.Errorf("nil analyser must stay silent")
	}
	if a.Enabled() {
		t.Errorf("nil analyser is disabled")
	}
}
