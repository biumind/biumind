// PricingRepo is the read+append surface for model_relay.pricing.
//
// Pricing is append-only: GetCurrent picks the row with the highest
// effective_at <= now(). New prices are inserted via Set (which is
// "insert a new row, never UPDATE the old one") so historical billing
// can be reconstructed by walking backward through effective_at.
//
// Why no Delete: deleting a pricing row would orphan usage_log entries
// that referenced it — the audit trail breaks. If a price was wrong,
// insert a new effective_at>=now row with the correct values.

package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PricingRepo struct {
	pool *pgxpool.Pool
}

// GetCurrent returns the pricing row in effect "now" for a model,
// or ErrNotFound if no pricing has ever been set. Used by the usage
// writer at request settlement time.
func (r *PricingRepo) GetCurrent(ctx context.Context, modelID uuid.UUID) (*Pricing, error) {
	return r.GetAt(ctx, modelID, time.Now().UTC())
}

// GetAt returns the pricing row that was effective at a specific
// instant. Used by retroactive audit / billing reruns.
func (r *PricingRepo) GetAt(ctx context.Context, modelID uuid.UUID, t time.Time) (*Pricing, error) {
	const q = `
		SELECT id, model_id, currency,
		       input_per_mtok, output_per_mtok,
		       cache_write_per_mtok, cache_read_per_mtok,
		       cost_per_image, cost_per_video_second,
		       cost_per_audio_second, cost_per_character,
		       cost_per_search_unit,
		       markup_ratio, min_charge, max_charge_per_request,
		       effective_at, created_by, created_at
		FROM model_relay.pricing
		WHERE model_id = $1 AND effective_at <= $2
		ORDER BY effective_at DESC
		LIMIT 1
	`
	var p Pricing
	err := r.pool.QueryRow(ctx, q, modelID, t).Scan(
		&p.ID, &p.ModelID, &p.Currency,
		&p.InputPerMTok, &p.OutputPerMTok,
		&p.CacheWritePerMTok, &p.CacheReadPerMTok,
		&p.CostPerImage, &p.CostPerVideoSecond,
		&p.CostPerAudioSecond, &p.CostPerCharacter,
		&p.CostPerSearchUnit,
		&p.MarkupRatio, &p.MinCharge, &p.MaxChargePerRequest,
		&p.EffectiveAt, &p.CreatedBy, &p.CreatedAt,
	)
	if err != nil {
		return nil, translateErr("pricing.get_at", err)
	}
	return &p, nil
}

// BatchLatest — F2.1. 给定 model_id 列表, 批量返每个 model 最近的 pricing
// (用 DISTINCT ON 一次 SQL). 给 admin Vue 列表 ?include_pricing=true 用,
// 避免 N+1 query. 找不到的 model 在返回 map 中缺失 (不报错).
func (r *PricingRepo) BatchLatest(ctx context.Context, modelIDs []uuid.UUID) (map[uuid.UUID]Pricing, error) {
	if len(modelIDs) == 0 {
		return map[uuid.UUID]Pricing{}, nil
	}
	const q = `
		SELECT DISTINCT ON (model_id)
		       id, model_id, currency,
		       input_per_mtok, output_per_mtok,
		       cache_write_per_mtok, cache_read_per_mtok,
		       cost_per_image, cost_per_video_second,
		       cost_per_audio_second, cost_per_character,
		       effective_at, created_by, created_at
		FROM model_relay.pricing
		WHERE model_id = ANY($1)
		ORDER BY model_id, effective_at DESC
	`
	rows, err := r.pool.Query(ctx, q, modelIDs)
	if err != nil {
		return nil, translateErr("pricing.batch_latest", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]Pricing, len(modelIDs))
	for rows.Next() {
		var p Pricing
		if err := rows.Scan(
			&p.ID, &p.ModelID, &p.Currency,
			&p.InputPerMTok, &p.OutputPerMTok,
			&p.CacheWritePerMTok, &p.CacheReadPerMTok,
			&p.CostPerImage, &p.CostPerVideoSecond,
			&p.CostPerAudioSecond, &p.CostPerCharacter,
			&p.CostPerSearchUnit,
			&p.MarkupRatio, &p.MinCharge, &p.MaxChargePerRequest,
			&p.EffectiveAt, &p.CreatedBy, &p.CreatedAt,
		); err != nil {
			return nil, translateErr("pricing.batch_latest.scan", err)
		}
		out[p.ModelID] = p
	}
	return out, rows.Err()
}

