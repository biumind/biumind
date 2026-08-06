package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/aigc/internal/authz"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 测试用 PG: 复用 biu-postgres (5432).
func dbURL() string {
	if v := os.Getenv("AIGC_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

const testJWTSecret = "aigc-test-secret-32-chars-aaaaaaaa"

func newTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dbURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	t.Cleanup(pool.Close)

	srv := &Server{
		Store:    store.New(pool),
		Authz:    authz.AlwaysAllow{}, // 测试默认放行; 单 deny 用例用 AlwaysDeny override
		Verifier: bauth.NewVerifier(testJWTSecret, "https://identity.biumind.local", "biumind-api"),
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return srv, mux
}

func issueToken(t *testing.T, uid uuid.UUID, plan string, roles []string) string {
	t.Helper()
	signer := bauth.NewSigner(testJWTSecret, "https://identity.biumind.local", "biumind-api", 15*time.Minute)
	tok, err := signer.Sign(&bauth.Claims{
		UserID: uid.String(), Plan: plan, Roles: roles,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// 把测试用 model 写库 (确保 GET /v1/models 能拉到).
func ensureSeedTestModel(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	if err := s.Store.UpsertProvider(ctx, store.UpsertProviderArgs{
		Code: "test-prov", Name: "Test", BaseURL: "https://test.local", Priority: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.UpsertModel(ctx, store.UpsertModelArgs{
		Code: "test-img-model", Type: "image", DisplayName: "Test Image",
		ProviderCode: "test-prov", PriceCredits: 30,
		Config: []byte(`{"aspect_ratios":[]}`), Enabled: true, SortOrder: 99,
	}); err != nil {
		t.Fatal(err)
	}
}

// ════════════════════════════════════════════════════════════
// /v1/models
// ════════════════════════════════════════════════════════════

func TestListModels_Public(t *testing.T) {
	srv, mux := newTestServer(t)
	ensureSeedTestModel(t, srv)

	req := httptest.NewRequest("GET", "/v1/models?type=image", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range resp.Models {
		if m["code"] == "test-img-model" {
			found = true
			if int(m["price_credits"].(float64)) != 30 {
				t.Errorf("price_credits wrong: %v", m["price_credits"])
			}
		}
	}
	if !found {
		t.Errorf("test model missing from list: %+v", resp.Models)
	}
}

func TestListModels_BadType(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/models?type=invalid", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// ════════════════════════════════════════════════════════════
// /v1/providers (admin)
// ════════════════════════════════════════════════════════════

// P4.S3.6: TestListProviders_* 已删 — /v1/providers 端点下线, provider
// 字典统一在 model-relay 的 /v1/admin/providers (admin Vue 单源).

// ════════════════════════════════════════════════════════════
// /v1/gallery
// ════════════════════════════════════════════════════════════

func TestListGallery_Public(t *testing.T) {
	srv, mux := newTestServer(t)
	ensureSeedTestModel(t, srv)
	ctx := context.Background()
	uid := uuid.New()
	// 清自己上次留的脏数据
	_, _ = srv.Store.Pool().Exec(ctx, `DELETE FROM aigc.tasks WHERE user_id = $1`, uid)

	// 写 1 个公开 completed
	tk, err := srv.Store.CreateTask(ctx, store.CreateTaskArgs{
		UserID: uid, Type: "image",
		ModelCode: "test-img-model", ProviderCode: "test-prov",
		Prompt: "测试公开柯基", CostCredits: 30, IsPublic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	prog := int16(100)
	_ = srv.Store.UpdateTaskStatus(ctx, store.UpdateTaskStatusArgs{
		ID: tk.ID, Status: "completed", Progress: &prog, CompletedAt: &now,
	})
	_, _ = srv.Store.CreateTaskOutput(ctx, store.CreateTaskOutputArgs{
		TaskID: tk.ID, Idx: 0, Kind: "image",
		SHA256: "sha-gallery-1", StorageURL: "cas:sha-gallery-1",
		StorageKey: "outputs/sh/a/sha-gallery-1.png",
		Width:      1024, Height: 1024,
	})

	req := httptest.NewRequest("GET", "/v1/gallery?keyword=测试公开", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	found := false
	for _, it := range resp.Items {
		if strings.Contains(it["prompt"].(string), "测试公开") {
			found = true
			outs, _ := it["outputs"].([]any)
			if len(outs) == 0 {
				t.Errorf("outputs empty")
			}
		}
	}
	if !found {
		t.Errorf("public task not in gallery: %+v", resp.Items)
	}
}

func TestListGallery_BadType(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/gallery?type=bogus", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
