// W6-7 coupons — 12 PG 集成测试.

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ─── helpers ──────────────────────────────────────

func newCouponRepo(t *testing.T) *CouponRepo {
	t.Helper()
	return NewCouponRepo(plansDB(t))
}

func cleanupCoupon(t *testing.T, r *CouponRepo, code string) {
	t.Helper()
	_, _ = r.pool.Exec(context.Background(),
		`DELETE FROM billing.coupons WHERE code=$1`, code)
}

func cleanupRedemptionsForUser(t *testing.T, r *CouponRepo, uid uuid.UUID) {
	t.Helper()
	_, _ = r.pool.Exec(context.Background(),
		`DELETE FROM billing.coupon_redemptions WHERE user_id=$1`, uid)
}

func seedCoupon(t *testing.T, r *CouponRepo, code, kind string, value int64, opts ...func(*Coupon)) *Coupon {
	t.Helper()
	c := Coupon{
		Code: code, Kind: CouponKind(kind), Value: value,
		Status: CouponStatusActive, OncePerUser: true,
	}
	for _, o := range opts {
		o(&c)
	}
	row := r.pool.QueryRow(context.Background(), `
		INSERT INTO billing.coupons
		  (code, kind, value, max_amount_cents, plan_codes, currency, once_per_user,
		   max_total_uses, valid_from, valid_until, status, description)
		VALUES ($1, $2, $3, $4, COALESCE($5, '{}'::text[]), $6, $7, $8,
		        COALESCE($9, now()), $10, $11, $12)
		RETURNING id, valid_from, created_at
	`, c.Code, string(c.Kind), c.Value, c.MaxAmountCents, c.PlanCodes, c.Currency,
		c.OncePerUser, c.MaxTotalUses, nullableTime(c.ValidFrom), c.ValidUntil,
		string(c.Status), c.Description)
	if err := row.Scan(&c.ID, &c.ValidFrom, &c.CreatedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { cleanupCoupon(t, r, code) })
	return &c
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func strPtr(s string) *string { return &s }

// ─── Validate ─────────────────────────────────────

// 1. amount_off — happy
func TestCoupon_AmountOff_Happy(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_OFF10_"+uuid.NewString()[:6], "amount_off", 1000,
		func(c *Coupon) { c.Currency = strPtr("CNY") })

	res, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(), AmountCents: 5000, Currency: "CNY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DiscountCents != 1000 {
		t.Fatalf("discount = %d want 1000", res.DiscountCents)
	}
}

// 2. amount_off — discount cap by amount (避免负数)
func TestCoupon_AmountOff_CapByAmount(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_BIG_"+uuid.NewString()[:6], "amount_off", 50000,
		func(c *Coupon) { c.Currency = strPtr("CNY") })
	res, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(), AmountCents: 1000, Currency: "CNY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DiscountCents != 1000 {
		t.Fatalf("discount = %d want capped to 1000", res.DiscountCents)
	}
}

// 3. percent_off — happy + cap
func TestCoupon_PercentOff_WithCap(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_P20_"+uuid.NewString()[:6], "percent_off", 20,
		func(c *Coupon) { c.MaxAmountCents = 500 })
	// 20% of 10000 = 2000; cap 500
	res, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(), AmountCents: 10000, Currency: "CNY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DiscountCents != 500 {
		t.Fatalf("discount = %d want 500 (capped)", res.DiscountCents)
	}
}

// 4. credits_grant — credits amount
func TestCoupon_CreditsGrant(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_GIFT_"+uuid.NewString()[:6], "credits_grant", 500)
	res, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CreditsAmount != 500 {
		t.Fatalf("credits = %d", res.CreditsAmount)
	}
	if res.DiscountCents != 0 {
		t.Fatalf("credits_grant should not return discount")
	}
}

// 5. trial_extend — days
func TestCoupon_TrialExtend(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_TR14_"+uuid.NewString()[:6], "trial_extend", 14)
	res, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TrialExtraDays != 14 {
		t.Fatalf("days = %d", res.TrialExtraDays)
	}
}

