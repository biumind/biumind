package credits

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resetHolds 清空指定 user 的 hold 行 (与 resetUser 配合, 让每个测试隔离).
func resetHolds(t *testing.T, pool *pgxpool.Pool, uid uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM identity.credit_holds WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("reset holds: %v", err)
	}
}

// resetAll 一次性清掉 4 张表, 避免 fixture 漏配.
func resetAll(t *testing.T, pool *pgxpool.Pool, uid uuid.UUID) {
	resetUser(t, pool, uid)
	resetHolds(t, pool, uid)
}

// ════════════════════════════════════════════════════════════
// Hold
// ════════════════════════════════════════════════════════════

func TestHold_Permanent_Sufficient(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	h, bal, err := svc.Hold(ctx, HoldArgs{
		UserID:    uid,
		MaxAmount: 200,
		RefType:   RefChatMessage,
		RefID:     "msg-1",
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if h.Status != HoldStatusHeld || h.MaxAmount != 200 {
		t.Fatalf("hold = %+v", h)
	}
	// 余额应该已扣 200
	if bal.Total() != 800 {
		t.Fatalf("bal = %+v", bal)
	}
	if len(h.HoldBreakdown) == 0 {
		t.Fatal("breakdown empty")
	}
}

func TestHold_Insufficient(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 50, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	_, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100, RefType: RefChatMessage,
	})
	if err != ErrInsufficientCredits {
		t.Fatalf("want ErrInsufficientCredits, got %v", err)
	}
}

func TestHold_TimeLimitedFirst(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	exp := time.Now().Add(48 * time.Hour)
	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 100, Kind: KindTimeLimited, Source: SourcePlanGrant,
		ExpiresAt: &exp,
	}); err != nil {
		t.Fatalf("grant tl: %v", err)
	}
	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant perm: %v", err)
	}

	h, bal, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 80, RefType: RefChatMessage,
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	// 全部从时效扣
	if bal.TimeLimitedBalance != 20 || bal.PermanentBalance != 1000 {
		t.Fatalf("bal = %+v", bal)
	}
	if len(h.HoldBreakdown) != 1 || h.HoldBreakdown[0].Amount != 80 {
		t.Fatalf("breakdown = %+v", h.HoldBreakdown)
	}
}

func TestHold_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	h1, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 200, RefType: RefChatMessage,
		IdempotencyKey: "req-abc",
	})
	if err != nil {
		t.Fatalf("hold1: %v", err)
	}

	h2, bal, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 200, RefType: RefChatMessage,
		IdempotencyKey: "req-abc",
	})
	if err != nil {
		t.Fatalf("hold2: %v", err)
	}
	if h1.ID != h2.ID {
		t.Fatalf("idempotent should return same id; got %s vs %s", h1.ID, h2.ID)
	}
	// 只扣一次
	if bal.Total() != 800 {
		t.Fatalf("bal = %+v", bal)
	}
}

func TestHold_RejectInvalidRefType(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	_, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100, RefType: RefRecharge,
	})
	if err != ErrInvalidHoldRefType {
		t.Fatalf("want ErrInvalidHoldRefType, got %v", err)
	}
}

func TestHold_DefaultsTTL(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	frozen := time.Now()
	svc.SetClock(func() time.Time { return frozen })

	h, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100, RefType: RefChatMessage,
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if !h.ExpiresAt.Equal(frozen.Add(DefaultHoldTTL)) {
		t.Fatalf("expires_at = %v, want %v", h.ExpiresAt, frozen.Add(DefaultHoldTTL))
	}
}

// ════════════════════════════════════════════════════════════
// Settle
// ════════════════════════════════════════════════════════════

func TestSettle_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	h, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 300, RefType: RefChatMessage, RefID: "msg-1",
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	settled, log, bal, err := svc.Settle(ctx, SettleArgs{
		HoldID: h.ID, ActualAmount: 120,
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if settled.Status != HoldStatusSettled {
		t.Fatalf("status = %s", settled.Status)
	}
	if settled.ActualAmount == nil || *settled.ActualAmount != 120 {
		t.Fatalf("actual = %v", settled.ActualAmount)
	}
	if bal.Total() != 880 {
		t.Fatalf("bal = %+v (want 880)", bal)
	}
	if log == nil || log.Delta != -120 {
		t.Fatalf("log = %+v", log)
	}
	if log.RefType != RefChatMessage || log.RefID != "msg-1" {
		t.Fatalf("log ref = (%s, %s)", log.RefType, log.RefID)
	}
}

func TestSettle_ZeroAmount_RefundsAll(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	h, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 300, RefType: RefChatMessage,
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	_, log, bal, err := svc.Settle(ctx, SettleArgs{HoldID: h.ID, ActualAmount: 0})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if log != nil {
		t.Fatalf("expected no log when actual=0, got %+v", log)
	}
	if bal.Total() != 1000 {
		t.Fatalf("bal = %+v (want 1000)", bal)
	}
}

func TestSettle_ExceedsHold(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	h, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100, RefType: RefChatMessage,
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	_, _, _, err = svc.Settle(ctx, SettleArgs{HoldID: h.ID, ActualAmount: 200})
	if err != ErrSettleExceedsHold {
		t.Fatalf("want ErrSettleExceedsHold, got %v", err)
	}
}

func TestSettle_AlreadySettled(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	h, _, _ := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100, RefType: RefChatMessage,
	})
	if _, _, _, err := svc.Settle(ctx, SettleArgs{HoldID: h.ID, ActualAmount: 50}); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	_, _, _, err := svc.Settle(ctx, SettleArgs{HoldID: h.ID, ActualAmount: 30})
	if err != ErrHoldNotActive {
		t.Fatalf("want ErrHoldNotActive, got %v", err)
	}
}

