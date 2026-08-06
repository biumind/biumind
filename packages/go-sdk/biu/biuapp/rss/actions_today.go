// today_picks — returns the user's curated daily front-page payload.
// Wired only when a TodayPicker is attached via WithTodayPicker.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// TodayPicks is the SDK projection of the picker output. The services
// layer adapts its concrete *today.Picks → this shape.
type TodayPicks struct {
	Headline    []TodayEntry  `json:"headline"`
	Missed      []TodayEntry  `json:"missed"`
	Trends      []TodayTrend  `json:"trends"`
	Stats       TodayStats    `json:"stats"`
	GeneratedAt time.Time     `json:"generated_at"`
}

type TodayEntry struct {
	ID           string    `json:"id"`
	FeedID       string    `json:"feed_id"`
	FeedTitle    string    `json:"feed_title"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Author       string    `json:"author,omitempty"`
	AITakeaway   string    `json:"ai_takeaway,omitempty"`
	AIBullets    []string  `json:"ai_bullets,omitempty"`
	AITopics     []string  `json:"ai_topics,omitempty"`
	AIImportance int       `json:"ai_importance,omitempty"`
	WordCount    int       `json:"word_count,omitempty"`
	ReadingSec   int       `json:"reading_seconds,omitempty"`
	PublishedAt  time.Time `json:"published_at,omitempty"`
	ClusterSize  int       `json:"cluster_size,omitempty"`
	OtherURLs    []string  `json:"other_urls,omitempty"`
}

type TodayTrend struct {
	Topic string `json:"topic"`
	Count int    `json:"count"`
}

type TodayStats struct {
	UnreadTotal  int `json:"unread_total"`
	ReadToday    int `json:"read_today"`
	StreakDays   int `json:"streak_days"`
	WikiThisWeek int `json:"wiki_this_week"`
}

// TodayPicker is the SDK-side surface; services wires it via the
// today.Picker → SDK adapter.
type TodayPicker interface {
	PickFor(ctx context.Context, userID string) (*TodayPicks, error)
	Invalidate(userID string)
}

// WithTodayPicker wires the picker. Optional; when set, today_picks
// action is exposed.
func (a *App) WithTodayPicker(p TodayPicker) *App {
	a.today = p
	return a
}

func (a *App) invokeTodayPicks(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.today == nil {
		return nil, errors.New("rss: today picker not wired")
	}
	_, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	picks, err := a.today.PickFor(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	return picks, nil
}
