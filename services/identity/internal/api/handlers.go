// Package api exposes Identity HTTP endpoints.
//
//	POST /v1/auth/register             {email, password, display_name}
//	POST /v1/auth/verify-email         {email, code}
//	POST /v1/auth/resend-verification  {email}
//	POST /v1/auth/forgot-password      {email}
//	POST /v1/auth/reset-password       {email, code, new_password}
//	POST /v1/auth/login                {email, password, device_name}
//	POST /v1/auth/refresh              {refresh_token}
//	POST /v1/auth/logout               {refresh_token}
//	GET  /v1/identity/me               (Bearer)
//	POST /v1/identity/me/keys          (Bearer)  → 创建虚拟密钥
//	GET  /v1/identity/me/keys          (Bearer)
//	DELETE /v1/identity/me/keys/{id}   (Bearer)
//	GET    /v1/identity/me/sessions          (Bearer) → 列已登录设备
//	DELETE /v1/identity/me/sessions/others   (Bearer) → 踢其他设备
//	DELETE /v1/identity/me/sessions/{id}     (Bearer) → 撤单条 session
//	POST   /v1/auth/wechat/mp-login          {code, installation_id, device_name} → MiniApp 微信登录
//	GET    /v1/identity/me/providers         (Bearer) → 列出已绑定第三方
//	DELETE /v1/identity/me/providers/{id}    (Bearer) → 解绑 (至少留一种登录方式)
//	GET  /.well-known/jwks.json        (公开)
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/admin"
	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/biumind/biumind/services/identity/internal/byok"
	"github.com/biumind/biumind/services/identity/internal/credits"
	"github.com/biumind/biumind/services/identity/internal/mailer"
	"github.com/biumind/biumind/services/identity/internal/passwords"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/biumind/biumind/services/identity/internal/token"
	"github.com/google/uuid"
)

// verifyMaxAttempts — 单条 code 允许的错码次数; 超过作废, 用户必须 resend.
const verifyMaxAttempts = 5

// defaultRefreshAbsoluteTTL — RefreshAbsoluteTTL 零值时的兜底值 (1 年).
// 跟 BiuMind-Identity-Session-Design §五 推荐 prod/dev 一致.
const defaultRefreshAbsoluteTTL = 8760 * time.Hour

// defaultRefreshReuseGrace — RefreshReuseGrace 零值兜底 (10s). Auth0 Reuse
// Interval / Okta 30s grace 同款量级: 够客户端丢响应重试, 短到攻击者难以利用。
const defaultRefreshReuseGrace = 10 * time.Second

