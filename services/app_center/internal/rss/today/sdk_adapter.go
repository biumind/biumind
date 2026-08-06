// Adapter — bridges *Picker to rss.TodayPicker so the SDK doesn't
// need to import services internal packages.

package today

import (
	"context"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
)

type SDKAdapter struct {
	Picker *Picker
}

var _ rss.TodayPicker = (*SDKAdapter)(nil)

func (a *SDKAdapter) PickFor(ctx context.Context, userID string) (*rss.TodayPicks, error) {
	picks, err := a.Picker.PickFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	return projectPicks(picks), nil
}

func (a *SDKAdapter) Invalidate(userID string) {
	a.Picker.Invalidate(userID)
}

func projectPicks(p *Picks) *rss.TodayPicks {
	out := &rss.TodayPicks{
		GeneratedAt: p.GeneratedAt,
		Stats: rss.TodayStats{
			UnreadTotal:  p.Stats.UnreadTotal,
			ReadToday:    p.Stats.ReadToday,
			StreakDays:   p.Stats.StreakDays,
			WikiThisWeek: p.Stats.WikiThisWeek,
		},
	}
	for _, e := range p.Headline {
		out.Headline = append(out.Headline, projectEntry(e))
	}
	for _, e := range p.Missed {
		out.Missed = append(out.Missed, projectEntry(e))
	}
	for _, t := range p.Trends {
		out.Trends = append(out.Trends, rss.TodayTrend{Topic: t.Topic, Count: t.Count})
	}
	return out
}

func projectEntry(e *Entry) rss.TodayEntry {
	return rss.TodayEntry{
		ID:           e.ID.String(),
		FeedID:       e.FeedID.String(),
		FeedTitle:    e.FeedTitle,
		URL:          e.URL,
		Title:        e.Title,
		Author:       e.Author,
		AITakeaway:   e.AITakeaway,
		AIBullets:    e.AIBullets,
		AITopics:     e.AITopics,
		AIImportance: e.AIImportance,
		WordCount:    e.WordCount,
		ReadingSec:   e.ReadingSec,
		PublishedAt:  e.PublishedAt,
		ClusterSize:  e.ClusterSize,
		OtherURLs:    e.OtherURLs,
	}
}
