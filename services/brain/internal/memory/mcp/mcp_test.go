package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration test setup. Skips when DATABASE_URL is unset or
// brain.memories has not been migrated.

const testJWTSecret = "mcp-test-secret-32-chars-aaaaaaa"

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	var ok bool
	if err := p.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		   WHERE table_schema = 'brain' AND table_name = 'memories')`,
	).Scan(&ok); err != nil || !ok {
		t.Skip("brain.memories not present; apply migrations/00004_memory.sql first")
	}
	return p
}

func seedProject(t *testing.T, p *pgxpool.Pool) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	owner := uuid.New()
	var id uuid.UUID
	err := p.QueryRow(context.Background(),
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		owner, "mcp-test-"+uuid.NewString(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, id)
	})
	tok := mintJWT(t, owner)
	return id, owner, tok
}

func mintJWT(t *testing.T, uid uuid.UUID) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uid.String(), "uid": uid.String(),
		"iss": "https://identity.biumind.local", "aud": "biumind-api",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	s, err := tok.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func newServer(t *testing.T, p *pgxpool.Pool) *httptest.Server {
	t.Helper()
	verifier := bauth.NewVerifier(testJWTSecret,
		"https://identity.biumind.local", "biumind-api")
	srv := New(memstore.New(p), wikistore.New(p), verifier,
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	return httptest.NewServer(mux)
}

func rpc(t *testing.T, ts *httptest.Server, token string, body any) *rpcResponse {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/mcp", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("rpc: %v", err)
	}
	defer resp.Body.Close()
	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &out
}

// ─── tests ──────────────────────────────────────────────

func TestMCP_Initialize(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	_, _, tok := seedProject(t, p)

	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if r.Error != nil {
		t.Fatalf("initialize: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion: %v", res["protocolVersion"])
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != serverName {
		t.Errorf("serverInfo.name: %v", info["name"])
	}
}

func TestMCP_ToolsList_MatchesRegistered(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	_, _, tok := seedProject(t, p)

	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	if r.Error != nil {
		t.Fatalf("tools/list: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	tools, _ := res["tools"].([]any)

	// tools/list must advertise exactly the canonical registered set
	// (memory.* from toolSchemas + wiki.* spliced via init()). Asserting
	// against toolSchemas keeps this from going stale every time a tool
	// is added — unlike the old hardcoded count.
	if len(tools) != len(toolSchemas) {
		t.Errorf("expected %d tools, got %d", len(toolSchemas), len(tools))
	}
	want := make(map[string]bool, len(toolSchemas))
	for _, s := range toolSchemas {
		want[s["name"].(string)] = true
	}
	// The four memory tools must always be present.
	for _, name := range []string{"memory.store", "memory.list", "memory.recall", "memory.delete"} {
		if !want[name] {
			t.Errorf("canonical set missing %q", name)
		}
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		delete(want, tool["name"].(string))
	}
	if len(want) != 0 {
		t.Errorf("advertised set missing registered tools: %v", want)
	}
}

func TestMCP_StoreListRecallDelete_Roundtrip(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	pid, _, tok := seedProject(t, p)

	// Store
	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "memory.store",
			"arguments": map[string]any{
				"project_id": pid.String(),
				"kind":       "recall",
				"content":    "uses Vim with vimwiki",
			},
		},
	})
	if r.Error != nil {
		t.Fatalf("store: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	structured, _ := res["structuredContent"].(map[string]any)
	mem, _ := structured["memory"].(map[string]any)
	mid, _ := mem["id"].(string)
	if mid == "" {
		t.Fatal("store: no id returned")
	}

	// List
	r = rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "memory.list",
			"arguments": map[string]any{"project_id": pid.String()},
		},
	})
	if r.Error != nil {
		t.Fatalf("list: %+v", r.Error)
	}
	res, _ = r.Result.(map[string]any)
	structured, _ = res["structuredContent"].(map[string]any)
	memories, _ := structured["memories"].([]any)
	if len(memories) != 1 {
		t.Errorf("list: want 1 mem, got %d", len(memories))
	}

	// Recall
	r = rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name": "memory.recall",
			"arguments": map[string]any{
				"project_id": pid.String(), "query": "vim",
			},
		},
	})
	if r.Error != nil {
		t.Fatalf("recall: %+v", r.Error)
	}
	res, _ = r.Result.(map[string]any)
	structured, _ = res["structuredContent"].(map[string]any)
	memories, _ = structured["memories"].([]any)
	if len(memories) != 1 {
		t.Errorf("recall: want 1 hit, got %d", len(memories))
	}
	if structured["mode"] != "lexical" {
		t.Errorf("mode: want lexical (no embedder), got %v", structured["mode"])
	}

	// Delete
	r = rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{
			"name":      "memory.delete",
			"arguments": map[string]any{"id": mid},
		},
	})
	if r.Error != nil {
		t.Fatalf("delete: %+v", r.Error)
	}
}

func TestMCP_UnknownMethod_ReturnsMethodNotFound(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	_, _, tok := seedProject(t, p)

	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 99, "method": "garbage",
	})
	if r.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if r.Error.Code != codeMethodNotFound {
		t.Errorf("code: got %d, want %d", r.Error.Code, codeMethodNotFound)
	}
}

func TestMCP_UnknownTool_ReturnsMethodNotFound(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	_, _, tok := seedProject(t, p)

	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{
			"name":      "memory.fly_to_the_moon",
			"arguments": map[string]any{},
		},
	})
	if r.Error == nil || r.Error.Code != codeMethodNotFound {
		t.Errorf("expected method-not-found, got %+v", r.Error)
	}
}

func TestMCP_InvalidProjectID_ReturnsInvalidParams(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	_, _, tok := seedProject(t, p)

	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{
			"name": "memory.store",
			"arguments": map[string]any{
				"project_id": "not-a-uuid", "content": "x",
			},
		},
	})
	if r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Errorf("expected invalid-params, got %+v", r.Error)
	}
}

func TestMCP_NoAuth_Rejected(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()

	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	resp, err := http.Post(ts.URL+"/v1/mcp", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out rpcResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == nil {
		t.Fatal("expected auth error")
	}
}

func TestMCP_CrossTenantAccess_Forbidden(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	pid, _, _ := seedProject(t, p)
	stranger := mintJWT(t, uuid.New())

	r := rpc(t, ts, stranger, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{
			"name":      "memory.list",
			"arguments": map[string]any{"project_id": pid.String()},
		},
	})
	if r.Error == nil {
		t.Fatal("stranger access should be rejected")
	}
}

func TestMCP_RejectsBadJSONRPCVersion(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	_, _, tok := seedProject(t, p)

	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "1.0", "id": 1, "method": "ping",
	})
	if r.Error == nil || r.Error.Code != codeInvalidRequest {
		t.Errorf("expected invalid-request for jsonrpc=1.0, got %+v", r.Error)
	}
}