type Server struct {
	Store      *store.Store
	Signer     *bauth.Signer
	Verifier   *bauth.Verifier
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	// RefreshAbsoluteTTL — refresh_token 的绝对上限 (created_at + this).
	// rotation 不重置;防永久泄漏。详见 BiuMind-Identity-Session-Design §3.1。
	// 零值 fallback 到 8760h (1y),保持向前兼容。
	RefreshAbsoluteTTL time.Duration
	// RefreshReuseGrace — rotation 宽限窗口 (Auth0 Reuse Interval / Okta 30s
	// grace 同款): rotate 后窗口内重放老 refresh_token 不判 token_reuse, 沿
	// rotated_to 链找回 head 直接 200。零值 fallback 到 10s。
	RefreshReuseGrace time.Duration
	// RefreshGraceKey — grace replay 密文的 AES-256 key (DeriveGraceKey 输出,
	// 32B)。空时 grace replay 禁用: 宽限窗口内重放仍走 reuse detection。
	RefreshGraceKey []byte
	PasswordParams  passwords.Params
	Logger          *slog.Logger

	// RoleCache 注入后,login/register/me 响应附带 role + permissions.
	// nil 时退化为只返 id/email/display_name (单元测试场景).
	RoleCache *bauth.RoleCache

	// Audit 注入后, login/register 成功失败都写审计.
	// LoginThrottle 检测到爆破攻击时写 anomaly 审计 + 触发邮件告警.
	// nil 时全部 noop (单元测试场景).
	Audit         admin.Auditor
	LoginThrottle *LoginThrottle

	// VerificationMailer 注入后, 注册时发邮箱验证码; nil 时 code 仅写日志
	// (dev / 单测兜底). VerificationThrottle 限制 resend 频次.
	VerificationMailer   *VerificationMailer
	VerificationThrottle *VerificationThrottle

	// PasswordResetThrottle 限制 forgot-password 调用频次. 跟邮箱验证 throttle
	// 走独立 bucket — 滥用密码重置不应影响注册验证码额度.
	PasswordResetThrottle *VerificationThrottle

	// SystemConfig 注入后, brute-force 触发时读 alert.email 发告警邮件.
	// nil 不影响登录流程 — 只是告警邮件不会发, audit 仍写.
	SystemConfig *admin.SystemConfigStore

	// MiniApp 第三方登录 — 各平台独立配置, nil 时对应路由返 503.
	WechatMP   *WechatMPConfig
	AlipayMP   *AlipayMPConfig
	ToutiaoMP  *ToutiaoMPConfig
	BaiduMP    *BaiduMPConfig
	QQMP       *QQMPConfig
	KuaishouMP *KuaishouMPConfig
	JDMP       *JDMPConfig
	LarkMP     *LarkMPConfig

	// H5 OAuth 网页授权 — 配 FrontendBaseURL 才生效, 否则 /authorize 返 503.
	WechatWeb *WechatWebConfig

	// 积分系统 — 注入后, /v1/identity/me/credits/* 与 /v1/credits/recharge-options
	// 才会响应; 否则返 503. 见 internal/credits 包.
	Credits *credits.Service

	// BYOK — 用户自带 API Key. 注入后 /v1/identity/me/api-keys/* 生效.
	// BYOKValidator nil 时跳过上游 ping (新 Key 默认 valid 不主动验).
	BYOK          *byok.Store
	BYOKValidator *byok.Validator

	// W2-5 / W2-6 会员体系. 注入后 /v1/plans + /v1/subscriptions/me
	// 才会响应; nil 时返 503. 仓储见 internal/billing/{plans,subscriptions}.go.
	Plans         *billing.PlansRepo
	Subscriptions *billing.SubscriptionsRepo

	// W5-2/3/8 支付通道 + 试用防刷. 全 nil 时 checkout endpoint 返 503;
	// 注入后 (main.go 根据 system_config.payment.* 解析) 即生效.
	Wechat *billing.WechatClient
	Alipay *billing.AlipayClient
	Trial  *billing.TrialChecker

	// W6-7/8 优惠券 + 邀请奖励. nil 时对应 endpoint 返 503.
	Coupons   *billing.CouponRepo
	Referrals *billing.ReferralRepo

	// Clock — 测试注入. nil 时用 time.Now.
	Clock func() time.Time

	// AnnouncementNotifier — 公告发布时通知 Realtime 即时下发 (PERI-4). nil 时仅入库,
	// 客户端靠轮询兜底.
	AnnouncementNotifier AnnouncementNotifier
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /v1/auth/verify-email", s.handleVerifyEmail)
	mux.HandleFunc("POST /v1/auth/resend-verification", s.handleResendVerification)
	mux.HandleFunc("POST /v1/auth/forgot-password", s.handleForgotPassword)
	mux.HandleFunc("POST /v1/auth/reset-password", s.handleResetPassword)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /v1/auth/logout", s.handleLogout)

	// W2-5 / W2-6 会员体系
	mux.HandleFunc("GET /v1/plans", s.handleListPlans)
	mux.HandleFunc("GET /v1/subscriptions/me", s.requireAuth(s.handleMySubscription))
	mux.HandleFunc("POST /v1/subscriptions/checkout", s.requireAuth(s.handleCheckout))
	mux.HandleFunc("POST /v1/subscriptions/cancel", s.requireAuth(s.handleCancelSubscription))
	mux.HandleFunc("POST /v1/subscriptions/change_plan", s.requireAuth(s.handleChangePlan))
	mux.HandleFunc("POST /v1/subscriptions/resume", s.requireAuth(s.handleResumeSubscription))
	mux.HandleFunc("GET /v1/subscriptions/orders", s.requireAuth(s.handleListOrders))
	mux.HandleFunc("POST /v1/coupons/redeem", s.requireAuth(s.handleCouponRedeem))
	mux.HandleFunc("POST /v1/referrals/invite", s.requireAuth(s.handleReferralInvite))
	mux.HandleFunc("POST /v1/referrals/claim", s.requireAuth(s.handleReferralClaim))

	// 公告 / 通知 inbox (PERI-4) — 公共拉取 + 读态;后台 CRUD 走 requireAdmin。
	mux.HandleFunc("GET /v1/announcements", s.requireAuth(s.handleListAnnouncements))
	mux.HandleFunc("POST /v1/announcements/{id}/read", s.requireAuth(s.handleMarkAnnouncementRead))
	mux.HandleFunc("POST /v1/announcements/read-all", s.requireAuth(s.handleMarkAllAnnouncementsRead))
	mux.HandleFunc("GET /v1/admin/announcements", s.requireAdmin(s.handleAdminListAnnouncements))
	mux.HandleFunc("POST /v1/admin/announcements", s.requireAdmin(s.handleAdminCreateAnnouncement))
	mux.HandleFunc("PUT /v1/admin/announcements/{id}", s.requireAdmin(s.handleAdminUpdateAnnouncement))
	mux.HandleFunc("DELETE /v1/admin/announcements/{id}", s.requireAdmin(s.handleAdminDeleteAnnouncement))
	mux.HandleFunc("POST /v1/billing/wechat/callback", s.handleWechatCallback)
	mux.HandleFunc("POST /v1/billing/alipay/callback", s.handleAlipayCallback)
	mux.HandleFunc("GET /v1/identity/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("PATCH /v1/identity/me/profile", s.requireAuth(s.handleUpdateProfile))
	mux.HandleFunc("POST /v1/identity/me/keys", s.requireAuth(s.handleCreateKey))
	mux.HandleFunc("GET /v1/identity/me/keys", s.requireAuth(s.handleListKeys))
	mux.HandleFunc("DELETE /v1/identity/me/keys/{id}", s.requireAuth(s.handleRevokeKey))
	// 积分（双账户：永久 + 时效；mock 充值 v1，v2 接 Stripe / 微信 / 支付宝）
	mux.HandleFunc("GET /v1/identity/me/credits/balance", s.requireAuth(s.handleCreditsBalance))
	mux.HandleFunc("GET /v1/identity/me/credits/logs", s.requireAuth(s.handleCreditsLogs))
	mux.HandleFunc("GET /v1/identity/me/credits/packages", s.requireAuth(s.handleCreditsPackages))
	mux.HandleFunc("POST /v1/identity/me/credits/recharge", s.requireAuth(s.handleRecharge))
	mux.HandleFunc("GET /v1/credits/recharge-options", s.handleRechargeOptions)
	mux.HandleFunc("POST /v1/credits/checkout", s.requireAuth(s.handleCreditsCheckout))

	// 已登录设备 — 用户自助
	mux.HandleFunc("GET /v1/identity/me/sessions", s.requireAuth(s.handleListSessions))
	// 安全事件审计 (B2-c reuse banner / 安全活动页用)
	mux.HandleFunc("GET /v1/identity/me/security-events", s.requireAuth(s.handleListSecurityEvents))
	// 注意路由顺序: /others 必须在 /{id} 之前注册否则 net/http 会把 'others' 当 id 路径参数
	mux.HandleFunc("DELETE /v1/identity/me/sessions/others", s.requireAuth(s.handleRevokeOtherSessions))
	mux.HandleFunc("DELETE /v1/identity/me/sessions/{id}", s.requireAuth(s.handleRevokeSession))

	// MiniApp 9 端登录 — alipay / jd 留 RSA / SDK 桩, 其他平台已通.
	mux.HandleFunc("POST /v1/auth/wechat/mp-login", s.handleWechatMPLogin)
	mux.HandleFunc("POST /v1/auth/alipay/mp-login", s.handleAlipayMPLogin)
	mux.HandleFunc("POST /v1/auth/toutiao/mp-login", s.handleToutiaoMPLogin)
	mux.HandleFunc("POST /v1/auth/baidu/mp-login", s.handleBaiduMPLogin)
	mux.HandleFunc("POST /v1/auth/qq/mp-login", s.handleQQMPLogin)
	mux.HandleFunc("POST /v1/auth/kuaishou/mp-login", s.handleKuaishouMPLogin)
	mux.HandleFunc("POST /v1/auth/jd/mp-login", s.handleJDMPLogin)
	mux.HandleFunc("POST /v1/auth/lark/mp-login", s.handleLarkMPLogin)

	// me 页面 — 已绑定第三方账号 CRUD (绑定走具体平台 oauth 流, 这里只列出 + 解绑).
	mux.HandleFunc("GET /v1/identity/me/providers", s.requireAuth(s.handleListProviders))
	mux.HandleFunc("DELETE /v1/identity/me/providers/{id}", s.requireAuth(s.handleUnbindProvider))

	// MiniApp 订阅消息 — 用户授权后客户端把 template_id 上报这里.
	mux.HandleFunc("POST /v1/notify/mp-subscribe", s.requireAuth(s.handleSubscribeMP))
	mux.HandleFunc("GET /v1/notify/me/subscriptions", s.requireAuth(s.handleListSubscriptionsMP))
	// Inbox 占位 — 真实通知派发 worker 在 v2.0 路线图; 现在返空 inbox
	// 让客户端 UI 不 404. handler 在 mp_subscribe.go 末尾.
	mux.HandleFunc("GET /v1/notify/me/inbox", s.requireAuth(s.handleListInbox))

	// H5 OAuth 2.0 授权码流 (现支持微信网页授权; 支付宝 / GitHub 后续接入).
	mux.HandleFunc("GET /v1/auth/wechat/h5-authorize", s.handleWechatH5Authorize)
	mux.HandleFunc("GET /v1/auth/wechat/h5-callback", s.handleWechatH5Callback)

	// API Tokens (PAT) — long-lived programmatic access (P2-I-1).
	s.MountAPITokens(mux)

	// BYOK — 用户上传自己的上游 Key (W1).
	s.MountAPIKeys(mux)

	// Activity Feed — user-facing event stream (P2-I-3).
	s.MountActivity(mux)

	// OAuth 2.1 — biumind acts as Authorization Server for third-party
	// clients (Claude Desktop, Cursor, browser extensions).
	s.MountOAuthMetadata(mux)
	s.MountOAuthRegister(mux)
	s.MountOAuthAuthorize(mux)
	s.MountOAuthToken(mux)
}

// ─── register ───────────────────────────────────────────

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type tokensResp struct {
	AccessToken      string  `json:"access_token"`
	RefreshToken     string  `json:"refresh_token"`
	ExpiresInSeconds int64   `json:"expires_in_seconds"`
	User             userOut `json:"user"`
}

type userOut struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	// EmailVerified — 客户端据此决定是否跳"验证邮箱"页. 已验证返 true,
	// 否则 false. 即使响应内含 token (例如 verify-email 成功) 也以此为准.
	EmailVerified bool `json:"email_verified"`
	// role/plan/permissions: 仅 RoleCache 注入时填充. 旧 client 忽略这些
	// 字段无影响 (向后兼容); admin web 用它判断后台权限.
	Role        string   `json:"role,omitempty"`
	Plan        string   `json:"plan,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

// buildUserOut 把 store.User 投影到 userOut. 注入 RoleCache 时附带
// role/plan/permissions; nil 时退化为只填基础字段 (兼容老测试).
func buildUserOut(u *store.User, cache *bauth.RoleCache) userOut {
	out := userOut{
		ID: u.ID.String(), Email: u.Email, DisplayName: u.DisplayName,
		EmailVerified: u.EmailVerifiedAt != nil,
	}
	if cache != nil {
		out.Role = u.Role
		out.Plan = u.Plan
		out.Permissions = cache.PermissionsForRole(u.Role)
	}
	return out
}

// registerResp — 注册不再下发 token. 客户端拿到后跳"验证邮箱"页.
// VerificationRequired 总是 true (历史一致性: 即使 dev 兜底也要走 verify-email
// 端点完成验证, 防止前端有 if-else 分支漏测).
type registerResp struct {
	UserID               string `json:"user_id"`
	Email                string `json:"email"`
	VerificationRequired bool   `json:"verification_required"`
	// EmailSent — 真发了邮件返 true; dev 兜底 (SMTP 未配置) 返 false,
	// 客户端据此提示用户"开发模式: 请联系管理员获取验证码".
	EmailSent bool `json:"email_sent"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !validEmail(req.Email) {
		writeErr(w, http.StatusBadRequest, "invalid_email", "email looks invalid")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "weak_password", "min 8 chars")
		return
	}
	hash, err := passwords.Hash(req.Password, s.PasswordParams)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	ip := admin.ClientIP(r)
	ua := r.UserAgent()

	u, err := s.Store.CreateUser(r.Context(), email, hash, req.DisplayName)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			s.appendAudit(admin.AuditEvent{
				ActorEmail: email, ActorIP: ip, ActorUA: ua,
				Action: "auth.register.failed", Resource: "user",
				Success: false, ErrorCode: "already_exists",
			})
			writeErr(w, http.StatusConflict, "already_exists", "email taken")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.appendAudit(admin.AuditEvent{
		ActorID: u.ID.String(), ActorEmail: email, ActorRole: u.Role,
		ActorIP: ip, ActorUA: ua,
		Action: "auth.register.success", Resource: "user",
		Target: u.ID.String(), TargetType: "user",
		Success: true,
	})
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "identity: register",
			"user_id", u.ID, "email_domain", emailDomain(email), "ip", ip)
	}

	// 生成 + 写库 + 发送验证码. 失败时用户已建好账户 — 让其后续 resend
	// 仍能完成验证, 不回滚 (回滚意味着用户得换邮箱重注, 体验更差).
	sent, vErr := s.issueAndSendVerification(r.Context(), u, email, ip, ua)
	if vErr != nil {
		s.logErr("register: verification issue failed", vErr,
			"user_id", u.ID.String(), "email", email)
		// 不阻塞响应 — 用户可在前端按 resend 重发.
	}
	writeJSON(w, http.StatusOK, registerResp{
		UserID:               u.ID.String(),
		Email:                email,
		VerificationRequired: true,
		EmailSent:            sent,
	})
}

