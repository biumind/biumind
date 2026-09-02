package chunks

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
)

func TestChunkPage_EmptyTitleAndBlocks(t *testing.T) {
	got := ChunkPage("", nil, Options{})
	if len(got) != 0 {
		t.Fatalf("want 0 chunks, got %d", len(got))
	}
}

func TestChunkPage_TitleOnlyWhenBlocksEmpty(t *testing.T) {
	got := ChunkPage("Hello", nil, Options{})
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "Hello") {
		t.Fatalf("title-only chunk missing title: %q", got[0].Text)
	}
	if got[0].BlockID != nil {
		t.Fatalf("title-only chunk should not link a block")
	}
}

func TestChunkPage_HeadingPathCarriesTitle(t *testing.T) {
	bid := uuid.New()
	got := ChunkPage("Topic", []BlockSlice{
		{BlockID: bid, Text: "first paragraph"},
		{BlockID: uuid.New(), Text: "second paragraph"},
	}, Options{TargetChars: 1000, MinChars: 1, MaxChars: 1500})
	if len(got) < 2 {
		t.Fatalf("want >=2 chunks, got %d", len(got))
	}
	// Title now lives in HeadingPath (on EVERY chunk), not prefixed to
	// the first chunk's text. This is the post-②-behaviour: a strict
	// improvement — every chunk carries the page context.
	for i, c := range got {
		if c.HeadingPath != "Topic" {
			t.Errorf("chunk %d HeadingPath = %q, want %q", i, c.HeadingPath, "Topic")
		}
		if strings.HasPrefix(c.Text, "# Topic") {
			t.Errorf("chunk %d text should not carry title prefix: %q", i, c.Text)
		}
	}
}

func TestChunkPage_LongBlockSplitsBySentence(t *testing.T) {
	long := strings.Repeat("First sentence. Second sentence. Third sentence. ", 50)
	bid := uuid.New()
	got := ChunkPage("", []BlockSlice{{BlockID: bid, Text: long}}, Options{
		TargetChars: 200, MaxChars: 400, MinChars: 1, OverlapChars: 0,
	})
	if len(got) < 2 {
		t.Fatalf("expected long block to split; got %d chunks", len(got))
	}
	for i, c := range got {
		n := utf8.RuneCountInString(c.Text)
		if n > 400 {
			t.Errorf("chunk %d exceeds MaxChars: %d runes", i, n)
		}
	}
}

func TestChunkPage_CJKSentenceSplit(t *testing.T) {
	long := strings.Repeat("第一段话。第二段话。第三段话。", 30)
	bid := uuid.New()
	got := ChunkPage("中文标题", []BlockSlice{{BlockID: bid, Text: long}}, Options{
		TargetChars: 60, MaxChars: 120, MinChars: 1, OverlapChars: 0,
	})
	if len(got) < 2 {
		t.Fatalf("expected CJK long block to split; got %d", len(got))
	}
	if !strings.HasPrefix(got[0].HeadingPath, "中文标题") {
		t.Fatalf("first chunk missing CJK title in HeadingPath: %q", got[0].HeadingPath)
	}
}

func TestChunkPage_CaptionConcatenated(t *testing.T) {
	bid := uuid.New()
	got := ChunkPage("", []BlockSlice{
		{BlockID: bid, Text: "screenshot", Caption: "login form"},
	}, Options{TargetChars: 1000, MinChars: 1, MaxChars: 1500})
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "login form") {
		t.Fatalf("caption missing from chunk text: %q", got[0].Text)
	}
}

func TestChunkPage_SkipsEmptyBlocks(t *testing.T) {
	got := ChunkPage("T", []BlockSlice{
		{BlockID: uuid.New(), Text: "  "},
		{BlockID: uuid.New(), Text: "real"},
	}, Options{TargetChars: 1000, MinChars: 1, MaxChars: 1500})
	if len(got) != 1 {
		t.Fatalf("want 1 chunk (empty block skipped); got %d", len(got))
	}
}

func TestChunkPage_OrdMonotonic(t *testing.T) {
	long := strings.Repeat("alpha beta gamma. ", 50)
	got := ChunkPage("", []BlockSlice{{BlockID: uuid.New(), Text: long}}, Options{
		TargetChars: 100, MaxChars: 200, MinChars: 1, OverlapChars: 0,
	})
	if len(got) < 2 {
		t.Fatalf("setup: need >=2 chunks")
	}
	for i := 1; i < len(got); i++ {
		if got[i].Ord != got[i-1].Ord+1 {
			t.Errorf("ord not monotonic at %d: prev=%d cur=%d",
				i, got[i-1].Ord, got[i].Ord)
		}
	}
}

