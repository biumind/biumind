// referrals.go — W6-8 邀请奖励.
//
// 工作流:
//   1. 邀请人调 GenerateInviteCode(userID) → 拿到长期邀请码 (一人一码)
//   2. 邀请人分享 invite link / 海报 / 微信卡片
//   3. 被邀请人注册时携 invite_code → Claim(invitee, code, deviceFP, ip)
//      建立 referrals 行 (status=pending)
//   4. 触发条件 (e.g. 被邀请人首次付费 / 7 天活跃) 满足时调
//      GrantRewards(referralID, rewards) → 给双方发积分 (status=rewarded)
//   5. 风控异常 / 退款时调 Revert(referralID) (status=reverted)
//
// 防刷三元组:
//   - 同 invitee_user_id 只能被邀请一次 (UNIQUE pair)
//   - 同 invitee_device_fp 多个不同 invitee 受邀 ≥ 阈值 → 拒
//   - 同 invitee_ip 24h 内 ≥ 阈值次受邀 → 拒
//
// 邀请码生成: 8 位 base32 (无歧义字符) 哈希 user_id, 不可逆但确定性 (一人一码).

package billing

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInviteCodeNotFound     = errors.New("referral: invite code not found")
	ErrSelfReferralForbidden  = errors.New("referral: cannot invite self")
	ErrInviteeAlreadyReferred = errors.New("referral: invitee already referred")
	ErrDeviceShared           = errors.New("referral: device shared by too many invitees")
	ErrIPRateLimited          = errors.New("referral: ip rate limited")
	ErrReferralNotPending     = errors.New("referral: status not pending")
)

// ─── invite code 编码 ──────────────────────────────

// inviteAlphabet — Crockford base32 风格, 去掉易混 (0/O, 1/I, U).
const inviteAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// inviteCodeForUser — 由 user_id 派生固定 8 字符邀请码.
//
// 哈希 sha256(user_id), 取前 5 字节 = 40 bits → 取 8 个 5-bit chunk → 32 base32 chars.
// 所以截 8 个 char.
func inviteCodeForUser(userID uuid.UUID) string {
	h := sha256.Sum256(userID[:])
	// 取前 5 字节, 拼成 40-bit unsigned, 再 8 个 5-bit chunk
	val := uint64(h[0])<<32 | uint64(h[1])<<24 | uint64(h[2])<<16 | uint64(h[3])<<8 | uint64(h[4])
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = inviteAlphabet[val&0x1f]
		val >>= 5
	}
	return string(out)
}

// encodeInviterFromCode — 反向: 给定 invite code 找到 inviter user_id.
// 因为 hash 不可逆, 我们用一张物化映射表 (referrals.invite_code) 反查.
// 这里实现是从 referrals 表 / 一个独立 invite_codes 表查; 简化起见用
// referrals 行的 invite_code 字段 (查到任意一行的 inviter_user_id 即可).
//
// 边缘情况: 还没人用过该码 → 走主键查询 (按 user_id 哈希) 不可行, 我们要求
// 邀请人先调 GenerateInviteCode 落表; 见 ReferralRepo.GenerateInviteCode.

// ─── Repo ────────────────────────────────────────

type ReferralRepo struct {
	pool              *pgxpool.Pool
	deviceMaxInvitees int
	ipMax24h          int
	ipWindow          time.Duration
	now               func() time.Time
}

func NewReferralRepo(pool *pgxpool.Pool) *ReferralRepo {
	return &ReferralRepo{
		pool:              pool,
		deviceMaxInvitees: 3,
		ipMax24h:          5,
		ipWindow:          24 * time.Hour,
		now:               time.Now,
	}
}

func (r *ReferralRepo) SetThresholds(deviceMax, ipMax int, ipWindow time.Duration) {
	if deviceMax > 0 {
		r.deviceMaxInvitees = deviceMax
	}
	if ipMax > 0 {
		r.ipMax24h = ipMax
	}
	if ipWindow > 0 {
		r.ipWindow = ipWindow
	}
}

func (r *ReferralRepo) SetClock(now func() time.Time) { r.now = now }

// GenerateInviteCode — 给 user 派生邀请码. 多次调用返同一个码 (确定性).
//
// 不写表, 仅返字符串. 实际"码 → user_id" 反查由 invite_codes 表或在
// Claim 时由调用方传 inviterUserID 直接关联. 简化路径: 客户端拿 code 后
// 本地携 (inviter_user_id, code) 一同提交给服务端 Claim.
func (r *ReferralRepo) GenerateInviteCode(userID uuid.UUID) string {
	return inviteCodeForUser(userID)
}

