// Package cron — Identity 内置定时任务 (月初积分发放等).
//
// 每个 job 走 "tick 检查 + 幂等键" 模式: goroutine 每分钟跑一次 tick 函数,
// tick 内部判断是否到达触发时刻; 触发时调对应 Process 函数 (Process 函数
// 自身幂等, 跑两次只生效一次).
//
// 选这个简单模型而不是引第三方 cron lib 的原因:
//   - 单个 identity 实例就够 (没有分布式 leader 选举);
//   - 调度精度 ±1 分钟可接受 (会员发放不是秒级业务);
//   - 重启后 tick 自动恢复, 没有任务持久化负担.
//
// 多副本部署时通过 idempotency_key 兜底 (credits.Grant + INSERT ON CONFLICT
// 都不会重复发); 但仍只该有一个 leader 跑 tick 减少 DB 抢锁. 后续接入
// k8s leader election 再优化.

package cron

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/identity/internal/credits"
)

// MonthlyGrantConfig — 月初积分 + quota 重置任务的配置.
type MonthlyGrantConfig struct {
	// Location 触发时区. 留 nil 默认 Asia/Shanghai (与 dev plan §5.2 W4-4 一致).
	Location *time.Location
	// HourOfDay 触发小时 (0-23). 默认 0.
	HourOfDay int
	// MinuteOfDay 触发分钟 (0-59). 默认 30.
	// 0:30 是夜间低流量窗口, 不撞用户在线高峰.
	MinuteOfDay int
	// Now 时间注入 (测试用), 留 nil 取 time.Now.
	Now func() time.Time
}

func (c *MonthlyGrantConfig) defaults() {
	if c.Location == nil {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			loc = time.FixedZone("Asia/Shanghai", 8*3600)
		}
		c.Location = loc
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.HourOfDay == 0 && c.MinuteOfDay == 0 {
		c.MinuteOfDay = 30
	}
}

// MonthlyGrantJob — 持有 dependencies, Run 跑后台 goroutine.
type MonthlyGrantJob struct {
	pool   *pgxpool.Pool
	svc    *credits.Service
	cfg    MonthlyGrantConfig
	logger *slog.Logger

	// lastFired 记最近一次 ProcessMonthlyGrant 跑过的 (year, month) 防止
	// 同一日内多次 tick 重复触发. 进程内状态; 重启会重置, 但 idempotency_key
	// 会兜底 (credits.Grant 重复键直接命中已有 log).
	lastFired struct {
		year  int
		month time.Month
	}
}

func NewMonthlyGrant(pool *pgxpool.Pool, svc *credits.Service, cfg MonthlyGrantConfig, logger *slog.Logger) *MonthlyGrantJob {
	cfg.defaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &MonthlyGrantJob{pool: pool, svc: svc, cfg: cfg, logger: logger}
}

// Run blocks until ctx canceled; 每分钟检查一次 tick.
func (j *MonthlyGrantJob) Run(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// 首次 tick 立即检查 (服务启动时若错过当月 1 号 00:30 也能补).
	j.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			j.tick(ctx)
		}
	}
}

func (j *MonthlyGrantJob) tick(ctx context.Context) {
	now := j.cfg.Now().In(j.cfg.Location)
	// 触发条件: 当月 1 号, 时分 ≥ 触发时刻, 且本月未跑过.
	if now.Day() != 1 {
		return
	}
	if now.Hour() < j.cfg.HourOfDay ||
		(now.Hour() == j.cfg.HourOfDay && now.Minute() < j.cfg.MinuteOfDay) {
		return
	}
	if j.lastFired.year == now.Year() && j.lastFired.month == now.Month() {
		return
	}
	j.logger.Info("monthly grant tick fired", "now", now.Format(time.RFC3339))
	stats, err := ProcessMonthlyGrant(ctx, j.pool, j.svc, now, j.logger)
	if err != nil {
		j.logger.Error("monthly grant failed", "err", err.Error())
		return
	}
	j.lastFired.year, j.lastFired.month = now.Year(), now.Month()
	j.logger.Info("monthly grant done",
		"users_processed", stats.UsersProcessed,
		"credits_granted", stats.CreditsGranted,
		"quota_reset", stats.QuotaReset,
		"errors", stats.ErrorCount)
}

