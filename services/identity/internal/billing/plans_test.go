// Tests for PlansRepo. DB-backed; skip when DATABASE_URL not set.
//
// 测试覆盖 (5 个):
//   1. List 返回 4 个 active plan, 按 sort_order 升序
//   2. Get(PlanPro) 命中, benefits 反序列化为 PlanLimits 字段值正确
//   3. Get(unknown) → ErrPlanNotFound
//   4. GetByID 用 uuid 命中
//   5. ResolveLimits fallback: DB 不可用 (nil pool) → DefaultLimits

package billing

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func plansDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG ping fail: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPlansList(t *testing.T) {
	r := NewPlansRepo(plansDB(t))
	plans, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(plans) < 4 {
		t.Fatalf("expected ≥4 active plans, got %d", len(plans))
	}
	wantCodes := []Plan{PlanFree, PlanPro, PlanTeam, "enterprise"}
	for i, p := range plans {
		if i < len(wantCodes) && p.Code != wantCodes[i] {
			t.Errorf("idx=%d code=%s, want %s", i, p.Code, wantCodes[i])
		}
		// sort_order 升序
		if i > 0 && plans[i-1].SortOrder > p.SortOrder {
			t.Errorf("sort_order regression at idx=%d", i)
		}
	}
}

func TestPlansGetPro(t *testing.T) {
	r := NewPlansRepo(plansDB(t))
	p, err := r.Get(context.Background(), PlanPro)
	if err != nil {
		t.Fatalf("Get pro: %v", err)
	}
	if p.Code != PlanPro {
		t.Fatalf("code = %q, want pro", p.Code)
	}
	// benefits 反序列化匹配 W2-2 seed 字段
	if p.Benefits.HubRPM != 600 {
		t.Errorf("pro hub_rpm = %d, want 600", p.Benefits.HubRPM)
	}
	if p.Benefits.HubTPM != 500_000 {
		t.Errorf("pro hub_tpm = %d, want 500_000", p.Benefits.HubTPM)
	}
	if p.Benefits.SandboxConcurrent != 5 {
		t.Errorf("pro sandbox_concurrent = %d, want 5", p.Benefits.SandboxConcurrent)
	}
	if p.Benefits.MemoryQuota != 5000 {
		t.Errorf("pro memory_quota = %d, want 5000", p.Benefits.MemoryQuota)
	}
	if p.MonthlyCredits != 10_000 {
		t.Errorf("pro monthly_credits = %d, want 10000", p.MonthlyCredits)
	}
	if p.PriceMonthly != 19 {
		t.Errorf("pro price_monthly = %f, want 19", p.PriceMonthly)
	}
}

func TestPlansGetUnknown(t *testing.T) {
	r := NewPlansRepo(plansDB(t))
	_, err := r.Get(context.Background(), Plan("nonexistent"))
	if !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("expected ErrPlanNotFound, got %v", err)
	}
}

func TestPlansGetByID(t *testing.T) {
	r := NewPlansRepo(plansDB(t))
	pro, err := r.Get(context.Background(), PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByID(context.Background(), pro.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Code != PlanPro {
		t.Errorf("GetByID code = %q, want pro", got.Code)
	}

	// 不存在的 uuid
	_, err = r.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("expected ErrPlanNotFound, got %v", err)
	}
}

func TestResolveLimitsFallback(t *testing.T) {
	// nil pool — DB 不可用路径, 必须 fallback 到 DefaultLimits
	var r *PlansRepo
	got := r.ResolveLimits(context.Background(), PlanPro)
	if got.HubRPM != DefaultLimits[PlanPro].HubRPM {
		t.Fatalf("nil pool fallback wrong: %+v", got)
	}

	// 未知 plan → DefaultLimits[Free]
	got2 := r.ResolveLimits(context.Background(), Plan("custom"))
	if got2.HubRPM != DefaultLimits[PlanFree].HubRPM {
		t.Fatalf("unknown fallback wrong: %+v", got2)
	}
}

func TestResolveLimitsDB(t *testing.T) {
	r := NewPlansRepo(plansDB(t))
	got := r.ResolveLimits(context.Background(), PlanTeam)
	if got.HubRPM != 6000 {
		t.Fatalf("team hub_rpm via DB = %d, want 6000", got.HubRPM)
	}
}
