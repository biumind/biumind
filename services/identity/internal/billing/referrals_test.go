// W6-8 referrals — 8 PG 集成测试.

package billing

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newReferralRepo(t *testing.T) *ReferralRepo {
	t.Helper()
	return NewReferralRepo(plansDB(t))
}

func cleanupReferrals(t *testing.T, r *ReferralRepo, uid uuid.UUID) {
	t.Helper()
	_, _ = r.pool.Exec(context.Background(),
		`DELETE FROM billing.referrals WHERE inviter_user_id=$1 OR invitee_user_id=$1`, uid)
}

// 1. invite code — 同 user 多次调 returns same code.
func TestReferral_InviteCode_Stable(t *testing.T) {
	r := newReferralRepo(t)
	uid := uuid.New()
	c1 := r.GenerateInviteCode(uid)
	c2 := r.GenerateInviteCode(uid)
	if c1 != c2 || len(c1) != 8 {
		t.Fatalf("c1=%s c2=%s", c1, c2)
	}
}

// 2. invite code — 不同 user 不同码 (高概率).
func TestReferral_InviteCode_Unique(t *testing.T) {
	r := newReferralRepo(t)
	c1 := r.GenerateInviteCode(uuid.New())
	c2 := r.GenerateInviteCode(uuid.New())
	if c1 == c2 {
		t.Fatalf("collision: both %s", c1)
	}
}

// 3. Verify code — 真 code 通过.
func TestReferral_Verify_Happy(t *testing.T) {
	r := newReferralRepo(t)
	uid := uuid.New()
	c := r.GenerateInviteCode(uid)
	if !r.VerifyInviteCode(uid, c) {
		t.Fatal("verify should pass")
	}
	if !r.VerifyInviteCode(uid, " "+c+" ") {
		t.Fatal("trim ws expected")
	}
}

// 4. Verify code — 假码拒.
func TestReferral_Verify_Bad(t *testing.T) {
	r := newReferralRepo(t)
	if r.VerifyInviteCode(uuid.New(), "FAKECODE") {
		t.Fatal("fake code should fail")
	}
}

// 5. Claim happy.
func TestReferral_Claim_Happy(t *testing.T) {
	r := newReferralRepo(t)
	inviter := uuid.New()
	invitee := uuid.New()
	defer cleanupReferrals(t, r, inviter)
	defer cleanupReferrals(t, r, invitee)

	code := r.GenerateInviteCode(inviter)
	id, err := r.Claim(context.Background(), ClaimArgs{
		InviterUserID: inviter, InviteeUserID: invitee,
		InviteCode: code, DeviceFP: "dev1",
		IP: netip.MustParseAddr("1.2.3.4"),
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("nil id")
	}
}

// 6. Self-referral 拒绝.
func TestReferral_SelfRefForbidden(t *testing.T) {
	r := newReferralRepo(t)
	uid := uuid.New()
	defer cleanupReferrals(t, r, uid)
	code := r.GenerateInviteCode(uid)
	_, err := r.Claim(context.Background(), ClaimArgs{
		InviterUserID: uid, InviteeUserID: uid, InviteCode: code,
	})
	if err != ErrSelfReferralForbidden {
		t.Fatalf("got %v want ErrSelfReferralForbidden", err)
	}
}

// 7. Device 防刷 → ErrDeviceShared.
func TestReferral_DeviceShared(t *testing.T) {
	r := newReferralRepo(t)
	r.SetThresholds(3, 100, 24*time.Hour)
	inviter := uuid.New()
	defer cleanupReferrals(t, r, inviter)
	code := r.GenerateInviteCode(inviter)

	dev := "shared-dev-" + uuid.NewString()[:8]
	for i := 0; i < 3; i++ {
		invitee := uuid.New()
		defer cleanupReferrals(t, r, invitee)
		_, err := r.Claim(context.Background(), ClaimArgs{
			InviterUserID: inviter, InviteeUserID: invitee, InviteCode: code,
			DeviceFP: dev, IP: netip.MustParseAddr("10.0.0.1"),
		})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	// 第 4 个被邀请人同 device → 拒
	invitee4 := uuid.New()
	defer cleanupReferrals(t, r, invitee4)
	_, err := r.Claim(context.Background(), ClaimArgs{
		InviterUserID: inviter, InviteeUserID: invitee4, InviteCode: code,
		DeviceFP: dev, IP: netip.MustParseAddr("10.0.0.99"),
	})
	if err != ErrDeviceShared {
		t.Fatalf("got %v want ErrDeviceShared", err)
	}
}

// 8. GrantRewards + Revert 状态机.
func TestReferral_GrantAndRevert(t *testing.T) {
	r := newReferralRepo(t)
	inviter := uuid.New()
	invitee := uuid.New()
	defer cleanupReferrals(t, r, inviter)
	defer cleanupReferrals(t, r, invitee)

	code := r.GenerateInviteCode(inviter)
	id, err := r.Claim(context.Background(), ClaimArgs{
		InviterUserID: inviter, InviteeUserID: invitee, InviteCode: code,
	})
	if err != nil {
		t.Fatal(err)
	}
	logA := uuid.New()
	logB := uuid.New()
	if err := r.GrantRewards(context.Background(), id, RewardArgs{
		InviterCreditLogID: &logA, InviteeCreditLogID: &logB,
	}); err != nil {
		t.Fatal(err)
	}
	// 第二次 grant 应失败 (status != pending)
	if err := r.GrantRewards(context.Background(), id, RewardArgs{}); err != ErrReferralNotPending {
		t.Fatalf("re-grant got %v", err)
	}
	if err := r.Revert(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// 验证状态
	var status string
	_ = r.pool.QueryRow(context.Background(),
		`SELECT status FROM billing.referrals WHERE id=$1`, id,
	).Scan(&status)
	if status != "reverted" {
		t.Fatalf("status = %s", status)
	}
}
