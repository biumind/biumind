// /v1/auth/refresh end-to-end 集成测试 — 真 PG + 真 Server。
//
// 覆盖 BiuMind-Identity-Session-Design 的核心 SLA:
//   - rotation: 每次 refresh 签发新 access + 新 refresh, 老的失效
//   - sliding window: 多次 rotate 不撞 absolute cap
//   - reuse detection: 老 token 二次提交 → 401 token_reuse + 整族撤销
//   - absolute cap: created_at + absoluteTTL 后强制 401
//   - sliding expiry: expires_at 之前过 → 401 expired_token
//
// 跟 store 层的 refresh_rotation_test.go 区别 — 这里走 HTTP handler,
// 验证 wire format + 错误码 + 响应字段。

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/mailer"
	"github.com/biumind/biumind/services/identity/internal/passwords"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func e2eDBURL() string {
	if v := os.Getenv("IDENTITY_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

func newE2EServer(t *testing.T, opts ...func(*Server)) (*Server, *http.ServeMux, *pgxpool.Pool) {
	t.Helper()
	url := e2eDBURL()
	if url == "skip" {
		t.Skip("IDENTITY_TEST_DATABASE_URL=skip")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	t.Cleanup(pool.Close)

	signer := bauth.NewSigner("test-secret-very-long-string-for-hmac-32", "iss", "aud", time.Minute)
	verifier := bauth.NewVerifier("test-secret-very-long-string-for-hmac-32", "iss", "aud")
	s := &Server{
		Store:              store.New(pool),
		Signer:             signer,
		Verifier:           verifier,
		AccessTTL:          time.Minute,
		RefreshTTL:         24 * time.Hour,
		RefreshAbsoluteTTL: 365 * 24 * time.Hour,
		PasswordParams:     passwords.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	}
	for _, o := range opts {
		o(s)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return s, mux, pool
}

// loginE2E 跑 register → verify → login(自动验证 email),返回 access + refresh。
// 简化:直接用 store 创建 verified user + 调 issueTokensAndRespond 等价逻辑(loginHandler)。
func loginE2E(t *testing.T, mux *http.ServeMux, pool *pgxpool.Pool, s *Server, email, pw, install string) (access, refresh string) {
	t.Helper()
	ctx := context.Background()

	// 1) 直接 INSERT user + 设置 password_hash + email_verified_at
	hash, err := passwords.Hash(pw, s.PasswordParams)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO identity.users (email, password_hash, email_verified_at)
		VALUES ($1, $2, now())
	`, email, hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM identity.refresh_tokens
			 WHERE user_id IN (SELECT id FROM identity.users WHERE email = $1)
		`, email)
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM identity.security_events
			 WHERE user_id IN (SELECT id FROM identity.users WHERE email = $1)
		`, email)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.users WHERE email = $1`, email)
	})

	// 2) /v1/auth/login
	rr := doJSON(mux, "POST", "/v1/auth/login", map[string]any{
		"email": email, "password": pw,
		"installation_id": install,
		"device_name":     "test-device",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("login: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var lr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	if lr.AccessToken == "" || lr.RefreshToken == "" {
		t.Fatalf("missing tokens in login response: %s", rr.Body.String())
	}
	return lr.AccessToken, lr.RefreshToken
}

// callRefresh 用 refresh_token 调 /v1/auth/refresh, 返回响应 + body。
func callRefresh(mux *http.ServeMux, refreshToken string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"refresh_token": refreshToken})
	req := httptest.NewRequest("POST", "/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// TestRefresh_E2E_RotationFiveTimes — login → refresh × 5, 每次都拿到不同
// access + refresh, expires_in / refresh_expires_in / session_id 字段都返。
func TestRefresh_E2E_RotationFiveTimes(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-rot-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-rot-e2e")

	seen := map[string]bool{rt: true}
	curRefresh := rt

	for i := 0; i < 5; i++ {
		rr := callRefresh(mux, curRefresh)
		if rr.Code != http.StatusOK {
			t.Fatalf("iter %d: code=%d body=%s", i, rr.Code, rr.Body.String())
		}
		var resp struct {
			AccessToken             string `json:"access_token"`
			RefreshToken            string `json:"refresh_token"`
			ExpiresInSeconds        int64  `json:"expires_in_seconds"`
			RefreshExpiresInSeconds int64  `json:"refresh_expires_in_seconds"`
			SessionID               string `json:"session_id"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.AccessToken == "" || resp.RefreshToken == "" {
			t.Errorf("iter %d: missing token fields", i)
		}
		if resp.ExpiresInSeconds <= 0 {
			t.Errorf("iter %d: expires_in_seconds=%d", i, resp.ExpiresInSeconds)
		}
		if resp.RefreshExpiresInSeconds <= 0 {
			t.Errorf("iter %d: refresh_expires_in_seconds=%d", i, resp.RefreshExpiresInSeconds)
		}
		if resp.SessionID == "" {
			t.Errorf("iter %d: missing session_id", i)
		}
		if seen[resp.RefreshToken] {
			t.Errorf("iter %d: refresh token reused %q", i, resp.RefreshToken)
		}
		seen[resp.RefreshToken] = true
		curRefresh = resp.RefreshToken
	}
}

// TestRefresh_E2E_OldTokenAfterRotateReturnsTokenReuse — rotate 之后用老
// refresh 调, 返 401 token_reuse 错误码 + family 全部 revoke + security_event 落库。
func TestRefresh_E2E_OldTokenAfterRotateReturnsTokenReuse(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-reuse-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-reuse-e2e")

	// 第一次 refresh 成功
	rr := callRefresh(mux, rt)
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d %s", rr.Code, rr.Body.String())
	}
	var first struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&first)

	// 用老 refresh_token (rt) 再调 — 应该返 401 token_reuse
	rr = callRefresh(mux, rt)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("reuse should 401, got %d", rr.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "token_reuse" {
		t.Errorf("expected error.code=token_reuse, got %q (body=%s)",
			errResp.Error.Code, rr.Body.String())
	}

	// 验证 family 已经被撤销 — 攻击者拿走的新 refresh (first.RefreshToken)
	// 现在也用不了
	rr = callRefresh(mux, first.RefreshToken)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("attacker's token should also be 401, got %d", rr.Code)
	}

	// 验证 security_events 落库
	var count int
	_ = pool.QueryRow(context.Background(), `
		SELECT count(*) FROM identity.security_events se
		JOIN identity.users u ON u.id = se.user_id
		WHERE u.email = $1 AND se.kind = 'refresh_token_reuse'
	`, email).Scan(&count)
	if count == 0 {
		t.Error("expected security_events row for refresh_token_reuse")
	}
}

// TestRefresh_E2E_SlidingExpired — 把 expires_at 改到过去, 返 401 expired_token
// (而不是 token_reuse, 因为没 revoked)。
func TestRefresh_E2E_SlidingExpired(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-slide-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-slide-e2e")

	// 把这个 token 的 sliding expires_at 改到 1 小时前(absolute 还在)
	_, err := pool.Exec(context.Background(), `
		UPDATE identity.refresh_tokens
		   SET expires_at = now() - interval '1 hour'
		 WHERE user_id IN (SELECT id FROM identity.users WHERE email = $1)
		   AND revoked_at IS NULL
	`, email)
	if err != nil {
		t.Fatal(err)
	}

	rr := callRefresh(mux, rt)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "expired_token" {
		t.Errorf("expected expired_token, got %q", errResp.Error.Code)
	}
	_ = s
}

// TestRefresh_E2E_AbsoluteCapReached — sliding 还没过, 但 absolute_expires_at
// 已经过了 → 返 401 absolute_cap_reached。
func TestRefresh_E2E_AbsoluteCapReached(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-abs-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-abs-e2e")

	_, err := pool.Exec(context.Background(), `
		UPDATE identity.refresh_tokens
		   SET absolute_expires_at = now() - interval '1 hour'
		 WHERE user_id IN (SELECT id FROM identity.users WHERE email = $1)
		   AND revoked_at IS NULL
	`, email)
	if err != nil {
		t.Fatal(err)
	}

	rr := callRefresh(mux, rt)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "absolute_cap_reached" {
		t.Errorf("expected absolute_cap_reached, got %q", errResp.Error.Code)
	}
	_ = s
}

// seedPasswordResetCode 给 user 写一条 active password_reset code 入库。
// 模拟 forgot-password 邮件流程的服务端落地点。
func seedPasswordResetCode(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, code string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO identity.password_resets (user_id, code_hash, expires_at)
		VALUES ($1, $2, now() + interval '15 minutes')
	`, userID, mailer.HashCode(code))
	if err != nil {
		t.Fatal(err)
	}
}

// findActiveSessionByInstall 直接查 (user, installation_id) 下的 active row。
func findActiveSessionByInstall(t *testing.T, s *Server, userID uuid.UUID, install string) *store.RefreshToken {
	t.Helper()
	rows, err := s.Store.ListActiveRefreshTokens(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.InstallationID == install {
			return r
		}
	}
	return nil
}

// TestResetPassword_KeepSession_PreservesCurrentRevokesOthers —
// B2-a: 改密带 keep_session_id 时只撤其他 session,当前设备 token 保留。
func TestResetPassword_KeepSession_PreservesCurrentRevokesOthers(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-keep-%s@test", uuid.NewString()[:8])

	// 设备 A: 走 loginE2E (建 user + 第一条 session)
	_, _ = loginE2E(t, mux, pool, s, email, "longpassword123", "install-keep-A")
	ctx := context.Background()
	var userID uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM identity.users WHERE email = $1`, email).Scan(&userID)

	// 设备 B: 同 user 第二个 installation,直接走 Store
	hashB := make([]byte, 32)
	for i := range hashB {
		hashB[i] = byte(i)
	}
	idB, err := s.Store.CreateOrRotateRefreshToken(
		ctx, userID, "install-keep-B", hashB, "device-B",
		s.RefreshTTL, s.refreshAbsoluteTTL(),
	)
	if err != nil {
		t.Fatal(err)
	}

	sessA := findActiveSessionByInstall(t, s, userID, "install-keep-A")
	if sessA == nil {
		t.Fatal("session A not found")
	}

	codeRaw := "123456"
	seedPasswordResetCode(t, pool, userID, codeRaw)

	body, _ := json.Marshal(map[string]any{
		"email":           email,
		"code":            codeRaw,
		"new_password":    "newlongpassword123",
		"keep_session_id": sessA.ID.String(),
	})
	req := httptest.NewRequest("POST", "/v1/auth/reset-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reset-password: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		KeptSession     bool  `json:"kept_session"`
		RevokedSessions int64 `json:"revoked_sessions"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !resp.KeptSession {
		t.Errorf("expected kept_session=true, body=%s", rr.Body.String())
	}
	if resp.RevokedSessions != 1 {
		t.Errorf("expected 1 other session revoked, got %d", resp.RevokedSessions)
	}

	// A 仍 active
	rows, _ := s.Store.ListActiveRefreshTokens(ctx, userID)
	var aFound, bFound bool
	for _, r := range rows {
		if r.ID == sessA.ID {
			aFound = true
		}
		if r.ID == idB {
			bFound = true
		}
	}
	if !aFound {
		t.Error("session A should still be active")
	}
	if bFound {
		t.Error("session B should be revoked")
	}
}

// TestResetPassword_NoKeepSession_RevokesAll — 不传 keep_session_id
// (邮箱忘密流程) 全撤,kept_session=false。
func TestResetPassword_NoKeepSession_RevokesAll(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-nokeep-%s@test", uuid.NewString()[:8])
	_, _ = loginE2E(t, mux, pool, s, email, "longpassword123", "install-nokeep")

	ctx := context.Background()
	var userID uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM identity.users WHERE email = $1`, email).Scan(&userID)

	codeRaw := "654321"
	seedPasswordResetCode(t, pool, userID, codeRaw)

	body, _ := json.Marshal(map[string]any{
		"email":        email,
		"code":         codeRaw,
		"new_password": "anothernewpassword123",
	})
	req := httptest.NewRequest("POST", "/v1/auth/reset-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reset-password: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		KeptSession     bool  `json:"kept_session"`
		RevokedSessions int64 `json:"revoked_sessions"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.KeptSession {
		t.Error("expected kept_session=false")
	}
	if resp.RevokedSessions < 1 {
		t.Errorf("expected ≥1 revoked, got %d", resp.RevokedSessions)
	}
	rows, _ := s.Store.ListActiveRefreshTokens(ctx, userID)
	if len(rows) != 0 {
		t.Errorf("expected 0 active sessions after no-keep reset, got %d", len(rows))
	}
}

// TestResetPassword_BogusKeepSessionID_FallsBackToRevokeAll — keep_session_id
// 不属于该 user → 防御性 fallback 到全撤 (绝不能因为参数构造少撤 session)。
func TestResetPassword_BogusKeepSessionID_FallsBackToRevokeAll(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-bogus-%s@test", uuid.NewString()[:8])
	_, _ = loginE2E(t, mux, pool, s, email, "longpassword123", "install-bogus")
	ctx := context.Background()
	var userID uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM identity.users WHERE email = $1`, email).Scan(&userID)

	codeRaw := "999888"
	seedPasswordResetCode(t, pool, userID, codeRaw)

	bogusID := uuid.NewString() // 完全不属于该 user
	body, _ := json.Marshal(map[string]any{
		"email":           email,
		"code":            codeRaw,
		"new_password":    "newlongpassword999",
		"keep_session_id": bogusID,
	})
	req := httptest.NewRequest("POST", "/v1/auth/reset-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reset-password: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		KeptSession     bool  `json:"kept_session"`
		RevokedSessions int64 `json:"revoked_sessions"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.KeptSession {
		t.Error("bogus keep_session_id should NOT preserve any session")
	}
	if resp.RevokedSessions < 1 {
		t.Errorf("expected fallback revoke all, got %d", resp.RevokedSessions)
	}
}

// TestSecurityEvents_E2E_ReuseDetectionLeaksToList — reuse detection
// 命中后, GET /v1/identity/me/security-events 能拉到该事件 (B2-c)。
func TestSecurityEvents_E2E_ReuseDetectionLeaksToList(t *testing.T) {
	s, mux, pool := newE2EServer(t)
	email := fmt.Sprintf("e2e-evt-%s@test", uuid.NewString()[:8])
	access, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-evt")

	// 第一次 refresh 拿新 token
	rr := callRefresh(mux, rt)
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d", rr.Code)
	}
	// 用老 refresh 触发 reuse detection (security_events 落表)
	rr = callRefresh(mux, rt)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}

	// GET /v1/identity/me/security-events
	req := httptest.NewRequest("GET", "/v1/identity/me/security-events", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list events: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Events []struct {
			Kind      string `json:"kind"`
			CreatedAt string `json:"created_at"`
		} `json:"events"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	found := false
	for _, e := range resp.Events {
		if e.Kind == "refresh_token_reuse" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected refresh_token_reuse event in list, got %d events: %+v",
			len(resp.Events), resp.Events)
	}
}

// TestSecurityEvents_E2E_RequiresAuth — 没 access token → 401。
func TestSecurityEvents_E2E_RequiresAuth(t *testing.T) {
	_, mux, _ := newE2EServer(t)
	req := httptest.NewRequest("GET", "/v1/identity/me/security-events", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// TestRefresh_E2E_InvalidToken — 完全不存在的 refresh_token → 401 invalid_token。
func TestRefresh_E2E_InvalidToken(t *testing.T) {
	_, mux, _ := newE2EServer(t)

	rr := callRefresh(mux, "rt-live-totallybogus123456789012345")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "invalid_token" {
		t.Errorf("expected invalid_token, got %q", errResp.Error.Code)
	}
}
