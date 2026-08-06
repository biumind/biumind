// coupons.go — W6-7 优惠券核心.
//
// 4 类券:
//   amount_off    — 立减 N 分
//   percent_off   — 立减 N% (cap by max_amount_cents)
//   credits_grant — 直接发 N 积分 (走 credits.Service.Grant, 时效包 30 天)
//   trial_extend  — 试用期 +N 天 (subscriptions.trial_end_at 后挪)
//
// 工作流:
//   Validate(code, userID, planCode, amountCents) — 不写 DB, 只校验 + 算 discount
//   Redeem(code, userID, ctx)                     — 走事务, 校验 + 落 redemption + 触发 credit/trial 副作用
//
// 设计来源: docs/BiuMind-Billing-Membership-Dev-Plan.md §7 (W6-7).

package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Errors ───────────────────────────────────────────

var (
	ErrCouponNotFound         = errors.New("coupon: code not found")
	ErrCouponInactive         = errors.New("coupon: status not active")
	ErrCouponExpired          = errors.New("coupon: outside valid window")
	ErrCouponAlreadyUsed      = errors.New("coupon: already redeemed by this user")
	ErrCouponPlanMismatch     = errors.New("coupon: not applicable to this plan")
	ErrCouponCurrencyMismatch = errors.New("coupon: currency mismatch")
	ErrCouponMaxUsesReached   = errors.New("coupon: max total uses reached")
)

// ─── Types ─────────────────────────────────────────────

type CouponKind string

const (
	CouponAmountOff    CouponKind = "amount_off"
	CouponPercentOff   CouponKind = "percent_off"
	CouponCreditsGrant CouponKind = "credits_grant"
	CouponTrialExtend  CouponKind = "trial_extend"
)

type CouponStatus string

const (
	CouponStatusActive   CouponStatus = "active"
	CouponStatusPaused   CouponStatus = "paused"
	CouponStatusArchived CouponStatus = "archived"
)

type Coupon struct {
	ID             uuid.UUID
	Code           string
	Kind           CouponKind
	Value          int64
	MaxAmountCents int64
	PlanCodes      []string
	Currency       *string
	OncePerUser    bool
	MaxTotalUses   int64
	ValidFrom      time.Time
	ValidUntil     *time.Time
	Status         CouponStatus
	Description    string
	CreatedAt      time.Time
}

// ValidateResult — Validate 返回的预计算结果. 调用方按此调起支付流程.
type ValidateResult struct {
	Coupon         *Coupon
	DiscountCents  int64 // amount_off / percent_off 时 > 0
	CreditsAmount  int64 // credits_grant 时 > 0
	TrialExtraDays int   // trial_extend 时 > 0
}

// CouponRepo — 业务层入口.
type CouponRepo struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewCouponRepo(pool *pgxpool.Pool) *CouponRepo {
	return &CouponRepo{pool: pool, now: time.Now}
}

func (r *CouponRepo) SetClock(now func() time.Time) { r.now = now }

// ─── Reads ────────────────────────────────────────────

// GetByCode — 按 code (不区分大小写) 查券. 不存在返 ErrCouponNotFound.
func (r *CouponRepo) GetByCode(ctx context.Context, code string) (*Coupon, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, code, kind, value, max_amount_cents, plan_codes, currency,
		       once_per_user, max_total_uses, valid_from, valid_until,
		       status, description, created_at
		FROM billing.coupons
		WHERE upper(code) = upper($1)
	`, code)
	c := Coupon{}
	var status, kind string
	if err := row.Scan(
		&c.ID, &c.Code, &kind, &c.Value, &c.MaxAmountCents, &c.PlanCodes,
		&c.Currency, &c.OncePerUser, &c.MaxTotalUses,
		&c.ValidFrom, &c.ValidUntil, &status, &c.Description, &c.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, err
	}
	c.Kind = CouponKind(kind)
	c.Status = CouponStatus(status)
	return &c, nil
}

// HasUserRedeemed — 检查 user 是否已兑换过这张券.
func (r *CouponRepo) HasUserRedeemed(ctx context.Context, couponID, userID uuid.UUID) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM billing.coupon_redemptions
		WHERE coupon_id = $1 AND user_id = $2
	`, couponID, userID).Scan(&n)
	return n > 0, err
}

