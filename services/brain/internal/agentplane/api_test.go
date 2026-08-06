// Agent Plane API 测试。需要 DATABASE_URL 指到一个跑过 migration 的
// Postgres，否则 skip（跟 services/brain/internal/code/api_test.go 同约定）。

package agentplane

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/agentcrypto"
	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	testJWTSecret   = "biumind-agentplane-test-secret-32-chars-pls"
	testJWTIssuer   = "https://identity.test"
	testJWTAudience = "biumind-api"
)

type apiHarness struct {
	server *httptest.Server
	signer *bauth.Signer
	pool   *pgxpool.Pool
	store  *Store
	// fakeQueue 让 createAgent/Task session 跑得通，并暴露 publishes 供
	// 集成测试断言。S3-6 引入。
	fakeQueue *fakeJSForAPI
}

// fakeJSForAPI 是 fakeJS 的拷贝（避免 _test.go 跨文件用），让 api_test
// 也能持有一个 mock JetStream 让 Queue.Enqueue 跑通而不真发 NATS。
type fakeJSForAPI struct {
	publishes []fakePublish
}

func (f *fakeJSForAPI) EnsureStream(_ context.Context, _ bus.StreamSpec) error { return nil }
func (f *fakeJSForAPI) Publish(_ context.Context, subject string, payload any, headers ...bus.Header) error {
	f.publishes = append(f.publishes, fakePublish{Subject: subject, Payload: payload, Headers: headers})
	return nil
}
func (f *fakeJSForAPI) Subscribe(_ context.Context, _ bus.ConsumerSpec, _ bus.JSHandler) (bus.Subscription, error) {
	return nil, nil
}
func (f *fakeJSForAPI) RawJetStream() jetstream.JetStream { return nil }

func (h *apiHarness) close() {
	h.server.Close()
	h.pool.Close()
}

func (h *apiHarness) mintToken(uid uuid.UUID) string {
	tok, err := h.signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		panic(err)
	}
	return tok
}

func (h *apiHarness) req(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, h.server.URL+path, buf)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	return resp
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping agentplane integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	// Truncate 表保证测试隔离。CASCADE 因为 sessions / results 有 FK。
	// R6.3：含 agent_devices / agent_pairings，否则 device-token 测试的固定
	// token_hash 跨 run 撞 UNIQUE 约束。
	_, _ = pool.Exec(context.Background(),
		`TRUNCATE agent_environments, agent_sessions, agent_session_results, agent_devices, agent_pairings CASCADE`)

	store := NewStore(pool)
	fakeJS := &fakeJSForAPI{}
	srv := &Server{
		Store:    store,
		Verifier: bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		Signer:   bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, SessionTokenTTL),
		Queue:    NewQueue(fakeJS), // mock JetStream，让 agent/task mode 跑通
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return &apiHarness{
		server:    httptest.NewServer(mux),
		signer:    bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, 5*time.Minute),
		pool:      pool,
		store:     store,
		fakeQueue: fakeJS,
	}
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

// ── Auth 边界 ───────────────────────────────────────────────

