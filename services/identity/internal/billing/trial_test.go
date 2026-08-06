// W5-8 trial 防刷 — 5 cases.
//
// 走真 PG (复用 plansDB).

package billing

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
)

// resetTrials 清掉测试 user / device 的所有 trial_attempts.
func resetTrials(t *testing.T, c *TrialChecker, deviceFP string) {
	t.Helper()
	if deviceFP != "" {
		_, _ = c.pool.Exec(context.Background(),
			`DELETE FROM billing.trial_attempts WHERE device_fp=$1`, deviceFP)
	}
}

// 1. 干净三元组 → eligible.
func TestTrial_Clean_Eligible(t *testing.T) {
	pool := plansDB(t)
	c := NewTrialChecker(pool)

	dev := "dev-clean-" + uuid.NewString()[:8]
	defer resetTrials(t, c, dev)

	uid := uuid.New()
	got := c.Check(context.Background(), uid, dev, netip.MustParseAddr("1.2.3.4"))
	if !got.Eligible {
		t.Fatalf("clean user should be eligible: %+v", got)
	}
}

// 2. 同 user 已 succeeded → not eligible.
func TestTrial_UserAlreadyTrialed(t *testing.T) {
	pool := plansDB(t)
	c := NewTrialChecker(pool)

	dev := "dev-user-" + uuid.NewString()[:8]
	defer resetTrials(t, c, dev)

	uid := uuid.New()
	if err := c.Record(context.Background(), uid, dev, netip.MustParseAddr("1.2.3.4"), true, ""); err != nil {
		t.Fatal(err)
	}
	got := c.Check(context.Background(), uid, dev, netip.MustParseAddr("9.9.9.9"))
	if got.Eligible || got.Reason != TrialReasonUserUsed {
		t.Fatalf("got %+v want UserUsed", got)
	}
}

// 3. device_fp 多用户超阈值 → not eligible.
func TestTrial_DeviceShared(t *testing.T) {
	pool := plansDB(t)
	c := NewTrialChecker(pool)
	c.SetThresholds(3, 100, 24*time.Hour) // device 上限 3 (含将要的)

	dev := "dev-shared-" + uuid.NewString()[:8]
	defer resetTrials(t, c, dev)

	// 3 个不同 user 已 succeeded
	for i := 0; i < 3; i++ {
		_ = c.Record(context.Background(), uuid.New(), dev,
			netip.MustParseAddr("10.0.0.1"), true, "")
	}
	// 第 4 个新 user 来 → 应被拒
	got := c.Check(context.Background(), uuid.New(), dev, netip.MustParseAddr("10.0.0.2"))
	if got.Eligible || got.Reason != TrialReasonDeviceShared {
		t.Fatalf("got %+v want DeviceShared", got)
	}
}

// 4. ip 24h 内 ≥ ipMaxAttempts → not eligible.
func TestTrial_IPRateLimit(t *testing.T) {
	pool := plansDB(t)
	c := NewTrialChecker(pool)
	c.SetThresholds(100, 5, 24*time.Hour) // ip 5 次

	ip := netip.MustParseAddr("203.0.113.42")
	dev := "dev-ip-" + uuid.NewString()[:8]
	defer resetTrials(t, c, dev)

	// 5 次申请 (允许 succeeded 或 failed, 都计数)
	for i := 0; i < 5; i++ {
		_ = c.Record(context.Background(), uuid.New(), dev, ip, false, "test_seed")
	}
	got := c.Check(context.Background(), uuid.New(), "different-dev", ip)
	if got.Eligible || got.Reason != TrialReasonIPRateLimited {
		t.Fatalf("got %+v want IPRateLimited", got)
	}
}

// 5. 缺 device_fp / ip → 仅按 user 轴判定, eligible.
func TestTrial_MissingFields_StillChecksUser(t *testing.T) {
	pool := plansDB(t)
	c := NewTrialChecker(pool)

	uid := uuid.New()
	// 没 device 没 ip — 全新 user 应 eligible
	got := c.Check(context.Background(), uid, "", netip.Addr{})
	if !got.Eligible {
		t.Fatalf("missing fields + clean user should be eligible: %+v", got)
	}

	// 标记 user 已用 → 即使三元组缺仍拒
	_ = c.Record(context.Background(), uid, "", netip.Addr{}, true, "")
	got2 := c.Check(context.Background(), uid, "", netip.Addr{})
	if got2.Eligible {
		t.Fatalf("user already trialed should be rejected even without device/ip")
	}
}