// issueAndSendVerification — 失效该用户当前未消费 code → 生成新 code →
// 写 email_verifications → 发邮件. 成功审计 auth.email.verification_sent.
//
// 调用方需注意: 即便 SendCode 返 (false, nil) (dev 兜底), 库里也已写好 code,
// 用户可走 verify-email. 仅当 err≠nil 时表示流程整体失败.
func (s *Server) issueAndSendVerification(
	ctx context.Context, u *store.User, email, ip, ua string,
) (sent bool, err error) {
	if err := s.Store.InvalidateActiveEmailVerifications(ctx, u.ID); err != nil {
		return false, err
	}
	code, codeHash, err := mailer.GenerateCode()
	if err != nil {
		return false, err
	}
	ttl := time.Duration(s.verificationTTLSeconds(ctx)) * time.Second
	if _, err := s.Store.CreateEmailVerification(ctx, u.ID, codeHash, ttl); err != nil {
		return false, err
	}
	if s.VerificationMailer != nil {
		var sendErr error
		sent, sendErr = s.VerificationMailer.SendCode(ctx, email, code, PurposeEmailVerification)
		if sendErr != nil {
			s.appendAudit(admin.AuditEvent{
				ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
				Action: "auth.email.verification_send_failed", Resource: "email_verification",
				Target: u.ID.String(), TargetType: "user",
				Success: false, ErrorCode: "smtp_error", ErrorMessage: sendErr.Error(),
			})
			return false, sendErr
		}
	} else if s.Logger != nil {
		s.Logger.Warn("EMAIL VERIFICATION CODE (no mailer injected)",
			"to", email, "code", code)
	}
	s.appendAudit(admin.AuditEvent{
		ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
		Action: "auth.email.verification_sent", Resource: "email_verification",
		Target: u.ID.String(), TargetType: "user",
		Success: true,
		Detail:  fmt.Sprintf("ttl=%s sent=%v", ttl, sent),
	})
	return sent, nil
}

func (s *Server) verificationTTLSeconds(ctx context.Context) int {
	if s.VerificationMailer == nil {
		return 600
	}
	return s.VerificationMailer.CodeTTLSeconds(ctx)
}

func (s *Server) logErr(msg string, err error, kv ...any) {
	if s.Logger == nil {
		return
	}
	s.Logger.Error(msg, append([]any{"err", err}, kv...)...)
}

// ─── login ──────────────────────────────────────────────