// MonthlyGrantStats — Process 单次跑的聚合结果, 用于日志 / 测试断言.
type MonthlyGrantStats struct {
	UsersProcessed int
	CreditsGranted int64
	QuotaReset     int
	ErrorCount     int
}

// ProcessMonthlyGrant — 给所有 active/trialing 订阅发月度积分 + 重置 quota.
//
// 流程:
//  1. SELECT 所有 (sub.status IN active/trialing/past_due) 用户 + plan.monthly_credits
//  2. 对每用户调 credits.Grant {Kind=time_limited, Source=plan_grant,
//     ExpiresAt=月末+1月, IdempotencyKey="monthly:<uid>:<YYYY-MM>"}
//  3. 重置 identity.user_quota_usage (per ref_type) 到当月 + used=0
//
// 所有错误日志记录但不中断 — 单个用户失败不影响其他.
//
// idempotent: 同 (now.Year, now.Month) 跑多次只生效一次 (Grant 走 idempotency_key,
// quota reset 走 INSERT ... ON CONFLICT 覆盖 period_end + used).
//
// Stripe webhook (W4-5) 也调本函数 (传入 stripeNow 替 now), 与 cron 幂等.
func ProcessMonthlyGrant(ctx context.Context, pool *pgxpool.Pool, svc *credits.Service, now time.Time, logger *slog.Logger) (MonthlyGrantStats, error) {
	if logger == nil {
		logger = slog.Default()
	}
	period := now.UTC().Format("2006-01")
	expiresAt := monthEndExpiry(now)

	// 1. 拉所有 active 订阅 + 对应 plan.monthly_credits.
	rows, err := pool.Query(ctx, `
		SELECT s.user_id, p.code, p.monthly_credits
		FROM billing.subscriptions s
		JOIN billing.plans p ON p.id = s.plan_id
		WHERE s.status IN ('trialing', 'active', 'past_due')
		  AND p.monthly_credits > 0
	`)
	if err != nil {
		return MonthlyGrantStats{}, fmt.Errorf("query subscriptions: %w", err)
	}
	defer rows.Close()

	type userPlan struct {
		uid     uuid.UUID
		plan    string
		credits int64
	}
	var todo []userPlan
	for rows.Next() {
		var up userPlan
		if err := rows.Scan(&up.uid, &up.plan, &up.credits); err != nil {
			return MonthlyGrantStats{}, fmt.Errorf("scan: %w", err)
		}
		todo = append(todo, up)
	}
	if err := rows.Err(); err != nil {
		return MonthlyGrantStats{}, err
	}

	stats := MonthlyGrantStats{}

	// 2. 逐用户发积分.
	for _, up := range todo {
		stats.UsersProcessed++
		if err := GrantUserMonthly(ctx, svc, up.uid, up.plan, up.credits, period, expiresAt); err != nil {
			stats.ErrorCount++
			logger.Error("monthly grant: user grant failed",
				"user", up.uid.String(), "plan", up.plan, "err", err.Error())
			continue
		}
		stats.CreditsGranted += up.credits
	}

	// 3. 重置 quota — 给所有 (active 订阅 user, ref_type) 推到当月新周期.
	resetCount, err := resetQuotaUsage(ctx, pool, now)
	if err != nil {
		logger.Error("monthly grant: quota reset failed", "err", err.Error())
		stats.ErrorCount++
	}
	stats.QuotaReset = resetCount
	return stats, nil
}

