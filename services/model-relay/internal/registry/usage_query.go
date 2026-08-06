// User-facing usage aggregations over model_relay.usage_log — backs the
// client 数据统计 · 用量 (usage) page (GET /v1/me/usage). All queries are
// owner-scoped (user_id = $1) and account-wide (cross-device).
//
// Spend is reported in 积分 (credits_charged); tokens / latency come from the
// same per-request rows. Provider-level grouping is NOT supported (usage_log
// has no provider column) — model grouping only for v1.

package registry

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// UsageSummary — the 用量 page header cards + the 概览 累计 Token card.
type UsageSummary struct {
	TodayCredits  int64 `json:"today_credits"`
	MonthCredits  int64 `json:"month_credits"`
	MonthRequests int64 `json:"month_requests"`
	ActiveModels  int64 `json:"active_models"`
	// All-time token totals (for the 概览 累计 Token card + its MoM %).
	// TotalTokensPrev is the cumulative total strictly before monthStart.
	TotalTokens     int64 `json:"total_tokens"`
	TotalTokensPrev int64 `json:"total_tokens_prev"`
}

// UsageDailyBucket — one UTC-day of the selected month's usage trend.
type UsageDailyBucket struct {
	Date     string `json:"date"` // YYYY-MM-DD (UTC)
	Credits  int64  `json:"credits"`
	Tokens   int64  `json:"tokens"`
	Requests int64  `json:"requests"`
}

// UsageModelBucket — per-model breakdown for the selected month.
type UsageModelBucket struct {
	Model        string `json:"model"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Credits      int64  `json:"credits"`
}

// UsageSummaryFor computes the header-card figures. todayStart is the absolute
// start of the current day (UTC) — today spend is unbounded by the selected
// month so an old-month view still shows real "today" = 0 only when idle.
func (r *UsageLogRepo) UsageSummaryFor(
	ctx context.Context, userID uuid.UUID, monthStart, monthEnd, todayStart time.Time,
) (UsageSummary, error) {
	var s UsageSummary
	if err := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(credits_charged), 0),
			COUNT(*),
			COUNT(DISTINCT model_code)
		FROM model_relay.usage_log
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
	`, userID, monthStart, monthEnd).Scan(&s.MonthCredits, &s.MonthRequests, &s.ActiveModels); err != nil {
		return s, translateErr("usage_log.summary_month", err)
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(credits_charged), 0)
		FROM model_relay.usage_log
		WHERE user_id = $1 AND created_at >= $2
	`, userID, todayStart).Scan(&s.TodayCredits); err != nil {
		return s, translateErr("usage_log.summary_today", err)
	}
	// All-time token totals (+ cumulative before this month for MoM).
	if err := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0),
			COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens)
			         FILTER (WHERE created_at < $2), 0)
		FROM model_relay.usage_log
		WHERE user_id = $1
	`, userID, monthStart).Scan(&s.TotalTokens, &s.TotalTokensPrev); err != nil {
		return s, translateErr("usage_log.summary_tokens", err)
	}
	return s, nil
}

// UsageDailyFor returns the per-day buckets within [monthStart, monthEnd).
// Only days with activity are returned; the client pads zero days.
func (r *UsageLogRepo) UsageDailyFor(
	ctx context.Context, userID uuid.UUID, monthStart, monthEnd time.Time,
) ([]UsageDailyBucket, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS d,
		       COALESCE(SUM(credits_charged), 0),
		       COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0),
		       COUNT(*)
		FROM model_relay.usage_log
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY d ORDER BY d
	`, userID, monthStart, monthEnd)
	if err != nil {
		return nil, translateErr("usage_log.daily", err)
	}
	defer rows.Close()
	out := make([]UsageDailyBucket, 0, 31)
	for rows.Next() {
		var b UsageDailyBucket
		if err := rows.Scan(&b.Date, &b.Credits, &b.Tokens, &b.Requests); err != nil {
			return nil, translateErr("usage_log.daily_scan", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UsageByModelFor returns the per-model breakdown for the selected month,
// ranked by credits spent.
func (r *UsageLogRepo) UsageByModelFor(
	ctx context.Context, userID uuid.UUID, monthStart, monthEnd time.Time, limit int,
) ([]UsageModelBucket, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT model_code, COUNT(*),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(credits_charged), 0)
		FROM model_relay.usage_log
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY model_code
		ORDER BY SUM(credits_charged) DESC, COUNT(*) DESC
		LIMIT $4
	`, userID, monthStart, monthEnd, limit)
	if err != nil {
		return nil, translateErr("usage_log.by_model", err)
	}
	defer rows.Close()
	out := make([]UsageModelBucket, 0, limit)
	for rows.Next() {
		var b UsageModelBucket
		if err := rows.Scan(&b.Model, &b.Requests, &b.InputTokens, &b.OutputTokens, &b.Credits); err != nil {
			return nil, translateErr("usage_log.by_model_scan", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListByUserMonth returns one page of per-call rows for the selected month,
// newest first, plus the total row count for pagination.
func (r *UsageLogRepo) ListByUserMonth(
	ctx context.Context, userID uuid.UUID, monthStart, monthEnd time.Time, limit, offset int,
) ([]UsageLog, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM model_relay.usage_log
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
	`, userID, monthStart, monthEnd).Scan(&total); err != nil {
		return nil, 0, translateErr("usage_log.list_month_count", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, model_id, channel_id, model_code, upstream_model, user_plan,
		       input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
		       cost_origin_currency, cost_origin_amount,
		       cost_settle_currency, cost_settle_amount, fx_rate,
		       latency_ms, credits_charged, status, error_code, request_id, created_at
		FROM model_relay.usage_log
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5
	`, userID, monthStart, monthEnd, limit, offset)
	if err != nil {
		return nil, 0, translateErr("usage_log.list_month", err)
	}
	defer rows.Close()
	logs, err := scanUsageLogs(rows)
	if err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}
