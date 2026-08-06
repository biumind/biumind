// quota.go — W4-2 套餐月度配额优先级分配.
//
// 扣减优先级 (W4 起): plan_quota → 时效积分 → 永久积分.
// 本文件实现第一段 (plan_quota); 后两段沿用 packages 现有顺序.
//
// 工作流:
//
//	1. AllocateQuota 在事务内查 user 当前 plan + plan_quotas[plan, ref_type]
//	   + user_quota_usage[user, ref_type] (如不存在自动建当月行).
//	2. 若 used+amount ≤ monthly_amount 则全 quota 命中 (PackageAmount=0);
//	   否则拆成 (quota_remaining, amount-quota_remaining).
//	3. 外层 Consume / Hold 用 PackageAmount 走 reserveFromPackages.
//	4. 调用方在事务 commit 前调 RecordQuotaUsage 把 quota 部分落账
//	   (used_amount += quota_amount). RecordQuotaUsage 与 AllocateQuota
//	   分离, 因为 Hold 时不计入 used (释放后还要回退); Consume / Settle
//	   才真正消耗.
//
// 周期: period_start = 当前自然月 UTC 1 号 00:00, period_end = 下月 1 号
// 00:00. cron (W4-4) 月初推进到下个月.
//
// 不变量:
//   - free 用户 plan_quotas 行为 monthly_amount=0, 永远 PackageAmount=amount.
//   - period_end ≤ now() 视作过期周期, 同步重置 (used=0, period 推进).
//     这是兜底 — cron 可能延迟; 兜底不依赖 cron 也能正确扣账.

package credits

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QuotaRefType — 仅这两类业务支持 quota 优先级 (与 plan_quotas CHECK 对齐).
var quotaRefTypes = map[LogRefType]struct{}{
	RefChatMessage: {},
	RefAIGCTask:    {},
}

// Allocation — quota 优先级分配结果. 二者之和 = 调用方传入的 amount.
type Allocation struct {
	QuotaAmount   int64 // 从 plan_quota 扣 (调用方需调 RecordQuotaUsage 落账)
	PackageAmount int64 // 剩余, 走 packages (时效优先, 永久兜底)
}

// AllocateQuotaInTx — 在已开事务内查 quota 余量并算出拆分.
// 不修改 user_quota_usage; 调用方在确认实际消费后调 RecordQuotaUsage.
//
// refType 不在 quotaRefTypes 范围内 → 全部走 packages (Allocation{0, amount}).
// user 没有有效订阅或 plan 无配置 → 同上.
func AllocateQuotaInTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, refType LogRefType, amount int64, now time.Time) (Allocation, error) {
	if amount <= 0 {
		return Allocation{}, ErrInvalidAmount
	}
	if _, ok := quotaRefTypes[refType]; !ok {
		return Allocation{PackageAmount: amount}, nil
	}

	monthly, used, _, _, err := loadQuotaState(ctx, tx, userID, refType, now)
	if err != nil {
		return Allocation{}, err
	}
	if monthly <= 0 {
		return Allocation{PackageAmount: amount}, nil
	}

	remaining := monthly - used
	if remaining <= 0 {
		return Allocation{PackageAmount: amount}, nil
	}
	if remaining >= amount {
		return Allocation{QuotaAmount: amount}, nil
	}
	return Allocation{
		QuotaAmount:   remaining,
		PackageAmount: amount - remaining,
	}, nil
}