type loginReq struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
	// InstallationID 客户端首次启动生成的稳定 UUID. 同 (user, install) 上
	// 反复登录复用同一行 refresh_token, 不堆积. 空字符串走兼容路径每次新建.
	InstallationID string `json:"installation_id"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	ip := admin.ClientIP(r)
	ua := r.UserAgent()

	// 失败路径都走这里 — 写审计 + 喂 throttle, 触发阈值时再写 anomaly.
	recordFail := func(code, message string, userID string) {
		s.appendAudit(admin.AuditEvent{
			ActorID:      userID,
			ActorEmail:   email,
			ActorIP:      ip,
			ActorUA:      ua,
			Action:       "auth.login.failed",
			Resource:     "session",
			Success:      false,
			ErrorCode:    code,
			ErrorMessage: message,
		})
		if s.LoginThrottle != nil {
			s.LoginThrottle.recordFailure(email, ip, ua, s)
		}
	}

	u, err := s.Store.GetUserByEmail(r.Context(), email)
	if err != nil || u.PasswordHash == nil {
		recordFail("invalid_credentials", "user not found or no password set", "")
		// 同样的错误避免账户枚举
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "")
		return
	}
	ok, err := passwords.Verify(req.Password, *u.PasswordHash)
	if err != nil || !ok {
		recordFail("invalid_credentials", "password mismatch", u.ID.String())
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "")
		return
	}

	// 密码对了, 但邮箱没验证 — 不发 token. 客户端据 code 跳验证页.
	// 不算 login.failed (不喂 LoginThrottle), 因为这是产品流程而非攻击信号.
	if u.EmailVerifiedAt == nil {
		s.appendAudit(admin.AuditEvent{
			ActorID:      u.ID.String(),
			ActorEmail:   email,
			ActorRole:    u.Role,
			ActorIP:      ip,
			ActorUA:      ua,
			Action:       "auth.login.email_not_verified",
			Resource:     "session",
			Success:      false,
			ErrorCode:    "email_not_verified",
			ErrorMessage: "user has not completed email verification",
		})
		writeErr(w, http.StatusForbidden, "email_not_verified",
			"please verify your email before signing in")
		return
	}

	// 成功 — 写审计后续发 token. issueTokens 失败的 5xx 不写 audit (运维
	// 看 panic / log 排查, 不污染 audit 流).
	s.appendAudit(admin.AuditEvent{
		ActorID:    u.ID.String(),
		ActorEmail: email,
		ActorRole:  u.Role,
		ActorIP:    ip,
		ActorUA:    ua,
		Action:     "auth.login.success",
		Resource:   "session",
		Success:    true,
		Detail:     "device=" + req.DeviceName,
	})
	if s.LoginThrottle != nil {
		s.LoginThrottle.recordSuccess(email, ip)
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "identity: login",
			"user_id", u.ID, "email_domain", emailDomain(email),
			"role", u.Role, "device", req.DeviceName,
			"installation_id", req.InstallationID, "ip", ip)
	}
	s.issueTokensAndRespond(w, r.Context(), u, req.DeviceName, req.InstallationID)
}

// ─── verify email ───────────────────────────────────────

type verifyEmailReq struct {
	Email          string `json:"email"`
	Code           string `json:"code"`
	DeviceName     string `json:"device_name"`
	InstallationID string `json:"installation_id"`
}

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req verifyEmailReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	code := strings.TrimSpace(req.Code)
	ip := admin.ClientIP(r)
	ua := r.UserAgent()

	if email == "" || len(code) != 6 {
		writeErr(w, http.StatusBadRequest, "bad_request", "email and 6-digit code required")
		return
	}

	u, err := s.Store.GetUserByEmail(r.Context(), email)
	if err != nil {
		// 不暴露用户存在性 — 跟 login 一致返同样错误码.
		s.appendAudit(admin.AuditEvent{
			ActorEmail: email, ActorIP: ip, ActorUA: ua,
			Action: "auth.email.verification_failed", Resource: "email_verification",
			Success: false, ErrorCode: "user_not_found",
		})
		writeErr(w, http.StatusBadRequest, "invalid_code", "")
		return
	}

	// 已验证 → 直接走 issueTokens (幂等). 用户重提 verify 不应报错.
	if u.EmailVerifiedAt != nil {
		s.issueTokensAndRespond(w, r.Context(), u, req.DeviceName, req.InstallationID)
		return
	}

	v, err := s.Store.GetLatestEmailVerification(r.Context(), u.ID)
	if err != nil {
		s.appendAudit(admin.AuditEvent{
			ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
			Action: "auth.email.verification_failed", Resource: "email_verification",
			Target: u.ID.String(), TargetType: "user",
			Success: false, ErrorCode: "no_pending_code",
		})
		writeErr(w, http.StatusBadRequest, "no_pending_code",
			"no verification code on record; please request a new one")
		return
	}

	// 状态判断顺序: consumed > expired > attempts > 比对.
	if v.ConsumedAt != nil {
		writeErr(w, http.StatusBadRequest, "code_already_used",
			"code already consumed; request a new one")
		return
	}
	if time.Now().After(v.ExpiresAt) {
		writeErr(w, http.StatusBadRequest, "code_expired",
			"code expired; request a new one")
		return
	}
	if v.Attempts >= verifyMaxAttempts {
		writeErr(w, http.StatusBadRequest, "code_locked",
			"too many wrong attempts; request a new one")
		return
	}

	if !mailer.VerifyCode(code, v.CodeHash) {
		n, _ := s.Store.IncEmailVerificationAttempts(r.Context(), v.ID)
		s.appendAudit(admin.AuditEvent{
			ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
			Action: "auth.email.verification_failed", Resource: "email_verification",
			Target: u.ID.String(), TargetType: "user",
			Success: false, ErrorCode: "wrong_code",
			Detail: fmt.Sprintf("attempts=%d", n),
		})
		writeErr(w, http.StatusBadRequest, "invalid_code", "")
		return
	}

	// 成功. 标 user 已验证 + 消费 code. 顺序: 先消费 code 再标 user, 失败回滚
	// 都得用户重发 — 任一失败用户都还能 resend, 不死锁.
	if err := s.Store.ConsumeEmailVerification(r.Context(), v.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := s.Store.MarkUserEmailVerified(r.Context(), u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if s.VerificationThrottle != nil {
		s.VerificationThrottle.Reset(email)
	}
	s.appendAudit(admin.AuditEvent{
		ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
		Action: "auth.email.verified", Resource: "email_verification",
		Target: u.ID.String(), TargetType: "user",
		Success: true,
	})
	// 重新读 user (带最新 email_verified_at), 走 issueTokens 把 access+refresh 发给客户端.
	u2, err := s.Store.GetUserByID(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.issueTokensAndRespond(w, r.Context(), u2, req.DeviceName, req.InstallationID)
}

// ─── resend verification ────────────────────────────────

type resendReq struct {
	Email string `json:"email"`
}

func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	var req resendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	ip := admin.ClientIP(r)
	ua := r.UserAgent()
	if email == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "email required")
		return
	}

	// 节流先行 — 即便用户不存在也消耗节流额度, 防枚举攻击同时不暴露存在性.
	if s.VerificationThrottle != nil {
		if allow, retry := s.VerificationThrottle.AllowAndRecord(email); !allow {
			s.appendAudit(admin.AuditEvent{
				ActorEmail: email, ActorIP: ip, ActorUA: ua,
				Action: "auth.email.resend_throttled", Resource: "email_verification",
				Success: false, ErrorCode: "rate_limited",
				Detail: fmt.Sprintf("retry_after=%s", retry.Round(time.Second)),
			})
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retry.Seconds())+1))
			writeErr(w, http.StatusTooManyRequests, "rate_limited",
				fmt.Sprintf("try again in %s", retry.Round(time.Second)))
			return
		}
	}

	u, err := s.Store.GetUserByEmail(r.Context(), email)
	if err != nil {
		// 不暴露存在性: 返 200 假装发送, 实际 noop. 攻击者不能通过响应差异
		// 枚举注册过的邮箱.
		writeJSON(w, http.StatusOK, map[string]any{"email_sent": false})
		return
	}
	if u.EmailVerifiedAt != nil {
		// 已验证用户重发也 200, 同样不暴露状态. 客户端不应到这里 (UI 已发 token).
		writeJSON(w, http.StatusOK, map[string]any{"email_sent": false})
		return
	}

	sent, vErr := s.issueAndSendVerification(r.Context(), u, email, ip, ua)
	if vErr != nil {
		writeErr(w, http.StatusInternalServerError, "internal", vErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email_sent": sent})
}

// ─── forgot password ────────────────────────────────────

type forgotPasswordReq struct {
	Email string `json:"email"`
}

// handleForgotPassword 发重置密码 code 到邮箱. 跟 resend-verification 一样,
// 不暴露用户存在性 (无论是否找到该邮箱都返 200), 防账号枚举.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	ip := admin.ClientIP(r)
	ua := r.UserAgent()
	if email == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "email required")
		return
	}

	// 节流先行 — 不存在用户也消耗额度, 同样防枚举.
	if s.PasswordResetThrottle != nil {
		if allow, retry := s.PasswordResetThrottle.AllowAndRecord(email); !allow {
			s.appendAudit(admin.AuditEvent{
				ActorEmail: email, ActorIP: ip, ActorUA: ua,
				Action: "auth.password_reset.throttled", Resource: "password_reset",
				Success: false, ErrorCode: "rate_limited",
				Detail: fmt.Sprintf("retry_after=%s", retry.Round(time.Second)),
			})
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retry.Seconds())+1))
			writeErr(w, http.StatusTooManyRequests, "rate_limited",
				fmt.Sprintf("try again in %s", retry.Round(time.Second)))
			return
		}
	}

	u, err := s.Store.GetUserByEmail(r.Context(), email)
	if err != nil {
		// 用户不存在 — 假装成功, 不暴露
		writeJSON(w, http.StatusOK, map[string]any{"email_sent": false})
		return
	}

	sent, rErr := s.issueAndSendPasswordReset(r.Context(), u, email, ip, ua)
	if rErr != nil {
		s.logErr("forgot-password: issue failed", rErr,
			"user_id", u.ID.String(), "email", email)
		writeErr(w, http.StatusInternalServerError, "internal", rErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email_sent": sent})
}

// issueAndSendPasswordReset 失效旧 code → 生成新 code → 写库 → 发邮件.
// 跟 issueAndSendVerification 同骨架, 单独写避免 if-else 在共享函数里劈叉.
func (s *Server) issueAndSendPasswordReset(
	ctx context.Context, u *store.User, email, ip, ua string,
) (sent bool, err error) {
	if err := s.Store.InvalidateActivePasswordResets(ctx, u.ID); err != nil {
		return false, err
	}
	code, codeHash, err := mailer.GenerateCode()
	if err != nil {
		return false, err
	}
	ttl := time.Duration(s.verificationTTLSeconds(ctx)) * time.Second
	if _, err := s.Store.CreatePasswordReset(ctx, u.ID, codeHash, ttl); err != nil {
		return false, err
	}
	if s.VerificationMailer != nil {
		var sendErr error
		sent, sendErr = s.VerificationMailer.SendCode(ctx, email, code, PurposePasswordReset)
		if sendErr != nil {
			s.appendAudit(admin.AuditEvent{
				ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
				Action: "auth.password_reset.send_failed", Resource: "password_reset",
				Target: u.ID.String(), TargetType: "user",
				Success: false, ErrorCode: "smtp_error", ErrorMessage: sendErr.Error(),
			})
			return false, sendErr
		}
	} else if s.Logger != nil {
		s.Logger.Warn("PASSWORD RESET CODE (no mailer injected)",
			"to", email, "code", code)
	}
	s.appendAudit(admin.AuditEvent{
		ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
		Action: "auth.password_reset.sent", Resource: "password_reset",
		Target: u.ID.String(), TargetType: "user",
		Success: true,
		Detail:  fmt.Sprintf("ttl=%s sent=%v", ttl, sent),
	})
	return sent, nil
}

// ─── reset password ─────────────────────────────────────

type resetPasswordReq struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
	// KeepSessionID — 可选,客户端在登录态下走 reset-password (Settings →
	// 改密) 时传当前会话的 refresh_tokens.id。服务端验证该 session 属于
	// 同一用户 + 仍 active,只撤其他 session,保留当前会话不打扰用户。
	// 空 / 不属于该 user / 已 revoked → fallback 全撤(向后兼容老调用)。
	KeepSessionID string `json:"keep_session_id,omitempty"`
}

// handleResetPassword 校验 code → 改 password_hash → 撤 refresh_token.
// 不下发 token (用户重置后用新密码 login, 防错觉; 也避免重置邮件一旦泄漏
// 就直接拿到 session)。
//
// 撤销策略 (BiuMind-Identity-Session-Design B2-a):
//   - keep_session_id 给了 + 校通过 → 只撤其他 session,保留当前会话
//   - 其他情况 → 撤所有 (兼容老路径 / 邮件重置 / 防御性兜底)
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	code := strings.TrimSpace(req.Code)
	ip := admin.ClientIP(r)
	ua := r.UserAgent()

	if email == "" || len(code) != 6 {
		writeErr(w, http.StatusBadRequest, "bad_request", "email + 6-digit code required")
		return
	}
	if len(req.NewPassword) < 8 {
		writeErr(w, http.StatusBadRequest, "weak_password", "min 8 chars")
		return
	}

	u, err := s.Store.GetUserByEmail(r.Context(), email)
	if err != nil {
		s.appendAudit(admin.AuditEvent{
			ActorEmail: email, ActorIP: ip, ActorUA: ua,
			Action: "auth.password_reset.failed", Resource: "password_reset",
			Success: false, ErrorCode: "user_not_found",
		})
		writeErr(w, http.StatusBadRequest, "invalid_code", "")
		return
	}

	v, err := s.Store.GetLatestPasswordReset(r.Context(), u.ID)
	if err != nil {
		s.appendAudit(admin.AuditEvent{
			ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
			Action: "auth.password_reset.failed", Resource: "password_reset",
			Target: u.ID.String(), TargetType: "user",
			Success: false, ErrorCode: "no_pending_code",
		})
		writeErr(w, http.StatusBadRequest, "no_pending_code",
			"no reset code on record; please request a new one")
		return
	}

	if v.ConsumedAt != nil {
		writeErr(w, http.StatusBadRequest, "code_already_used",
			"code already consumed; request a new one")
		return
	}
	if time.Now().After(v.ExpiresAt) {
		writeErr(w, http.StatusBadRequest, "code_expired",
			"code expired; request a new one")
		return
	}
	if v.Attempts >= verifyMaxAttempts {
		writeErr(w, http.StatusBadRequest, "code_locked",
			"too many wrong attempts; request a new one")
		return
	}

	if !mailer.VerifyCode(code, v.CodeHash) {
		n, _ := s.Store.IncPasswordResetAttempts(r.Context(), v.ID)
		s.appendAudit(admin.AuditEvent{
			ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
			Action: "auth.password_reset.failed", Resource: "password_reset",
			Target: u.ID.String(), TargetType: "user",
			Success: false, ErrorCode: "wrong_code",
			Detail: fmt.Sprintf("attempts=%d", n),
		})
		writeErr(w, http.StatusBadRequest, "invalid_code", "")
		return
	}

	// 校通过 — hash 新密码, 改库, 撤 session, 消费 code.
	hash, err := passwords.Hash(req.NewPassword, s.PasswordParams)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := s.Store.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	revoked, keptSession := s.revokeSessionsAfterPasswordReset(r.Context(), u.ID, req.KeepSessionID)
	if err := s.Store.ConsumePasswordReset(r.Context(), v.ID); err != nil {
		// password 已改 — code 没消费成功是 minor, 业务上幂等 (下次 reset 重新生成).
		s.logErr("consume password_reset failed (non-fatal)", err)
	}
	if s.PasswordResetThrottle != nil {
		s.PasswordResetThrottle.Reset(email)
	}
	s.appendAudit(admin.AuditEvent{
		ActorID: u.ID.String(), ActorEmail: email, ActorIP: ip, ActorUA: ua,
		Action: "auth.password_reset.success", Resource: "password_reset",
		Target: u.ID.String(), TargetType: "user",
		Success: true,
		Detail:  fmt.Sprintf("revoked_sessions=%d kept_session=%v", revoked, keptSession),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"reset":            true,
		"revoked_sessions": revoked,
		"kept_session":     keptSession,
	})
}

// revokeSessionsAfterPasswordReset — 改密后的 session 撤销策略。
//
//	keepID == "" / 解析失败 / 不属于该 user / 已 revoked → 全撤,返 (n, false)
//	keepID 校通过                                           → 撤其他, 返 (n, true)
//
// 全撤 fallback 保险:邮件重置 / 老客户端 / 错误的 keepID 都安全降级到"全撤",
// 不会因为参数被攻击者构造而少撤了 session 留下漏洞。
func (s *Server) revokeSessionsAfterPasswordReset(
	ctx context.Context, userID uuid.UUID, keepID string,
) (int64, bool) {
	if keepID == "" {
		n, _ := s.Store.RevokeAllRefreshTokens(ctx, userID)
		return n, false
	}
	keepUUID, err := uuid.Parse(keepID)
	if err != nil {
		n, _ := s.Store.RevokeAllRefreshTokens(ctx, userID)
		return n, false
	}
	// 取 keepID 这条行,校验 (user_id 匹配 + 仍 active)
	rows, err := s.Store.ListActiveRefreshTokens(ctx, userID)
	if err != nil {
		n, _ := s.Store.RevokeAllRefreshTokens(ctx, userID)
		return n, false
	}
	found := false
	for _, t := range rows {
		if t.ID == keepUUID {
			found = true
			break
		}
	}
	if !found {
		n, _ := s.Store.RevokeAllRefreshTokens(ctx, userID)
		return n, false
	}
	n, err := s.Store.RevokeOtherRefreshTokens(ctx, userID, keepUUID)
	if err != nil {
		n, _ := s.Store.RevokeAllRefreshTokens(ctx, userID)
		return n, false
	}
	return n, true
}

// appendAudit nil-safe wrapper. 单元测试 / 早启动时 Audit 为 nil 直接吞掉.
func (s *Server) appendAudit(ev admin.AuditEvent) {
	if s.Audit == nil {
		return
	}
	s.Audit.Append(ev)
}

// buildClaims 把 user 投影到 JWT claims. login + refresh 共用避免漂移.
//   - Roles: 'user' 不写, 节省 token 体积
//   - Plan:  空字符串不写; 跟 store 一样用规范化值 (free/pro/team)
//   - DeviceID: 当前 access_token 对应的 refresh_token.id (= session id),
//     给 "已登录设备"列表标识 is_current. 没 sid 时留空 — 老 access_token
//     在该字段上线之前签发的没 DeviceID, FE 兜底所有 session 都不标 current.
func buildClaims(u *store.User, sid string) *bauth.Claims {
	c := &bauth.Claims{UserID: u.ID.String()}
	// org_id propagation. Downstream services (runtime / brain / authz)
	// scope rows by org via this claim; missing org_id yields 403
	// "no_org" on every per-tenant route. Two cases:
	//   1. User has been assigned an explicit org → use it.
	//   2. No org table / no assignment yet → fall back to user_id as
	//      a single-user-org sentinel. This preserves tenancy isolation
	//      (each user is their own tenant) without forcing every
	//      single-developer deployment to stand up an orgs table just
	//      to log in. When the orgs table lands, set DefaultOrgID on
	//      sign-up and this branch is the explicit value path.
	if u.DefaultOrgID != nil {
		c.OrgID = u.DefaultOrgID.String()
	} else {
		c.OrgID = u.ID.String()
	}
	if u.Role != "" && u.Role != "user" {
		c.Roles = []string{u.Role}
	}
	if u.Plan != "" {
		c.Plan = u.Plan
	}
	if sid != "" {
		c.DeviceID = sid
	}
	return c
}

// refreshAbsoluteTTL 兜底零值,统一访问入口。
func (s *Server) refreshAbsoluteTTL() time.Duration {
	if s.RefreshAbsoluteTTL > 0 {
		return s.RefreshAbsoluteTTL
	}
	return defaultRefreshAbsoluteTTL
}

// refreshReuseGrace 兜底零值,统一访问入口。
func (s *Server) refreshReuseGrace() time.Duration {
	if s.RefreshReuseGrace > 0 {
		return s.RefreshReuseGrace
	}
	return defaultRefreshReuseGrace
}

// handleGraceReplay — 宽限窗口内的老 token 重放 (Auth0 Reuse Interval 同款):
// 沿 rotated_to 链找到当前 head 行, 解密出 head refresh_token 明文, 重新
// 签发 access 返回 200, 客户端无感恢复 (丢响应重试 / app 被杀 / 并发刷新)。
//
// 命中返 true (响应已写); 未命中 / 任一步失败返 false, caller 维持原
// reuse detection 路径。本路径**不写 security_event, 不撤族**。
func (s *Server) handleGraceReplay(w http.ResponseWriter, r *http.Request, t *store.RefreshToken) bool {
	head := s.walkGraceChain(r, t)
	if head == nil {
		return false
	}
	return s.respondGraceRecovery(w, r, t, head, "refresh grace replay")
}

// walkGraceChain — 沿 rotated_to 链找 head。未配置 grace key / 断链 / 查库
// 失败 → nil。3s 超时兜底防 DB 卡住。
func (s *Server) walkGraceChain(r *http.Request, t *store.RefreshToken) *store.GraceHead {
	if len(s.RefreshGraceKey) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	// GraceReplayHead 查库拿最新行 — 并发 rotate 场景 t 是 Find 时的旧快照,
	// rotated_to 可能刚被胜者写入, 不能用快照字段。
	head, ok, err := s.Store.GraceReplayHead(ctx, t.ID)
	if err != nil {
		s.logErr("grace_replay.GraceReplayHead", err)
		return nil
	}
	if !ok {
		return nil
	}
	return head
}

// respondGraceRecovery — grace 恢复的统一响应: 解密 head refresh_token 明文,
// 重签 access (DeviceID = head 行 id), 写 200 refreshResp。任一步失败返
// false, caller 维持 reuse detection 路径。
func (s *Server) respondGraceRecovery(
	w http.ResponseWriter, r *http.Request,
	t *store.RefreshToken, head *store.GraceHead, logMsg string,
) bool {
	headToken, err := decryptGrace(s.RefreshGraceKey, head.TokenEnc)
	if err != nil {
		s.logErr("grace_replay.decrypt", err)
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	u, err := s.Store.GetUserByID(ctx, t.UserID)
	if err != nil {
		s.logErr("grace_replay.GetUserByID", err)
		return false
	}
	access, err := s.Signer.Sign(buildClaims(u, head.ID.String()))
	if err != nil {
		s.logErr("grace_replay.Sign", err)
		return false
	}
	if s.Logger != nil {
		s.Logger.Info(logMsg,
			"user_id", t.UserID, "installation_id", t.InstallationID,
			"session_id", t.ID, "head_session_id", head.ID,
			"hops", head.Hops, "ip", admin.ClientIP(r))
	}
	writeJSON(w, http.StatusOK, refreshResp{
		AccessToken:             access,
		RefreshToken:            string(headToken),
		ExpiresInSeconds:        int64(s.AccessTTL.Seconds()),
		RefreshExpiresInSeconds: int64(time.Until(head.ExpiresAt).Seconds()),
		SessionID:               head.ID.String(),
	})
	return true
}

// handleLateGraceRecovery — 超窗迟恢复 (kill / 崩溃 / 断网丢响应场景):
//
// 服务端 commit 轮换后响应没到达客户端时, 客户端磁盘上只有已 revoked 的
// 老 token, 下次启动 (可能数小时后) 拿它来刷新。这跟"攻击者重放偷来的老
// token"在请求字节上无法区分, 但有两个服务端可观测的行为差异:
//
//  1. 丢响应场景: 新 head 从未被任何人用过 — 链恰好 1 跳 (head = 直接
//     后继, 没人拿后继再轮换过)。≥2 跳说明有第二个活跃方。
//  2. 丢响应场景: 重试来自发起轮换的同一端 — IP / UA 与轮换时同步落库的
//     head.last_ip / last_ua 一致。
//
// 两条都满足 → 判定为原客户端找回丢失的 rotation: grace replay 同款 200
// 恢复, 写 info 级事件 'refresh_token_grace_recovery' (banner 只消费
// 'refresh_token_reuse', 用户无感), 不撤族。
// 任一不满足 → 返 false, caller 维持 reuse detection (撤全族 + 告警)。
//
// 残余风险: 攻击者与受害者同出口 IP (同 NAT) 且仿制 UA 时可能被误判为
// 同端恢复 — 即便如此, 攻击者再轮换一次后受害者重试即超 1 跳, 检测在下
// 一个轮换边界仍会命中, 只是延迟而非失效。
func (s *Server) handleLateGraceRecovery(w http.ResponseWriter, r *http.Request, t *store.RefreshToken) bool {
	head := s.walkGraceChain(r, t)
	if head == nil {
		return false
	}
	// 链已前进 ≥2 跳: 直接后继被用来再轮换过 → 有第二个活跃方, 真 reuse。
	if head.Hops != 1 {
		return false
	}
	// 同端判定: IP + UA 必须与轮换时记录的一致 (NULL 一律不命中, 保守)。
	if head.IP == nil || *head.IP != admin.ClientIP(r) {
		return false
	}
	if head.UA == nil || *head.UA != r.UserAgent() {
		return false
	}
	if !s.respondGraceRecovery(w, r, t, head, "refresh late grace recovery") {
		return false
	}
	// info 级审计, best-effort 不阻塞响应 (响应已写)。用 background ctx
	// 因为 r.Context 可能 caller 已经 cancel。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	detail, _ := json.Marshal(map[string]any{
		"session_id":      t.ID.String(),
		"installation_id": t.InstallationID,
		"head_session_id": head.ID.String(),
	})
	if err := s.Store.InsertSecurityEvent(ctx, store.SecurityEvent{
		UserID: t.UserID,
		Kind:   "refresh_token_grace_recovery",
		Detail: detail,
		IP:     admin.ClientIP(r),
		UA:     r.UserAgent(),
	}); err != nil {
		s.logErr("late_grace.InsertSecurityEvent", err)
	}
	return true
}

// handleTokenReuse — refresh_token 重放检测命中时调:撤销该 (user,
// installation) 整族 + 写 security_events 审计行。失败仅日志,不阻塞
// 401 返回 (refresh_token 那条已经 revoked, 攻击者下次提交也会被拒)。
//
// 用 background ctx 因为 r.Context 可能 caller 已经 cancel,但这一步
// 必须执行完。3s 超时兜底防止 DB 卡住。
func (s *Server) handleTokenReuse(r *http.Request, t *store.RefreshToken) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	revoked, err := s.Store.RevokeFamilyByInstallation(ctx, t.UserID, t.InstallationID)
	if err != nil {
		s.logErr("reuse_detection.RevokeFamily", err)
	}
	detail, _ := json.Marshal(map[string]any{
		"session_id":      t.ID.String(),
		"installation_id": t.InstallationID,
		"family_revoked":  revoked,
	})
	if err := s.Store.InsertSecurityEvent(ctx, store.SecurityEvent{
		UserID: t.UserID,
		Kind:   "refresh_token_reuse",
		Detail: detail,
		IP:     admin.ClientIP(r),
		UA:     r.UserAgent(),
	}); err != nil {
		s.logErr("reuse_detection.InsertSecurityEvent", err)
	}
	if s.Logger != nil {
		s.Logger.Warn("refresh token reuse detected",
			"user_id", t.UserID, "installation_id", t.InstallationID,
			"session_id", t.ID, "family_revoked", revoked,
			"ip", admin.ClientIP(r))
	}
}

// issueTokensAndRespond 同 (user, installationID) 已有 active session 则
// 在原行上 rotate token_hash; 否则新建. 客户端反复 login 不堆积.
func (s *Server) issueTokensAndRespond(
	w http.ResponseWriter, ctx context.Context,
	u *store.User, deviceName, installationID string,
) {
	full, hash, err := token.Generate(token.RefreshTokenPrefix)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	sid, err := s.Store.CreateOrRotateRefreshToken(
		ctx, u.ID, installationID, hash, deviceName,
		s.RefreshTTL, s.refreshAbsoluteTTL(),
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	access, err := s.Signer.Sign(buildClaims(u, sid.String()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokensResp{
		AccessToken:      access,
		RefreshToken:     full,
		ExpiresInSeconds: int64(s.AccessTTL.Seconds()),
		User:             buildUserOut(u, s.RoleCache),
	})
}

// ─── refresh ────────────────────────────────────────────

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshResp — /v1/auth/refresh 的成功响应。
//
// 跟之前的 accessOnlyResp 区别 (BiuMind-Identity-Session-Design §3.2):
//   - 同时返回新 access + 新 refresh (rotation)
//   - 旧 refresh_token 在 RotateRefreshToken 事务里 revoke,客户端必须切到新值
//   - refresh_expires_in_seconds 是 sliding window 的剩余 (rotation 后续期到的值)
//   - session_id = 新行的 refresh_tokens.id, 写进新 access JWT 的 DeviceID
//
// 字段名 expires_in_seconds 跟 login/register/verify 保持一致 (旧客户端解析
// 不会挂)。
type refreshResp struct {
	AccessToken             string `json:"access_token"`
	RefreshToken            string `json:"refresh_token"`
	ExpiresInSeconds        int64  `json:"expires_in_seconds"`
	RefreshExpiresInSeconds int64  `json:"refresh_expires_in_seconds"`
	SessionID               string `json:"session_id"`
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	oldHash := token.Hash(req.RefreshToken)
	t, err := s.Store.FindRefreshToken(r.Context(), oldHash)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid_token", "")
		return
	}
	// 三层过期校验 (Design §3.1):
	//   1. revoked_at != nil  → 老 token 被再次提交。分三级处理:
	//      a. 宽限窗口 (refreshReuseGrace) 内 → grace replay: 沿 rotated_to
	//         链找回 head 直接 200 (丢响应重试 / 并发刷新场景, 不撤族不写事件);
	//      b. 超窗但满足迟恢复条件 (链恰好 1 跳 + IP/UA 与轮换发起端一致)
	//         → late grace recovery: 同样 200 恢复, 写 info 级事件
	//         'refresh_token_grace_recovery', 用户无感 (kill 丢响应后任意
	//         时长重开都走这里);
	//      c. 都不满足 → reuse detection: 撤销该 (user, installation) 整族 +
	//         写 security_events ('refresh_token_reuse') + 401。
	//   2. expires_at 过期    → sliding window 到 (idle 太久没刷)
	//   3. absolute_expires_at 过期 → 绝对兜底,无论 sliding 多新都拒。
	now := time.Now()
	if t.RevokedAt != nil {
		if time.Since(*t.RevokedAt) <= s.refreshReuseGrace() {
			if s.handleGraceReplay(w, r, t) {
				return
			}
		} else if s.handleLateGraceRecovery(w, r, t) {
			return
		}
		s.handleTokenReuse(r, t)
		writeErr(w, http.StatusUnauthorized, "token_reuse", "")
		return
	}
	if t.ExpiresAt.Before(now) {
		writeErr(w, http.StatusUnauthorized, "expired_token", "")
		return
	}
	if t.AbsoluteExpiresAt.Before(now) {
		writeErr(w, http.StatusUnauthorized, "absolute_cap_reached", "")
		return
	}
	u, err := s.Store.GetUserByID(r.Context(), t.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid_token", "")
		return
	}
	// Rotation: 生成新 opaque token, 事务内 revoke 老行 + insert 新行 (继承
	// installation_id + device_name + absolute_expires_at, 续 sliding expires_at)。
	// ip/ua 随新行同事务落库 — 迟恢复 (late grace recovery) 的"同端"判定
	// 依赖轮换时记录的 head.last_ip / last_ua, 不能异步补写 (有竞态)。
	newFull, newHash, err := token.Generate(token.RefreshTokenPrefix)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// grace replay 用: 新 token 明文 AES-GCM 加密, 随 rotate 回写到老行
	// rotated_token_enc。加密失败仅降级 (grace replay 不命中), 不阻塞 refresh。
	var successorEnc []byte
	if len(s.RefreshGraceKey) > 0 {
		if enc, encErr := encryptGrace(s.RefreshGraceKey, []byte(newFull)); encErr != nil {
			s.logErr("refresh.encryptGrace", encErr)
		} else {
			successorEnc = enc
		}
	}
	newID, newExpiresAt, _, err := s.Store.RotateRefreshToken(
		r.Context(), t.ID, newHash, successorEnc, s.RefreshTTL,
		admin.ClientIP(r), r.UserAgent(),
	)
	if err != nil {
		// ErrNotFound 表示并发 rotate 已经把老行 revoke 了 — 胜者的事务已
		// commit (老行 rotated_to 已写入), 先尝试 grace replay; 未命中才跟
		// reuse detection 同处理 (这条请求看到的 "active" 老行已经不再 active,
		// 可疑)。
		if errors.Is(err, store.ErrNotFound) {
			if s.handleGraceReplay(w, r, t) {
				return
			}
			s.handleTokenReuse(r, t)
			writeErr(w, http.StatusUnauthorized, "token_reuse", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// refresh 时重新拿 role + plan: 改 role / 升级套餐 必须立即生效, 不能等
	// 老 access token 自然过期。 sid 用新行的 id (老 sid 已随 revoked 老行作废)。
	access, err := s.Signer.Sign(buildClaims(u, newID.String()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// refresh_expires_in_seconds 用 sliding window 剩余时间 (= 这条新行
	// expires_at - now)。客户端如果想知道 absolute cap, 后续可以加字段。
	writeJSON(w, http.StatusOK, refreshResp{
		AccessToken:             access,
		RefreshToken:            newFull,
		ExpiresInSeconds:        int64(s.AccessTTL.Seconds()),
		RefreshExpiresInSeconds: int64(time.Until(newExpiresAt).Seconds()),
		SessionID:               newID.String(),
	})
}

// ─── logout ─────────────────────────────────────────────

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.RefreshToken == "" {
		writeJSON(w, http.StatusOK, map[string]any{}) // 容忍空请求
		return
	}
	_ = s.Store.RevokeRefreshToken(r.Context(), token.Hash(req.RefreshToken))
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ─── me ─────────────────────────────────────────────────

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	u, err := s.Store.GetUserByID(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, buildUserOut(u, s.RoleCache))
}

// ─── virtual keys ───────────────────────────────────────

type createKeyReq struct {
	Name             string   `json:"name"`
	AllowedModels    []string `json:"allowed_models"`
	DailyTokenLimit  int64    `json:"daily_token_limit"`
	DailyCostLimit   int64    `json:"daily_cost_micro_usd_limit"`
	ExpiresInSeconds int64    `json:"expires_in_seconds"`
}

type createKeyResp struct {
	ID     string `json:"id"`
	Prefix string `json:"prefix"`
	Secret string `json:"secret"` // 仅创建时返回
	Name   string `json:"name"`
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req createKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)

	full, hash, err := token.Generate(token.VirtualKeyPrefix)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	scope, _ := json.Marshal(map[string]any{
		"allowed_models":             req.AllowedModels,
		"daily_token_limit":          req.DailyTokenLimit,
		"daily_cost_micro_usd_limit": req.DailyCostLimit,
	})
	var expiresAt *time.Time
	if req.ExpiresInSeconds > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresInSeconds) * time.Second)
		expiresAt = &t
	}
	v, err := s.Store.CreateVirtualKey(r.Context(), uid, token.DisplayPrefix(full), hash, req.Name, scope, expiresAt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, createKeyResp{
		ID: v.ID.String(), Prefix: v.Prefix, Secret: full, Name: v.Name,
	})
}

type keyOut struct {
	ID         string  `json:"id"`
	Prefix     string  `json:"prefix"`
	Name       string  `json:"name"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	keys, err := s.Store.ListVirtualKeys(r.Context(), uid, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]keyOut, 0, len(keys))
	for _, k := range keys {
		var lastUsed *string
		if k.LastUsedAt != nil {
			s := k.LastUsedAt.Format(time.RFC3339)
			lastUsed = &s
		}
		out = append(out, keyOut{
			ID: k.ID.String(), Prefix: k.Prefix, Name: k.Name,
			CreatedAt: k.CreatedAt.Format(time.RFC3339), LastUsedAt: lastUsed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	idStr := r.PathValue("id")
	keyID, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if err := s.Store.RevokeVirtualKey(r.Context(), keyID, uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ─── sessions (已登录设备 self-serve) ───────────────────

type sessionOut struct {
	ID         string  `json:"id"`
	DeviceName string  `json:"device_name"`
	DeviceKind string  `json:"device_kind"` // mobile|desktop|browser|unknown
	LastIP     string  `json:"last_ip,omitempty"`
	LastUA     string  `json:"last_ua,omitempty"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	ExpiresAt  string  `json:"expires_at"`
	CreatedAt  string  `json:"created_at"`
	TTLDays    int     `json:"ttl_days"`
	IsCurrent  bool    `json:"is_current"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_user", err.Error())
		return
	}
	sessions, err := s.Store.ListActiveRefreshTokens(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]sessionOut, 0, len(sessions))
	for _, t := range sessions {
		so := sessionOut{
			ID:         t.ID.String(),
			DeviceName: fallbackDeviceName(t.DeviceName),
			DeviceKind: inferDeviceKind(t.DeviceName, deref(t.LastUA)),
			LastIP:     deref(t.LastIP),
			LastUA:     deref(t.LastUA),
			ExpiresAt:  t.ExpiresAt.Format(time.RFC3339),
			CreatedAt:  t.CreatedAt.Format(time.RFC3339),
			TTLDays:    int(t.ExpiresAt.Sub(t.CreatedAt).Hours() / 24),
			IsCurrent:  c.DeviceID != "" && t.ID.String() == c.DeviceID,
		}
		if t.LastUsedAt != nil {
			s := t.LastUsedAt.Format(time.RFC3339)
			so.LastUsedAt = &s
		}
		out = append(out, so)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// securityEventOut — wire shape for /v1/identity/me/security-events。
type securityEventOut struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	IP        string          `json:"ip,omitempty"`
	UA        string          `json:"ua,omitempty"`
	CreatedAt string          `json:"created_at"`
}

// handleListSecurityEvents — 返当前用户最近 N 条安全事件 (默认 50, 上限
// 200)。当前唯一 kind 是 'refresh_token_reuse', 未来扩展不破坏 wire。
//
// 客户端用法 (B2-c):
//   - reuse banner: 检查最近 24h 内有无 kind == 'refresh_token_reuse'
//   - 安全活动页 (B2-b 暂未做): 完整列表渲染
//
// limit query param 可选, 越界静默 clamp 到 [1, 200] 默认 50。
func (s *Server) handleListSecurityEvents(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_user", err.Error())
		return
	}
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		var n int
		_, err := fmt.Sscanf(q, "%d", &n)
		if err == nil && n > 0 {
			limit = n
		}
	}
	events, err := s.Store.ListSecurityEvents(r.Context(), uid, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]securityEventOut, 0, len(events))
	for _, e := range events {
		row := securityEventOut{
			ID:        e.ID.String(),
			Kind:      e.Kind,
			CreatedAt: e.CreatedAt.Format(time.RFC3339),
		}
		if len(e.Detail) > 0 {
			row.Detail = json.RawMessage(e.Detail)
		}
		if e.IP != nil {
			row.IP = *e.IP
		}
		if e.UA != nil {
			row.UA = *e.UA
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_user", err.Error())
		return
	}
	sid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if err := s.Store.RevokeRefreshTokenByID(r.Context(), sid, uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 不区分"不存在" / "不属于你" / "已撤销" — 全 404 防 session id 枚举.
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	self := c.DeviceID != "" && sid.String() == c.DeviceID
	s.appendAudit(admin.AuditEvent{
		ActorID: uid.String(), ActorEmail: "", /*不在 claims, 留空*/
		ActorIP: admin.ClientIP(r), ActorUA: r.UserAgent(),
		Action: "auth.session.revoked", Resource: "session",
		Target: sid.String(), TargetType: "session",
		Success: true,
		Detail:  fmt.Sprintf("self=%v", self),
	})
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "self": self})
}