// VerifyInviteCode — 校验给定 code 是否就是 inviter 派生的码 (防伪造).
// 简单核验: hash(inviter) == code.
func (r *ReferralRepo) VerifyInviteCode(inviterUserID uuid.UUID, code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), inviteCodeForUser(inviterUserID))
}

// ─── Claim ────────────────────────────────────────

type ClaimArgs struct {
	InviterUserID uuid.UUID
	InviteeUserID uuid.UUID
	InviteCode    string
	DeviceFP      string
	IP            netip.Addr
}

func (r *ReferralRepo) Claim(ctx context.Context, a ClaimArgs) (uuid.UUID, error) {
	if a.InviterUserID == a.InviteeUserID {
		return uuid.Nil, ErrSelfReferralForbidden
	}
	if !r.VerifyInviteCode(a.InviterUserID, a.InviteCode) {
		return uuid.Nil, ErrInviteCodeNotFound
	}

	// 风控: device 多 invitee
	if a.DeviceFP != "" {
		var n int
		_ = r.pool.QueryRow(ctx, `
			SELECT COUNT(DISTINCT invitee_user_id) FROM billing.referrals
			WHERE invitee_device_fp = $1
		`, a.DeviceFP).Scan(&n)
		if n >= r.deviceMaxInvitees {
			return uuid.Nil, ErrDeviceShared
		}
	}
	if a.IP.IsValid() {
		var n int
		cutoff := r.now().Add(-r.ipWindow)
		_ = r.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM billing.referrals
			WHERE invitee_ip = $1 AND created_at >= $2
		`, a.IP.String(), cutoff).Scan(&n)
		if n >= r.ipMax24h {
			return uuid.Nil, ErrIPRateLimited
		}
	}

	// 落表
	var ip any
	if a.IP.IsValid() {
		ip = a.IP.String()
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO billing.referrals
		    (inviter_user_id, invitee_user_id, invite_code, invitee_device_fp, invitee_ip)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (inviter_user_id, invitee_user_id) DO NOTHING
		RETURNING id
	`, a.InviterUserID, a.InviteeUserID, strings.ToUpper(a.InviteCode), a.DeviceFP, ip)
	var id uuid.UUID
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrInviteeAlreadyReferred
		}
		return uuid.Nil, fmt.Errorf("referrals insert: %w", err)
	}
	return id, nil
}

// ─── GrantRewards / Revert ────────────────────

type RewardArgs struct {
	InviterCreditLogID *uuid.UUID
	InviteeCreditLogID *uuid.UUID
}

// GrantRewards — pending → rewarded. 业务层先调 credits.Grant 拿 log_id 后回填.
func (r *ReferralRepo) GrantRewards(ctx context.Context, referralID uuid.UUID, a RewardArgs) error {
	cmd, err := r.pool.Exec(ctx, `
		UPDATE billing.referrals
		SET status = 'rewarded',
		    inviter_credit_log_id = $1,
		    invitee_credit_log_id = $2,
		    rewarded_at = now()
		WHERE id = $3 AND status = 'pending'
	`, a.InviterCreditLogID, a.InviteeCreditLogID, referralID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrReferralNotPending
	}
	return nil
}

// Revert — rewarded → reverted (退款 / 风控判定异常时).
func (r *ReferralRepo) Revert(ctx context.Context, referralID uuid.UUID) error {
	cmd, err := r.pool.Exec(ctx, `
		UPDATE billing.referrals
		SET status = 'reverted'
		WHERE id = $1 AND status = 'rewarded'
	`, referralID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return errors.New("referral: status not rewarded")
	}
	return nil
}

// ─── Reads ────────────────────────────────────

type ReferralStats struct {
	Total      int
	Rewarded   int
	Pending    int
	Reverted   int
	InviteCode string
}

func (r *ReferralRepo) Stats(ctx context.Context, inviterUserID uuid.UUID) (*ReferralStats, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT status, COUNT(*) FROM billing.referrals
		WHERE inviter_user_id = $1
		GROUP BY status
	`, inviterUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s := &ReferralStats{InviteCode: r.GenerateInviteCode(inviterUserID)}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		s.Total += n
		switch status {
		case "rewarded":
			s.Rewarded = n
		case "pending":
			s.Pending = n
		case "reverted":
			s.Reverted = n
		}
	}
	// uuid encoding 占位 (linter)
	_ = binary.BigEndian
	return s, nil
}