// History returns all pricing rows for a model, newest first. Powers
// the admin "调价历史" tab (Phase 2).
func (r *PricingRepo) History(ctx context.Context, modelID uuid.UUID) ([]Pricing, error) {
	const q = `
		SELECT id, model_id, currency,
		       input_per_mtok, output_per_mtok,
		       cache_write_per_mtok, cache_read_per_mtok,
		       cost_per_image, cost_per_video_second,
		       cost_per_audio_second, cost_per_character,
		       cost_per_search_unit,
		       markup_ratio, min_charge, max_charge_per_request,
		       effective_at, created_by, created_at
		FROM model_relay.pricing
		WHERE model_id = $1
		ORDER BY effective_at DESC
	`
	rows, err := r.pool.Query(ctx, q, modelID)
	if err != nil {
		return nil, translateErr("pricing.history", err)
	}
	defer rows.Close()

	out := make([]Pricing, 0, 4)
	for rows.Next() {
		var p Pricing
		if err := rows.Scan(
			&p.ID, &p.ModelID, &p.Currency,
			&p.InputPerMTok, &p.OutputPerMTok,
			&p.CacheWritePerMTok, &p.CacheReadPerMTok,
			&p.CostPerImage, &p.CostPerVideoSecond,
			&p.CostPerAudioSecond, &p.CostPerCharacter,
			&p.CostPerSearchUnit,
			&p.MarkupRatio, &p.MinCharge, &p.MaxChargePerRequest,
			&p.EffectiveAt, &p.CreatedBy, &p.CreatedAt,
		); err != nil {
			return nil, translateErr("pricing.history.scan", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type PricingInput struct {
	ModelID           uuid.UUID
	Currency          Currency
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheWritePerMTok float64
	CacheReadPerMTok  float64

	// P4 段 2 多模态字段超集. 全部 nullable; 调用方按 model.pricing_strategy
	// 决定填哪些. fixed 策略下至少填 cost_per_image / video_second 之一.
	CostPerImage       *float64
	CostPerVideoSecond *float64
	CostPerAudioSecond *float64
	CostPerCharacter   *float64
	CostPerSearchUnit  float64 // rerank per_search_unit, 0 = 不适用

	// 平台加成 + 单次扣费上下限. 0 / nil 走默认 (3.0 / 0 / 不限).
	// W4 SoT 整合时引入,从 billing.pricing_book 迁过来.
	MarkupRatio         float64 // 0 = 用 default 3.0
	MinCharge           int64   // millicents, 0 = 不设下限
	MaxChargePerRequest *int64  // millicents, nil = 不设上限

	EffectiveAt time.Time  // zero = now()
	CreatedBy   *uuid.UUID // who set this
}

// markupOrDefault: 0 / 负数 → 默认 3.0;否则透传调用方值. PricingInput
// 跟 admin UI 共享:UI 不填时 0,我们 fallback 到合理默认.
func markupOrDefault(m float64) float64 {
	if m <= 0 {
		return 3.0
	}
	return m
}

// EffectiveInputPerMTok / EffectiveOutputPerMTok 返经 markup 后的用户计费
// 单价 (标价 = 成本 × MarkupRatio, 默认 3.0). 供 client picker 显价用 —
// 返实际扣费单价而非成本, 但不暴露 MarkupRatio 本身 (避免泄露加价倍数).
// 注意: 这是 per_mtok 显示价, 不套 MinCharge/MaxChargePerRequest 钳制
// (那俩是单次请求粒度, per_mtok 显示无关).
func (p Pricing) EffectiveInputPerMTok() float64 {
	return p.InputPerMTok * markupOrDefault(p.MarkupRatio)
}
func (p Pricing) EffectiveOutputPerMTok() float64 {
	return p.OutputPerMTok * markupOrDefault(p.MarkupRatio)
}

func (in PricingInput) validate() error {
	if in.ModelID == uuid.Nil {
		return fmt.Errorf("pricing: model_id required")
	}
	if in.Currency != CurrencyCNY && in.Currency != CurrencyUSD {
		return fmt.Errorf("pricing: invalid currency %q", in.Currency)
	}
	if in.InputPerMTok < 0 || in.OutputPerMTok < 0 ||
		in.CacheWritePerMTok < 0 || in.CacheReadPerMTok < 0 {
		return fmt.Errorf("pricing: rates must be non-negative")
	}
	return nil
}

// Set appends a new pricing row. Idempotency is the caller's concern —
// admin UI does double-confirm; sync-upstream tags `manual_override`
// to avoid re-inserting on every sync.
func (r *PricingRepo) Set(ctx context.Context, in PricingInput) (*Pricing, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.EffectiveAt.IsZero() {
		in.EffectiveAt = time.Now().UTC()
	}
	const q = `
		INSERT INTO model_relay.pricing
			(model_id, currency,
			 input_per_mtok, output_per_mtok,
			 cache_write_per_mtok, cache_read_per_mtok,
			 cost_per_image, cost_per_video_second,
			 cost_per_audio_second, cost_per_character,
			 cost_per_search_unit,
			 markup_ratio, min_charge, max_charge_per_request,
			 effective_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id, model_id, currency,
		          input_per_mtok, output_per_mtok,
		          cache_write_per_mtok, cache_read_per_mtok,
		          cost_per_image, cost_per_video_second,
		          cost_per_audio_second, cost_per_character,
		          cost_per_search_unit,
		          markup_ratio, min_charge, max_charge_per_request,
		          effective_at, created_by, created_at
	`
	var p Pricing
	err := r.pool.QueryRow(ctx, q,
		in.ModelID, in.Currency,
		in.InputPerMTok, in.OutputPerMTok,
		in.CacheWritePerMTok, in.CacheReadPerMTok,
		in.CostPerImage, in.CostPerVideoSecond,
		in.CostPerAudioSecond, in.CostPerCharacter,
		in.CostPerSearchUnit,
		markupOrDefault(in.MarkupRatio), in.MinCharge, in.MaxChargePerRequest,
		in.EffectiveAt, in.CreatedBy,
	).Scan(
		&p.ID, &p.ModelID, &p.Currency,
		&p.InputPerMTok, &p.OutputPerMTok,
		&p.CacheWritePerMTok, &p.CacheReadPerMTok,
		&p.CostPerImage, &p.CostPerVideoSecond,
		&p.CostPerAudioSecond, &p.CostPerCharacter,
		&p.CostPerSearchUnit,
		&p.MarkupRatio, &p.MinCharge, &p.MaxChargePerRequest,
		&p.EffectiveAt, &p.CreatedBy, &p.CreatedAt,
	)
	if err != nil {
		return nil, translateErr("pricing.set", err)
	}
	return &p, nil
}

// SetTx allows sync-upstream to append pricing as part of a larger
// transaction (model + group binding + pricing should land atomically).
func (r *PricingRepo) SetTx(ctx context.Context, tx pgx.Tx, in PricingInput) (*Pricing, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.EffectiveAt.IsZero() {
		in.EffectiveAt = time.Now().UTC()
	}
	const q = `
		INSERT INTO model_relay.pricing
			(model_id, currency,
			 input_per_mtok, output_per_mtok,
			 cache_write_per_mtok, cache_read_per_mtok,
			 cost_per_image, cost_per_video_second,
			 cost_per_audio_second, cost_per_character,
			 cost_per_search_unit,
			 markup_ratio, min_charge, max_charge_per_request,
			 effective_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id, model_id, currency,
		          input_per_mtok, output_per_mtok,
		          cache_write_per_mtok, cache_read_per_mtok,
		          cost_per_image, cost_per_video_second,
		          cost_per_audio_second, cost_per_character,
		          cost_per_search_unit,
		          markup_ratio, min_charge, max_charge_per_request,
		          effective_at, created_by, created_at
	`
	var p Pricing
	err := tx.QueryRow(ctx, q,
		in.ModelID, in.Currency,
		in.InputPerMTok, in.OutputPerMTok,
		in.CacheWritePerMTok, in.CacheReadPerMTok,
		in.CostPerImage, in.CostPerVideoSecond,
		in.CostPerAudioSecond, in.CostPerCharacter,
		in.CostPerSearchUnit,
		markupOrDefault(in.MarkupRatio), in.MinCharge, in.MaxChargePerRequest,
		in.EffectiveAt, in.CreatedBy,
	).Scan(
		&p.ID, &p.ModelID, &p.Currency,
		&p.InputPerMTok, &p.OutputPerMTok,
		&p.CacheWritePerMTok, &p.CacheReadPerMTok,
		&p.CostPerImage, &p.CostPerVideoSecond,
		&p.CostPerAudioSecond, &p.CostPerCharacter,
		&p.CostPerSearchUnit,
		&p.MarkupRatio, &p.MinCharge, &p.MaxChargePerRequest,
		&p.EffectiveAt, &p.CreatedBy, &p.CreatedAt,
	)
	if err != nil {
		return nil, translateErr("pricing.set_tx", err)
	}
	return &p, nil
}

// ─── pricing_rules CRUD (P4 段 4 / F2.1) ────────────────
//
// model_relay.pricing_rules 是 model.pricing_strategy='parameter' 时的
// 多维乘数表 (by_duration / by_resolution etc.). append-only, 历史保留.
// 真实结算时取最近一条 effective_at.

type PricingRulesRepo struct {
	pool *pgxpool.Pool
}

func NewPricingRulesRepo(pool *pgxpool.Pool) *PricingRulesRepo {
	return &PricingRulesRepo{pool: pool}
}

// History 返某 model 的所有 pricing_rule 行, 最新优先.
func (r *PricingRulesRepo) History(ctx context.Context, modelID uuid.UUID) ([]PricingRule, error) {
	const q = `
		SELECT id, model_id, rule_jsonb, effective_at, created_by, created_at
		FROM model_relay.pricing_rules
		WHERE model_id = $1
		ORDER BY effective_at DESC
	`
	rows, err := r.pool.Query(ctx, q, modelID)
	if err != nil {
		return nil, translateErr("pricing_rules.history", err)
	}
	defer rows.Close()
	out := make([]PricingRule, 0, 4)
	for rows.Next() {
		var p PricingRule
		if err := rows.Scan(&p.ID, &p.ModelID, &p.RuleJSON, &p.EffectiveAt, &p.CreatedBy, &p.CreatedAt); err != nil {
			return nil, translateErr("pricing_rules.history.scan", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Latest — 取最近一条; 不存在返 ErrNotFound.
func (r *PricingRulesRepo) Latest(ctx context.Context, modelID uuid.UUID) (*PricingRule, error) {
	const q = `
		SELECT id, model_id, rule_jsonb, effective_at, created_by, created_at
		FROM model_relay.pricing_rules
		WHERE model_id = $1
		ORDER BY effective_at DESC LIMIT 1
	`
	var p PricingRule
	err := r.pool.QueryRow(ctx, q, modelID).Scan(
		&p.ID, &p.ModelID, &p.RuleJSON, &p.EffectiveAt, &p.CreatedBy, &p.CreatedAt,
	)
	if err != nil {
		return nil, translateErr("pricing_rules.latest", err)
	}
	return &p, nil
}

// Append — 添加一条新规则 (effective_at = now()). 不修改旧规则.
type PricingRuleInput struct {
	ModelID   uuid.UUID
	RuleJSON  []byte // jsonb wire bytes; 调用方自行 marshal
	CreatedBy *uuid.UUID
}

func (r *PricingRulesRepo) Append(ctx context.Context, in PricingRuleInput) (*PricingRule, error) {
	if in.ModelID == uuid.Nil {
		return nil, fmt.Errorf("pricing_rules: model_id required")
	}
	if len(in.RuleJSON) == 0 {
		return nil, fmt.Errorf("pricing_rules: rule_jsonb required")
	}
	const q = `
		INSERT INTO model_relay.pricing_rules (model_id, rule_jsonb, created_by)
		VALUES ($1, $2::jsonb, $3)
		RETURNING id, model_id, rule_jsonb, effective_at, created_by, created_at
	`
	var p PricingRule
	err := r.pool.QueryRow(ctx, q, in.ModelID, in.RuleJSON, in.CreatedBy).Scan(
		&p.ID, &p.ModelID, &p.RuleJSON, &p.EffectiveAt, &p.CreatedBy, &p.CreatedAt,
	)
	if err != nil {
		return nil, translateErr("pricing_rules.append", err)
	}
	return &p, nil
}
