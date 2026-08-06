package rss

import (
	"strings"
	"testing"
)

func TestFromPicks_Empty(t *testing.T) {
	got := FromPicks(nil)
	if !strings.Contains(got.Text, "暂时没有") {
		t.Errorf("nil picks should produce empty-state script, got %q", got.Text)
	}
	if got.HeadlineN != 0 {
		t.Errorf("HeadlineN: got %d, want 0", got.HeadlineN)
	}

	got = FromPicks(&TodayPicks{Headline: nil})
	if !strings.Contains(got.Text, "暂时没有") {
		t.Errorf("empty headline should produce empty-state, got %q", got.Text)
	}
}

func TestFromPicks_HappyPath(t *testing.T) {
	picks := &TodayPicks{
		Headline: []TodayEntry{
			{
				Title:      "OpenAI 发布 GPT-5",
				FeedTitle:  "PingWest",
				AITakeaway: "GPT-5 在推理任务上比 GPT-4o 提升 40%。",
			},
			{
				Title:      "苹果 Vision Pro 二代曝光",
				FeedTitle:  "MacRumors",
				AITakeaway: "电池续航从 2 小时延长到 4 小时",
			},
		},
	}
	got := FromPicks(picks)

	if !strings.Contains(got.Text, "今天为你挑了 2 篇") {
		t.Errorf("opener missing: %q", got.Text)
	}
	if !strings.Contains(got.Text, "第一篇") {
		t.Errorf("first article numeral missing: %q", got.Text)
	}
	if !strings.Contains(got.Text, "第二篇") {
		t.Errorf("second article numeral missing: %q", got.Text)
	}
	if !strings.Contains(got.Text, "OpenAI 发布 GPT-5") {
		t.Errorf("title not embedded: %q", got.Text)
	}
	if !strings.Contains(got.Text, "PingWest") {
		t.Errorf("feed title missing: %q", got.Text)
	}
	if !strings.Contains(got.Text, "以上就是今天的简报") {
		t.Errorf("closer missing: %q", got.Text)
	}
	if got.HeadlineN != 2 {
		t.Errorf("HeadlineN: got %d, want 2", got.HeadlineN)
	}
}

func TestFromPicks_StripsMarkdown(t *testing.T) {
	picks := &TodayPicks{
		Headline: []TodayEntry{
			{
				Title:      "**Bold title** with `code`",
				AITakeaway: "Multi*line*\nbreak \t and tabs",
			},
		},
	}
	got := FromPicks(picks)
	for _, banned := range []string{"**", "`", "\n", "\t"} {
		if strings.Contains(got.Text, banned) {
			t.Errorf("markdown leak %q in: %q", banned, got.Text)
		}
	}
}

func TestFromPicks_CapsAt5Headlines(t *testing.T) {
	picks := &TodayPicks{Headline: make([]TodayEntry, 10)}
	for i := range picks.Headline {
		picks.Headline[i] = TodayEntry{Title: "t"}
	}
	got := FromPicks(picks)
	if got.HeadlineN != 5 {
		t.Errorf("HeadlineN cap: got %d, want 5", got.HeadlineN)
	}
	if strings.Contains(got.Text, "第六篇") {
		t.Error("should cap at 5; 第六篇 should not appear")
	}
}

func TestFromPicks_TrimsTrailingPunct(t *testing.T) {
	picks := &TodayPicks{
		Headline: []TodayEntry{
			{Title: "x", AITakeaway: "已经有句号了。"},
		},
	}
	got := FromPicks(picks)
	// "已经有句号了。。" 不应出现 (trim 后只保留一个句号)
	if strings.Contains(got.Text, "了。。") {
		t.Errorf("double period leak: %q", got.Text)
	}
}
