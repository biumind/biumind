package api

import (
	"testing"
)

// TestFeedbackRerankEmptyVerdicts — no verdicts ⇒ identity order, even
// though scores differ. The whole rerank is a no-op without feedback.
func TestFeedbackRerankEmptyVerdicts(t *testing.T) {
	items := []feedbackScore{
		{score: 0.1, pageID: "a"},
		{score: 0.9, pageID: "b"},
	}
	got := feedbackRerank(items, nil)
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("empty verdicts must return identity [0 1], got %v", got)
	}
	// Same for an empty (non-nil) map.
	got = feedbackRerank(items, map[string]string{})
	if got[0] != 0 || got[1] != 1 {
		t.Fatalf("empty map must return identity, got %v", got)
	}
}

// TestFeedbackRerankUpPromotes — an upvoted mid-ranked page overtakes a
// higher unvoted page once the +0.5 bonus lands.
func TestFeedbackRerankUpPromotes(t *testing.T) {
	items := []feedbackScore{
		{score: 1.0, pageID: "a"}, // normalized 1.0
		{score: 0.6, pageID: "b"}, // normalized 0.6 + 0.5 = 1.1 → first
	}
	got := feedbackRerank(items, map[string]string{"b": "up"})
	if got[0] != 1 || got[1] != 0 {
		t.Fatalf("upvoted b should lead, got order %v", got)
	}
}

// TestFeedbackRerankDownDemotes — a downvoted top page sinks below an
// unvoted mid page.
func TestFeedbackRerankDownDemotes(t *testing.T) {
	items := []feedbackScore{
		{score: 1.0, pageID: "a"}, // 1.0 − 0.5 = 0.5
		{score: 0.6, pageID: "b"}, // 0.6 → leads
	}
	got := feedbackRerank(items, map[string]string{"a": "down"})
	if got[0] != 1 || got[1] != 0 {
		t.Fatalf("downvoted a should sink, got order %v", got)
	}
}

// TestFeedbackRerankStable — equal adjusted scores preserve the engine's
// original order (sort.SliceStable, not sort.Slice).
func TestFeedbackRerankStable(t *testing.T) {
	items := []feedbackScore{
		{score: 0.5, pageID: "a"},
		{score: 0.5, pageID: "b"},
		{score: 0.5, pageID: "c"},
	}
	// All equal → original a,b,c order.
	got := feedbackRerank(items, map[string]string{"x": "up"})
	if got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("equal scores must stay stable a,b,c, got %v", got)
	}
}

// TestFeedbackRerankAllZeroSafe — a list of zero scores must not
// divide-by-zero; the upvoted page still leads via its +0.5 bonus.
func TestFeedbackRerankAllZeroSafe(t *testing.T) {
	items := []feedbackScore{
		{score: 0, pageID: "a"},
		{score: 0, pageID: "b"},
	}
	got := feedbackRerank(items, map[string]string{"b": "up"})
	if got[0] != 1 {
		t.Fatalf("zero-score upvoted b should lead, got %v", got)
	}
}

// TestReorderWikiBadgesAndReorders — the helper both reorders and stamps
// the Feedback badge on the rows the user moved; unvoted rows get no
// badge.
func TestReorderWikiBadgesAndReorders(t *testing.T) {
	hits := []wikiHit{
		{PageID: "a", Score: 1.0, Title: "A"},
		{PageID: "b", Score: 0.6, Title: "B"},
	}
	out := reorderWiki(hits, map[string]string{"b": "up"})
	if out[0].PageID != "b" {
		t.Fatalf("b should lead after upvote, got %v", out)
	}
	if out[0].Feedback != "up" {
		t.Fatalf("b should carry up badge, got %q", out[0].Feedback)
	}
	if out[1].Feedback != "" {
		t.Fatalf("unvoted a should have no badge, got %q", out[1].Feedback)
	}
	// Raw score preserved — only order moved.
	if out[0].Score != 0.6 {
		t.Fatalf("raw score must be preserved, got %v", out[0].Score)
	}
}

// TestReorderFusedExtractsPageID — fused hits carry page_id inside Meta;
// web items (no page_id) are never badged and stay put relative to other
// unvoted items.
func TestReorderFusedExtractsPageID(t *testing.T) {
	hits := []fusedHit{
		{ID: "wiki:page:a", Score: 1.0, Meta: map[string]any{"page_id": "a", "source": "wiki"}},
		{ID: "wiki:page:b", Score: 0.6, Meta: map[string]any{"page_id": "b", "source": "vector"}},
		{ID: "web:https://x", Score: 0.8, Meta: map[string]any{"url": "https://x", "source": "web"}},
	}
	out := reorderFused(hits, map[string]string{"b": "up"})
	// b normalized 0.6 + 0.5 = 1.1 → first, a 1.0 second, web 0.8 third.
	if out[0].ID != "wiki:page:b" {
		t.Fatalf("b should lead, got %v", []string{out[0].ID, out[1].ID, out[2].ID})
	}
	if out[0].Feedback != "up" {
		t.Fatalf("b should be badged up, got %q", out[0].Feedback)
	}
	if out[2].Feedback != "" {
		t.Fatalf("web hit must never be badged, got %q", out[2].Feedback)
	}
}
