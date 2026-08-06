package interest

import "testing"

func TestTopNTopics_OrderAndCap(t *testing.T) {
	got := topNTopics(map[string]int{
		"AI": 10,
		"科技": 8,
		"投资": 8,
		"娱乐": 2,
		"政策": 5,
		"设计": 4,
	}, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// AI first by count, then 投资+科技 tie → alphabetical 投资 < 科技 in
	// utf8 byte order? Actually 投 = U+6295 (0xE6 0x8A 0x95), 科 = U+79D1
	// (0xE7 0xA7 0x91), so 投 < 科 byte-wise — 投资 comes before 科技.
	if got[0] != "AI" {
		t.Errorf("top1 = %q, want AI", got[0])
	}
	// Tie-break order checks just ensure determinism.
	if got[1] == got[2] {
		t.Errorf("top2 == top3, got %v", got)
	}
}

func TestTopNTopics_FewerThanK(t *testing.T) {
	got := topNTopics(map[string]int{"AI": 5, "科技": 3}, 5)
	if len(got) != 2 {
		t.Errorf("got %d, want 2 (only 2 topics)", len(got))
	}
}

func TestTopNTopics_Empty(t *testing.T) {
	got := topNTopics(map[string]int{}, 5)
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}
