package credits

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMain 启用真 PG 集成测试. 默认连接本地开发 biu-postgres
// (deploy/docker-compose, exposed at localhost:5433).
//
// 设 CREDITS_TEST_DATABASE_URL 可以覆盖. 设为 "skip" 跳过整组测试.
func dbURL() string {
	if v := os.Getenv("CREDITS_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := dbURL()
	if url == "skip" {
		t.Skip("CREDITS_TEST_DATABASE_URL=skip")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect (set CREDITS_TEST_DATABASE_URL or run docker-compose): %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// resetUser 清空指定 user 在 4 张积分表中的数据（每个测试隔离用户避免互相干扰）。
func resetUser(t *testing.T, pool *pgxpool.Pool, uid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM identity.credit_logs WHERE user_id = $1`,
		`DELETE FROM identity.credit_packages WHERE user_id = $1`,
		`DELETE FROM identity.user_credits WHERE user_id = $1`,
	} {
		if _, err := pool.Exec(ctx, q, uid); err != nil {
			t.Fatalf("reset (%s): %v", q, err)
		}
	}
}

func newUser() uuid.UUID { return uuid.New() }

// ════════════════════════════════════════════════════════════
// Grant (入账)
// ════════════════════════════════════════════════════════════

func TestGrant_Permanent(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	pkg, bal, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 500, Kind: KindPermanent, Source: SourceRecharge,
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if pkg.Remaining != 500 || pkg.Kind != KindPermanent {
		t.Fatalf("pkg = %+v", pkg)
	}
	if bal.PermanentBalance != 500 || bal.TimeLimitedBalance != 0 {
		t.Fatalf("bal = %+v", bal)
	}
	if bal.Total() != 500 {
		t.Fatalf("total = %d", bal.Total())
	}
}

func TestGrant_TimeLimited_RequiresExpiresAt(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	_, _, err := svc.Grant(context.Background(), GrantArgs{
		UserID: uid, Amount: 100, Kind: KindTimeLimited, Source: SourcePlanGrant,
	})
	if err != ErrInvalidKindExpiresAt {
		t.Fatalf("want ErrInvalidKindExpiresAt, got %v", err)
	}
}

func TestGrant_Permanent_RejectsExpiresAt(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	exp := time.Now().Add(24 * time.Hour)
	_, _, err := svc.Grant(context.Background(), GrantArgs{
		UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge,
		ExpiresAt: &exp,
	})
	if err != ErrInvalidKindExpiresAt {
		t.Fatalf("want ErrInvalidKindExpiresAt, got %v", err)
	}
}

func TestGrant_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()
	key := "grant-key-1"

	pkg1, _, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("grant 1: %v", err)
	}

	pkg2, bal2, err := svc.Grant(ctx, GrantArgs{
		UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("grant 2: %v", err)
	}
	if pkg1.ID != pkg2.ID {
		t.Fatalf("idempotency broken: pkg1=%s pkg2=%s", pkg1.ID, pkg2.ID)
	}
	if bal2.PermanentBalance != 100 {
		t.Fatalf("balance double-counted: %d", bal2.PermanentBalance)
	}
}

// ════════════════════════════════════════════════════════════
// Consume (扣减) — 优先扣最早过期的时效包，最后扣永久
// ════════════════════════════════════════════════════════════

func TestConsume_TimeLimitedFirst(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	exp1 := time.Now().Add(24 * time.Hour)
	exp2 := time.Now().Add(48 * time.Hour)

	// 永久包 200
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 200, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}
	// 时效包 100，48h
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindTimeLimited, Source: SourcePlanGrant, ExpiresAt: &exp2}); err != nil {
		t.Fatal(err)
	}
	// 时效包 50，24h（应该最先扣）
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 50, Kind: KindTimeLimited, Source: SourcePlanGrant, ExpiresAt: &exp1}); err != nil {
		t.Fatal(err)
	}

	// 扣 60: 先扣 50 (exp1) + 10 (exp2) = 60. 永久不动.
	log, bal, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 60, RefType: RefAIGCTask, RefID: "task-1",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(log.ConsumeBreakdown) != 2 {
		t.Fatalf("breakdown len = %d (want 2)", len(log.ConsumeBreakdown))
	}
	if log.ConsumeBreakdown[0].Amount != 50 || log.ConsumeBreakdown[1].Amount != 10 {
		t.Fatalf("breakdown = %+v (want [50, 10])", log.ConsumeBreakdown)
	}
	if bal.PermanentBalance != 200 {
		t.Fatalf("permanent should not be touched: %d", bal.PermanentBalance)
	}
	if bal.TimeLimitedBalance != 90 {
		t.Fatalf("time_limited = %d (want 90 = 100-10)", bal.TimeLimitedBalance)
	}
}

func TestConsume_OverflowToPermanent(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	exp := time.Now().Add(24 * time.Hour)
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 30, Kind: KindTimeLimited, Source: SourcePlanGrant, ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}

	// 扣 50: 30 时效 + 20 永久
	log, bal, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 50, RefType: RefAIGCTask, RefID: "task-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(log.ConsumeBreakdown) != 2 {
		t.Fatalf("breakdown = %+v", log.ConsumeBreakdown)
	}
	if log.ConsumeBreakdown[0].Amount != 30 || log.ConsumeBreakdown[1].Amount != 20 {
		t.Fatalf("breakdown amounts = %+v", log.ConsumeBreakdown)
	}
	if bal.PermanentBalance != 80 || bal.TimeLimitedBalance != 0 {
		t.Fatalf("bal = %+v", bal)
	}
}

func TestConsume_InsufficientCredits(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 30, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 100, RefType: RefAIGCTask, RefID: "task-3",
	})
	if err != ErrInsufficientCredits {
		t.Fatalf("want ErrInsufficientCredits, got %v", err)
	}
	// 余额没动
	bal, _ := svc.GetBalance(ctx, uid)
	if bal.PermanentBalance != 30 {
		t.Fatalf("balance changed on insufficient: %d", bal.PermanentBalance)
	}
}

func TestConsume_InvalidAmount(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	for _, n := range []int64{0, -1, -100} {
		_, _, err := svc.Consume(context.Background(), ConsumeArgs{
			UserID: uid, Amount: n, RefType: RefAIGCTask,
		})
		if err != ErrInvalidAmount {
			t.Fatalf("amount=%d: want ErrInvalidAmount, got %v", n, err)
		}
	}
}

func TestConsume_ExpiredTimeLimitedSkipped(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	// 已过期的时效包：写一个未来时间，然后用 SetClock 让 svc 看到的"现在"在它之后
	pastExpires := time.Now().Add(time.Hour)
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 50, Kind: KindTimeLimited, Source: SourceReward, ExpiresAt: &pastExpires}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}

	// 把"现在"调到时效过期之后
	svc.SetClock(func() time.Time { return pastExpires.Add(time.Minute) })

	log, bal, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 30, RefType: RefAIGCTask, RefID: "task-4",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 全部从永久扣（时效已过期，被跳过）
	if len(log.ConsumeBreakdown) != 1 {
		t.Fatalf("breakdown = %+v (want 1 entry from permanent)", log.ConsumeBreakdown)
	}
	if log.ConsumeBreakdown[0].Amount != 30 {
		t.Fatalf("amount = %d", log.ConsumeBreakdown[0].Amount)
	}
	if bal.PermanentBalance != 70 {
		t.Fatalf("permanent = %d (want 70)", bal.PermanentBalance)
	}
}

func TestConsume_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()
	key := "consume-key-task-1"

	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 200, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}
	log1, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 40, RefType: RefAIGCTask, RefID: "task-1", IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	log2, bal2, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 40, RefType: RefAIGCTask, RefID: "task-1", IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if log1.ID != log2.ID {
		t.Fatalf("idempotency broken: log1=%s log2=%s", log1.ID, log2.ID)
	}
	if bal2.PermanentBalance != 160 {
		t.Fatalf("balance double-deducted: %d (want 160)", bal2.PermanentBalance)
	}
}

// ════════════════════════════════════════════════════════════
// Concurrency: 100 个 goroutine 同时扣 1 不超扣
// ════════════════════════════════════════════════════════════

func TestConsume_Concurrent_NoOverdraft(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	const initial = 50
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: initial, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}

	const N = 100
	var wg sync.WaitGroup
	var success, insufficient, other int64
	var mu sync.Mutex

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := svc.Consume(ctx, ConsumeArgs{
				UserID:         uid,
				Amount:         1,
				RefType:        RefAIGCTask,
				RefID:          fmt.Sprintf("c-%d", i),
				IdempotencyKey: fmt.Sprintf("c-key-%d", i),
			})
			mu.Lock()
			defer mu.Unlock()
			switch err {
			case nil:
				success++
			case ErrInsufficientCredits:
				insufficient++
			default:
				other++
				t.Logf("unexpected: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if success != initial {
		t.Errorf("success = %d, want %d", success, initial)
	}
	if insufficient != N-initial {
		t.Errorf("insufficient = %d, want %d", insufficient, N-initial)
	}
	if other != 0 {
		t.Errorf("other errors = %d", other)
	}
	bal, _ := svc.GetBalance(ctx, uid)
	if bal.Total() != 0 {
		t.Errorf("balance = %d, want 0 (50 grant - 50 success)", bal.Total())
	}
}

// ════════════════════════════════════════════════════════════
// Refund — 反向遍历，原路径回填
// ════════════════════════════════════════════════════════════

func TestRefund_FullAmount_RestoresOriginalPackages(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	exp := time.Now().Add(24 * time.Hour)
	pkgTL, _, _ := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 30, Kind: KindTimeLimited, Source: SourcePlanGrant, ExpiresAt: &exp})
	pkgP, _, _ := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge})

	// 扣 50: 30 时效 + 20 永久
	consumeLog, _, err := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 50, RefType: RefAIGCTask, RefID: "task-r1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 全额退款
	refundLog, bal, err := svc.Refund(ctx, RefundArgs{
		OriginalLogID: consumeLog.ID, Amount: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if refundLog.Delta != 50 {
		t.Fatalf("refund delta = %d", refundLog.Delta)
	}
	// 反向：先退永久 20（最后扣的），再退时效 30
	if len(refundLog.ConsumeBreakdown) != 2 {
		t.Fatalf("refund breakdown = %+v", refundLog.ConsumeBreakdown)
	}
	if refundLog.ConsumeBreakdown[0].PackageID != pkgP.ID || refundLog.ConsumeBreakdown[0].Amount != 20 {
		t.Errorf("first restored should be permanent 20, got %+v", refundLog.ConsumeBreakdown[0])
	}
	if refundLog.ConsumeBreakdown[1].PackageID != pkgTL.ID || refundLog.ConsumeBreakdown[1].Amount != 30 {
		t.Errorf("second restored should be time_limited 30, got %+v", refundLog.ConsumeBreakdown[1])
	}
	if bal.PermanentBalance != 100 || bal.TimeLimitedBalance != 30 {
		t.Errorf("bal after refund = %+v (want perm=100 tl=30)", bal)
	}
}

func TestRefund_PartialAmount(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}
	consumeLog, _, _ := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 60, RefType: RefAIGCTask, RefID: "task-pr",
	})

	// 部分退 25
	_, _, err := svc.Refund(ctx, RefundArgs{OriginalLogID: consumeLog.ID, Amount: 25})
	if err != nil {
		t.Fatal(err)
	}
	bal, _ := svc.GetBalance(ctx, uid)
	if bal.PermanentBalance != 65 { // 100 - 60 + 25
		t.Errorf("bal = %d (want 65)", bal.PermanentBalance)
	}

	// 再退 25
	_, _, err = svc.Refund(ctx, RefundArgs{OriginalLogID: consumeLog.ID, Amount: 25})
	if err != nil {
		t.Fatal(err)
	}
	bal, _ = svc.GetBalance(ctx, uid)
	if bal.PermanentBalance != 90 {
		t.Errorf("bal = %d (want 90)", bal.PermanentBalance)
	}

	// 再退 20 → 超额（已退 50，原扣 60，最多还能退 10）
	_, _, err = svc.Refund(ctx, RefundArgs{OriginalLogID: consumeLog.ID, Amount: 20})
	if err != ErrAmountExceedsOriginal {
		t.Errorf("want ErrAmountExceedsOriginal, got %v", err)
	}

	// 退最后 10
	_, _, err = svc.Refund(ctx, RefundArgs{OriginalLogID: consumeLog.ID, Amount: 10})
	if err != nil {
		t.Fatal(err)
	}
	bal, _ = svc.GetBalance(ctx, uid)
	if bal.PermanentBalance != 100 {
		t.Errorf("bal = %d (want 100)", bal.PermanentBalance)
	}
}

func TestRefund_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()
	key := "refund-key-1"

	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}
	consumeLog, _, _ := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 40, RefType: RefAIGCTask, RefID: "task-rid",
	})

	r1, _, err := svc.Refund(ctx, RefundArgs{OriginalLogID: consumeLog.ID, Amount: 40, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	r2, bal, err := svc.Refund(ctx, RefundArgs{OriginalLogID: consumeLog.ID, Amount: 40, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID != r2.ID {
		t.Errorf("idempotency broken: r1=%s r2=%s", r1.ID, r2.ID)
	}
	if bal.PermanentBalance != 100 {
		t.Errorf("balance double-refunded: %d", bal.PermanentBalance)
	}
}

func TestRefund_LogNotFound(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	resetUser(t, pool, newUser())
	_, _, err := svc.Refund(context.Background(), RefundArgs{
		OriginalLogID: uuid.New(), Amount: 10,
	})
	if err != ErrLogNotFound {
		t.Errorf("want ErrLogNotFound, got %v", err)
	}
}

func TestRefund_LogIsNotConsumption(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	// 创建一笔入账 log（delta > 0）
	_, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge})
	if err != nil {
		t.Fatal(err)
	}
	logs, _ := svc.ListLogs(ctx, uid, "", nil, 10, 0)
	grantLogID := logs[0].ID

	_, _, err = svc.Refund(ctx, RefundArgs{OriginalLogID: grantLogID, Amount: 10})
	if err != ErrLogIsNotConsumption {
		t.Errorf("want ErrLogIsNotConsumption, got %v", err)
	}
}

func TestRefund_AllPackagesExpired(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	exp := time.Now().Add(time.Hour)
	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 50, Kind: KindTimeLimited, Source: SourceReward, ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	consumeLog, _, _ := svc.Consume(ctx, ConsumeArgs{
		UserID: uid, Amount: 50, RefType: RefAIGCTask, RefID: "task-exp",
	})
	// 把 svc 的"现在"推到时效过期之后
	svc.SetClock(func() time.Time { return exp.Add(time.Minute) })

	_, _, err := svc.Refund(ctx, RefundArgs{OriginalLogID: consumeLog.ID, Amount: 50})
	if err != ErrAllPackagesExpired {
		t.Errorf("want ErrAllPackagesExpired, got %v", err)
	}
}

// ════════════════════════════════════════════════════════════
// GetBalance / ListPackages / ListLogs
// ════════════════════════════════════════════════════════════

func TestGetBalance_NewUser(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser() // 全新 user，无任何 grant
	bal, err := svc.GetBalance(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if bal.PermanentBalance != 0 || bal.TimeLimitedBalance != 0 {
		t.Errorf("new user should have zero balance: %+v", bal)
	}
}

func TestListLogs_Order(t *testing.T) {
	pool := newTestPool(t)
	svc := New(pool)
	uid := newUser()
	resetUser(t, pool, uid)
	ctx := context.Background()

	if _, _, err := svc.Grant(ctx, GrantArgs{UserID: uid, Amount: 100, Kind: KindPermanent, Source: SourceRecharge}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Consume(ctx, ConsumeArgs{UserID: uid, Amount: 10, RefType: RefAIGCTask, RefID: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Consume(ctx, ConsumeArgs{UserID: uid, Amount: 5, RefType: RefAIGCTask, RefID: "b"}); err != nil {
		t.Fatal(err)
	}

	logs, err := svc.ListLogs(ctx, uid, "", nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("logs len = %d (want 3)", len(logs))
	}
	// 倒序：最新的在前（consume "b" → consume "a" → grant）
	if logs[0].Delta != -5 || logs[1].Delta != -10 || logs[2].Delta != 100 {
		t.Errorf("order wrong: deltas = %d, %d, %d", logs[0].Delta, logs[1].Delta, logs[2].Delta)
	}
}
