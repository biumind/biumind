package credits

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// quotaTestUser — 在 identity.users 插入一行测试用户, 指定 plan code.
// 测试结束自动清理.
func quotaTestUser(t *testing.T, pool *pgxpool.Pool, plan string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	uid := uuid.New()
	email := "quota-" + uid.String() + "@test.local"
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (id, email, plan)
		VALUES ($1, $2, $3)
	`, uid, email, plan); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.user_quota_usage WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.users WHERE id=$1`, uid)
	})
	return uid
}

// ════════════════════════════════════════════════════════════
// AllocateQuotaInTx + RecordQuotaUsage + RefundQuotaUsage + GetQuotaStates
// 10 cases — 与 W4 dev plan §5.2 W4-2 验收一致.
// ════════════════════════════════════════════════════════════

func TestAllocateQuota_RealCases(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. free 用户, chat — quota=0 全走 packages.
	t.Run("free_user_all_package", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "free")
		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		got, err := AllocateQuotaInTx(ctx, tx, uid, RefChatMessage, 100, now)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got.QuotaAmount != 0 || got.PackageAmount != 100 {
			t.Fatalf("got %+v, want {0,100}", got)
		}
	})

	// 2. pro 用户, amount < quota — 全走 quota.
	t.Run("pro_amount_lt_quota", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		got, err := AllocateQuotaInTx(ctx, tx, uid, RefChatMessage, 100, now)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got.QuotaAmount != 100 || got.PackageAmount != 0 {
			t.Fatalf("got %+v, want {100,0}", got)
		}
	})

	// 3. pro 用户, amount = quota — 全走 quota (边界).
	t.Run("pro_amount_eq_quota", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		got, err := AllocateQuotaInTx(ctx, tx, uid, RefChatMessage, 5000, now)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got.QuotaAmount != 5000 || got.PackageAmount != 0 {
			t.Fatalf("got %+v, want {5000,0}", got)
		}
	})

	// 4. pro 用户, amount > quota — 拆分.
	t.Run("pro_amount_gt_quota_split", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		got, err := AllocateQuotaInTx(ctx, tx, uid, RefChatMessage, 6000, now)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got.QuotaAmount != 5000 || got.PackageAmount != 1000 {
			t.Fatalf("got %+v, want {5000,1000}", got)
		}
	})

	// 5. pro 用户, quota 已耗尽 — 全走 packages.
	t.Run("pro_quota_exhausted", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		tx, _ := pool.Begin(ctx)
		if err := RecordQuotaUsage(ctx, tx, uid, RefChatMessage, 5000, now); err != nil {
			tx.Rollback(ctx)
			t.Fatalf("seed: %v", err)
		}
		_ = tx.Commit(ctx)

		tx2, _ := pool.Begin(ctx)
		defer tx2.Rollback(ctx)
		got, err := AllocateQuotaInTx(ctx, tx2, uid, RefChatMessage, 100, now)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got.QuotaAmount != 0 || got.PackageAmount != 100 {
			t.Fatalf("got %+v, want {0,100}", got)
		}
	})

	// 6. ref_type 不在 quota 支持范围 — 全走 packages.
	t.Run("ref_type_out_of_scope", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		got, err := AllocateQuotaInTx(ctx, tx, uid, RefRecharge, 100, now)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got.QuotaAmount != 0 || got.PackageAmount != 100 {
			t.Fatalf("got %+v, want {0,100}", got)
		}
	})

	// 7. RecordQuotaUsage 累加 + 自动建行.
	t.Run("record_creates_then_increments", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		tx, _ := pool.Begin(ctx)
		if err := RecordQuotaUsage(ctx, tx, uid, RefChatMessage, 100, now); err != nil {
			tx.Rollback(ctx)
			t.Fatalf("first: %v", err)
		}
		if err := RecordQuotaUsage(ctx, tx, uid, RefChatMessage, 200, now); err != nil {
			tx.Rollback(ctx)
			t.Fatalf("second: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		var used int64
		if err := pool.QueryRow(ctx,
			`SELECT used_amount FROM identity.user_quota_usage WHERE user_id=$1 AND ref_type='chat_message'`, uid,
		).Scan(&used); err != nil {
			t.Fatalf("query: %v", err)
		}
		if used != 300 {
			t.Fatalf("used=%d want 300", used)
		}
	})

	// 8. RefundQuotaUsage 减少, 不到 0.
	t.Run("refund_clamps_at_zero", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		tx, _ := pool.Begin(ctx)
		_ = RecordQuotaUsage(ctx, tx, uid, RefChatMessage, 50, now)
		_ = RefundQuotaUsage(ctx, tx, uid, RefChatMessage, 200, now)
		_ = tx.Commit(ctx)
		var used int64
		_ = pool.QueryRow(ctx,
			`SELECT used_amount FROM identity.user_quota_usage WHERE user_id=$1 AND ref_type='chat_message'`, uid,
		).Scan(&used)
		if used != 0 {
			t.Fatalf("used=%d want 0", used)
		}
	})

	// 9. 跨周期: 已过期 row, used 视作 0, 全 quota.
	t.Run("expired_period_resets", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		oldStart := now.AddDate(0, -2, 0).Truncate(24 * time.Hour)
		oldEnd := now.AddDate(0, -1, 0).Truncate(24 * time.Hour)
		if _, err := pool.Exec(ctx, `
			INSERT INTO identity.user_quota_usage (user_id, ref_type, period_start, period_end, used_amount, monthly_amount, updated_at)
			VALUES ($1, 'chat_message', $2, $3, 4900, 5000, $4)
		`, uid, oldStart, oldEnd, now); err != nil {
			t.Fatalf("seed expired: %v", err)
		}
		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		got, err := AllocateQuotaInTx(ctx, tx, uid, RefChatMessage, 1000, now)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got.QuotaAmount != 1000 || got.PackageAmount != 0 {
			t.Fatalf("got %+v want full quota (period reset)", got)
		}
	})

	// 10. GetQuotaStates 返 pro 用户两种 ref_type.
	t.Run("get_states_returns_all_ref_types", func(t *testing.T) {
		uid := quotaTestUser(t, pool, "pro")
		_, _ = pool.Exec(ctx, `INSERT INTO identity.user_quota_usage
			(user_id, ref_type, period_start, period_end, used_amount, monthly_amount, updated_at)
			VALUES ($1, 'chat_message', $2, $3, 1500, 5000, $4)`,
			uid, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC),
			time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0), now)

		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		states, err := GetQuotaStates(ctx, tx, uid, now)
		if err != nil {
			t.Fatalf("states: %v", err)
		}
		if len(states) != 2 {
			t.Fatalf("got %d states, want 2 (chat_message + aigc_task)", len(states))
		}
		var chatState, aigcState QuotaState
		for _, s := range states {
			switch s.RefType {
			case RefChatMessage:
				chatState = s
			case RefAIGCTask:
				aigcState = s
			}
		}
		if chatState.MonthlyAmount != 5000 || chatState.UsedAmount != 1500 {
			t.Fatalf("chat state = %+v", chatState)
		}
		if aigcState.MonthlyAmount != 1000 || aigcState.UsedAmount != 0 {
			t.Fatalf("aigc state = %+v", aigcState)
		}
	})
}
