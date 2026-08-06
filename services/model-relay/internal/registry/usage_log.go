// UsageLogRepo is the write+query surface for model_relay.usage_log.
//
// The runtime hot path is Append, fired once per LLM request after the
// provider responds. It MUST be fire-and-forget — usage_writer wraps
// this in a goroutine + recover; an outage in the log path cannot block
// user requests (invariant I5 in Dev Plan §10).
//
// Read paths (RecentByUser, RecentByChannel) are admin-only, called
// from the future "drill into a channel's failures" UI.

package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UsageLogRepo struct {
	pool *pgxpool.Pool
}

type UsageLogInput struct {
	UserID        uuid.UUID
	ModelID       uuid.UUID
	ChannelID     uuid.UUID
	ModelCode     string
	UpstreamModel string
	UserPlan      Plan

	InputTokens      int64
	OutputTokens     int64
	CacheWriteTokens int64
	CacheReadTokens  int64

	CostOriginCurrency Currency
	CostOriginAmount   float64
	CostSettleCurrency Currency
	CostSettleAmount   float64
	FxRate             float64

	LatencyMs      int
	CreditsCharged int64
	Status         UsageStatus
	ErrorCode      string
	RequestID      string
}

func (in UsageLogInput) validate() error {
	if in.UserID == uuid.Nil || in.ModelID == uuid.Nil || in.ChannelID == uuid.Nil {
		return fmt.Errorf("usage_log: user_id/model_id/channel_id required")
	}
	if in.ModelCode == "" {
		return fmt.Errorf("usage_log: model_code required")
	}
	if in.Status == "" {
		in.Status = UsageOK
	}
	if in.CostOriginCurrency == "" || in.CostSettleCurrency == "" {
		return fmt.Errorf("usage_log: currencies required")
	}
	if in.FxRate <= 0 {
		return fmt.Errorf("usage_log: fx_rate must be > 0")
	}
	if in.UserPlan == "" {
		in.UserPlan = PlanFree
	}
	return nil
}

// Append writes one row. Always returns quickly; caller should run on
// a worker goroutine to keep the request path snappy.
func (r *UsageLogRepo) Append(ctx context.Context, in UsageLogInput) error {
	if err := in.validate(); err != nil {
		return err
	}
	if in.UserPlan == "" {
		in.UserPlan = PlanFree
	}
	if in.Status == "" {
		in.Status = UsageOK
	}
	const q = `
		INSERT INTO model_relay.usage_log
			(user_id, model_id, channel_id, model_code, upstream_model, user_plan,
			 input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
			 cost_origin_currency, cost_origin_amount,
			 cost_settle_currency, cost_settle_amount, fx_rate,
			 latency_ms, credits_charged, status, error_code, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,
		        $7,$8,$9,$10,
		        $11,$12,$13,$14,$15,
		        $16,$17,$18,$19,$20)
	`
	_, err := r.pool.Exec(ctx, q,
		in.UserID, in.ModelID, in.ChannelID,
		in.ModelCode, in.UpstreamModel, in.UserPlan,
		in.InputTokens, in.OutputTokens, in.CacheWriteTokens, in.CacheReadTokens,
		in.CostOriginCurrency, in.CostOriginAmount,
		in.CostSettleCurrency, in.CostSettleAmount, in.FxRate,
		in.LatencyMs, in.CreditsCharged, in.Status, in.ErrorCode, in.RequestID,
	)
	if err != nil {
		return translateErr("usage_log.append", err)
	}
	return nil
}

// RecentByUser returns the most recent N entries for a user. Default
// admin "show me what this user spent" view.
func (r *UsageLogRepo) RecentByUser(ctx context.Context, userID uuid.UUID, limit int) ([]UsageLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	const q = `
		SELECT id, user_id, model_id, channel_id, model_code, upstream_model, user_plan,
		       input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
		       cost_origin_currency, cost_origin_amount,
		       cost_settle_currency, cost_settle_amount, fx_rate,
		       latency_ms, credits_charged, status, error_code, request_id, created_at
		FROM model_relay.usage_log
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, translateErr("usage_log.recent_by_user", err)
	}
	defer rows.Close()
	return scanUsageLogs(rows)
}

// RecentByChannel returns the most recent N entries for a channel,
// optionally filtered to non-OK status. Used by the "this channel's
// recent failures" admin drilldown.
func (r *UsageLogRepo) RecentByChannel(ctx context.Context, channelID uuid.UUID, errorsOnly bool, limit int) ([]UsageLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `
		SELECT id, user_id, model_id, channel_id, model_code, upstream_model, user_plan,
		       input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
		       cost_origin_currency, cost_origin_amount,
		       cost_settle_currency, cost_settle_amount, fx_rate,
		       latency_ms, credits_charged, status, error_code, request_id, created_at
		FROM model_relay.usage_log
		WHERE channel_id = $1
		  AND ($2::bool = false OR status != 'ok')
		ORDER BY created_at DESC
		LIMIT $3
	`
	rows, err := r.pool.Query(ctx, q, channelID, errorsOnly, limit)
	if err != nil {
		return nil, translateErr("usage_log.recent_by_channel", err)
	}
	defer rows.Close()
	return scanUsageLogs(rows)
}

// PurgeOlderThan deletes rows older than `cutoff`. Phase-2 cleanup
// task; not wired up in MVP. Returned count helps the caller log the
// retention sweep.
func (r *UsageLogRepo) PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM model_relay.usage_log WHERE created_at < $1`
	tag, err := r.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, translateErr("usage_log.purge", err)
	}
	return tag.RowsAffected(), nil
}

// ─── helpers ──────────────────────────────────────────────────────

func scanUsageLogs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]UsageLog, error) {
	out := make([]UsageLog, 0, 16)
	for rows.Next() {
		var u UsageLog
		if err := rows.Scan(
			&u.ID, &u.UserID, &u.ModelID, &u.ChannelID,
			&u.ModelCode, &u.UpstreamModel, &u.UserPlan,
			&u.InputTokens, &u.OutputTokens, &u.CacheWriteTokens, &u.CacheReadTokens,
			&u.CostOriginCurrency, &u.CostOriginAmount,
			&u.CostSettleCurrency, &u.CostSettleAmount, &u.FxRate,
			&u.LatencyMs, &u.CreditsCharged, &u.Status, &u.ErrorCode, &u.RequestID, &u.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("usage_log.scan: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
