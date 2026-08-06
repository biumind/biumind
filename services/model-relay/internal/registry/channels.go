// ChannelRepo is the CRUD surface for model_relay.channels.
//
// The runtime hot path is ListActiveByModel — Strategy.Pick consumes
// its output. Indexed by (model_id, status, priority DESC) at the SQL
// layer; the Go side just hands the slice over without re-sorting.
//
// Channel mutation paths used by the supervisor (auto-disable on N
// failures, recovery probe) are surfaced as small focused helpers
// (RecordFailure, RecordSuccess, ListAutoDisabled) rather than going
// through the generic Update — they need atomic increment / status
// transitions that are awkward to express as a Patch struct.

package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChannelRepo struct {
	pool *pgxpool.Pool
}

type ChannelFilter struct {
	ModelID      uuid.UUID    // uuid.Nil = any
	CredentialID uuid.UUID    // uuid.Nil = any
	Status       EntityStatus // "" = any
}

func (r *ChannelRepo) List(ctx context.Context, f ChannelFilter) ([]Channel, error) {
	q := `
		SELECT id, model_id, credential_id, upstream_model,
		       priority, weight, rpm_limit, tpm_limit,
		       status, failure_count, last_error_at, last_error,
		       last_test_at, latency_p50_ms, extra,
		       endpoint_capability,
		       created_at, updated_at
		FROM model_relay.channels
		WHERE ($1::uuid IS NULL OR model_id      = $1)
		  AND ($2::uuid IS NULL OR credential_id = $2)
		  AND ($3 = '' OR status = $3)
		ORDER BY priority DESC, weight DESC, created_at ASC
	`
	var mid, cid any
	if f.ModelID != uuid.Nil {
		mid = f.ModelID
	}
	if f.CredentialID != uuid.Nil {
		cid = f.CredentialID
	}
	rows, err := r.pool.Query(ctx, q, mid, cid, string(f.Status))
	if err != nil {
		return nil, translateErr("channels.list", err)
	}
	defer rows.Close()
	return scanChannels(rows)
}

// ListActiveByModel is the resolver hot path — only active channels for
// a given model, already sorted by priority (DESC) then weight (DESC).
// Strategy.Pick gets to work on the returned slice without re-sorting.
func (r *ChannelRepo) ListActiveByModel(ctx context.Context, modelID uuid.UUID) ([]Channel, error) {
	const q = `
		SELECT id, model_id, credential_id, upstream_model,
		       priority, weight, rpm_limit, tpm_limit,
		       status, failure_count, last_error_at, last_error,
		       last_test_at, latency_p50_ms, extra,
		       endpoint_capability,
		       created_at, updated_at
		FROM model_relay.channels
		WHERE model_id = $1 AND status = 'active'
		ORDER BY priority DESC, weight DESC, id ASC
	`
	rows, err := r.pool.Query(ctx, q, modelID)
	if err != nil {
		return nil, translateErr("channels.list_active_by_model", err)
	}
	defer rows.Close()
	return scanChannels(rows)
}

// ChannelStats 是 admin 列表页"渠道数"列的聚合形态.
// admin Vue 用 active=0&&total>0 标红, auto_disabled>0 标黄.
type ChannelStats struct {
	Active       int `json:"active"`
	Total        int `json:"total"`
	AutoDisabled int `json:"auto_disabled"`
}

