package rss

import "testing"

func TestExtractCitations(t *testing.T) {
	items := []CopilotItem{
		{N: 1, EntryID: "e1", Title: "AI 监管"},
		{N: 2, EntryID: "e2", Title: "苹果发布"},
		{N: 3, EntryID: "e3", Title: "GPT-5"},
	}

	cases := []struct {
		name   string
		answer string
		want   []string // entry_id sequence
	}{
		{
			"single citation",
			"今天 [1] 是关键事件",
			[]string{"e1"},
		},
		{
			"multiple in order",
			"先看 [1], 然后 [3], 最后 [2] 也值得",
			[]string{"e1", "e3", "e2"},
		},
		{
			"duplicate citations dedup, first occurrence wins",
			"重要的是 [2], 同样 [2] 提到了, 也参考 [1]",
			[]string{"e2", "e1"},
		},
		{
			"hallucinated N skipped",
			"看 [1] 和 [99] 和 [2]",
			[]string{"e1", "e2"},
		},
		{
			"no citations",
			"这是一段无引用的纯文本",
			nil,
		},
		{
			"large N over 99 not matched",
			"[100] 不应匹配, 但 [1] 应该",
			[]string{"e1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractCitations(tc.answer, items)
			if len(got) != len(tc.want) {
				t.Fatalf("count: got %d %+v, want %d", len(got), got, len(tc.want))
			}
			for i, want := range tc.want {
				if got[i].EntryID != want {
					t.Errorf("[%d]: got %q, want %q", i, got[i].EntryID, want)
				}
			}
		})
	}
}

func TestExtractCitations_EmptyItems(t *testing.T) {
	got := extractCitations("含 [1] [2]", nil)
	if got != nil {
		t.Errorf("expected nil for empty items, got %+v", got)
	}
}
