package reviews

import "testing"

func TestSuggestLinkTarget_FindsCloseTitle(t *testing.T) {
	got := SuggestLinkTarget("missing page", []string{"Completely Different", "Missing Pages"})
	if got != "Missing Pages" {
		t.Errorf("want Missing Pages, got %q", got)
	}
}

func TestSuggestLinkTarget_ReturnsEmptyWhenNothingClose(t *testing.T) {
	got := SuggestLinkTarget("quantum entanglement", []string{"Shopping List", "Meeting Notes"})
	if got != "" {
		t.Errorf("want no suggestion, got %q", got)
	}
}

func TestSuggestLinkTarget_SubstringContainment(t *testing.T) {
	// Short dead target contained in a live title — trigram Jaccard
	// alone undervalues this; the containment bonus must lift it over
	// the threshold.
	got := SuggestLinkTarget("机器学习", []string{"机器学习入门", "随机噪声页面"})
	if got != "机器学习入门" {
		t.Errorf("want 机器学习入门, got %q", got)
	}
}

func TestSuggestLinkTarget_SkipsExactMatch(t *testing.T) {
	// A title equal (case-folded) to the target would mean the link
	// isn't dead — never suggest it.
	got := SuggestLinkTarget("alpha", []string{"Alpha", "alpha"})
	if got != "" {
		t.Errorf("exact-match title must not be suggested, got %q", got)
	}
}

func TestSuggestLinkTarget_CaseInsensitiveScoring(t *testing.T) {
	got := SuggestLinkTarget("getting started", []string{"Getting Started Guide"})
	if got != "Getting Started Guide" {
		t.Errorf("want Getting Started Guide, got %q", got)
	}
}

func TestSuggestLinkTarget_DeterministicOnTies(t *testing.T) {
	titles := []string{"Alpha Notes", "Alpha Docs"}
	a := SuggestLinkTarget("alpha", titles)
	b := SuggestLinkTarget("alpha", titles)
	if a != b {
		t.Errorf("non-deterministic suggestion: %q vs %q", a, b)
	}
}

func TestSuggestLinkTarget_EmptyInputs(t *testing.T) {
	if got := SuggestLinkTarget("", []string{"A"}); got != "" {
		t.Errorf("empty target must yield no suggestion, got %q", got)
	}
	if got := SuggestLinkTarget("a", nil); got != "" {
		t.Errorf("nil titles must yield no suggestion, got %q", got)
	}
}