func TestChunkPage_TokenCountPositive(t *testing.T) {
	got := ChunkPage("T", []BlockSlice{{BlockID: uuid.New(), Text: "some text"}}, Options{})
	if got[0].TokenCount <= 0 {
		t.Fatalf("token count should be positive, got %d", got[0].TokenCount)
	}
}

// Deep-research output is one big markdown block (synthesis + ## References).
// Locks in: title prefixes the first chunk, references survive into chunks,
// long body splits into multiple chunks for embedding.
func TestChunkPage_ResearchPageShape(t *testing.T) {
	body := strings.Repeat(
		"Mixture-of-Experts routing trades sparsity for capacity. "+
			"Anthropic's [[claude]] uses a different architecture. ", 30) +
		"\n\n## References\n\n" +
		"1. [paper](https://example.com) — arxiv\n" +
		"2. [blog](https://example.org) — anthropic\n"
	got := ChunkPage("Research: MoE in 2025", []BlockSlice{
		{BlockID: uuid.New(), Text: body},
	}, Options{TargetChars: 400, MaxChars: 800, MinChars: 1})
	if len(got) < 2 {
		t.Fatalf("want >=2 chunks, got %d", len(got))
	}
	if !strings.HasPrefix(got[0].HeadingPath, "Research: MoE in 2025") {
		t.Fatalf("first chunk missing research title in HeadingPath: %q", got[0].HeadingPath)
	}
	// References section must land in some chunk so search/citations can
	// recover the source URLs.
	var refsHit bool
	for _, c := range got {
		if strings.Contains(c.Text, "## References") {
			refsHit = true
			break
		}
	}
	if !refsHit {
		t.Errorf("references section missing from all %d chunks", len(got))
	}
	// Wikilink survived into chunks (the embed path indexes the bare
	// `[[claude]]` text — search/api highlights it later).
	var wlHit bool
	for _, c := range got {
		if strings.Contains(c.Text, "[[claude]]") {
			wlHit = true
			break
		}
	}
	if !wlHit {
		t.Error("wikilink lost during chunking")
	}
}

func TestEstimateTokens_CJKHigherDensity(t *testing.T) {
	// 100 CJK chars should estimate to > 100 ASCII chars / 4.
	cjk := estimateTokens(strings.Repeat("中", 100))
	ascii := estimateTokens(strings.Repeat("a", 100))
	if cjk <= ascii {
		t.Fatalf("CJK density should exceed ASCII: cjk=%d ascii=%d", cjk, ascii)
	}
}

// ── headingPath breadcrumb ──────────────────────────────────────

func TestChunkPage_HeadingPathMultiLevel(t *testing.T) {
	got := ChunkPage("Page", []BlockSlice{
		{Type: "heading", Level: 2, Text: "Section A"},
		{Text: "body under A"},
		{Type: "heading", Level: 3, Text: "Sub A1"},
		{Text: "body under A1"},
		{Type: "heading", Level: 2, Text: "Section B"}, // pops Sub A1 + is sibling of A
		{Text: "body under B"},
	}, Options{TargetChars: 1000, MinChars: 1, MaxChars: 1500})

	// Body chunks (skip heading-only blocks which emit nothing) should
	// carry the accumulated breadcrumb, and a level-2 heading must pop
	// the prior level-3 sub.
	type want struct{ idx, path string }
	expected := []want{
		{"body under A", "Page > Section A"},
		{"body under A1", "Page > Section A > Sub A1"},
		{"body under B", "Page > Section B"},
	}
	seen := 0
	for _, c := range got {
		if seen >= len(expected) {
			break
		}
		if c.Text != expected[seen].idx {
			continue
		}
		if c.HeadingPath != expected[seen].path {
			t.Errorf("chunk %q HeadingPath = %q, want %q",
				expected[seen].idx, c.HeadingPath, expected[seen].path)
		}
		seen++
	}
	if seen != len(expected) {
		t.Fatalf("only matched %d/%d expected body chunks (got %d total): %+v",
			seen, len(expected), len(got), got)
	}
}

func TestChunkPage_HeadingBlockEmitsNoChunk(t *testing.T) {
	// A page that is headings only still needs a retrievable chunk, but
	// individual heading blocks don't emit — their context rides on body
	// chunks. With zero body chunks we fall back to title-only.
	got := ChunkPage("Title", []BlockSlice{
		{Type: "heading", Level: 2, Text: "Just a heading"},
	}, Options{TargetChars: 1000, MinChars: 1, MaxChars: 1500})
	// No body → no chunks from the loop; heading emitted nothing.
	if len(got) != 0 {
		t.Fatalf("heading-only page should emit no body chunks, got %d: %+v", len(got), got)
	}
}

