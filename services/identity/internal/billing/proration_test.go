// W5-6 proration 数字断言 — 6 cases.

package billing

import (
	"testing"
	"time"
)

// 测试用周期: 2026-07-01 ~ 2026-08-01 (31 天)
var (
	pStart = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	pEnd   = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
)

// 1. 周期开始 (now == period_start): ratio=1, 全额按比例.
func TestProration_PeriodStart_FullRatio(t *testing.T) {
	got, err := ComputeProration(ProrationArgs{
		OldPriceCents: 1900, NewPriceCents: 9900,
		PeriodStart: pStart, PeriodEnd: pEnd, Now: pStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.UnusedRefundCents != 1900 {
		t.Fatalf("unused = %d want 1900", got.UnusedRefundCents)
	}
	if got.NewProrateChargeCents != 9900 {
		t.Fatalf("new charge = %d want 9900", got.NewProrateChargeCents)
	}
	if got.NetChargeCents != 8000 {
		t.Fatalf("net = %d want 8000", got.NetChargeCents)
	}
}

// 2. 周期中点 (now = period 中点): ratio≈0.5, 全额一半.
func TestProration_Midpoint_HalfRatio(t *testing.T) {
	mid := pStart.Add(pEnd.Sub(pStart) / 2)
	got, err := ComputeProration(ProrationArgs{
		OldPriceCents: 2000, NewPriceCents: 10000,
		PeriodStart: pStart, PeriodEnd: pEnd, Now: mid,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 31 天对半切 ratio = 0.5; 1900 * 0.5 round
	if got.UnusedRefundCents != 1000 {
		t.Fatalf("unused = %d want 1000", got.UnusedRefundCents)
	}
	if got.NewProrateChargeCents != 5000 {
		t.Fatalf("charge = %d want 5000", got.NewProrateChargeCents)
	}
	if got.NetChargeCents != 4000 {
		t.Fatalf("net = %d", got.NetChargeCents)
	}
}

// 3. 周期已过 (now >= period_end): ratio=0, 不动钱.
func TestProration_AfterPeriodEnd_ZeroRatio(t *testing.T) {
	got, err := ComputeProration(ProrationArgs{
		OldPriceCents: 1900, NewPriceCents: 9900,
		PeriodStart: pStart, PeriodEnd: pEnd, Now: pEnd.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.UnusedRefundCents != 0 || got.NewProrateChargeCents != 0 || got.NetChargeCents != 0 {
		t.Fatalf("got %+v want all 0", got)
	}
}

// 4. 同价升级 (无差价): net=0.
func TestProration_SamePrice_NoCharge(t *testing.T) {
	mid := pStart.Add(pEnd.Sub(pStart) / 4)
	got, err := ComputeProration(ProrationArgs{
		OldPriceCents: 9900, NewPriceCents: 9900,
		PeriodStart: pStart, PeriodEnd: pEnd, Now: mid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NetChargeCents != 0 {
		t.Fatalf("same price net = %d want 0", got.NetChargeCents)
	}
}

// 5. 降级 (new < old): net 负, 表示用户多付了 (实际不退款, 服务到期).
func TestProration_Downgrade_NegativeNet(t *testing.T) {
	mid := pStart.Add(pEnd.Sub(pStart) / 2)
	got, err := ComputeProration(ProrationArgs{
		OldPriceCents: 9900, NewPriceCents: 1900,
		PeriodStart: pStart, PeriodEnd: pEnd, Now: mid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NetChargeCents >= 0 {
		t.Fatalf("downgrade net = %d should be negative", got.NetChargeCents)
	}
}

// 6. 边界 / 错误: period_end <= period_start → ErrInvalidPeriod.
func TestProration_InvalidPeriod(t *testing.T) {
	_, err := ComputeProration(ProrationArgs{
		OldPriceCents: 100, NewPriceCents: 200,
		PeriodStart: pEnd, PeriodEnd: pStart, Now: pStart,
	})
	if err != ErrInvalidPeriod {
		t.Fatalf("got %v want ErrInvalidPeriod", err)
	}
	_, err = ComputeProration(ProrationArgs{
		OldPriceCents: -1, NewPriceCents: 1,
		PeriodStart: pStart, PeriodEnd: pEnd, Now: pStart,
	})
	if err != ErrNonNegative {
		t.Fatalf("got %v want ErrNonNegative", err)
	}
}

// 额外: IsUpgrade.
func TestIsUpgrade(t *testing.T) {
	if !IsUpgrade(1, 2) {
		t.Fatal("1→2 should be upgrade")
	}
	if IsUpgrade(2, 1) {
		t.Fatal("2→1 not upgrade")
	}
	if IsUpgrade(1, 1) {
		t.Fatal("equal not upgrade")
	}
}
