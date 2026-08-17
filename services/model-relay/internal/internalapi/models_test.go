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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
