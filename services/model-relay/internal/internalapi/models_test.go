// Tests for GET /v1/internal/models/default-chat (Phase B).
//
// DB-backed (skips when DATABASE_URL is unset, same convention as
// server_test.go). A fresh registry.Cache is built per assertion —
// NewCache starts dirty so the first read reloads from PG, which
// sidesteps LISTEN/NOTIFY timing in tests.

package internalapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// freshCacheServer wires a Server whose Cache is cold (dirty → first
// read hits PG). Cache.Start is intentionally not called; Close on an
// unstarted cache is a no-op.
func freshCacheServer(store *registry.Store) *Server {
	cache := registry.NewCache(store, registry.CacheConfig{TTL: time.Minute})
	return &Server{Token: testToken, Cache: cache}
}

func getDefaultChat(t *testing.T, srv *Server, auth bool) (int, []byte) {
	t.Helper()
	mux := http.NewServeMux()
	srv.MountModels(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/internal/models/default-chat", nil)
	if auth {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestDefaultChatModelEndpoint(t *testing.T) {
	pool := openDB(t)
	store := registry.NewStore(pool)
	ctx := context.Background()

	// Serialize against other default-chat tests on this shared dev DB —
	// the "one default globally" invariant means a concurrent test that
	// sets a new default silently clears this one.
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire lock conn: %v", err)
	}
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock(20260810)"); err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	t.Cleanup(func() {
		lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(20260810)") //nolint:errcheck
		lockConn.Release()
	})

	code := fmt.Sprintf("m_defchat_%d", time.Now().UnixNano())
	m, err := store.Models.Insert(ctx, registry.ModelInput{
		Code: code, DisplayName: "Default Chat Test",
		MinPlan: registry.PlanFree, Status: registry.StatusActive,
		Mode: registry.ModeChat, IsDefaultChat: true,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", m.ID)    //nolint:errcheck
	defer pool.Exec(ctx, "UPDATE model_relay.models SET is_default_chat=false") //nolint:errcheck

	if !m.IsDefaultChat {
		t.Fatalf("insert did not roundtrip is_default_chat")
	}

	// 200 — active default returns its code.
	status, body := getDefaultChat(t, freshCacheServer(store), true)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if out.Code != code {
		t.Fatalf("code = %q, want %q", out.Code, code)
	}

	// 401 — same bearer middleware as /v1/internal/chat.
	status, _ = getDefaultChat(t, freshCacheServer(store), false)
	if status != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", status)
	}

	// 404 — deactivated default is treated as "no default".
	if err := store.Models.SetStatus(ctx, m.ID, registry.StatusDisabled); err != nil {
		t.Fatalf("set status: %v", err)
	}
	status, body = getDefaultChat(t, freshCacheServer(store), true)
	if status != http.StatusNotFound {
		t.Fatalf("disabled default: status = %d, body = %s, want 404", status, body)
	}

	// 404 — flag cleared entirely.
	if _, err := pool.Exec(ctx,
		"UPDATE model_relay.models SET is_default_chat=false WHERE id=$1", m.ID); err != nil {
		t.Fatalf("clear default: %v", err)
	}
	status, body = getDefaultChat(t, freshCacheServer(store), true)
	if status != http.StatusNotFound {
		t.Fatalf("no default: status = %d, body = %s, want 404", status, body)
	}
}

func TestDefaultChatModelCacheNotWired(t *testing.T) {
	srv := &Server{Token: testToken, Cache: nil}
	status, _ := getDefaultChat(t, srv, true)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("nil cache: status = %d, want 503", status)
	}
}

