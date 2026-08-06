package agentplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	tokTestSecret   = "biumind-tokens-test-secret-32-chars-please-yes"
	tokTestIssuer   = "https://identity.test"
	tokTestAudience = "biumind-api"
)

func newTokenTestSigner() *bauth.Signer {
	return bauth.NewSigner(tokTestSecret, tokTestIssuer, tokTestAudience, SessionTokenTTL)
}
func newTokenTestVerifier() *bauth.Verifier {
	return bauth.NewVerifier(tokTestSecret, tokTestIssuer, tokTestAudience)
}

func TestTokens_IssueVerify(t *testing.T) {
	signer := newTokenTestSigner()
	verifier := newTokenTestVerifier()

	uid := uuid.New()
	sid := uuid.New()
	tok, expiresAt, err := IssueSessionToken(signer, uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	// expires_at 应该 ≈ now + SessionTokenTTL（容忍 ±5s）
	wantExp := time.Now().Add(SessionTokenTTL)
	delta := expiresAt.Sub(wantExp)
	if delta < -5*time.Second || delta > 5*time.Second {
		t.Errorf("expires_at offset %v, want ~0", delta)
	}

	claims, err := VerifySessionToken(verifier, tok, sid)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserID != uid.String() {
		t.Errorf("user_id=%q want %q", claims.UserID, uid)
	}
	// scope 必含 session:<id>
	var sawSessionScope bool
	for _, s := range claims.Scope {
		if strings.HasPrefix(s, scopeSessionPrefix) {
			sawSessionScope = true
		}
	}
	if !sawSessionScope {
		t.Errorf("scope missing session: prefix; got %v", claims.Scope)
	}
}

func TestTokens_Verify_NoSessionScope(t *testing.T) {
	// 直接用同一 signer 签个 user-only token（无 session scope）—— 拿来当
	// session_token 用应该被拒。
	signer := newTokenTestSigner()
	verifier := newTokenTestVerifier()

	tok, err := signer.Sign(&bauth.Claims{UserID: uuid.New().String()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = VerifySessionToken(verifier, tok, uuid.New())
	if !errors.Is(err, ErrSessionScopeMissing) {
		t.Errorf("got %v, want ErrSessionScopeMissing", err)
	}
}

func TestTokens_Verify_ScopeMismatch(t *testing.T) {
	signer := newTokenTestSigner()
	verifier := newTokenTestVerifier()

	uid := uuid.New()
	sidA := uuid.New()
	sidB := uuid.New()

	tok, _, _ := IssueSessionToken(signer, uid, sidA)
	_, err := VerifySessionToken(verifier, tok, sidB)
	if !errors.Is(err, ErrSessionScopeMismatch) {
		t.Errorf("got %v, want ErrSessionScopeMismatch", err)
	}
}

func TestTokens_Verify_BadSignature(t *testing.T) {
	// 用一个 secret 签，另一个 secret 验
	signerA := bauth.NewSigner(tokTestSecret, tokTestIssuer, tokTestAudience, SessionTokenTTL)
	verifierB := bauth.NewVerifier("different-secret-32-chars-still-valid", tokTestIssuer, tokTestAudience)

	tok, _, _ := IssueSessionToken(signerA, uuid.New(), uuid.New())
	_, err := VerifySessionToken(verifierB, tok, uuid.Nil)
	if err == nil {
		t.Error("verify with wrong secret should fail")
	}
}

func TestTokens_Issue_NilSigner(t *testing.T) {
	_, _, err := IssueSessionToken(nil, uuid.New(), uuid.New())
	if err == nil {
		t.Error("nil signer should error")
	}
}

func TestTokens_Verify_NilVerifier(t *testing.T) {
	_, err := VerifySessionToken(nil, "anything", uuid.Nil)
	if err == nil {
		t.Error("nil verifier should error")
	}
}

// ── HTTP refresh endpoint 集成测试（DATABASE_URL 必需） ─────────

// TestAPI_RefreshSessionToken 走完整流程：seed agent_environments + agent_sessions
// → 调 refresh → 验 session_token 用 VerifySessionToken 能解
func TestAPI_RefreshSessionToken(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)

	// seed environment + session（直接走 store）
	env, err := h.store.RegisterEnvironment(context.Background(), CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	envID := env.EnvironmentID
	sess, err := h.store.InsertSession(context.Background(), CreateSessionReq{
		UserID:        uid,
		EnvironmentID: &envID,
		Mode:          "agent",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 调 refresh
	resp := h.req(t, "POST", "/v1/agent/sessions/"+sess.SessionID.String()+"/refresh-token", tok, nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]any](t, resp)
	sessionTok, _ := got["session_token"].(string)
	if sessionTok == "" {
		t.Fatal("missing session_token")
	}

	// 用 VerifySessionToken 校验返回的 token
	verifier := bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience)
	claims, err := VerifySessionToken(verifier, sessionTok, sess.SessionID)
	if err != nil {
		t.Fatalf("returned token does not verify: %v", err)
	}
	if claims.UserID != uid.String() {
		t.Errorf("session_token user_id=%q want %q", claims.UserID, uid)
	}
}

func TestAPI_RefreshSessionToken_CrossUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	// user A 的 session
	uidA := uuid.New()
	sessA, _ := h.store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uidA, Mode: "chat",
	})

	// user B 试 refresh A 的 session_token —— 404
	uidB := uuid.New()
	resp := h.req(t, "POST", "/v1/agent/sessions/"+sessA.SessionID.String()+"/refresh-token",
		h.mintToken(uidB), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user refresh = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_RefreshSessionToken_NotFound(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/sessions/"+uuid.New().String()+"/refresh-token",
		h.mintToken(uuid.New()), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("nonexistent session refresh = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_RefreshSessionToken_BadID(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/sessions/not-a-uuid/refresh-token",
		h.mintToken(uuid.New()), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad uuid = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_RefreshSessionToken_NoSigner(t *testing.T) {
	// 用 NewServer 显式传 nil signer，测 503 路径
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, _ = pool.Exec(context.Background(),
		`TRUNCATE agent_environments, agent_sessions, agent_session_results CASCADE`)

	store := NewStore(pool)
	srv := NewServer(store,
		bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		nil, // ← 重点：nil signer
		nil, // queue
		nil, // ingress
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	uid := uuid.New()
	signer := bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, time.Hour)
	signTok, _ := signer.Sign(&bauth.Claims{UserID: uid.String()})

	// seed session 让前置查通过；signer 检查在 GetSession 之前
	sess, _ := store.InsertSession(context.Background(), CreateSessionReq{
		UserID: uid, Mode: "chat",
	})

	req, _ := http.NewRequest("POST",
		ts.URL+"/v1/agent/sessions/"+sess.SessionID.String()+"/refresh-token", nil)
	req.Header.Set("Authorization", "Bearer "+signTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("nil signer = %d, want 503", resp.StatusCode)
	}
}
