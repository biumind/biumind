package enrich

import "testing"

func TestBuildIndex(t *testing.T) {
	got := BuildIndex([]string{"Transformer", "RNN", "  ", "transformer", "Attention"})
	want := "- Attention\n- RNN\n- Transformer"
	if got != want {
		t.Errorf("BuildIndex sort/dedup wrong:\n got: %q\nwant: %q", got, want)
	}
}

func TestParseLinkResponse_HappyPath(t *testing.T) {
	raw := `{"links":[{"term":"Transformer","target":"transformer"},{"term":"RNN","target":"rnn"}]}`
	got := ParseLinkResponse(raw)
	if len(got) != 2 || got[0].Term != "Transformer" || got[1].Target != "rnn" {
		t.Errorf("parsed wrong: %+v", got)
	}
}

func TestParseLinkResponse_CodeFence(t *testing.T) {
	raw := "```json\n{\"links\":[{\"term\":\"x\",\"target\":\"y\"}]}\n```"
	got := ParseLinkResponse(raw)
	if len(got) != 1 || got[0].Term != "x" || got[0].Target != "y" {
		t.Errorf("parsed wrong: %+v", got)
	}
}

func TestParseLinkResponse_PreambleAndPostamble(t *testing.T) {
	raw := "Sure, here you go:\n{\"links\":[{\"term\":\"a\",\"target\":\"b\"}]}\n\nLet me know if that works!"
	got := ParseLinkResponse(raw)
	if len(got) != 1 || got[0].Term != "a" {
		t.Errorf("parsed wrong: %+v", got)
	}
}

func TestParseLinkResponse_MalformedReturnsEmpty(t *testing.T) {
	cases := []string{
		"",
		"not json at all",
		"{links:[]}", // unquoted key, invalid JSON
		"{",
	}
	for _, c := range cases {
		if got := ParseLinkResponse(c); len(got) != 0 {
			t.Errorf("ParseLinkResponse(%q) = %+v, want empty", c, got)
		}
	}
}

func TestParseLinkResponse_DropsEmptyEntries(t *testing.T) {
	raw := `{"links":[{"term":"","target":"y"},{"term":"x","target":""},{"term":"x","target":"y"}]}`
	got := ParseLinkResponse(raw)
	if len(got) != 1 || got[0].Term != "x" || got[0].Target != "y" {
		t.Errorf("got %+v, want one valid", got)
	}
}

func TestApplyLinks_BasicSubstitution(t *testing.T) {
	body := "Transformers replaced RNNs in NLP."
	links := []LinkEntry{
		{Term: "Transformers", Target: "transformer"},
		{Term: "RNNs", Target: "rnn"},
	}
	got := ApplyLinks(body, links)
	want := "[[transformer|Transformers]] replaced [[rnn|RNNs]] in NLP."
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}

func TestApplyLinks_CaseInsensitiveTermEqualsTargetGivesShortForm(t *testing.T) {
	body := "transformer is great."
	got := ApplyLinks(body, []LinkEntry{{Term: "transformer", Target: "Transformer"}})
	want := "[[transformer]] is great."
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}

func TestApplyLinks_FirstOccurrenceOnly(t *testing.T) {
	body := "RNN was popular. RNN had issues."
	got := ApplyLinks(body, []LinkEntry{{Term: "RNN", Target: "rnn"}})
	// term ≈ target case-insensitively → short form [[term]].
	// Second RNN unmodified — matches v2 behaviour.
	want := "[[RNN]] was popular. RNN had issues."
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}

func TestApplyLinks_SkipsExistingWikilinks(t *testing.T) {
	body := "We compare [[rnn|RNN]] to GRU; later RNN appears bare."
	// Previously-linked RNN stays. Bare RNN is the first UNLINKED occurrence.
	got := ApplyLinks(body, []LinkEntry{{Term: "RNN", Target: "rnn"}})
	want := "We compare [[rnn|RNN]] to GRU; later [[RNN]] appears bare."
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}

func TestApplyLinks_FrontmatterPreserved(t *testing.T) {
	body := "---\ntitle: A\n---\nTransformer is here."
	got := ApplyLinks(body, []LinkEntry{{Term: "Transformer", Target: "transformer"}})
	want := "---\ntitle: A\n---\n[[Transformer]] is here."
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}

func TestApplyLinks_FrontmatterContainingTitleNotLinked(t *testing.T) {
	// frontmatter mentions Transformer too, but body splitter excludes it.
	body := "---\ntitle: Transformer notes\n---\nThe Transformer body."
	got := ApplyLinks(body, []LinkEntry{{Term: "Transformer", Target: "transformer"}})
	want := "---\ntitle: Transformer notes\n---\nThe [[Transformer]] body."
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}

func TestApplyLinks_NoMatchSilent(t *testing.T) {
	body := "nothing of note."
	got := ApplyLinks(body, []LinkEntry{{Term: "Transformer", Target: "transformer"}})
	if got != body {
		t.Errorf("got %q, expected unchanged", got)
	}
}

func TestApplyLinks_DedupesByTarget(t *testing.T) {
	body := "RNN GRU."
	links := []LinkEntry{
		{Term: "RNN", Target: "rnn"},
		{Term: "GRU", Target: "rnn"}, // same target — should be skipped after first
	}
	got := ApplyLinks(body, links)
	want := "[[RNN]] GRU."
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}
