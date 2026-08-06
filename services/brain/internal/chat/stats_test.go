package chat

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// streakStats is a pure function — unit-test it without a DB.
func TestStreakStats(t *testing.T) {
	now := time.Date(2026, 6, 25, 13, 0, 0, 0, time.UTC)

	cases := []struct {
		name                         string
		days                         []string // ascending, count>0 (as the query returns)
		wantActive, wantCur, wantMax int
	}{
		{
			name:       "empty",
			days:       nil,
			wantActive: 0, wantCur: 0, wantMax: 0,
		},
		{
			name:       "today anchors current; separate longer run sets max",
			days:       []string{"2026-06-15", "2026-06-16", "2026-06-17", "2026-06-18", "2026-06-19", "2026-06-20", "2026-06-23", "2026-06-24", "2026-06-25"},
			wantActive: 9, wantCur: 3, wantMax: 6,
		},
		{
			name:       "today idle but yesterday active → current anchors on yesterday",
			days:       []string{"2026-06-23", "2026-06-24"},
			wantActive: 2, wantCur: 2, wantMax: 2,
		},
		{
			name:       "today and yesterday both idle → current 0",
			days:       []string{"2026-06-01", "2026-06-02", "2026-06-03"},
			wantActive: 3, wantCur: 0, wantMax: 3,
		},
		{
			name:       "single day today",
			days:       []string{"2026-06-25"},
			wantActive: 1, wantCur: 1, wantMax: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hm := make([]HeatmapDay, len(c.days))
			for i, d := range c.days {
				hm[i] = HeatmapDay{Date: d, Count: 1}
			}
			active, cur, max := streakStats(hm, now)
			if active != c.wantActive || cur != c.wantCur || max != c.wantMax {
				t.Errorf("got active=%d cur=%d max=%d; want active=%d cur=%d max=%d",
					active, cur, max, c.wantActive, c.wantCur, c.wantMax)
			}
		})
	}
}

// TestStatsAggregation hits a real Postgres (skips without DATABASE_URL).
func TestStatsAggregation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	mGlm := "glm-5.1"
	mOpus := "claude-opus-4-8"

	// Thread A: 3 messages (2 glm, 1 opus). Thread B: 1 message (opus).
	tA, err := s.CreateThread(ctx, CreateThreadInput{UserID: uid, Title: "A", SyncEnabled: true})
	if err != nil {
		t.Fatalf("thread A: %v", err)
	}
	tB, err := s.CreateThread(ctx, CreateThreadInput{UserID: uid, Title: "B", SyncEnabled: true})
	if err != nil {
		t.Fatalf("thread B: %v", err)
	}
	mk := func(tid uuid.UUID, role, model string) {
		in := CreateMessageInput{ThreadID: tid, UserID: uid, Role: role, Content: "x", Status: StatusSuccess}
		if model != "" {
			in.Model = &model
		}
		if _, err := s.CreateMessage(ctx, in); err != nil {
			t.Fatalf("msg: %v", err)
		}
	}
	mk(tA.ID, RoleUser, "")
	mk(tA.ID, RoleAssistant, mGlm)
	mk(tA.ID, RoleAssistant, mGlm)
	mk(tB.ID, RoleAssistant, mOpus)

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	res, err := s.Stats(ctx, uid, monthStart, now.AddDate(-1, 0, 0))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if res.Threads.Count != 2 {
		t.Errorf("threads.count = %d, want 2", res.Threads.Count)
	}
	if res.Messages.Count != 4 {
		t.Errorf("messages.count = %d, want 4", res.Messages.Count)
	}
	if res.Models.Count != 2 {
		t.Errorf("models.count = %d, want 2", res.Models.Count)
	}
	// Just-created rows are after monthStart → prev counts are 0.
	if res.Threads.Prev != 0 || res.Messages.Prev != 0 {
		t.Errorf("prev should be 0 for freshly created rows: %+v / %+v", res.Threads, res.Messages)
	}
	// Model rank: glm (2) before opus (1).
	if len(res.ModelRank) != 2 || res.ModelRank[0].Model != mGlm || res.ModelRank[0].Count != 2 {
		t.Errorf("model rank unexpected: %+v", res.ModelRank)
	}
	// Topic rank: A (3 msgs) before B (1 msg).
	if len(res.TopicRank) != 2 || res.TopicRank[0].ThreadID != tA.ID.String() || res.TopicRank[0].Count != 3 {
		t.Errorf("topic rank unexpected: %+v", res.TopicRank)
	}
	// Heatmap: all activity is today → one bucket with count 4.
	var total int64
	for _, h := range res.Heatmap {
		total += h.Count
	}
	if total != 4 {
		t.Errorf("heatmap total = %d, want 4", total)
	}
}