// RecordQuotaUsage — 累加 quota 消耗到 user_quota_usage. 自动建行 / 推周期.
// 调用方应在事务内、扣 packages 前后均可 (单调递增).
func RecordQuotaUsage(ctx context.Context, tx pgx.Tx, userID uuid.UUID, refType LogRefType, amount int64, now time.Time) error {
	if amount <= 0 {
		return nil
	}
	if _, ok := quotaRefTypes[refType]; !ok {
		return nil
	}
	periodStart, periodEnd := monthBounds(now)
	monthly, _, rowExists, rowExpired, err := loadQuotaState(ctx, tx, userID, refType, now)
	if err != nil {
		return err
	}
	if !rowExists || rowExpired {
		// upsert 一行; 周期已过的 used 重置为本次量.
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.user_quota_usage
			    (user_id, ref_type, period_start, period_end, used_amount, monthly_amount, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (user_id, ref_type) DO UPDATE SET
			    period_start   = EXCLUDED.period_start,
			    period_end     = EXCLUDED.period_end,
			    used_amount    = EXCLUDED.used_amount,
			    monthly_amount = EXCLUDED.monthly_amount,
			    updated_at     = EXCLUDED.updated_at
		`, userID, string(refType), periodStart, periodEnd, amount, monthly, now)
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE identity.user_quota_usage
		SET used_amount = used_amount + $1,
		    updated_at  = $2
		WHERE user_id = $3 AND ref_type = $4
	`, amount, now, userID, string(refType))
	return err
}

// RefundQuotaUsage — 回滚 quota 消耗 (Refund / Release / Settle 部分退还时).
// 不会让 used_amount < 0 (CHECK 兜底); 跨周期退款也只在当周期减.
func RefundQuotaUsage(ctx context.Context, tx pgx.Tx, userID uuid.UUID, refType LogRefType, amount int64, now time.Time) error {
	if amount <= 0 {
		return nil
	}
	if _, ok := quotaRefTypes[refType]; !ok {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE identity.user_quota_usage
		SET used_amount = GREATEST(used_amount - $1, 0),
		    updated_at  = $2
		WHERE user_id = $3 AND ref_type = $4
	`, amount, now, userID, string(refType))
	return err
}

// QuotaState — GET /v1/subscriptions/me 用; 返当前 plan 在某 ref_type
// 的本月 quota 配额 + 用量.
type QuotaState struct {
	RefType       LogRefType
	MonthlyAmount int64
	UsedAmount    int64
	PeriodStart   time.Time
	PeriodEnd     time.Time
}

// QuotaQuerier — pgx.Tx / pgxpool.Pool 共同满足 (Query 方法).
type QuotaQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// GetQuotaStates — Service 方法形式; 接 pool 直查 (无事务).
// API handler (GET /v1/subscriptions/me) 用.
func (s *Service) GetQuotaStates(ctx context.Context, userID uuid.UUID) ([]QuotaState, error) {
	return GetQuotaStates(ctx, s.pool, userID, s.now())
}

// GetQuotaStates — 给定 user, 返该用户当前 plan 所有 ref_type 的 quota 状态.
// 行不存在 (新用户) 时返当月 zero-used 状态.
//
// 接 interface 而不是 pgx.Tx, 这样 pgxpool.Pool 直连也能用 (handler
// 不必为读侧操作开事务).
func GetQuotaStates(ctx context.Context, q QuotaQuerier, userID uuid.UUID, now time.Time) ([]QuotaState, error) {
	periodStart, periodEnd := monthBounds(now)
	rows, err := q.Query(ctx, `
		SELECT pq.ref_type, pq.monthly_amount,
		       COALESCE(uqu.used_amount, 0)::bigint AS used,
		       COALESCE(uqu.period_start, $2)::timestamptz AS p_start,
		       COALESCE(uqu.period_end, $3)::timestamptz AS p_end
		FROM identity.users u
		JOIN billing.plans p   ON p.code = u.plan
		JOIN billing.plan_quotas pq ON pq.plan_id = p.id
		LEFT JOIN identity.user_quota_usage uqu
		    ON uqu.user_id = u.id AND uqu.ref_type = pq.ref_type
		WHERE u.id = $1
		ORDER BY pq.ref_type
	`, userID, periodStart, periodEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuotaState
	for rows.Next() {
		var s QuotaState
		var rt string
		if err := rows.Scan(&rt, &s.MonthlyAmount, &s.UsedAmount, &s.PeriodStart, &s.PeriodEnd); err != nil {
			return nil, err
		}
		s.RefType = LogRefType(rt)
		// 周期已过的用量当作 0 (cron 还没推进时的兜底显示).
		if !s.PeriodEnd.After(now) {
			s.UsedAmount = 0
			s.PeriodStart = periodStart
			s.PeriodEnd = periodEnd
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── helpers ──────────────────────────────────────────

// loadQuotaState — 一次 query 拿 monthly_amount + used + 行状态.
// monthly_amount=0 (free 用户 / plan_quotas 无行) → quota 不可用.
// rowExists=false → user_quota_usage 没行 (新用户 / 首次该 ref_type).
// rowExpired=true → 行存在但 period_end ≤ now (跨月 cron 还没跑).
func loadQuotaState(ctx context.Context, tx pgx.Tx, userID uuid.UUID, refType LogRefType, now time.Time) (monthly, used int64, rowExists, rowExpired bool, err error) {
	row := tx.QueryRow(ctx, `
		SELECT
		    COALESCE(pq.monthly_amount, 0)::bigint AS monthly,
		    COALESCE(uqu.used_amount, 0)::bigint AS used,
		    uqu.period_end
		FROM identity.users u
		LEFT JOIN billing.plans p   ON p.code = u.plan
		LEFT JOIN billing.plan_quotas pq
		    ON pq.plan_id = p.id AND pq.ref_type = $2
		LEFT JOIN identity.user_quota_usage uqu
		    ON uqu.user_id = u.id AND uqu.ref_type = $2
		WHERE u.id = $1
	`, userID, string(refType))
	var periodEnd *time.Time
	if scanErr := row.Scan(&monthly, &used, &periodEnd); scanErr != nil {
		if errors.Is(scanErr, pgx.ErrNoRows) {
			// user 不存在 — quota 兜底关闭, 让外层正常路径报权限错.
			return 0, 0, false, false, nil
		}
		return 0, 0, false, false, scanErr
	}
	if periodEnd != nil {
		rowExists = true
		if !periodEnd.After(now) {
			rowExpired = true
			used = 0 // 视作下个周期已重置
		}
	}
	return monthly, used, rowExists, rowExpired, nil
}

// monthBounds — 返当前自然月 UTC 边界 [period_start, period_end).
//
//	periodStart = 当月 1 号 00:00:00 UTC
//	periodEnd   = 下月 1 号 00:00:00 UTC
func monthBounds(now time.Time) (start, end time.Time) {
	t := now.UTC()
	start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = start.AddDate(0, 1, 0)
	return start, end
}