// GrantUserMonthly — 给单个用户发月度积分 (cron + Stripe webhook 共用入口).
//
// idempotency_key="monthly:<uid>:<period>" 保证同一周期重复调用只发一次.
// W4-5 Stripe webhook 收到 invoice.payment_succeeded (subscription_create /
// subscription_cycle) 时立即调本函数, 不等月初 cron, 用户即时拿到额度.
func GrantUserMonthly(ctx context.Context, svc *credits.Service, uid uuid.UUID, planCode string, amount int64, period string, expiresAt time.Time) error {
	if amount <= 0 {
		return nil
	}
	idem := fmt.Sprintf("monthly:%s:%s", uid.String(), period)
	_, _, err := svc.Grant(ctx, credits.GrantArgs{
		UserID:         uid,
		Amount:         amount,
		Kind:           credits.KindTimeLimited,
		Source:         credits.SourcePlanGrant,
		ExpiresAt:      &expiresAt,
		Remark:         fmt.Sprintf("月度积分发放 (%s, %s)", planCode, period),
		IdempotencyKey: idem,
	})
	return err
}

// ResetUserQuota — 给单用户当前 plan 的 quota 推到当月 + used=0.
// W4-5 Stripe webhook 内调用; cron 走批量 resetQuotaUsage.
func ResetUserQuota(ctx context.Context, pool *pgxpool.Pool, uid uuid.UUID, now time.Time) error {
	periodStart := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	_, err := pool.Exec(ctx, `
		INSERT INTO identity.user_quota_usage
		    (user_id, ref_type, period_start, period_end, used_amount, monthly_amount, updated_at)
		SELECT u.id, pq.ref_type, $2, $3, 0, pq.monthly_amount, $4
		FROM identity.users u
		JOIN billing.plans p        ON p.code = u.plan
		JOIN billing.plan_quotas pq ON pq.plan_id = p.id
		WHERE u.id = $1
		ON CONFLICT (user_id, ref_type) DO UPDATE SET
		    period_start   = EXCLUDED.period_start,
		    period_end     = EXCLUDED.period_end,
		    used_amount    = 0,
		    monthly_amount = EXCLUDED.monthly_amount,
		    updated_at     = EXCLUDED.updated_at
	`, uid, periodStart, periodEnd, now)
	return err
}

// MonthEndExpiry — 公开化, Stripe webhook 复用.
func MonthEndExpiry(now time.Time) time.Time { return monthEndExpiry(now) }

// PeriodKey — 公开化 "YYYY-MM" 格式化, Stripe webhook 复用.
func PeriodKey(now time.Time) string { return now.UTC().Format("2006-01") }

// resetQuotaUsage — 给所有 active/trialing/past_due 订阅 + 每 ref_type 一行
// upsert 到当月 (used=0). monthly_amount 同步取 plan_quotas 当前值.
func resetQuotaUsage(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int, error) {
	periodStart := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	tag, err := pool.Exec(ctx, `
		INSERT INTO identity.user_quota_usage
		    (user_id, ref_type, period_start, period_end, used_amount, monthly_amount, updated_at)
		SELECT s.user_id, pq.ref_type, $1, $2, 0, pq.monthly_amount, $3
		FROM billing.subscriptions s
		JOIN billing.plans p        ON p.id = s.plan_id
		JOIN billing.plan_quotas pq ON pq.plan_id = p.id
		WHERE s.status IN ('trialing', 'active', 'past_due')
		ON CONFLICT (user_id, ref_type) DO UPDATE SET
		    period_start   = EXCLUDED.period_start,
		    period_end     = EXCLUDED.period_end,
		    used_amount    = 0,
		    monthly_amount = EXCLUDED.monthly_amount,
		    updated_at     = EXCLUDED.updated_at
	`, periodStart, periodEnd, now)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// monthEndExpiry — 月度时效积分到期时间 = 当月 + 1 个月 23:59:59.
// 让用户有完整一个月用完积分; Stripe 续费时旧积分若未消耗则过期蒸发.
func monthEndExpiry(now time.Time) time.Time {
	t := now.UTC()
	// 当月 1 号 + 1 月 = 下月 1 号 00:00:00 → 减 1 秒 = 当月最后一秒
	// 但我们想要 "下月底" (用户使用一整月新发的积分)
	nextMonthStart := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	monthAfter := nextMonthStart.AddDate(0, 1, 0)
	return monthAfter.Add(-time.Second)
}
