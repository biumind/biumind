package mcp

// wiki_chat_test.go — wiki.chat / wiki.list_projects MCP tool tests.
// Unit tests run without a DB (validation + projection paths); the
// end-to-end ones follow mcp_test.go's DATABASE_URL-gated pattern with
// a scripted fake model-relay standing in for the LLM.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/chat"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// ── No-DB unit tests ─────────────────────────────────────────────

func TestWikiChat_RejectsEmptyMessage(t *testing.T) {
	srv := &Server{}
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	args, _ := json.Marshal(map[string]any{
		"project_id": uuid.NewString(),
		"message":    "   ",
	})
	_, rerr := srv.callWikiChat(ctx, uid, args)
	if rerr == nil {
		t.Fatal("expected error for empty message")
	}
	if !strings.Contains(rerr.Message, "message is required") {
		t.Errorf("expected message-required error, got %q", rerr.Message)
	}
}

func TestWikiChat_ReturnsInternalErrorWhenAgentUnset(t *testing.T) {
	srv := &Server{} // no Agent wired
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	args, _ := json.Marshal(map[string]any{
		"project_id": uuid.NewString(),
		"message":    "anything",
	})
	_, rerr := srv.callWikiChat(ctx, uid, args)
	if rerr == nil {
		t.Fatal("expected error when Agent unset")
	}
	if rerr.Code != codeInternalError {
		t.Errorf("want internal-error code, got %d", rerr.Code)
	}
	if !strings.Contains(rerr.Message, "chat not configured") {
		t.Errorf("message should explain config gap, got %q", rerr.Message)
	}
}

func TestWikiChatModeBudgets_MirrorHTTPTiers(t *testing.T) {
	// Same ladder as wiki/api wikiAgentMaxTurns + wikiAgentRetrievalBudget.
	cases := []struct {
		mode             string
		turns, retrieval int
	}{
		{"fast", 4, 2},
		{"standard", 8, 4},
		{"deep", 12, 6},
		{"", 8, 4},        // empty → standard
		{"garbage", 8, 4}, // unknown → standard
	}
	for _, c := range cases {
		if got := wikiChatMaxTurns(c.mode); got != c.turns {
			t.Errorf("mode %q: turns got %d want %d", c.mode, got, c.turns)
		}
		if got := wikiChatRetrievalBudget(c.mode); got != c.retrieval {
			t.Errorf("mode %q: retrieval got %d want %d", c.mode, got, c.retrieval)
		}
		if got := normalizeWikiChatMode(c.mode); c.mode != "" &&
			c.mode != "garbage" && got != c.mode {
			t.Errorf("mode %q: normalized to %q", c.mode, got)
		}
	}
}

func TestCitedPagesFromParts_DedupesWikiSearchHits(t *testing.T) {
	p1, p2 := uuid.NewString(), uuid.NewString()
	parts := []map[string]any{
		{"id": "b0", "type": "text", "text": "looking…"},
		{
			"id": "b1", "type": "tool_use", "name": "wiki_search",
			"phase": "success",
			"result": map[string]any{
				"query": "q",
				"results": []any{
					map[string]any{"page_id": p1, "title": "Alpha"},
					map[string]any{"page_id": p2, "title": "Beta"},
				},
			},
		},
		{
			// Second search returning p1 again — must dedupe.
			"id": "b2", "type": "tool_use", "name": "wiki_search",
			"phase": "success",
			"result": map[string]any{
				"query":   "q2",
				"results": []any{map[string]any{"page_id": p1, "title": "Alpha"}},
			},
		},
		{
			// Non-search tools never contribute citations.
			"id": "b3", "type": "tool_use", "name": "memory_recall",
			"phase":  "success",
			"result": map[string]any{"memories": []any{}},
		},
	}
	raw, _ := json.Marshal(parts)
	got := citedPagesFromParts(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 cited pages, got %d: %+v", len(got), got)
	}
	if got[0]["page_id"] != p1 || got[0]["title"] != "Alpha" {
		t.Errorf("first citation: %+v", got[0])
	}
	if got[1]["page_id"] != p2 || got[1]["title"] != "Beta" {
		t.Errorf("second citation: %+v", got[1])
	}

	if got := citedPagesFromParts([]byte("not json")); got != nil {
		t.Errorf("malformed parts should yield nil, got %+v", got)
	}
	if got := citedPagesFromParts([]byte("[]")); len(got) != 0 {
		t.Errorf("empty parts should yield nothing, got %+v", got)
	}
}