// GET /v1/internal/models/preferred-chat — auto-picks the best usable
// chat model (mode=chat + status=active, sort_order ASC / code ASC).
// The dev DB is shared, so expectations are computed via SQL rather
// than pinned to the rows this test inserts.
func TestPreferredChatModelEndpoint(t *testing.T) {
	pool := openDB(t)
	store := registry.NewStore(pool)
	ctx := context.Background()

	// Excluded rows: a disabled chat model and an active non-chat model,
	// both with a sort_order low enough to win if the filter were wrong.
	suffix := time.Now().UnixNano()
	disabled, err := store.Models.Insert(ctx, registry.ModelInput{
		Code: fmt.Sprintf("m_pref_dis_%d", suffix), DisplayName: "Pref Disabled",
		MinPlan: registry.PlanFree, Status: registry.StatusDisabled,
		Mode: registry.ModeChat, SortOrder: -1_000_000,
	})
	if err != nil {
		t.Fatalf("insert disabled: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", disabled.ID) //nolint:errcheck

	nonChat, err := store.Models.Insert(ctx, registry.ModelInput{
		Code: fmt.Sprintf("m_pref_img_%d", suffix), DisplayName: "Pref Image",
		MinPlan: registry.PlanFree, Status: registry.StatusActive,
		Mode: registry.ModeImageGeneration, SortOrder: -1_000_001,
	})
	if err != nil {
		t.Fatalf("insert non-chat: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", nonChat.ID) //nolint:errcheck

	// Expected winner straight from the table — same rule the cache
	// applies in memory.
	var want string
	err = pool.QueryRow(ctx, `SELECT code FROM model_relay.models
		WHERE mode='chat' AND status='active'
		ORDER BY sort_order ASC, code ASC LIMIT 1`).Scan(&want)
	noUsable := err != nil // sql.ErrNoRows → 404 case
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected query: %v", err)
	}

	get := func(auth bool) (int, []byte) {
		t.Helper()
		mux := http.NewServeMux()
		srv := freshCacheServer(store)
		srv.MountModels(mux)
		ts := httptest.NewServer(mux)
		defer ts.Close()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/internal/models/preferred-chat", nil)
		if auth {
			req.Header.Set("Authorization", "Bearer "+testToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}

	// 401 — same bearer middleware as default-chat.
	if status, _ := get(false); status != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", status)
	}

	status, body := get(true)
	if noUsable {
		if status != http.StatusNotFound {
			t.Fatalf("no usable chat model: status = %d, body = %s, want 404", status, body)
		}
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if out.Code != want {
		t.Fatalf("code = %q, want %q (excluded rows must not win)", out.Code, want)
	}
	if out.Code == disabled.Code || out.Code == nonChat.Code {
		t.Fatalf("excluded model picked: %q", out.Code)
	}
}

func TestPreferredChatModelCacheNotWired(t *testing.T) {
	srv := &Server{Token: testToken, Cache: nil}
	mux := http.NewServeMux()
	srv.MountModels(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/internal/models/preferred-chat", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("nil cache: status = %d, want 503", resp.StatusCode)
	}
}

// GET /v1/internal/models/preferred — 泛化优选端点: mode 必填(400),
// capability=vision 过滤掉无视觉能力的 chat 模型。期望值用 SQL 直算
// (共享 dev 库)。
func TestPreferredModelEndpoint(t *testing.T) {
	pool := openDB(t)
	store := registry.NewStore(pool)
	ctx := context.Background()

	// 一个无视觉能力、sort_order 极低的 chat 模型 —— capability=vision
	// 查询必须跳过它。
	suffix := time.Now().UnixNano()
	blind, err := store.Models.Insert(ctx, registry.ModelInput{
		Code: fmt.Sprintf("m_pref_novision_%d", suffix), DisplayName: "No Vision",
		MinPlan: registry.PlanFree, Status: registry.StatusActive,
		Mode: registry.ModeChat, SortOrder: -1_000_002,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", blind.ID) //nolint:errcheck

	mux := http.NewServeMux()
	freshCacheServer(store).MountModels(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	get := func(path string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}

	// 400 — mode 必填。
	if status, _ := get("/v1/internal/models/preferred"); status != http.StatusBadRequest {
		t.Fatalf("no mode: status = %d, want 400", status)
	}

	// capability=vision — 结果必须是 active chat 且 capabilities.vision=true
	// 里 sort_order 最小者;刚插入的无视觉模型不能赢。
	var want string
	err = pool.QueryRow(ctx, `SELECT code FROM model_relay.models
		WHERE mode='chat' AND status='active'
		  AND COALESCE(capabilities->>'vision','false')='true'
		ORDER BY sort_order ASC, code ASC LIMIT 1`).Scan(&want)
	if errors.Is(err, pgx.ErrNoRows) {
		// 库里没有视觉模型 → 404 也是合法答案。
		if status, _ := get("/v1/internal/models/preferred?mode=chat&capability=vision"); status != http.StatusNotFound {
			t.Fatalf("no vision model: want 404")
		}
		return
	}
	if err != nil {
		t.Fatalf("expected query: %v", err)
	}
	status, body := get("/v1/internal/models/preferred?mode=chat&capability=vision")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if out.Code != want {
		t.Fatalf("code = %q, want %q", out.Code, want)
	}
	if out.Code == blind.Code {
		t.Fatalf("non-vision model picked: %q", out.Code)
	}
}
