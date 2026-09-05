package vector

import (
	"math"
	"testing"
)

func TestOverFetchLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{1, 30},   // floor kicks in
		{10, 30},  // 10×3 = 30, exactly at floor
		{20, 60},  // ×3 above floor
		{100, 300},
		{0, 30},
	}
	for _, c := range cases {
		if got := OverFetchLimit(c.in); got != c.want {
			t.Errorf("OverFetchLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func chunk(chunkID, pageID string, score float64) Hit {
	return Hit{ChunkID: chunkID, Kind: "chunk", PageID: pageID, Score: score}
}

func TestCollapsePages_TailWeightOrdering(t *testing.T) {
	// Page A: one chunk at 0.9. Page B: best chunk 0.85 + tail chunks
	// 0.5 + 0.4. Blended B = 0.85 + 0.3×0.9 = 1.12 → capped contribution
	// is min(0.27, 0.15) = 0.15 → 1.00. So B (two strong chunks) must
	// outrank A (single strong chunk).
	hits := []Hit{
		chunk("a1", "page-a", 0.9),
		chunk("b1", "page-b", 0.85),
		chunk("b2", "page-b", 0.5),
		chunk("b3", "page-b", 0.4),
	}
	got := CollapsePages(hits, 10)
	if len(got) != 2 {
		t.Fatalf("pages = %d, want 2", len(got))
	}
	if got[0].PageID != "page-b" {
		t.Errorf("rank 1 = %s, want page-b (tail contribution wins)", got[0].PageID)
	}
	wantB := 0.85 + math.Min(0.3*(0.5+0.4), 1-0.85)
	if math.Abs(got[0].Score-wantB) > 1e-9 {
		t.Errorf("page-b score = %v, want %v (max + min(0.3·tail, 1−max))", got[0].Score, wantB)
	}
	if got[1].PageID != "page-a" || got[1].Score != 0.9 {
		t.Errorf("page-a = %+v, want score 0.9 unchanged (single chunk)", got[1])
	}
	// Representative row is the best chunk — deep-link stays intact.
	if got[0].ChunkID != "b1" {
		t.Errorf("page-b rep chunk = %s, want b1", got[0].ChunkID)
	}
}

func TestCollapsePages_TailCapPreventsDrowning(t *testing.T) {
	// Page C: many mediocre chunks whose raw 0.3-weighted tail would
	// exceed the headroom — the blend must cap at score 1.0.
	hits := []Hit{chunk("x1", "page-x", 0.99)}
	for range 10 {
		hits = append(hits, chunk("c", "page-c", 0.8))
	}
	got := CollapsePages(hits, 10)
	var cScore float64
	for _, h := range got {
		if h.PageID == "page-c" {
			cScore = h.Score
		}
	}
	if cScore > 1.0+1e-9 {
		t.Errorf("page-c score = %v, want ≤ 1.0 (cap)", cScore)
	}
	// 0.8 + min(0.3×9×0.8, 0.2) = 1.0 exactly.
	if math.Abs(cScore-1.0) > 1e-9 {
		t.Errorf("page-c score = %v, want 1.0", cScore)
	}
}

func TestCollapsePages_SingleChunkPagesUnaffected(t *testing.T) {
	hits := []Hit{
		chunk("a1", "page-a", 0.7),
		chunk("b1", "page-b", 0.9),
		chunk("c1", "page-c", 0.5),
	}
	got := CollapsePages(hits, 10)
	if len(got) != 3 {
		t.Fatalf("pages = %d, want 3", len(got))
	}
	for i, want := range []struct {
		id    string
		score float64
	}{{"page-b", 0.9}, {"page-a", 0.7}, {"page-c", 0.5}} {
		if got[i].PageID != want.id || got[i].Score != want.score {
			t.Errorf("rank %d = %s@%v, want %s@%v", i, got[i].PageID, got[i].Score, want.id, want.score)
		}
	}
}

func TestCollapsePages_TruncatesToLimit(t *testing.T) {
	var hits []Hit
	for i := range 40 {
		hits = append(hits, chunk("c", "page-"+string(rune('a'+i%26))+string(rune('a'+i/26)), float64(i)/100))
	}
	got := CollapsePages(hits, 5)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Errorf("not sorted desc at %d: %v < %v", i, got[i-1].Score, got[i].Score)
		}
	}
}

func TestCollapsePages_BestChunkBecomesRep(t *testing.T) {
	// Best chunk seen later in the list must still become the rep, with
	// the earlier (weaker) chunk folded into the tail.
	hits := []Hit{
		chunk("weak", "page-a", 0.4),
		chunk("strong", "page-a", 0.8),
	}
	got := CollapsePages(hits, 10)
	if len(got) != 1 {
		t.Fatalf("pages = %d, want 1", len(got))
	}
	if got[0].ChunkID != "strong" {
		t.Errorf("rep = %s, want strong", got[0].ChunkID)
	}
	want := 0.8 + 0.3*0.4
	if math.Abs(got[0].Score-want) > 1e-9 {
		t.Errorf("score = %v, want %v", got[0].Score, want)
	}
}

func TestCollapsePages_EmptyAndZeroLimit(t *testing.T) {
	if got := CollapsePages(nil, 10); got != nil {
		t.Errorf("nil hits = %v, want nil", got)
	}
	if got := CollapsePages([]Hit{chunk("a", "p", 0.5)}, 0); got != nil {
		t.Errorf("zero limit = %v, want nil", got)
	}
}
