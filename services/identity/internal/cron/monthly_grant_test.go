package cron

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/identity/internal/credits"
)

func dbURL() string {
	if v := os.Getenv("CREDITS_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dbURL())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Skipf("PG: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedSub — 给 (uid, plan_code) 在 billing.subscriptions 落一条 active 行.
func seedSub(t *testing.T, pool *pgxpool.Pool, uid uuid.UUID, planCode string) {
	t.Helper()
	ctx := context.Background()
	var planID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM billing.plans WHERE code=$1`, planCode,
	).Scan(&planID); err != nil {
		t.Fatalf("plan id: %v", err)
	}
	periodStart := time.Now().Add(-24 * time.Hour)
	periodEnd := time.Now().Add(30 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing.subscriptions
		    (user_id, plan_id, status, current_period_start, current_period_end)
		VALUES ($1, $2, 'active', $3, $4)
	`, uid, planID, periodStart, periodEnd); err != nil {
		t.Fatalf("insert sub: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM billing.subscriptions WHERE user_id=$1`, uid)
	})
}

// 1. ProcessMonthlyGrant — pro 用户拿到 monthly_credits + quota 重置.
func TestProcessMonthlyGrant_ProUser(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	svc := credits.New(pool)

	uid := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO identity.users (id, email, plan) VALUES ($1, $2, 'pro')`,
		uid, "monthly-pro-"+uid.String()+"@test.local",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.user_quota_usage WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.credit_logs WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.credit_packages WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.user_credits WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.users WHERE id=$1`, uid)
	})
	seedSub(t, pool, uid, "pro")

	// 触发时刻: 2026-07-01 00:30:00 UTC
	now := time.Date(2026, 7, 1, 0, 30, 0, 0, time.UTC)
	stats, err := ProcessMonthlyGrant(ctx, pool, svc, now, slog.Default())
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if stats.UsersProcessed < 1 {
		t.Fatalf("UsersProcessed=%d want ≥1", stats.UsersProcessed)
	}
	if stats.CreditsGranted < 10000 {
		t.Fatalf("CreditsGranted=%d, pro should grant 10000", stats.CreditsGranted)
	}
	// Verify the user actually got 10K time_limited credits.
	bal, err := svc.GetBalance(ctx, uid)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal.TimeLimitedBalance < 10000 {
		t.Fatalf("user time_limited=%d want ≥10000", bal.TimeLimitedBalance)
	}
	// Verify quota reset.
	var used int64
	_ = pool.QueryRow(ctx,
		`SELECT used_amount FROM identity.user_quota_usage WHERE user_id=$1 AND ref_type='chat_message'`, uid,
	).Scan(&used)
	if used != 0 {
		t.Fatalf("quota used=%d want 0 after reset", used)
	}
}

// 2. ProcessMonthlyGrant — 幂等: 同月跑两次, 用户只拿一次积分.
func TestProcessMonthlyGrant_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	svc := credits.New(pool)

	uid := uuid.New()
	_, _ = pool.Exec(ctx,
		`INSERT INTO identity.users (id, email, plan) VALUES ($1, $2, 'pro')`,
		uid, "monthly-idem-"+uid.String()+"@test.local")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.user_quota_usage WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.credit_logs WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.credit_packages WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.user_credits WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.users WHERE id=$1`, uid)
	})
	seedSub(t, pool, uid, "pro")

	now := time.Date(2026, 7, 1, 0, 30, 0, 0, time.UTC)
	if _, err := ProcessMonthlyGrant(ctx, pool, svc, now, slog.Default()); err != nil {
		t.Fatalf("first: %v", err)
	}
	bal1, _ := svc.GetBalance(ctx, uid)

	// Run again same period.
	if _, err := ProcessMonthlyGrant(ctx, pool, svc, now, slog.Default()); err != nil {
		t.Fatalf("second: %v", err)
	}
	bal2, _ := svc.GetBalance(ctx, uid)
	if bal2.TimeLimitedBalance != bal1.TimeLimitedBalance {
		t.Fatalf("balance changed on second run: %d → %d (idempotency broken)",
			bal1.TimeLimitedBalance, bal2.TimeLimitedBalance)
	}
}

// 3. tick 触发条件 — 1 号 00:30 + 上次未跑过.
func TestMonthlyGrantJob_TickFires(t *testing.T) {
	pool := newTestPool(t)
	svc := credits.New(pool)

	cfg := MonthlyGrantConfig{
		Location:    time.UTC,
		HourOfDay:   0,
		MinuteOfDay: 30,
	}
	job := NewMonthlyGrant(pool, svc, cfg, slog.Default())

	// 假装现在是 2026-07-01 00:30 UTC.
	now := time.Date(2026, 7, 1, 0, 30, 0, 0, time.UTC)
	cfg.Now = func() time.Time { return now }
	job.cfg.Now = cfg.Now

	// 跑 tick.
	job.tick(context.Background())
	if job.lastFired.year != 2026 || job.lastFired.month != 7 {
		t.Fatalf("lastFired=%v/%v, want 2026/7", job.lastFired.year, job.lastFired.month)
	}

	// 再 tick 一次 → 不重复跑 (lastFired 已记录).
	prevYear := job.lastFired.year
	job.tick(context.Background())
	if job.lastFired.year != prevYear {
		t.Fatalf("re-fired on same month")
	}
}

// 4. tick 不在 1 号 → 不触发.
func TestMonthlyGrantJob_TickSkipsNonFirstDay(t *testing.T) {
	pool := newTestPool(t)
	svc := credits.New(pool)
	job := NewMonthlyGrant(pool, svc, MonthlyGrantConfig{
		Location:    time.UTC,
		MinuteOfDay: 30,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 0, 30, 0, 0, time.UTC)
		},
	}, slog.Default())
	job.tick(context.Background())
	if job.lastFired.year != 0 {
		t.Fatalf("should not have fired on 7-15: %+v", job.lastFired)
	}
}