// StatsByModelIDs 一次 SQL 拿一批 model 的 channel 计数, 给 admin 分页列表用.
// 没有 channel 的 model 不会出现在返回 map 里, 调用方按 zero 处理.
func (r *ChannelRepo) StatsByModelIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]ChannelStats, error) {
	out := make(map[uuid.UUID]ChannelStats, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const q = `
		SELECT model_id, status, COUNT(*)
		FROM model_relay.channels
		WHERE model_id = ANY($1::uuid[])
		GROUP BY model_id, status
	`
	rows, err := r.pool.Query(ctx, q, ids)
	if err != nil {
		return nil, translateErr("channels.stats_by_model_ids", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var status string
		var n int
		if err := rows.Scan(&mid, &status, &n); err != nil {
			return nil, translateErr("channels.stats_by_model_ids.scan", err)
		}
		s := out[mid]
		s.Total += n
		switch EntityStatus(status) {
		case StatusActive:
			s.Active += n
		case StatusAutoDisabled:
			s.AutoDisabled += n
		}
		out[mid] = s
	}
	if err := rows.Err(); err != nil {
		return nil, translateErr("channels.stats_by_model_ids.iter", err)
	}
	return out, nil
}

func (r *ChannelRepo) Get(ctx context.Context, id uuid.UUID) (*Channel, error) {
	const q = `
		SELECT id, model_id, credential_id, upstream_model,
		       priority, weight, rpm_limit, tpm_limit,
		       status, failure_count, last_error_at, last_error,
		       last_test_at, latency_p50_ms, extra,
		       endpoint_capability,
		       created_at, updated_at
		FROM model_relay.channels WHERE id = $1
	`
	rows, err := r.pool.Query(ctx, q, id)
	if err != nil {
		return nil, translateErr("channels.get", err)
	}
	defer rows.Close()
	out, err := scanChannels(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("channels.get: %w", ErrNotFound)
	}
	return &out[0], nil
}

type ChannelInput struct {
	ModelID       uuid.UUID
	CredentialID  uuid.UUID
	UpstreamModel string
	Priority      int
	Weight        int
	RPMLimit      int
	TPMLimit      int
	Status        EntityStatus
	Extra         map[string]any
	// EndpointCapability v0.3 — "standard" / "realtime" / "passthrough".
	// 空值 = 仓库层默认 EndpointStandard (向后兼容老调用方).
	EndpointCapability string
}

func (in ChannelInput) validate() error {
	if in.ModelID == uuid.Nil {
		return fmt.Errorf("channels: model_id required")
	}
	if in.CredentialID == uuid.Nil {
		return fmt.Errorf("channels: credential_id required")
	}
	if in.UpstreamModel == "" {
		return fmt.Errorf("channels: upstream_model required")
	}
	if in.Weight < 0 {
		return fmt.Errorf("channels: weight must be >= 0")
	}
	switch in.EndpointCapability {
	case "", EndpointStandard, EndpointRealtime, EndpointPassthrough:
	default:
		return fmt.Errorf("channels: invalid endpoint_capability %q", in.EndpointCapability)
	}
	return nil
}

func (r *ChannelRepo) Insert(ctx context.Context, in ChannelInput) (*Channel, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusDisabled
	}
	if in.Weight == 0 {
		in.Weight = 1
	}
	if in.EndpointCapability == "" {
		in.EndpointCapability = EndpointStandard
	}
	extra, err := marshalExtra(in.Extra)
	if err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO model_relay.channels
			(model_id, credential_id, upstream_model,
			 priority, weight, rpm_limit, tpm_limit, status, extra,
			 endpoint_capability)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, model_id, credential_id, upstream_model,
		          priority, weight, rpm_limit, tpm_limit,
		          status, failure_count, last_error_at, last_error,
		          last_test_at, latency_p50_ms, extra,
		          endpoint_capability,
		          created_at, updated_at
	`
	rows, err := r.pool.Query(ctx, q,
		in.ModelID, in.CredentialID, in.UpstreamModel,
		in.Priority, in.Weight, in.RPMLimit, in.TPMLimit, in.Status, extra,
		in.EndpointCapability,
	)
	if err != nil {
		return nil, translateErr("channels.insert", err)
	}
	defer rows.Close()
	out, err := scanChannels(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("channels.insert: no row returned")
	}
	return &out[0], nil
}

func (r *ChannelRepo) Update(ctx context.Context, id uuid.UUID, in ChannelInput) (*Channel, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	extra, err := marshalExtra(in.Extra)
	if err != nil {
		return nil, err
	}
	if in.EndpointCapability == "" {
		in.EndpointCapability = EndpointStandard
	}
	const q = `
		UPDATE model_relay.channels
		   SET model_id = $1, credential_id = $2, upstream_model = $3,
		       priority = $4, weight = $5, rpm_limit = $6, tpm_limit = $7,
		       status = $8, extra = $9, endpoint_capability = $10,
		       updated_at = now()
		 WHERE id = $11
		RETURNING id, model_id, credential_id, upstream_model,
		          priority, weight, rpm_limit, tpm_limit,
		          status, failure_count, last_error_at, last_error,
		          last_test_at, latency_p50_ms, extra,
		          endpoint_capability,
		          created_at, updated_at
	`
	rows, err := r.pool.Query(ctx, q,
		in.ModelID, in.CredentialID, in.UpstreamModel,
		in.Priority, in.Weight, in.RPMLimit, in.TPMLimit,
		in.Status, extra, in.EndpointCapability, id,
	)
	if err != nil {
		return nil, translateErr("channels.update", err)
	}
	defer rows.Close()
	out, err := scanChannels(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("channels.update: %w", ErrNotFound)
	}
	return &out[0], nil
}

func (r *ChannelRepo) SetStatus(ctx context.Context, id uuid.UUID, status EntityStatus) error {
	const q = `UPDATE model_relay.channels SET status = $1, updated_at = now() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, q, status, id)
	if err != nil {
		return translateErr("channels.set_status", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("channels.set_status: %w", ErrNotFound)
	}
	return nil
}

func (r *ChannelRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM model_relay.channels WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return translateErr("channels.delete", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("channels.delete: %w", ErrNotFound)
	}
	return nil
}

// ─── Health supervisor helpers ────────────────────────────────────

// RecordFailure increments failure_count atomically; if the new count
// crosses autoDisableThreshold the row's status flips to 'auto_disabled'.
// Returns the new failure_count and resulting status. last_error_at /
// last_error / last_test_at are stamped in the same UPDATE.
func (r *ChannelRepo) RecordFailure(
	ctx context.Context, id uuid.UUID, errMsg string, autoDisableThreshold int,
) (int, EntityStatus, error) {
	const q = `
		UPDATE model_relay.channels
		   SET failure_count = failure_count + 1,
		       last_error_at = now(),
		       last_error    = $1,
		       last_test_at  = now(),
		       status = CASE
		           WHEN status = 'active' AND failure_count + 1 >= $2
		           THEN 'auto_disabled'
		           ELSE status
		       END,
		       updated_at = now()
		 WHERE id = $3
		RETURNING failure_count, status
	`
	var fc int
	var status EntityStatus
	if err := r.pool.QueryRow(ctx, q, errMsg, autoDisableThreshold, id).
		Scan(&fc, &status); err != nil {
		return 0, "", translateErr("channels.record_failure", err)
	}
	return fc, status, nil
}

// RecordSuccess clears the failure tally; if status is auto_disabled,
// it transitions back to active (recovery path). latencyMs is the
// observed P50 used by lowest_latency strategy in P2.
func (r *ChannelRepo) RecordSuccess(ctx context.Context, id uuid.UUID, latencyMs int) error {
	const q = `
		UPDATE model_relay.channels
		   SET failure_count  = 0,
		       last_error     = '',
		       last_test_at   = now(),
		       cooldown_until = NULL,                    -- R4-B: 恢复时清退避截止
		       latency_p50_ms = CASE
		           WHEN latency_p50_ms = 0 THEN $1
		           ELSE (latency_p50_ms * 7 + $1) / 8   -- EWMA, 1/8 weight on new sample
		       END,
		       status = CASE WHEN status = 'auto_disabled' THEN 'active' ELSE status END,
		       updated_at = now()
		 WHERE id = $2
	`
	tag, err := r.pool.Exec(ctx, q, latencyMs, id)
	if err != nil {
		return translateErr("channels.record_success", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("channels.record_success: %w", ErrNotFound)
	}
	return nil
}

// ListAutoDisabled returns rows the supervisor cron should retry.
//
// R4-B：恢复时机以 `cooldown_until` 为准（绝对时间，由 RecordFailure 按错误类型
// 设——429=Retry-After、瞬态=指数退避、鉴权/计费=长冷却）。cooldown_until 为
// NULL（pre-migration 行 / 旧路径）时回退到老的 age-based 兜底（last_error_at +
// olderThan）。捞 cooldown 已到期的行,先老的先探。
func (r *ChannelRepo) ListAutoDisabled(ctx context.Context, olderThan time.Duration) ([]Channel, error) {
	const q = `
		SELECT id, model_id, credential_id, upstream_model,
		       priority, weight, rpm_limit, tpm_limit,
		       status, failure_count, last_error_at, last_error,
		       last_test_at, latency_p50_ms, extra,
		       endpoint_capability,
		       created_at, updated_at
		FROM model_relay.channels
		WHERE status = 'auto_disabled'
		  AND (
		        (cooldown_until IS NOT NULL AND cooldown_until <= now())
		     OR (cooldown_until IS NULL AND (last_error_at IS NULL OR last_error_at < now() - make_interval(secs => $1::float8)))
		      )
		ORDER BY cooldown_until ASC NULLS FIRST, last_error_at ASC NULLS FIRST
	`
	rows, err := r.pool.Query(ctx, q, olderThan.Seconds())
	if err != nil {
		return nil, translateErr("channels.list_auto_disabled", err)
	}
	defer rows.Close()
	return scanChannels(rows)
}

// SetCooldownUntil stamps the recovery deadline on an auto_disabled channel
// (R4-B). The supervisor computes `until` from the failure class
// (Retry-After / exponential backoff / long manual cooldown) after
// RecordFailure flips the channel to auto_disabled. No-op-safe on rows that
// aren't auto_disabled (guarded by the WHERE).
func (r *ChannelRepo) SetCooldownUntil(ctx context.Context, id uuid.UUID, until time.Time) error {
	const q = `
		UPDATE model_relay.channels
		   SET cooldown_until = $1, updated_at = now()
		 WHERE id = $2 AND status = 'auto_disabled'
	`
	if _, err := r.pool.Exec(ctx, q, until, id); err != nil {
		return translateErr("channels.set_cooldown_until", err)
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────

func scanChannels(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Channel, error) {
	out := make([]Channel, 0, 8)
	for rows.Next() {
		var c Channel
		var extra []byte
		if err := rows.Scan(
			&c.ID, &c.ModelID, &c.CredentialID, &c.UpstreamModel,
			&c.Priority, &c.Weight, &c.RPMLimit, &c.TPMLimit,
			&c.Status, &c.FailureCount, &c.LastErrorAt, &c.LastError,
			&c.LastTestAt, &c.LatencyP50Ms, &extra,
			&c.EndpointCapability,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("channels.scan: %w", err)
		}
		if len(extra) > 0 {
			if err := json.Unmarshal(extra, &c.Extra); err != nil {
				return nil, fmt.Errorf("channels.scan.extra: %w", err)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func marshalExtra(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("channels: marshal extra: %w", err)
	}
	return b, nil
}