// TotalRedemptions — 当前总使用数 (max_total_uses 比较用).
func (r *CouponRepo) TotalRedemptions(ctx context.Context, couponID uuid.UUID) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM billing.coupon_redemptions WHERE coupon_id = $1
	`, couponID).Scan(&n)
	return n, err
}

// ─── Validate ─────────────────────────────────────────

// ValidateArgs — Validate 入参.
type ValidateArgs struct {
	Code        string
	UserID      uuid.UUID
	PlanCode    string // 当前打算订阅 / 续费的 plan code
	AmountCents int64  // 当前 checkout 金额 (用于 percent_off / amount_off cap)
	Currency    string // 当前金额币种
}

// Validate — 不写 DB. 调用方按返回的 ValidateResult 调整 checkout 金额 /
// 触发 trial 延长. 真实落账需调 Redeem.
func (r *CouponRepo) Validate(ctx context.Context, a ValidateArgs) (*ValidateResult, error) {
	c, err := r.GetByCode(ctx, a.Code)
	if err != nil {
		return nil, err
	}
	if c.Status != CouponStatusActive {
		return nil, ErrCouponInactive
	}
	now := r.now()
	if now.Before(c.ValidFrom) {
		return nil, ErrCouponExpired
	}
	if c.ValidUntil != nil && !now.Before(*c.ValidUntil) {
		return nil, ErrCouponExpired
	}
	if len(c.PlanCodes) > 0 && a.PlanCode != "" && !contains(c.PlanCodes, a.PlanCode) {
		return nil, ErrCouponPlanMismatch
	}
	if c.OncePerUser {
		used, err := r.HasUserRedeemed(ctx, c.ID, a.UserID)
		if err != nil {
			return nil, err
		}
		if used {
			return nil, ErrCouponAlreadyUsed
		}
	}
	if c.MaxTotalUses > 0 {
		n, err := r.TotalRedemptions(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		if n >= c.MaxTotalUses {
			return nil, ErrCouponMaxUsesReached
		}
	}

	res := &ValidateResult{Coupon: c}
	switch c.Kind {
	case CouponAmountOff:
		if c.Currency != nil && a.Currency != "" && *c.Currency != a.Currency {
			return nil, ErrCouponCurrencyMismatch
		}
		discount := c.Value
		if a.AmountCents > 0 && discount > a.AmountCents {
			discount = a.AmountCents
		}
		res.DiscountCents = discount
	case CouponPercentOff:
		if a.AmountCents <= 0 {
			res.DiscountCents = 0
			break
		}
		discount := a.AmountCents * c.Value / 100
		if c.MaxAmountCents > 0 && discount > c.MaxAmountCents {
			discount = c.MaxAmountCents
		}
		res.DiscountCents = discount
	case CouponCreditsGrant:
		res.CreditsAmount = c.Value
	case CouponTrialExtend:
		res.TrialExtraDays = int(c.Value)
	}
	return res, nil
}

// ─── Redeem ───────────────────────────────────────────

// RedeemArgs — Redeem 入参.
type RedeemArgs struct {
	Code           string
	UserID         uuid.UUID
	PlanCode       string
	AmountCents    int64
	Currency       string
	PaymentOrderID *uuid.UUID
	SubscriptionID *uuid.UUID
}

// RedeemResult — 落账结果.
type RedeemResult struct {
	RedemptionID   uuid.UUID
	Coupon         *Coupon
	DiscountCents  int64
	CreditsAmount  int64
	TrialExtraDays int
}

// Redeem — 写 redemption 行. credits / trial 副作用由调用方 (api 层 / Stripe webhook)
// 在事务外调对应服务接口完成 (避免跨 schema 长事务).
//
// 调用顺序建议:
//  1. r.Validate(...) 提前拿 ValidateResult 让客户端展示 discount.
//  2. 用户确认后, 调 r.Redeem 落 redemption.
//  3. 按 result.CreditsAmount / TrialExtraDays 调 credits.Grant /
//     subscriptions.ExtendTrial.
func (r *CouponRepo) Redeem(ctx context.Context, a RedeemArgs) (*RedeemResult, error) {
	v, err := r.Validate(ctx, ValidateArgs{
		Code: a.Code, UserID: a.UserID, PlanCode: a.PlanCode,
		AmountCents: a.AmountCents, Currency: a.Currency,
	})
	if err != nil {
		return nil, err
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO billing.coupon_redemptions
		    (coupon_id, user_id, payment_order_id, subscription_id,
		     discount_cents, extra_days)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (coupon_id, user_id) DO NOTHING
		RETURNING id
	`, v.Coupon.ID, a.UserID, a.PaymentOrderID, a.SubscriptionID,
		v.DiscountCents, v.TrialExtraDays)
	var rid uuid.UUID
	if err := row.Scan(&rid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// once_per_user 的并发冲突
			return nil, ErrCouponAlreadyUsed
		}
		return nil, fmt.Errorf("redemptions insert: %w", err)
	}
	return &RedeemResult{
		RedemptionID:   rid,
		Coupon:         v.Coupon,
		DiscountCents:  v.DiscountCents,
		CreditsAmount:  v.CreditsAmount,
		TrialExtraDays: v.TrialExtraDays,
	}, nil
}

// AttachCreditLog — credits_grant 兑换后, 调 credits.Grant 拿到 log_id 后回填.
func (r *CouponRepo) AttachCreditLog(ctx context.Context, redemptionID, creditLogID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE billing.coupon_redemptions SET credit_log_id = $1 WHERE id = $2
	`, creditLogID, redemptionID)
	return err
}

// ─── helpers ──────────────────────────────────────────

func contains(arr []string, s string) bool {
	target := strings.ToLower(s)
	for _, v := range arr {
		if strings.ToLower(v) == target {
			return true
		}
	}
	return false
}
