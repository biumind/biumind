// backfill-stripe — W2-8 一次性脚本.
//
// 把生产 identity.users 表中所有 stripe_customer_id / stripe_subscription_id
// 存量数据落到 billing.subscriptions, 让 W2-7 webhook 升级后的状态机
// 不会因「webhook 上线前已订阅的 user 在 billing.subscriptions 缺行」而
// 走回 backfill 兜底路径 (那个路径不写 audit event).
//
// 本工具不联系 Stripe API — 完全基于现有 users 表的 denorm 字段:
//   stripe_customer_id / stripe_subscription_id / plan
//
// 限制:
//   - 周期信息 (current_period_start/end) 取 max(now, users.updated_at)
//     + 30d 兜底 (因为 users 表没存周期). 真正周期由后续 webhook 修正.
//   - 不知道 trial 状态, status 一律设 'active' (W1 模式 SetUserPlan
//     成功后 user 一定是付费态).
//
// 使用:
//   go run ./services/identity/cmd/backfill-stripe -dry-run
//   go run ./services/identity/cmd/backfill-stripe                 # 真跑
//
// 输出:
//   每行一个 user, [DRY|INS|SKIP] tag + reason.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/identity/internal/billing"
)

// userRow 一行 SELECT 出来的 (id, plan, stripe_*) 投影.
type userRow struct {
	ID                   uuid.UUID
	Plan                 string
	StripeCustomerID     string
	StripeSubscriptionID string
	UpdatedAt            time.Time
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Don't write; just print what would happen.")
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "Postgres DSN (default $DATABASE_URL)")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("DATABASE_URL or -dsn required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	plans := billing.NewPlansRepo(pool)
	subs := billing.NewSubscriptionsRepo(pool)

	users, err := loadUsersWithStripe(ctx, pool)
	if err != nil {
		log.Fatalf("load users: %v", err)
	}
	fmt.Fprintf(os.Stderr, "found %d users with stripe_subscription_id\n", len(users))

	stats := struct {
		ins, skip, dryRunCount, missingPlan, alreadyExists int
	}{}

	for _, u := range users {
		// 1. 看 billing.subscriptions 里是否已存在
		existing, err := subs.GetByStripeSubID(ctx, u.StripeSubscriptionID)
		if err == nil && existing != nil {
			fmt.Printf("[SKIP] user=%s sub=%s already in billing.subscriptions (status=%s)\n",
				u.ID, u.StripeSubscriptionID, existing.Status)
			stats.alreadyExists++
			continue
		}

		// 2. 解 plan code → plan_id
		planRow, err := plans.Get(ctx, billing.Plan(u.Plan))
		if err != nil {
			fmt.Printf("[SKIP] user=%s plan=%q not in billing.plans: %v\n",
				u.ID, u.Plan, err)
			stats.missingPlan++
			continue
		}

		// 3. 兜底周期: 取 updated_at 作 start, +30 days 作 end (后续 webhook 会刷)
		start := u.UpdatedAt
		if start.IsZero() || start.Before(time.Now().Add(-365*24*time.Hour)) {
			start = time.Now().UTC()
		}
		end := start.Add(30 * 24 * time.Hour)

		if *dryRun {
			fmt.Printf("[DRY] user=%s plan=%s sub=%s start=%s end=%s\n",
				u.ID, u.Plan, u.StripeSubscriptionID,
				start.Format(time.RFC3339), end.Format(time.RFC3339))
			stats.dryRunCount++
			continue
		}

		_, err = subs.Create(ctx, billing.CreateInput{
			UserID:               u.ID,
			PlanID:               planRow.ID,
			Status:               billing.SubStatusActive,
			CurrentPeriodStart:   start,
			CurrentPeriodEnd:     end,
			BillingCycle:         "monthly",
			StripeCustomerID:     u.StripeCustomerID,
			StripeSubscriptionID: u.StripeSubscriptionID,
		})
		if err != nil {
			fmt.Printf("[ERR ] user=%s sub=%s: %v\n", u.ID, u.StripeSubscriptionID, err)
			stats.skip++
			continue
		}
		fmt.Printf("[INS ] user=%s plan=%s sub=%s\n", u.ID, u.Plan, u.StripeSubscriptionID)
		stats.ins++
	}

	mode := "REAL"
	if *dryRun {
		mode = "DRY-RUN"
	}
	fmt.Fprintf(os.Stderr,
		"\n=== summary (%s) ===\n  candidates:        %d\n  inserted:          %d\n  dry_would_insert:  %d\n  skipped (errors):  %d\n  skipped (no plan): %d\n  already in table:  %d\n",
		mode, len(users), stats.ins, stats.dryRunCount, stats.skip, stats.missingPlan, stats.alreadyExists)
}

// loadUsersWithStripe 拉所有 stripe_subscription_id 非空的 user.
//
// 兼容当前 dev schema 不含 stripe_* 字段的情况:
//   - 字段存在 → 读出来
//   - 字段不存在 → 打印 warning, 返空切片让 main 走 0 candidates summary
//
// 真上线时 R0 schema 已含这些字段 (W1 SetUserPlan 写入), 此函数自动
// 切到 happy path 不需改代码.
func loadUsersWithStripe(ctx context.Context, pool *pgxpool.Pool) ([]userRow, error) {
	// 探测字段是否存在
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema='identity'
			  AND table_name='users'
			  AND column_name='stripe_subscription_id'
		)
	`).Scan(&exists); err != nil {
		return nil, fmt.Errorf("probe schema: %w", err)
	}
	if !exists {
		fmt.Fprintln(os.Stderr,
			"WARN: identity.users.stripe_subscription_id 字段不存在 —")
		fmt.Fprintln(os.Stderr,
			"     当前部署可能尚未启用 Stripe 集成. 0 candidates.")
		fmt.Fprintln(os.Stderr,
			"     生产 deploy 前若有 Stripe 历史用户, 需先 ALTER TABLE 加字段并把存量数据 import.")
		return nil, nil
	}

	const q = `
		SELECT id, plan,
		       COALESCE(stripe_customer_id, ''),
		       stripe_subscription_id,
		       updated_at
		FROM identity.users
		WHERE stripe_subscription_id IS NOT NULL
		  AND stripe_subscription_id != ''
		ORDER BY updated_at ASC
	`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []userRow{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Plan, &u.StripeCustomerID, &u.StripeSubscriptionID, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