func TestAPI_RequireBearer(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/environments", "", map[string]any{
		"worker_kind": "biu_daemon", "machine_name": "x",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing bearer: status=%d want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── Register ───────────────────────────────────────────────

func TestAPI_Register_Success(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)

	resp := h.req(t, "POST", "/v1/agent/environments", tok, map[string]any{
		"worker_kind":  "biu_daemon",
		"machine_name": "didi-mbp",
		"os_arch":      "darwin/arm64",
		"capabilities": []string{"sandbox", "mcp:supabase"},
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["environment_id"] == "" {
		t.Errorf("missing environment_id")
	}
	if got["state"] != "online" {
		t.Errorf("state=%v want online", got["state"])
	}
	if got["worker_kind"] != "biu_daemon" {
		t.Errorf("worker_kind=%v", got["worker_kind"])
	}
	if got["user_id"] != uid.String() {
		t.Errorf("user_id=%v want %s", got["user_id"], uid)
	}
}

// TestAPI_Register_StoresPublicKeyRaw（R6.2）：daemon 以 hex 上报 X25519
// pubkey，brain 必须 hex-decode 成 raw 32B 落库（而非把 hex 串字节直接存）。
// 直查真 DB 的 BYTEA 长度做实证——等价于 daemon-run + psql 检查、可复现。
func TestAPI_Register_StoresPublicKeyRaw(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	priv, pub, err := agentcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	pubHex := hex.EncodeToString(pub)

	resp := h.req(t, "POST", "/v1/agent/environments", h.mintToken(uid), map[string]any{
		"worker_kind":  "biu_daemon",
		"machine_name": "pubkey-test",
		"public_key":   pubHex,
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	envID := decodeJSON[map[string]any](t, resp)["environment_id"].(string)

	var raw []byte
	err = h.pool.QueryRow(context.Background(),
		`SELECT public_key FROM agent_environments WHERE environment_id = $1`, envID).Scan(&raw)
	if err != nil {
		t.Fatalf("query public_key: %v", err)
	}
	if len(raw) != agentcrypto.X25519KeySize {
		t.Fatalf("stored public_key len=%d want %d (hex string was stored instead of raw?)",
			len(raw), agentcrypto.X25519KeySize)
	}
	if !bytes.Equal(raw, pub) {
		t.Fatalf("stored public_key != reported pubkey")
	}
	// 闭环：用存的 pubkey 加密、用对应 privkey 解密，验证落库的 key 真能用。
	ct, err := agentcrypto.EncryptForWorker(raw, []byte("ping"))
	if err != nil {
		t.Fatalf("encrypt with stored key: %v", err)
	}
	pt, err := agentcrypto.DecryptWithPrivkey(priv, ct)
	if err != nil || string(pt) != "ping" {
		t.Fatalf("roundtrip with stored key failed: pt=%q err=%v", pt, err)
	}
}

func TestAPI_Register_BadWorkerKind(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/environments", h.mintToken(uuid.New()),
		map[string]any{"worker_kind": "evil_bot", "machine_name": "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPI_Register_RuntimeStoresUserID(t *testing.T) {
	// S11-1 起 runtime worker 注册的 DB user_id 跟调用方 JWT 一致
	// （admin / 系统账号 uuid）。Pool 选择不按 user_id 过滤所以共享池
	// 语义保留；admin list 时能看见所有 runtime —— 运维需要观察哪些
	// runtime 在线。早期"runtime user_id=NULL"政策让 heartbeat / delete
	// 路径 user_id 匹配失败，撤销。
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	resp := h.req(t, "POST", "/v1/agent/environments", h.mintToken(uid),
		map[string]any{
			"worker_kind":  "runtime",
			"machine_name": "runtime-7fbc6dc9b8-x9p2k",
			"pool_tag":     "runtime-prod",
		})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]any](t, resp)
	if got["pool_tag"] != "runtime-prod" {
		t.Errorf("pool_tag=%v", got["pool_tag"])
	}
}

// ── Heartbeat ──────────────────────────────────────────────

func TestAPI_Heartbeat(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)

	// register
	resp := h.req(t, "POST", "/v1/agent/environments", tok,
		map[string]any{"worker_kind": "biu_daemon", "machine_name": "x"})
	got := decodeJSON[map[string]any](t, resp)
	envID := got["environment_id"].(string)

	// heartbeat
	hbResp := h.req(t, "POST", "/v1/agent/environments/"+envID+"/heartbeat", tok, nil)
	if hbResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(hbResp.Body)
		t.Fatalf("heartbeat status=%d body=%s", hbResp.StatusCode, body)
	}
	hbResp.Body.Close()

	// last_seen_at 应该 ≥ created_at（实际可能等于；用 store 直接查避免序列化损失）
	envUUID, _ := uuid.Parse(envID)
	env, err := h.store.GetEnvironment(context.Background(), uid, envUUID)
	if err != nil {
		t.Fatal(err)
	}
	if env.LastSeenAt.Before(env.CreatedAt) {
		t.Errorf("last_seen_at %v < created_at %v", env.LastSeenAt, env.CreatedAt)
	}
}

func TestAPI_Heartbeat_CrossUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	// user A 注册
	uidA := uuid.New()
	resp := h.req(t, "POST", "/v1/agent/environments", h.mintToken(uidA),
		map[string]any{"worker_kind": "biu_daemon", "machine_name": "x"})
	got := decodeJSON[map[string]any](t, resp)
	envID := got["environment_id"].(string)

	// user B 试 heartbeat 同一 env_id —— 应该 404（store 的 user_id strict match）
	uidB := uuid.New()
	hbResp := h.req(t, "POST", "/v1/agent/environments/"+envID+"/heartbeat",
		h.mintToken(uidB), nil)
	if hbResp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user heartbeat = %d, want 404", hbResp.StatusCode)
	}
	hbResp.Body.Close()
}