func TestSettle_HoldNotFound(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	_, _, _, err := svc.Settle(context.Background(), SettleArgs{
		HoldID: uuid.New(), ActualAmount: 50,
	})
	if err != ErrHoldNotFound {
		t.Fatalf("want ErrHoldNotFound, got %v", err)
	}
}

// ════════════════════════════════════════════════════════════
// Release
// ════════════════════════════════════════════════════════════

func TestRelease_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	h, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 300, RefType: RefChatMessage,
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	rel, bal, err := svc.Release(ctx, h.ID)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if rel.Status != HoldStatusReleased {
		t.Fatalf("status = %s", rel.Status)
	}
	if bal.Total() != 1000 {
		t.Fatalf("bal = %+v (want 1000 — fully refunded)", bal)
	}
}

func TestRelease_AlreadyReleased(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	h, _, _ := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 100, RefType: RefChatMessage,
	})
	if _, _, err := svc.Release(ctx, h.ID); err != nil {
		t.Fatalf("first release: %v", err)
	}
	_, _, err := svc.Release(ctx, h.ID)
	if err != ErrHoldNotActive {
		t.Fatalf("want ErrHoldNotActive, got %v", err)
	}
}

// ════════════════════════════════════════════════════════════
// Reaper
// ════════════════════════════════════════════════════════════

func TestReapExpired(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	// 注入一个 1ms TTL 的 hold, 立即过期
	frozen := time.Now()
	svc.SetClock(func() time.Time { return frozen })
	h, _, err := svc.Hold(ctx, HoldArgs{
		UserID: uid, MaxAmount: 200, RefType: RefChatMessage,
		TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	// 钟拨到 1 秒后, reaper 应该捞到这条
	svc.SetClock(func() time.Time { return frozen.Add(time.Second) })

	n, err := svc.ReapExpired(ctx, 500)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n < 1 {
		t.Fatalf("processed = %d (want ≥ 1)", n)
	}

	// 验证我们创建的 hold 状态 + 余额回填 (其他历史 hold 是否被处理不影响断言)
	got, err := svc.GetHold(ctx, h.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != HoldStatusExpired {
		t.Fatalf("status = %s", got.Status)
	}
	bal, _ := svc.GetBalance(ctx, uid)
	if bal.Total() != 1000 {
		t.Fatalf("bal = %+v (want 1000 — refunded by reaper)", bal)
	}
}

// TestReapExpired_NoOpWhenNothingExpired — 空跑应不报错. 不断言 n=0
// 因为多个测试并行时可能被别的测试触发的 hold 被同时清理.
func TestReapExpired_NoOpWhenNothingExpired(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	if _, err := svc.ReapExpired(context.Background(), 100); err != nil {
		t.Fatalf("reap: %v", err)
	}
}

// ════════════════════════════════════════════════════════════
// 并发 — N 个 hold 累计不超余额
// ════════════════════════════════════════════════════════════

func TestHold_Concurrent_TotalNotExceedsBalance(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetAll(t, pool, uid)
	ctx := context.Background()

	// 1000 余额, 10 个 goroutine 同时各 hold 200, 只有 5 个该成功
	if _, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 1000, Kind: KindPermanent, Source: SourceRecharge,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	const N = 10
	var wg sync.WaitGroup
	successes := make([]bool, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := svc.Hold(ctx, HoldArgs{
				UserID: uid, MaxAmount: 200, RefType: RefChatMessage,
				IdempotencyKey: "k-" + uuid.New().String(),
			})
			successes[i] = err == nil
		}(i)
	}
	wg.Wait()

	count := 0
	for _, ok := range successes {
		if ok {
			count++
		}
	}
	if count != 5 {
		t.Fatalf("successful holds = %d (want 5)", count)
	}
	bal, _ := svc.GetBalance(ctx, uid)
	if bal.Total() != 0 {
		t.Fatalf("bal = %+v (want 0)", bal)
	}
}
