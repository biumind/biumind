package credits

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// W4-3: Consume / Hold / Refund / Settle / Release 接入 quota 优先级的
// 8 个集成测试. 与 dev plan §5.2 W4-3 验收一致.

// quotaUserBalance — 给指定用户初始化 packages, 返 svc / uid.
// plan 为空时不插 user (走 newUser, 即 plan='free' 默认).
func quotaUserBalance(t *testing.T, pool *pgxpool.Pool, plan string, permanent int64, timeLimited int64, exp *time.Time) (*Service, uuid.UUID) {
	t.Helper()
	svc := New(pool)
	uid := quotaTestUser(t, pool, plan)
	ctx := context.Background()
	resetUser(t, pool, uid)
	if permanent > 0 {
		if _, _, err := svc.Grant(ctx, GrantArgs{
			UserID: uid, Amount: permanent, Kind: KindPermanent, Source: SourceRecharge,
		}); err != nil {
			t.Fatalf("grant permanent: %v", err)
		}
	}
	if timeLimited > 0 {
		if exp == nil {
			ex := time.Now().Add(48 * time.Hour)
			exp = &ex
		}
		if _, _, err := svc.Grant(ctx, GrantArgs{
			UserID: uid, Amount: timeLimited, Kind: KindTimeLimited, Source: SourcePlanGrant, ExpiresAt: exp,
		}); err != nil {
			t.Fatalf("grant time_limited: %v", err)
		}
	}
	return svc, uid
}

func quotaUsed(t *testing.T, pool *pgxpool.Pool, uid uuid.UUID, refType LogRefType) int64 {
	t.Helper()
	var used int64
	err := pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT used_amount FROM identity.user_quota_usage WHERE user_id=$1 AND ref_type=$2), 0)`,
		uid, string(refType),
	).Scan(&used)
	if err != nil {
		t.Fatalf("quota used: %v", err)
	}
	return used
}

// 1. Consume — pro 用户, amount < quota — breakdown 一段 quota.
func TestConsume_W43_ProQuotaOnly(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "pro", 1000, 0, nil)
	ctx := context.Background()

	log, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 100, RefType: RefChatMessage, RefID: "m1",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(log.ConsumeBreakdown) != 1 {
		t.Fatalf("breakdown=%+v want 1 quota seg", log.ConsumeBreakdown)
	}
	if !log.ConsumeBreakdown[0].IsQuota() || log.ConsumeBreakdown[0].Amount != 100 {
		t.Fatalf("breakdown[0]=%+v", log.ConsumeBreakdown[0])
	}
	if used := quotaUsed(t, pool, uid, RefChatMessage); used != 100 {
		t.Fatalf("quota used=%d want 100", used)
	}
}

// 2. Consume — pro 用户, amount > quota — quota+package 拆段.
func TestConsume_W43_QuotaPlusPackage(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "pro", 10000, 0, nil)
	ctx := context.Background()

	log, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 5500, RefType: RefChatMessage, RefID: "m2",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(log.ConsumeBreakdown) != 2 {
		t.Fatalf("breakdown=%+v want 2 segs", log.ConsumeBreakdown)
	}
	if !log.ConsumeBreakdown[0].IsQuota() || log.ConsumeBreakdown[0].Amount != 5000 {
		t.Fatalf("seg[0]=%+v", log.ConsumeBreakdown[0])
	}
	if log.ConsumeBreakdown[1].IsQuota() || log.ConsumeBreakdown[1].Amount != 500 {
		t.Fatalf("seg[1]=%+v", log.ConsumeBreakdown[1])
	}
	if used := quotaUsed(t, pool, uid, RefChatMessage); used != 5000 {
		t.Fatalf("quota used=%d want 5000", used)
	}
}

// 3. Consume — pro quota 已耗尽 — breakdown 全 package.
func TestConsume_W43_QuotaExhausted(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "pro", 10000, 0, nil)
	ctx := context.Background()

	if _, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 5000, RefType: RefChatMessage, RefID: "m-fill-quota",
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	log, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 100, RefType: RefChatMessage, RefID: "m-after",
	})
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	for _, b := range log.ConsumeBreakdown {
		if b.IsQuota() {
			t.Fatalf("seg %+v should not be quota (exhausted)", b)
		}
	}
}

// 4. Consume — free 用户 — breakdown 全 package.
func TestConsume_W43_FreeAllPackage(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "free", 1000, 0, nil)
	ctx := context.Background()

	log, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 100, RefType: RefChatMessage, RefID: "m-free",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	for _, b := range log.ConsumeBreakdown {
		if b.IsQuota() {
			t.Fatalf("free user should not hit quota: %+v", b)
		}
	}
}

// 5. Refund — quota 段反向退到 user_quota_usage.
//
// Refund 反向遍历 breakdown, 每段 step.Amount 是原扣金额; 当 left
// 大于末段 step.Amount 时溢出到前一段 (= quota 段).
func TestRefund_W43_QuotaSegment(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "pro", 10000, 0, nil)
	ctx := context.Background()

	consumeLog, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 5500, RefType: RefChatMessage, RefID: "m-refund",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	// 单次退 1500: 反向 → package 段全退 (500) + quota 段退 1000.
	if _, _, err := svc.Refund(ctx, RefundArgs{
		OriginalLogID: consumeLog.ID, Amount: 1500,
	}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if used := quotaUsed(t, pool, uid, RefChatMessage); used != 4000 {
		t.Fatalf("quota used=%d want 4000 (1000 退回)", used)
	}
}

// 6. Hold — pro 用户, MaxAmount 部分走 quota.
func TestHold_W43_QuotaPlusPackage(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "pro", 10000, 0, nil)
	ctx := context.Background()

	hold, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 5500, RefType: RefChatMessage, RefID: "h1",
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if len(hold.HoldBreakdown) != 2 {
		t.Fatalf("hold breakdown=%+v want 2", hold.HoldBreakdown)
	}
	if !hold.HoldBreakdown[0].IsQuota() || hold.HoldBreakdown[0].Amount != 5000 {
		t.Fatalf("seg[0]=%+v", hold.HoldBreakdown[0])
	}
	if used := quotaUsed(t, pool, uid, RefChatMessage); used != 5000 {
		t.Fatalf("hold should reserve quota; used=%d", used)
	}
}

// 7. Settle — 部分退还 quota (max-actual 反向).
func TestSettle_W43_QuotaRefundPartial(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "pro", 10000, 0, nil)
	ctx := context.Background()

	hold, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 5500, RefType: RefChatMessage, RefID: "h-settle",
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	// actual=4500: 反向退 1000; package seg 500 全退 + quota seg 退 500.
	if _, _, _, err := svc.Settle(ctx, SettleArgs{
		HoldID: hold.ID, ActualAmount: 4500,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if used := quotaUsed(t, pool, uid, RefChatMessage); used != 4500 {
		t.Fatalf("quota used=%d want 4500", used)
	}
}

// 8. Release — 全退还 quota (used 回到 0).
func TestRelease_W43_QuotaFullRefund(t *testing.T) {
	pool := newTestPool(t)
	svc, uid := quotaUserBalance(t, pool, "pro", 10000, 0, nil)
	ctx := context.Background()

	hold, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 5500, RefType: RefChatMessage, RefID: "h-rel",
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, _, err := svc.Release(ctx, hold.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if used := quotaUsed(t, pool, uid, RefChatMessage); used != 0 {
		t.Fatalf("quota used=%d want 0 (full refund)", used)
	}
}
