package api

// settings_test.go — /v1/identity/me/settings/ingest-model 端点集成测试.
// 走真 DB (SETTINGS_TEST_DATABASE_URL 覆盖; "skip" 跳过; 默认 dev 栈
// localhost:15432). 鉴权路径 / 读写 / 校验 / 清除语义全覆盖.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/settings"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const settingsTestSecret = "settings-test-secret-very-long-string-32"

func settingsDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SETTINGS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biumind?sslmode=disable"
	}
	if dsn == "skip" {
		t.Skip("SETTINGS_TEST_DATABASE_URL=skip")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG ping fail: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newSettingsTestServer(t *testing.T) (*http.ServeMux, *bauth.Signer, uuid.UUID) {
	t.Helper()
	pool := settingsDB(t)
	signer := bauth.NewSigner(settingsTestSecret, "iss", "aud", 15*time.Minute)
	verifier := bauth.NewVerifier(settingsTestSecret, "iss", "aud")
	s := &Server{
		Signer:    signer,
		Verifier:  verifier,
		Settings:  settings.NewStore(pool),
		AccessTTL: time.Minute,
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	// user_settings FK 到 identity.users — 建真用户, 测完删.
	uid := uuid.New()
	email := "settings-api-test-" + uid.String()[:8] + "@test.local"
	_, err := pool.Exec(context.Background(),
		`INSERT INTO identity.users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		uid, email)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.users WHERE id = $1`, uid)
	})
	return mux, signer, uid
}

func settingsToken(t *testing.T, signer *bauth.Signer, uid uuid.UUID) string {
	t.Helper()
	tok, err := signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func settingsReq(t *testing.T, mux *http.ServeMux, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeModel(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rr.Body.String())
	}
	return out.Model
}

const ingestModelPath = "/v1/identity/me/settings/ingest-model"

func TestIngestModel_RequiresAuth(t *testing.T) {
	mux, _, _ := newSettingsTestServer(t)
	if rr := settingsReq(t, mux, "GET", ingestModelPath, nil, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET no token: got %d want 401", rr.Code)
	}
	if rr := settingsReq(t, mux, "PUT", ingestModelPath, map[string]any{"model": "x"}, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("PUT no token: got %d want 401", rr.Code)
	}
}

func TestIngestModel_ReadWriteClear(t *testing.T) {
	mux, signer, uid := newSettingsTestServer(t)
	tok := settingsToken(t, signer, uid)

	// 未设置 → 200 {"model":""}
	rr := settingsReq(t, mux, "GET", ingestModelPath, nil, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET unset: %d body=%s", rr.Code, rr.Body.String())
	}
	if m := decodeModel(t, rr); m != "" {
		t.Fatalf("GET unset model = %q, want empty", m)
	}

	// PUT 设置 → GET 一致
	rr = settingsReq(t, mux, "PUT", ingestModelPath, map[string]any{"model": "claude-sonnet-4-6"}, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT: %d body=%s", rr.Code, rr.Body.String())
	}
	if m := decodeModel(t, rr); m != "claude-sonnet-4-6" {
		t.Fatalf("PUT resp model = %q", m)
	}
	rr = settingsReq(t, mux, "GET", ingestModelPath, nil, tok)
	if m := decodeModel(t, rr); m != "claude-sonnet-4-6" {
		t.Fatalf("GET after PUT model = %q", m)
	}

	// PUT 覆盖
	settingsReq(t, mux, "PUT", ingestModelPath, map[string]any{"model": "anthropic/claude-opus-4-8"}, tok)
	rr = settingsReq(t, mux, "GET", ingestModelPath, nil, tok)
	if m := decodeModel(t, rr); m != "anthropic/claude-opus-4-8" {
		t.Fatalf("GET after overwrite model = %q", m)
	}

	// PUT 空串 = 清除 → GET 回空
	rr = settingsReq(t, mux, "PUT", ingestModelPath, map[string]any{"model": ""}, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT clear: %d", rr.Code)
	}
	rr = settingsReq(t, mux, "GET", ingestModelPath, nil, tok)
	if m := decodeModel(t, rr); m != "" {
		t.Fatalf("GET after clear model = %q, want empty", m)
	}
}

func TestIngestModel_Validation(t *testing.T) {
	mux, signer, uid := newSettingsTestServer(t)
	tok := settingsToken(t, signer, uid)

	// 非法字符 → 400 invalid_model
	for _, bad := range []string{"model with space", "中文模型", "a;b", "x\ny", "a$b"} {
		rr := settingsReq(t, mux, "PUT", ingestModelPath, map[string]any{"model": bad}, tok)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("PUT %q: got %d want 400", bad, rr.Code)
		}
	}

	// 超长 (>200) → 400
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	rr := settingsReq(t, mux, "PUT", ingestModelPath, map[string]any{"model": string(long)}, tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT long: got %d want 400", rr.Code)
	}

	// 合法字符集全量: 字母数字 . - _ : /
	rr = settingsReq(t, mux, "PUT", ingestModelPath,
		map[string]any{"model": "Provider/Sub.Model_Name-01:v2"}, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT all-legal-chars: got %d body=%s", rr.Code, rr.Body.String())
	}

	// bad json → 400
	req := httptest.NewRequest("PUT", ingestModelPath, bytes.NewReader([]byte("{bad")))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad json: got %d want 400", rr.Code)
	}
}

func TestIngestModel_UserIsolation(t *testing.T) {
	mux, signer, uid := newSettingsTestServer(t)
	tokA := settingsToken(t, signer, uid)

	// A 设置后, 另一个用户 B 读不到 (B 用户直接在 DB 建)
	pool := settingsDB(t)
	uidB := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO identity.users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		uidB, "settings-api-test-b-"+uidB.String()[:8]+"@test.local")
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity.users WHERE id = $1`, uidB)
	})
	tokB := settingsToken(t, signer, uidB)

	settingsReq(t, mux, "PUT", ingestModelPath, map[string]any{"model": "a-model"}, tokA)
	rr := settingsReq(t, mux, "GET", ingestModelPath, nil, tokB)
	if m := decodeModel(t, rr); m != "" {
		t.Fatalf("user B sees A's setting: %q", m)
	}
}
