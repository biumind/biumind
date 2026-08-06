// Account-level chat statistics — read-only aggregations over chat.threads /
// chat.messages for the client 数据统计 (data statistics) overview page.
//
// All queries are owner-scoped (user_id = $1) and cross-device by nature:
// brain is the persisted source of truth for WS chat / agent transcripts
// (router.persistUserAndAssemble + TranscriptRecorder), so these counts
// span every device the user has chatted from.
//
// NOTE on tokens: the WS transcript path persists assistant turns WITHOUT
// token counts (TranscriptRecorder.finish sets content/model/status only).
// So 累计 Token does NOT come from here — it is sourced from
// model_relay.usage_log (the per-request billing ledger). This file owns the
// chat-structure metrics only: threads / messages / models / activity / ranks.

package chat

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StatsCount is a current total plus the cumulative total as of a cutoff
// (start of the current month), so the client can render month-over-month %.
type StatsCount struct {
	Count int64 `json:"count"`
	Prev  int64 `json:"prev"`
}

// HeatmapDay is one UTC-day bucket of message activity.
type HeatmapDay struct {
	Date  string `json:"date"` // YYYY-MM-DD (UTC)
	Count int64  `json:"count"`
}

// ModelRankItem — message count grouped by model, descending.
type ModelRankItem struct {
	Model string `json:"model"`
	Count int64  `json:"count"`
}

// TopicRankItem — message count grouped by thread (话题内容量), descending.
type TopicRankItem struct {
	ThreadID string `json:"thread_id"`
	Title    string `json:"title"`
	Count    int64  `json:"count"`
}

// StatsResult is the full payload for GET /v1/chat/stats.
type StatsResult struct {
	Threads   StatsCount      `json:"threads"`
	Messages  StatsCount      `json:"messages"`
	Models    StatsCount      `json:"models"`
	Heatmap   []HeatmapDay    `json:"heatmap"`
	ModelRank []ModelRankItem `json:"model_rank"`
	TopicRank []TopicRankItem `json:"topic_rank"`
}

// statsRankLimit caps each ranking list. The client shows the top 5 inline
// and offers a "view all" modal over the rest.
const statsRankLimit = 20

// Stats runs the account-level aggregations for one user.
//
//   - monthStart: start of the current month (UTC); cumulative counts strictly
//     before it back the month-over-month "prev" figures.
//   - heatmapSince: lower bound for the activity heatmap (typically now-365d).
func (s *Store) Stats(
	ctx context.Context, userID uuid.UUID, monthStart, heatmapSince time.Time,
) (*StatsResult, error) {
	out := &StatsResult{
		Heatmap:   []HeatmapDay{},
		ModelRank: []ModelRankItem{},
		TopicRank: []TopicRankItem{},
	}

	// ── Overview counts (current + as-of-month-start) ──
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE created_at < $2)
		FROM chat.threads WHERE user_id = $1
	`, userID, monthStart).Scan(&out.Threads.Count, &out.Threads.Prev); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE created_at < $2)
		FROM chat.messages WHERE user_id = $1
	`, userID, monthStart).Scan(&out.Messages.Count, &out.Messages.Prev); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT model),
		       COUNT(DISTINCT model) FILTER (WHERE created_at < $2)
		FROM chat.messages
		WHERE user_id = $1 AND model IS NOT NULL AND model <> ''
	`, userID, monthStart).Scan(&out.Models.Count, &out.Models.Prev); err != nil {
		return nil, err
	}

	// ── Activity heatmap (UTC-day buckets) ──
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS d, COUNT(*)
		FROM chat.messages
		WHERE user_id = $1 AND created_at >= $2
		GROUP BY d ORDER BY d
	`, userID, heatmapSince)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h HeatmapDay
		if err := rows.Scan(&h.Date, &h.Count); err != nil {
			rows.Close()
			return nil, err
		}
		out.Heatmap = append(out.Heatmap, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// ── Model rank ──
	mr, err := s.pool.Query(ctx, `
		SELECT model, COUNT(*) c
		FROM chat.messages
		WHERE user_id = $1 AND model IS NOT NULL AND model <> ''
		GROUP BY model ORDER BY c DESC, model ASC LIMIT $2
	`, userID, statsRankLimit)
	if err != nil {
		return nil, err
	}
	for mr.Next() {
		var it ModelRankItem
		if err := mr.Scan(&it.Model, &it.Count); err != nil {
			mr.Close()
			return nil, err
		}
		out.ModelRank = append(out.ModelRank, it)
	}
	mr.Close()
	if err := mr.Err(); err != nil {
		return nil, err
	}

	// ── Topic rank (messages per thread) ──
	tr, err := s.pool.Query(ctx, `
		SELECT t.id, t.title, COUNT(m.id) c
		FROM chat.threads t
		JOIN chat.messages m ON m.thread_id = t.id
		WHERE t.user_id = $1
		GROUP BY t.id, t.title ORDER BY c DESC, t.id LIMIT $2
	`, userID, statsRankLimit)
	if err != nil {
		return nil, err
	}
	for tr.Next() {
		var (
			id    uuid.UUID
			title string
			c     int64
		)
		if err := tr.Scan(&id, &title, &c); err != nil {
			tr.Close()
			return nil, err
		}
		out.TopicRank = append(out.TopicRank, TopicRankItem{
			ThreadID: id.String(), Title: title, Count: c,
		})
	}
	tr.Close()
	if err := tr.Err(); err != nil {
		return nil, err
	}

	return out, nil
}
