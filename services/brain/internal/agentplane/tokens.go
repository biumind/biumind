// Session token 签发 / 校验（S3-9）。
//
// 两类 token：
//
//   long-lived auth token（PAT / access+refresh）—— 由 identity 服务发，
//                                                    daemon / CLI 用，不在这里
//   session_token —— 由 brain 在 CreateSession (S3-6) 时颁发，30min TTL，
//                    专用于 WS upgrade 鉴权（窄 scope，泄漏代价小）
//
// 这个文件管 session_token 的签发跟校验，refresh endpoint 用。
//
// 实现：复用现有 `bauth.Signer` —— 不另起 secret，避免多套密钥管理。
// session 范围用 `Claims.Scope = ["session:<id>"]` 标记，校验时检查。

package agentplane

import (
	"errors"
	"fmt"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

// SessionTokenTTL —— session_token 默认 30min。客户端在剩 5min 时主动 refresh。
const SessionTokenTTL = 30 * time.Minute

// scopeSessionPrefix —— Claims.Scope 里 session 范围标记前缀。
// 例如 "session:550e8400-e29b-41d4-a716-446655440000"。
const scopeSessionPrefix = "session:"

// ErrSessionScopeMissing —— token 没标记成 session-scope 时的错误。
// 比如客户端拿 PAT/access_token 直接当 session_token 用 → 拒绝。
var ErrSessionScopeMissing = errors.New("agentplane: token missing session scope")

// ErrSessionScopeMismatch —— token scope 跟 URL path 里的 session_id 不匹配。
// 防 token 误用：拿 sessionA 的 token 操作 sessionB 应当拒绝。
var ErrSessionScopeMismatch = errors.New("agentplane: token scope does not match session")

// IssueSessionToken 给 (userID, sessionID) 颁发一个 30min session_token。
// signer 由调用方注入（共享 brain 的 bauth.Signer，HS256 / RS256 都行）。
//
// 返回 (token, expires_at)，expires_at 是绝对时间，方便客户端排定 refresh
// 计时器。
func IssueSessionToken(signer *bauth.Signer, userID, sessionID uuid.UUID) (string, time.Time, error) {
	if signer == nil {
		return "", time.Time{}, errors.New("agentplane: nil signer")
	}
	expiresAt := time.Now().Add(SessionTokenTTL)
	tok, err := signer.Sign(&bauth.Claims{
		UserID: userID.String(),
		Scope:  []string{scopeSessionPrefix + sessionID.String()},
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("agentplane: sign session token: %w", err)
	}
	return tok, expiresAt, nil
}

// VerifySessionToken 校验 token + 检查 scope 包含目标 session。
//
//	expectedSessionID == uuid.Nil → 只校签名 + scope 是 session-shape
//	非 nil → 严格匹配 scope 里的 session_id
//
// 返回 *bauth.Claims —— 已经校过签名 + 过期时间；调用方还可以读 UserID
// 等字段做后续策略。
func VerifySessionToken(verifier *bauth.Verifier, token string, expectedSessionID uuid.UUID) (*bauth.Claims, error) {
	if verifier == nil {
		return nil, errors.New("agentplane: nil verifier")
	}
	claims, err := verifier.Verify(token)
	if err != nil {
		return nil, fmt.Errorf("agentplane: verify session token: %w", err)
	}
	// 找 session: 前缀的 scope
	var sessionScope string
	for _, sc := range claims.Scope {
		if len(sc) > len(scopeSessionPrefix) && sc[:len(scopeSessionPrefix)] == scopeSessionPrefix {
			sessionScope = sc[len(scopeSessionPrefix):]
			break
		}
	}
	if sessionScope == "" {
		return nil, ErrSessionScopeMissing
	}
	if expectedSessionID != uuid.Nil && sessionScope != expectedSessionID.String() {
		return nil, ErrSessionScopeMismatch
	}
	return claims, nil
}
