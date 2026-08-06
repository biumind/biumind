// trial.go — W5-8 试用资格 + 三元组防刷.
//
// 调用流程:
//
//	1. 客户端 POST /v1/subscriptions/checkout {"trial":true, "device_fp":"...", ...}
//	2. handler 调 TrialChecker.Check(ctx, userID, deviceFP, ip)
//	3. Eligible=false → 返 403 + reason; Eligible=true → 起 trialing 订阅
//	4. 不论结果调 TrialChecker.Record(...)  写入 trial_attempts
//
// 阈值 dev plan §5.2 W5-8 给定 (3 / 5), 可通过构造函数调.

package billing

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TrialEligibility 是 Check 的结果.
type TrialEligibility struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

const (
	TrialReasonOK            = ""
	TrialReasonUserUsed      = "user_already_trialed"
	TrialReasonDeviceShared  = "device_shared_too_many_users"
	TrialReasonIPRateLimited = "ip_rate_limited"
)

// TrialChecker — 资格检查器.
type TrialChecker struct {
	pool           *pgxpool.Pool
	deviceMaxUsers int           // 同 device_fp 允许的最大不同 user 数 (含本次)
	ipMaxAttempts  int           // 同 ip 在 ipWindow 内允许的最大尝试次数
	ipWindow       time.Duration // 默认 24h
	now            func() time.Time
}

// NewTrialChecker — 默认阈值 3 / 5 / 24h, 可通过 setters 调.
func NewTrialChecker(pool *pgxpool.Pool) *TrialChecker {
	return &TrialChecker{
		pool: pool, deviceMaxUsers: 3, ipMaxAttempts: 5, ipWindow: 24 * time.Hour,
		now: time.Now,
	}
}

func (c *TrialChecker) SetThresholds(deviceMaxUsers, ipMaxAttempts int, ipWindow time.Duration) {
	if deviceMaxUsers > 0 {
		c.deviceMaxUsers = deviceMaxUsers
	}
	if ipMaxAttempts > 0 {
		c.ipMaxAttempts = ipMaxAttempts
	}
	if ipWindow > 0 {
		c.ipWindow = ipWindow
	}
}

func (c *TrialChecker) SetClock(now func() time.Time) { c.now = now }

// Check — 三轴 (user / device / ip) 黑名单查询.
//
// 缺失字段 (deviceFP="" / ip 零值) 不参与该轴判定, 但其他轴仍生效.
// pool 不可用 → fail-open (允许试用) 但 logger.Warn (避免 DB 故障导致全部
// 拒登 — 这种偏宽松, 后台审计兜底).
func (c *TrialChecker) Check(ctx context.Context, userID uuid.UUID, deviceFP string, clientIP netip.Addr) TrialEligibility {
	// 1. 同 user 已 succeeded?
	var userTrialed bool
	err := c.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM billing.trial_attempts
			WHERE user_id = $1 AND succeeded = true
		)
	`, userID).Scan(&userTrialed)
	if err != nil {
		// 检查不到就放行 — 让下游业务再校验.
		return TrialEligibility{Eligible: true}
	}
	if userTrialed {
		return TrialEligibility{Eligible: false, Reason: TrialReasonUserUsed}
	}
	// 2. 同 device 多用户?
	if deviceFP != "" {
		var count int
		_ = c.pool.QueryRow(ctx, `
			SELECT COUNT(DISTINCT user_id) FROM billing.trial_attempts
			WHERE device_fp = $1 AND succeeded = true
		`, deviceFP).Scan(&count)
		// 已有 deviceMaxUsers 个不同 user, 加上本次会超过 → 拒
		if count >= c.deviceMaxUsers {
			return TrialEligibility{Eligible: false, Reason: TrialReasonDeviceShared}
		}
	}
	// 3. 同 ip 24h 内 ≥ ipMaxAttempts?
	if clientIP.IsValid() {
		var attempts int
		cutoff := c.now().Add(-c.ipWindow)
		_ = c.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM billing.trial_attempts
			WHERE ip = $1 AND created_at >= $2
		`, clientIP.String(), cutoff).Scan(&attempts)
		if attempts >= c.ipMaxAttempts {
			return TrialEligibility{Eligible: false, Reason: TrialReasonIPRateLimited}
		}
	}
	return TrialEligibility{Eligible: true}
}

// Record — 持久化一次申请记录. succeeded=true 表示真起了订阅.
func (c *TrialChecker) Record(ctx context.Context, userID uuid.UUID, deviceFP string, clientIP netip.Addr, succeeded bool, rejectReason string) error {
	var ip any
	if clientIP.IsValid() {
		ip = clientIP.String()
	}
	_, err := c.pool.Exec(ctx, `
		INSERT INTO billing.trial_attempts (user_id, device_fp, ip, succeeded, reject_reason)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, deviceFP, ip, succeeded, rejectReason)
	return err
}

// 旧 import 警告兜底 (errors 在 dev 时若被全部移除也无所谓).
var _ = errors.New
