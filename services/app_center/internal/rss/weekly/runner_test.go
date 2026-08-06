package weekly

import (
	"strings"
	"testing"
	"time"
)

func TestShouldRunNow(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"sunday 7am - too early", time.Date(2026, 6, 14, 7, 30, 0, 0, time.UTC), false},
		{"sunday 8am - go", time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC), true},
		{"sunday 11pm - go", time.Date(2026, 6, 14, 23, 0, 0, 0, time.UTC), true},
		{"monday 8am - skip", time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC), false},
		{"saturday 10am - skip", time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldRunNow(c.t); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsoWeek(t *testing.T) {
	// 2026-06-14 is Sunday; check ISO week formatting
	w := isoWeek(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))
	// 2026-06-14 是 ISO week 24 (W23 ends Sun 2026-06-07; W24 = Mon 2026-06-08
	// .. Sun 2026-06-14)
	if !strings.HasPrefix(w, "2026-W") {
		t.Errorf("format wrong: %q", w)
	}
}

func TestBuildSummaryHeader(t *testing.T) {
	st := &userStats{
		StarredN:  3,
		ReadN:     12,
		WikiN:     2,
		TopTopics: []string{"AI", "硬件"},
	}
	got := buildSummaryHeader(st)
	for _, want := range []string{"读 12", "收藏 3", "沉 wiki 2", "AI", "硬件"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	st := &userStats{
		StarredN:  3,
		ReadN:     12,
		TopTopics: []string{"AI"},
		TopEntries: []entryBrief{
			{Title: "GPT-5 发布", FeedTitle: "PingWest", Takeaway: "比 GPT-4o 提升 40%"},
		},
	}
	got := buildSystemPrompt(st)
	for _, want := range []string{
		"BiuMind RSS 的周报作者",
		"starred=3",
		"read=12",
		"AI",
		"GPT-5 发布",
		"PingWest",
		"提升 40%",
		"[1]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