// ── code / list indivisibility ──────────────────────────────────

func TestChunkPage_CodeBlockIndivisible(t *testing.T) {
	// A code block over MaxChars must stay ONE chunk — slicing a code
	// block mid-statement wrecks the embedding (the old hardSlice path).
	huge := strings.Repeat("x := compute()\n", 200) // ~3k chars
	got := ChunkPage("", []BlockSlice{
		{Type: "code", Text: huge},
	}, Options{TargetChars: 100, MaxChars: 200, MinChars: 1, OverlapChars: 0})
	if len(got) != 1 {
		t.Fatalf("oversized code block must stay 1 chunk, got %d", len(got))
	}
	if got[0].Text != strings.TrimSpace(huge) {
		t.Errorf("code chunk text was altered (len %d vs %d)",
			len(got[0].Text), len(huge))
	}
}

func TestChunkPage_TableBlockIndivisible(t *testing.T) {
	// A GFM table over MaxChars must stay ONE chunk — the split ladder
	// would tear it mid-row, and a header row orphaned from its body
	// embeds as garbage (the pre-table-type behaviour).
	var sb strings.Builder
	sb.WriteString("| Name | Value |\n| --- | --- |\n")
	for i := 0; i < 100; i++ {
		sb.WriteString("| row-name-that-is-fairly-long | some cell value here |\n")
	}
	huge := sb.String() // ~6k chars
	got := ChunkPage("", []BlockSlice{
		{Type: "table", Text: huge},
	}, Options{TargetChars: 100, MaxChars: 200, MinChars: 1, OverlapChars: 0})
	if len(got) != 1 {
		t.Fatalf("oversized table block must stay 1 chunk, got %d", len(got))
	}
	if got[0].Text != strings.TrimSpace(huge) {
		t.Errorf("table chunk text was altered (len %d vs %d)",
			len(got[0].Text), len(huge))
	}
}

func TestChunkPage_ListBlockEmitted(t *testing.T) {
	got := ChunkPage("P", []BlockSlice{
		{Type: "list", Items: []string{"alpha", "beta", "gamma"}},
	}, Options{TargetChars: 1000, MinChars: 1, MaxChars: 1500})
	if len(got) != 1 {
		t.Fatalf("want 1 list chunk, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "alpha") || !strings.Contains(got[0].Text, "gamma") {
		t.Errorf("list items lost from chunk text: %q", got[0].Text)
	}
}

// ── overlap ─────────────────────────────────────────────────────

func TestChunkPage_OverlapEngagesSameSection(t *testing.T) {
	// One long text block splits into several chunks; with OverlapChars>0
	// each chunk after the first must start with a tail slice of its
	// predecessor (same section, additive — never shortens the source).
	body := strings.Repeat("sentence about routing. ", 120) // ~2.7k chars
	got := ChunkPage("Topic", []BlockSlice{{Text: body}}, Options{
		TargetChars: 200, MaxChars: 400, MinChars: 1, OverlapChars: 60,
	})
	if len(got) < 2 {
		t.Fatalf("need >=2 chunks to exercise overlap, got %d", len(got))
	}
	// chunk[0] is the un-overlapped source head; chunk[1] should begin
	// with the tail of chunk[0].
	tail := lastRunes(got[0].Text, 60)
	if tail == "" {
		t.Fatalf("could not derive overlap tail from chunk[0]: %q", got[0].Text)
	}
	if !strings.HasPrefix(got[1].Text, tail) {
		t.Errorf("chunk[1] should start with predecessor tail %q; got head %q",
			tail, got[1].Text[:min(len(tail), len(got[1].Text))])
	}
}

func TestChunkPage_OverlapZeroIsNoop(t *testing.T) {
	// OverlapChars:0 must not prepend anything (back-compat with the
	// three existing tests that pass 0 explicitly).
	body := strings.Repeat("alpha beta gamma. ", 50)
	got := ChunkPage("", []BlockSlice{{Text: body}}, Options{
		TargetChars: 100, MaxChars: 200, MinChars: 1, OverlapChars: 0,
	})
	if len(got) < 2 {
		t.Fatalf("need >=2 chunks, got %d", len(got))
	}
	// Reconstruct the no-overlap split and compare — chunk[1] must equal
	// exactly the second split piece, no prepended tail.
	if strings.Contains(got[0].Text, "\n") && got[0].Text == got[1].Text {
		t.Errorf("overlap unexpectedly applied with OverlapChars=0")
	}
}