// ── DB-backed end-to-end (skip without DATABASE_URL) ─────────────

func TestMCP_WikiListProjects(t *testing.T) {
	p := openDB(t)
	ts := newServer(t, p)
	defer ts.Close()
	pid, _, tok := seedProject(t, p)

	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "wiki.list_projects",
			"arguments": map[string]any{},
		},
	})
	if r.Error != nil {
		t.Fatalf("list_projects: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	structured, _ := res["structuredContent"].(map[string]any)
	projects, _ := structured["projects"].([]any)
	var found bool
	for _, raw := range projects {
		proj, _ := raw.(map[string]any)
		if proj["id"] == pid.String() {
			found = true
			if proj["name"] == "" {
				t.Error("project row missing name")
			}
		}
	}
	if !found {
		t.Errorf("seeded project %s not in list: %+v", pid, projects)
	}
}

func TestMCP_WikiChat_NonStreamingAnswer(t *testing.T) {
	p := openDB(t)

	// Fake model-relay: one SSE turn, plain answer, no tools.
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: delta\ndata: {\"text\":\"grounded answer\"}\n\n"+
			"event: stop\ndata: {\"reason\":\"end_turn\",\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n"+
			"event: end\ndata: {}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer relay.Close()

	verifier := bauth.NewVerifier(testJWTSecret,
		"https://identity.biumind.local", "biumind-api")
	srv := New(memstore.New(p), wikistore.New(p), verifier,
		slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithAgent(chat.NewHTTPSender(nil, relay.URL), nil)
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	pid, _, tok := seedProject(t, p)
	r := rpc(t, ts, tok, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "wiki.chat",
			"arguments": map[string]any{
				"project_id": pid.String(),
				"message":    "这个项目讲了什么？",
				"mode":       "fast",
				"model":      "test-model",
			},
		},
	})
	if r.Error != nil {
		t.Fatalf("wiki.chat: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	structured, _ := res["structuredContent"].(map[string]any)
	if structured["answer"] != "grounded answer" {
		t.Errorf("answer: got %v", structured["answer"])
	}
	if structured["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason: got %v", structured["stop_reason"])
	}
	if structured["mode"] != "fast" {
		t.Errorf("mode: got %v", structured["mode"])
	}
	// The text content carries the same answer for chat-UI clients.
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatal("content empty")
	}
	first, _ := content[0].(map[string]any)
	if first["text"] != "grounded answer" {
		t.Errorf("content text: got %v", first["text"])
	}
}

func TestMCP_WikiChat_StrangerForbidden(t *testing.T) {
	p := openDB(t)

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: delta\ndata: {\"text\":\"x\"}\n\n"+
			"event: stop\ndata: {\"reason\":\"end_turn\"}\n\nevent: end\ndata: {}\n\n")
	}))
	defer relay.Close()

	verifier := bauth.NewVerifier(testJWTSecret,
		"https://identity.biumind.local", "biumind-api")
	srv := New(memstore.New(p), wikistore.New(p), verifier,
		slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithAgent(chat.NewHTTPSender(nil, relay.URL), nil)
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	pid, _, _ := seedProject(t, p)
	stranger := mintJWT(t, uuid.New())
	r := rpc(t, ts, stranger, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "wiki.chat",
			"arguments": map[string]any{
				"project_id": pid.String(),
				"message":    "hi",
				"model":      "test-model",
			},
		},
	})
	if r.Error == nil {
		t.Fatal("stranger wiki.chat should be rejected")
	}
	if r.Error.Code != codeInvalidParams {
		t.Errorf("code: got %d, want %d", r.Error.Code, codeInvalidParams)
	}
}