func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_user", err.Error())
		return
	}
	// claims.DeviceID 空 (老 access_token) → exceptID = uuid.Nil → 撤全部.
	// 这是兜底; 用户重新 refresh 后 token 就有 sid, 此后再点 "踢其他" 就能保留当前.
	var exceptID uuid.UUID
	if c.DeviceID != "" {
		if id, perr := uuid.Parse(c.DeviceID); perr == nil {
			exceptID = id
		}
	}
	count, err := s.Store.RevokeOtherRefreshTokens(r.Context(), uid, exceptID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.appendAudit(admin.AuditEvent{
		ActorID: uid.String(),
		ActorIP: admin.ClientIP(r), ActorUA: r.UserAgent(),
		Action: "auth.sessions.revoke_others", Resource: "session",
		Success: true,
		Detail:  fmt.Sprintf("count=%d kept_current=%v", count, c.DeviceID != ""),
	})
	writeJSON(w, http.StatusOK, map[string]any{"revoked": count})
}

// ─── helpers ────────────────────────────────────────────

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func fallbackDeviceName(s string) string {
	if s == "" {
		return "未知设备"
	}
	return s
}

// inferDeviceKind 给 FE 选 icon 用. 优先 device_name (登录时 client 自己填,
// 信噪比高), 兜底用 last_ua 解析. 关键字命中则返对应 kind.
func inferDeviceKind(deviceName, ua string) string {
	combined := strings.ToLower(deviceName + " " + ua)
	switch {
	case containsAny(combined, "iphone", "ipad", "android", "mobile"):
		return "mobile"
	case containsAny(combined, "chrome", "safari", "firefox", "edge", "opera"):
		// "Chrome on macOS" 既含 chrome 也含 macos → 优先 browser (用户从浏览器登)
		return "browser"
	case containsAny(combined, "macos", "mac os", "windows", "linux"):
		return "desktop"
	}
	return "unknown"
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ─── middleware ─────────────────────────────────────────

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(auth[7:])
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		// 单点记录 identity 全部 protected 路由 — me / tokens / api_keys /
		// activity / coupons / referrals / billing 都共用,debug 排查"哪个
		// endpoint 在被 poll"最有用。
		if s.Logger != nil {
			s.Logger.DebugContext(r.Context(), "identity api: request",
				"user_id", claims.UserID, "method", r.Method,
				"path", r.URL.Path)
		}
		r = r.WithContext(bauth.WithClaims(r.Context(), claims))
		next(w, r)
	}
}

// ─── helpers ────────────────────────────────────────────

func validEmail(s string) bool {
	at := strings.Index(s, "@")
	if at < 1 || at == len(s)-1 {
		return false
	}
	if !strings.Contains(s[at+1:], ".") {
		return false
	}
	return true
}

// emailDomain — Debug 日志用,只暴露 @ 后缀 (gmail.com / 自建域),不泄露
// 完整邮箱。本地 dev 排查"哪个域名的邮件发不出去"够用了。
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return email[at+1:]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