// 6. inactive coupon → ErrCouponInactive
func TestCoupon_Inactive(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_PAUSED_"+uuid.NewString()[:6], "amount_off", 100,
		func(c *Coupon) { c.Status = CouponStatusPaused; c.Currency = strPtr("CNY") })
	_, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(), AmountCents: 1000, Currency: "CNY",
	})
	if err != ErrCouponInactive {
		t.Fatalf("got %v want ErrCouponInactive", err)
	}
}

// 7. expired (valid_until 在过去)
func TestCoupon_Expired(t *testing.T) {
	r := newCouponRepo(t)
	past := time.Now().Add(-1 * time.Hour)
	c := seedCoupon(t, r, "TEST_EXP_"+uuid.NewString()[:6], "amount_off", 100,
		func(c *Coupon) { c.ValidUntil = &past; c.Currency = strPtr("CNY") })
	_, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(), AmountCents: 1000, Currency: "CNY",
	})
	if err != ErrCouponExpired {
		t.Fatalf("got %v want ErrCouponExpired", err)
	}
}

// 8. plan mismatch
func TestCoupon_PlanMismatch(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_PRO_"+uuid.NewString()[:6], "amount_off", 100,
		func(c *Coupon) { c.PlanCodes = []string{"pro"}; c.Currency = strPtr("CNY") })
	_, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(), PlanCode: "team",
		AmountCents: 1000, Currency: "CNY",
	})
	if err != ErrCouponPlanMismatch {
		t.Fatalf("got %v want ErrCouponPlanMismatch", err)
	}
}

// 9. currency mismatch (amount_off only)
func TestCoupon_CurrencyMismatch(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_CNY_"+uuid.NewString()[:6], "amount_off", 100,
		func(c *Coupon) { c.Currency = strPtr("CNY") })
	_, err := r.Validate(context.Background(), ValidateArgs{
		Code: c.Code, UserID: uuid.New(), AmountCents: 1000, Currency: "USD",
	})
	if err != ErrCouponCurrencyMismatch {
		t.Fatalf("got %v want ErrCouponCurrencyMismatch", err)
	}
}

// ─── Redeem ─────────────────────────────────────

// 10. Redeem 落账成功
func TestCoupon_Redeem_Happy(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_RD_"+uuid.NewString()[:6], "credits_grant", 200)
	uid := uuid.New()
	defer cleanupRedemptionsForUser(t, r, uid)

	res, err := r.Redeem(context.Background(), RedeemArgs{
		Code: c.Code, UserID: uid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CreditsAmount != 200 {
		t.Fatalf("credits = %d", res.CreditsAmount)
	}
	if res.RedemptionID == uuid.Nil {
		t.Fatal("redemption id zero")
	}
}

// 11. Redeem 同 user 第二次 → ErrCouponAlreadyUsed
func TestCoupon_Redeem_AlreadyUsed(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_DUP_"+uuid.NewString()[:6], "credits_grant", 100)
	uid := uuid.New()
	defer cleanupRedemptionsForUser(t, r, uid)

	if _, err := r.Redeem(context.Background(), RedeemArgs{
		Code: c.Code, UserID: uid,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := r.Redeem(context.Background(), RedeemArgs{
		Code: c.Code, UserID: uid,
	})
	if err != ErrCouponAlreadyUsed {
		t.Fatalf("got %v want ErrCouponAlreadyUsed", err)
	}
}

// 12. AttachCreditLog 回填 log_id
func TestCoupon_AttachCreditLog(t *testing.T) {
	r := newCouponRepo(t)
	c := seedCoupon(t, r, "TEST_AT_"+uuid.NewString()[:6], "credits_grant", 100)
	uid := uuid.New()
	defer cleanupRedemptionsForUser(t, r, uid)

	res, err := r.Redeem(context.Background(), RedeemArgs{
		Code: c.Code, UserID: uid,
	})
	if err != nil {
		t.Fatal(err)
	}
	logID := uuid.New()
	if err := r.AttachCreditLog(context.Background(), res.RedemptionID, logID); err != nil {
		t.Fatal(err)
	}
	var got uuid.UUID
	_ = r.pool.QueryRow(context.Background(),
		`SELECT credit_log_id FROM billing.coupon_redemptions WHERE id=$1`, res.RedemptionID,
	).Scan(&got)
	if got != logID {
		t.Fatalf("credit_log_id = %s want %s", got, logID)
	}
}