func TestAPI_Heartbeat_NotFound(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "POST", "/v1/agent/environments/"+uuid.New().String()+"/heartbeat",
		h.mintToken(uuid.New()), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("nonexistent heartbeat = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── List ───────────────────────────────────────────────────

func TestAPI_List(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)
	for _, name := range []string{"laptop", "desktop"} {
		_ = h.req(t, "POST", "/v1/agent/environments", tok,
			map[string]any{"worker_kind": "biu_daemon", "machine_name": name})
	}

	resp := h.req(t, "GET", "/v1/agent/environments", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	got := decodeJSON[map[string]any](t, resp)
	envs := got["environments"].([]any)
	if len(envs) != 2 {
		t.Errorf("got %d environments, want 2", len(envs))
	}
}

func TestAPI_List_OnlyOwnUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	// user A 注册一个
	uidA := uuid.New()
	_ = h.req(t, "POST", "/v1/agent/environments", h.mintToken(uidA),
		map[string]any{"worker_kind": "biu_daemon", "machine_name": "a"})

	// user B 注册一个
	uidB := uuid.New()
	_ = h.req(t, "POST", "/v1/agent/environments", h.mintToken(uidB),
		map[string]any{"worker_kind": "biu_daemon", "machine_name": "b"})

	// user A 列表 —— 只看到自己的
	resp := h.req(t, "GET", "/v1/agent/environments", h.mintToken(uidA), nil)
	got := decodeJSON[map[string]any](t, resp)
	envs := got["environments"].([]any)
	if len(envs) != 1 {
		t.Errorf("user A sees %d, want 1", len(envs))
	}
	first := envs[0].(map[string]any)
	if first["machine_name"] != "a" {
		t.Errorf("expected machine_name=a, got %v", first["machine_name"])
	}
}

// ── Delete ────────────────────────────────────────────────

func TestAPI_Delete(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	uid := uuid.New()
	tok := h.mintToken(uid)

	resp := h.req(t, "POST", "/v1/agent/environments", tok,
		map[string]any{"worker_kind": "biu_daemon", "machine_name": "x"})
	got := decodeJSON[map[string]any](t, resp)
	envID := got["environment_id"].(string)

	delResp := h.req(t, "DELETE", "/v1/agent/environments/"+envID, tok, nil)
	if delResp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(delResp.Body)
		t.Fatalf("delete status=%d body=%s", delResp.StatusCode, body)
	}
	delResp.Body.Close()

	// 删除后 heartbeat 应当 404
	hbResp := h.req(t, "POST", "/v1/agent/environments/"+envID+"/heartbeat", tok, nil)
	if hbResp.StatusCode != http.StatusNotFound {
		t.Errorf("heartbeat after delete = %d, want 404", hbResp.StatusCode)
	}
	hbResp.Body.Close()
}

func TestAPI_Delete_BadID(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()

	resp := h.req(t, "DELETE", "/v1/agent/environments/not-a-uuid",
		h.mintToken(uuid.New()), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
