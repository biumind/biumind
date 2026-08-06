// plans.go — Phase 4 W2-3 plans 仓储.
//
// billing.plans 表的读路径 (写只在 migration seed). 反序列化 benefits
// jsonb 到 PlanLimits struct, 让 PlanLimits 可以从 DB 读 (W2-9).
//
// W2 阶段 PlanResolver 仍 hardcode 读 DefaultLimits; W2-9 切到 DB 读时
// fallback 到 DefaultLimits 当 DB 不可用 (启动时 cache miss).

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanRow — billing.plans 一行的 Go 表示. PriceCurrency / PriceMonthly /
// PriceYearly 主存原币种 (一般 USD), 客户端按用户结算币种走 fx_rates 折算.
type PlanRow struct {
	ID                   uuid.UUID
	Code                 Plan
	Name                 string
	Description          string
	SortOrder            int
	PriceCurrency        string
	PriceMonthly         float64
	PriceYearly          float64
	MonthlyCredits       int64
	Benefits             PlanLimits // 从 jsonb benefits 反序列化
	StripePriceMonthly   string     // NULL → ""
	StripePriceYearly    string     // NULL → ""
	Status               string     // 'active' | 'archived'
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// jsonBenefits 是 billing.plans.benefits 的 JSON wire shape.
// snake_case 与 W2-2 seed 对齐.
type jsonBenefits struct {
	HubRPM            int64 `json:"hub_rpm"`
	HubTPM            int64 `json:"hub_tpm"`
	SandboxDaily      int64 `json:"sandbox_daily"`
	SandboxConcurrent int   `json:"sandbox_concurrent"`
	MemoryQuota       int   `json:"memory_quota"`
	BrainProjects     int   `json:"brain_projects"`
}

func (jb jsonBenefits) toLimits() PlanLimits {
	return PlanLimits{
		HubRPM:            jb.HubRPM,
		HubTPM:            jb.HubTPM,
		SandboxDaily:      jb.SandboxDaily,
		SandboxConcurrent: jb.SandboxConcurrent,
		MemoryQuota:       jb.MemoryQuota,
		BrainProjects:     jb.BrainProjects,
	}
}

func limitsToJSONBenefits(l PlanLimits) jsonBenefits {
	return jsonBenefits{
		HubRPM:            l.HubRPM,
		HubTPM:            l.HubTPM,
		SandboxDaily:      l.SandboxDaily,
		SandboxConcurrent: l.SandboxConcurrent,
		MemoryQuota:       l.MemoryQuota,
		BrainProjects:     l.BrainProjects,
	}
}

// ErrPlanNotFound — 单 plan 查询未命中. 调用方一般 fallback 到
// DefaultLimits[plan] (W2-9).
var ErrPlanNotFound = errors.New("billing: plan not found")

// PlansRepo 是 billing.plans 表的读 CRUD.
// 当前只读 (写在 migration seed). W3+ 加 admin endpoint 时再补 Update.
type PlansRepo struct {
	pool *pgxpool.Pool
}

// NewPlansRepo wraps a pgxpool.
func NewPlansRepo(pool *pgxpool.Pool) *PlansRepo {
	return &PlansRepo{pool: pool}
}

// List 返回 status='active' 的所有 plans, 按 sort_order 升序.
// 用于 GET /v1/plans (W2-5).
func (r *PlansRepo) List(ctx context.Context) ([]PlanRow, error) {
	const q = `
		SELECT id, code, name, description, sort_order,
		       price_currency, price_monthly, price_yearly, monthly_credits,
		       benefits,
		       COALESCE(stripe_price_monthly, ''),
		       COALESCE(stripe_price_yearly, ''),
		       status, created_at, updated_at
		FROM billing.plans
		WHERE status = 'active'
		ORDER BY sort_order ASC
	`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("plans.list: %w", err)
	}
	defer rows.Close()
	out := make([]PlanRow, 0, 8)
	for rows.Next() {
		p, err := scanPlan(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get 按 code 取单 plan. 未命中返 ErrPlanNotFound.
func (r *PlansRepo) Get(ctx context.Context, code Plan) (*PlanRow, error) {
	const q = `
		SELECT id, code, name, description, sort_order,
		       price_currency, price_monthly, price_yearly, monthly_credits,
		       benefits,
		       COALESCE(stripe_price_monthly, ''),
		       COALESCE(stripe_price_yearly, ''),
		       status, created_at, updated_at
		FROM billing.plans
		WHERE code = $1
		LIMIT 1
	`
	row := r.pool.QueryRow(ctx, q, string(code))
	p, err := scanPlan(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByID 按 uuid 取 plan. subscriptions / subscription_events 表通过
// plan_id FK 关联, 写日志时常需要 code → uuid 反向, 这里提供.
func (r *PlansRepo) GetByID(ctx context.Context, id uuid.UUID) (*PlanRow, error) {
	const q = `
		SELECT id, code, name, description, sort_order,
		       price_currency, price_monthly, price_yearly, monthly_credits,
		       benefits,
		       COALESCE(stripe_price_monthly, ''),
		       COALESCE(stripe_price_yearly, ''),
		       status, created_at, updated_at
		FROM billing.plans
		WHERE id = $1
		LIMIT 1
	`
	row := r.pool.QueryRow(ctx, q, id)
	p, err := scanPlan(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// scanPlan 把一行 SELECT 结果转成 PlanRow.
// scan 是 pgx.Row.Scan 或 pgx.Rows.Scan 的 callable, 复用同一份字段顺序.
func scanPlan(scan func(...any) error) (PlanRow, error) {
	var (
		p           PlanRow
		code        string
		benefitsRaw []byte
	)
	if err := scan(
		&p.ID, &code, &p.Name, &p.Description, &p.SortOrder,
		&p.PriceCurrency, &p.PriceMonthly, &p.PriceYearly, &p.MonthlyCredits,
		&benefitsRaw, &p.StripePriceMonthly, &p.StripePriceYearly,
		&p.Status, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return PlanRow{}, err
	}
	p.Code = Plan(code)
	if len(benefitsRaw) > 0 {
		var jb jsonBenefits
		if err := json.Unmarshal(benefitsRaw, &jb); err != nil {
			return PlanRow{}, fmt.Errorf("plans.scan benefits: %w", err)
		}
		p.Benefits = jb.toLimits()
	}
	return p, nil
}

// ResolveLimits — W2-9 用. 给定 plan code, 返 PlanLimits.
//   1. 先查 DB (DefaultLimits 当 fallback)
//   2. DB 不可用 (查询 err) → DefaultLimits[code] (内置)
//   3. 都没有 → DefaultLimits[PlanFree] (永远不 panic)
//
// 调用频率: model-relay 每次解析 user plan 时, 60s cache 命中后才查 DB.
// 所以 N 次 query/min ≈ 服务副本数, 不会成瓶颈.
func (r *PlansRepo) ResolveLimits(ctx context.Context, code Plan) PlanLimits {
	if r != nil && r.pool != nil {
		if p, err := r.Get(ctx, code); err == nil {
			return p.Benefits
		}
	}
	if l, ok := DefaultLimits[code]; ok {
		return l
	}
	return DefaultLimits[PlanFree]
}
