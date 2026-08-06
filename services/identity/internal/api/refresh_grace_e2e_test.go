// /v1/auth/refresh grace window (refresh token rotation 宽限) end-to-end 测试。
//
// 覆盖 Auth0 Reuse Interval / Okta 30s grace 同款机制:
//   - 宽限窗口内重放老 token → 200, refresh_token 跟上次响应相同, 不写
//     security_events, 不撤族 (丢响应重试 / app 被杀 / 并发刷新场景)
//   - 超出宽限窗口 → 维持原 reuse detection: 401 token_reuse + 整族撤销 +
//     security_events 落库
//   - grace replay 签发的 access token 能过 verifier, DeviceID = head 行 id
//
// 跟 refresh_e2e_test.go 同 pool 模式: 无本地 PG 自动 skip。

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biumind/biumind/services/identity/internal/token"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// callRefreshWith — 带 XFF / UA 的 refresh 调用。迟恢复 (late grace
// recovery) 测试用它模拟"同端重放"(与轮换请求同 IP/UA) 和"异端重放"。
func callRefreshWith(mux *http.ServeMux, refreshToken, xff, ua string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"refresh_token": refreshToken})
	req := httptest.NewRequest("POST", "/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// ageRevokedRow 把指定 token 行的 revoked_at 改到 1 分钟前, 模拟"超窗重放"
// (10s 宽限窗口之外)。等价于 kill 丢响应后过了很久再重开 app。
func ageRevokedRow(t *testing.T, pool *pgxpool.Pool, refreshToken string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE identity.refresh_tokens
		   SET revoked_at = now() - interval '1 minute'
		 WHERE token_hash = $1
	`, token.Hash(refreshToken)); err != nil {
		t.Fatal(err)
	}
}

// withGrace 给 e2e Server 开 grace replay (key + 10s 窗口)。
func withGrace(s *Server) {
	s.RefreshGraceKey = DeriveGraceKey("e2e-grace-key-material")
	s.RefreshReuseGrace = 10 * time.Second
}

// countSecurityEvents 数该 email 用户的 security_events 行数。
func countSecurityEvents(t *testing.T, pool *pgxpool.Pool, email, kind string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM identity.security_events se
		JOIN identity.users u ON u.id = se.user_id
		WHERE u.email = $1 AND se.kind = $2
	`, email, kind).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestRefresh_E2E_GraceReplay_OldTokenWithinWindow — rotate 成功后立刻重放
// 老 rt: 200, refresh_token 与上一次 rotate 响应相同 (head token 明文找回),
// session_id 相同, 不写 security_events; head token 本身仍可正常 rotate。
func TestRefresh_E2E_GraceReplay_OldTokenWithinWindow(t *testing.T) {
	s, mux, pool := newE2EServer(t, withGrace)
	email := fmt.Sprintf("e2e-grace-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-grace-e2e")

	// 第一次 refresh: 正常 rotate
	rr := callRefresh(mux, rt)
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d %s", rr.Code, rr.Body.String())
	}
	var first refreshResp
	if err := json.NewDecoder(rr.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}

	// 宽限窗口内重放老 rt → 200, 返回 head (跟 first 相同) 的 refresh_token
	rr = callRefresh(mux, rt)
	if rr.Code != http.StatusOK {
		t.Fatalf("grace replay should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var replay refreshResp
	if err := json.NewDecoder(rr.Body).Decode(&replay); err != nil {
		t.Fatal(err)
	}
	if replay.RefreshToken != first.RefreshToken {
		t.Errorf("replay refresh_token differs from head token")
	}
	if replay.SessionID != first.SessionID {
		t.Errorf("replay session_id = %q, want head %q", replay.SessionID, first.SessionID)
	}
	if replay.AccessToken == "" || replay.ExpiresInSeconds <= 0 {
		t.Errorf("replay missing access fields: %+v", replay)
	}

	// 不写 security_events, 不撤族 — head token 仍能正常 rotate
	if n := countSecurityEvents(t, pool, email, "refresh_token_reuse"); n != 0 {
		t.Errorf("grace replay should not write security_events, got %d rows", n)
	}
	rr = callRefresh(mux, first.RefreshToken)
	if rr.Code != http.StatusOK {
		t.Errorf("head token should still rotate after grace replay, got %d body=%s",
			rr.Code, rr.Body.String())
	}
}

// TestRefresh_E2E_GraceExpired_StillTokenReuse — 老行 revoked_at 超过宽限
// 窗口 (SQL 改到 1 分钟前), 且重放来自**异端** (IP 与轮换端不一致) →
// 维持原 reuse 语义: 401 token_reuse + 整族 revoked + security_events 落库。
// (同端超窗重放已被 late grace recovery 接管, 见下方新测试。)
func TestRefresh_E2E_GraceExpired_StillTokenReuse(t *testing.T) {
	s, mux, pool := newE2EServer(t, withGrace)
	email := fmt.Sprintf("e2e-grace-exp-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-grace-exp")

	// 第一次 refresh 成功 (rotate 出 head) — 轮换端 203.0.113.7
	rr := callRefreshWith(mux, rt, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d %s", rr.Code, rr.Body.String())
	}
	var first refreshResp
	_ = json.NewDecoder(rr.Body).Decode(&first)

	// 把老行 revoked_at 改到 1 分钟前 → 超出 10s 宽限窗口
	ageRevokedRow(t, pool, rt)

	// 异端重放 (不同 IP) → 401 token_reuse
	rr = callRefreshWith(mux, rt, "198.51.100.9", "biumind-test/1.0")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expired grace from different IP should 401, got %d body=%s", rr.Code, rr.Body.String())
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "token_reuse" {
		t.Errorf("expected token_reuse, got %q", errResp.Error.Code)
	}

	// 整族撤销: head token 也用不了
	rr = callRefresh(mux, first.RefreshToken)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("head token should be family-revoked (401), got %d", rr.Code)
	}

	// security_events 落库
	if n := countSecurityEvents(t, pool, email, "refresh_token_reuse"); n == 0 {
		t.Error("expected security_events row for refresh_token_reuse")
	}
}

// TestRefresh_E2E_LateGraceRecovery_SameEndpoint — kill 丢响应场景: 超窗
// 重放但链恰好 1 跳 + IP/UA 与轮换端一致 → 200 恢复 (head token 原样找回,
// session_id 不变), 写 info 级 'refresh_token_grace_recovery' 事件,
// **不写** reuse 事件, 不撤族 (head 后续仍可正常 rotate)。
func TestRefresh_E2E_LateGraceRecovery_SameEndpoint(t *testing.T) {
	s, mux, pool := newE2EServer(t, withGrace)
	email := fmt.Sprintf("e2e-late-rec-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-late-rec")

	// 第一次 refresh: 正常 rotate (轮换端 203.0.113.7 / biumind-test/1.0)
	rr := callRefreshWith(mux, rt, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d %s", rr.Code, rr.Body.String())
	}
	var first refreshResp
	if err := json.NewDecoder(rr.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}

	// 模拟 kill 丢响应: 老行 revoked_at 已在 1 分钟前 (超窗)
	ageRevokedRow(t, pool, rt)

	// 同端重放老 rt → 200 恢复, 找回 head
	rr = callRefreshWith(mux, rt, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusOK {
		t.Fatalf("late grace recovery should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var replay refreshResp
	if err := json.NewDecoder(rr.Body).Decode(&replay); err != nil {
		t.Fatal(err)
	}
	if replay.RefreshToken != first.RefreshToken {
		t.Errorf("recovery refresh_token differs from head token")
	}
	if replay.SessionID != first.SessionID {
		t.Errorf("recovery session_id = %q, want head %q", replay.SessionID, first.SessionID)
	}

	// info 级审计落库; 不写 reuse 事件 (banner 不亮), 不撤族
	if n := countSecurityEvents(t, pool, email, "refresh_token_grace_recovery"); n != 1 {
		t.Errorf("expected 1 refresh_token_grace_recovery event, got %d", n)
	}
	if n := countSecurityEvents(t, pool, email, "refresh_token_reuse"); n != 0 {
		t.Errorf("late recovery should not write reuse event, got %d rows", n)
	}
	rr = callRefreshWith(mux, first.RefreshToken, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusOK {
		t.Errorf("head token should still rotate after late recovery, got %d body=%s",
			rr.Code, rr.Body.String())
	}
}

// TestRefresh_E2E_LateGraceRecovery_DifferentUA — 超窗重放, IP 相同但 UA
// 不同 → 同端判定不命中, 维持 reuse detection。
func TestRefresh_E2E_LateGraceRecovery_DifferentUA(t *testing.T) {
	s, mux, pool := newE2EServer(t, withGrace)
	email := fmt.Sprintf("e2e-late-ua-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-late-ua")

	rr := callRefreshWith(mux, rt, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d %s", rr.Code, rr.Body.String())
	}
	ageRevokedRow(t, pool, rt)

	rr = callRefreshWith(mux, rt, "203.0.113.7", "curl/8.0")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("different UA should 401, got %d body=%s", rr.Code, rr.Body.String())
	}
	if n := countSecurityEvents(t, pool, email, "refresh_token_reuse"); n == 0 {
		t.Error("expected security_events row for refresh_token_reuse")
	}
}

// TestRefresh_E2E_LateGraceRecovery_ChainAdvanced — 直接后继已被用来再轮换
// (链 ≥2 跳): 即使 IP/UA 与轮换端一致, 也说明有第二个活跃方 → 维持 reuse
// detection + 撤全族。
func TestRefresh_E2E_LateGraceRecovery_ChainAdvanced(t *testing.T) {
	s, mux, pool := newE2EServer(t, withGrace)
	email := fmt.Sprintf("e2e-late-chain-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-late-chain")

	// rotate A→B, 再用 B rotate B→C (同端) — 从 A 看链已 2 跳
	rr := callRefreshWith(mux, rt, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d %s", rr.Code, rr.Body.String())
	}
	var first refreshResp
	_ = json.NewDecoder(rr.Body).Decode(&first)
	rr = callRefreshWith(mux, first.RefreshToken, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusOK {
		t.Fatalf("second refresh: %d %s", rr.Code, rr.Body.String())
	}
	var second refreshResp
	_ = json.NewDecoder(rr.Body).Decode(&second)

	ageRevokedRow(t, pool, rt)

	// 同端重放 A → 2 跳 → 401 token_reuse + 撤全族
	rr = callRefreshWith(mux, rt, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("2-hop replay should 401, got %d body=%s", rr.Code, rr.Body.String())
	}
	if n := countSecurityEvents(t, pool, email, "refresh_token_reuse"); n == 0 {
		t.Error("expected security_events row for refresh_token_reuse")
	}
	rr = callRefreshWith(mux, second.RefreshToken, "203.0.113.7", "biumind-test/1.0")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("head C should be family-revoked (401), got %d", rr.Code)
	}
}

// TestRefresh_E2E_GraceReplay_AccessTokenValid — grace replay 返回的 access
// token 能过 signer 校验, 且 DeviceID = head 行 id (跟 rotate 响应一致)。
func TestRefresh_E2E_GraceReplay_AccessTokenValid(t *testing.T) {
	s, mux, pool := newE2EServer(t, withGrace)
	email := fmt.Sprintf("e2e-grace-at-%s@test", uuid.NewString()[:8])
	_, rt := loginE2E(t, mux, pool, s, email, "longpassword123", "install-grace-at")

	rr := callRefresh(mux, rt)
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: %d %s", rr.Code, rr.Body.String())
	}
	var first refreshResp
	_ = json.NewDecoder(rr.Body).Decode(&first)

	rr = callRefresh(mux, rt)
	if rr.Code != http.StatusOK {
		t.Fatalf("grace replay should 200, got %d", rr.Code)
	}
	var replay refreshResp
	_ = json.NewDecoder(rr.Body).Decode(&replay)

	claims, err := s.Verifier.Verify(replay.AccessToken)
	if err != nil {
		t.Fatalf("replay access token verify: %v", err)
	}
	if claims.DeviceID != first.SessionID {
		t.Errorf("access DeviceID = %q, want head session %q", claims.DeviceID, first.SessionID)
	}
	if claims.UserID == "" {
		t.Error("access token missing user id")
	}
}
