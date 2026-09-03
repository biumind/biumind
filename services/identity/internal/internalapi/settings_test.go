package internalapi

// settings_test.go — GET /v1/internal/settings/{user_id}/ingest-model 集成测试.
// 走真 DB (SETTINGS_TEST_DATABASE_URL 覆盖; "skip" 跳过; 默认 dev 栈
// localhost:15432). 覆盖: token 鉴权 / 200 命中 / 404 未设置 / 400 bad user_id.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/biumind/biumind/services/identity/internal/settings"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func settingsInternalDB(t *testing.T) *pgxpool.Pool {
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

func newSettingsInternalServer(t *testing.T, token string) (*httptest.Server, uuid.UUID) {
	t.Helper()
	pool := settingsInternalDB(t)
	st := settings.NewStore(pool)

	mux := http.NewServeMux()
	srv := New(token, nil)
	srv.MountSettings(mux, st)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// user_settings FK 到 identity.users — 建真用户, 测完删.
	uid := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO identity.users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		uid, "settings-internal-test-"+uid.String()[:8]+"@test.local")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.users WHERE id = $1`, uid)
	})
	return ts, uid
}

func TestInternalSettings_Auth(t *testing.T) {
	ts, uid := newSettingsInternalServer(t, "secret")

	// 无 token → 401
	resp := get(t, ts, "/v1/internal/settings/"+uid.String()+"/ingest-model", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d want 401", resp.StatusCode)
	}
	// 错 token → 401
	resp = get(t, ts, "/v1/internal/settings/"+uid.String()+"/ingest-model", "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: %d want 401", resp.StatusCode)
	}
}

func TestInternalSettings_NotFound404(t *testing.T) {
	ts, uid := newSettingsInternalServer(t, "secret")
	// 未设置 → 404 (与 BYOK 未配置语义一致, 消费方回落默认模型)
	resp := get(t, ts, "/v1/internal/settings/"+uid.String()+"/ingest-model", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unset: %d want 404", resp.StatusCode)
	}
}

func TestInternalSettings_HappyPath(t *testing.T) {
	ts, uid := newSettingsInternalServer(t, "secret")

	// 直接写 store, 经端点读回
	pool := settingsInternalDB(t)
	if err := settings.NewStore(pool).SetIngestModel(context.Background(), uid, "claude-sonnet-4-6"); err != nil {
		t.Fatalf("set: %v", err)
	}
	resp := get(t, ts, "/v1/internal/settings/"+uid.String()+"/ingest-model", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q", body.Model)
	}
}

func TestInternalSettings_BadUserID(t *testing.T) {
	ts, _ := newSettingsInternalServer(t, "secret")
	resp := get(t, ts, "/v1/internal/settings/not-a-uuid/ingest-model", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad user_id: %d want 400", resp.StatusCode)
	}
}
