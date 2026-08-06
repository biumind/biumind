// proration.go — W5-6 升降级按比例计费.
//
// 核心算法:
//
//	ratio = remaining_seconds / total_seconds        // 当前周期剩余时长占比
//	unused_refund_cents     = round(old_price * ratio)   // 旧 plan 未使用的钱 (信用退回)
//	new_prorate_charge_cents = round(new_price * ratio)  // 新 plan 本周期补差
//	net_charge_cents        = new_prorate_charge - unused_refund
//
// 升级 (net > 0): 立即扣 net, plan 切换立即生效.
// 降级 (net < 0 或 sort_order 下降): 当前周期保留旧 plan, period_end 时换.
//   降级流程不退款 (避免薅羊毛), 旧 plan 服务到期满.
//
// 边界:
//   now <  period_start → ratio=1 (整个周期都是新的)
//   now >= period_end   → ratio=0 (周期内已结束, no proration)
//   period_end <= period_start → 异常输入, 返 ErrInvalidPeriod
//
// 单位: 全部 cents (int64), 避免浮点累计误差. 中间 ratio 用 float64
// 精度足够 (周期 30 天 = 2.6e6 秒, ratio 7 位小数, 精度损失 << 1 cent).

package billing

import (
	"errors"
	"math"
	"time"
)

var (
	ErrInvalidPeriod = errors.New("proration: period_end must be after period_start")
	ErrNonNegative   = errors.New("proration: prices must be >= 0")
)

// ProrationArgs — 计算输入. 价格按相同周期长度归一化 (e.g. 月费 vs 月费;
// 跨年/月对比时调用方先归一化).
type ProrationArgs struct {
	OldPriceCents int64     // 旧 plan 该周期内总价 (cents)
	NewPriceCents int64     // 新 plan 同周期总价 (cents)
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Now           time.Time // 升级发生时刻
}

// Proration — 计算结果. 三个值满足 NetCharge = NewProrateCharge - UnusedRefund.
type Proration struct {
	UnusedRefundCents     int64 // 旧 plan 剩余周期金额, 作为信用扣减
	NewProrateChargeCents int64 // 新 plan 本周期补差
	NetChargeCents        int64 // 净支付 (升级 > 0; 同价 = 0; 降级 < 0)
	RemainingRatio        float64
}

// ComputeProration — 纯函数, 不依赖 DB.
func ComputeProration(a ProrationArgs) (Proration, error) {
	if a.OldPriceCents < 0 || a.NewPriceCents < 0 {
		return Proration{}, ErrNonNegative
	}
	if !a.PeriodEnd.After(a.PeriodStart) {
		return Proration{}, ErrInvalidPeriod
	}
	total := a.PeriodEnd.Sub(a.PeriodStart).Seconds()
	var remaining float64
	switch {
	case a.Now.Before(a.PeriodStart):
		remaining = total
	case !a.Now.Before(a.PeriodEnd):
		remaining = 0
	default:
		remaining = a.PeriodEnd.Sub(a.Now).Seconds()
	}
	ratio := remaining / total
	unused := int64(math.Round(float64(a.OldPriceCents) * ratio))
	newCharge := int64(math.Round(float64(a.NewPriceCents) * ratio))
	return Proration{
		UnusedRefundCents:     unused,
		NewProrateChargeCents: newCharge,
		NetChargeCents:        newCharge - unused,
		RemainingRatio:        ratio,
	}, nil
}

// IsUpgrade — sort_order 比较. plansRepo 给两个 plan, 返 new > old.
// 调用方提前查 plan.sort_order 即可.
func IsUpgrade(oldSortOrder, newSortOrder int) bool {
	return newSortOrder > oldSortOrder
}
