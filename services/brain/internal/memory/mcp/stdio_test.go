package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reuses openDB / seedProject helpers from mcp_test.go.

func newStdioServer(t *testing.T, p *pgxpool.Pool) *Server {
	t.Helper()
	return New(memstore.New(p), wikistore.New(p), nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// stdioRoundtrip drains all responses from the server until EOF and
// returns them in order. Lines are decoded as rpcResponse.
func stdioRoundtrip(t *testing.T, srv *Server, asUser uuid.UUID, requests []map[string]any) []rpcResponse {
	t.Helper()
	var input bytes.Buffer
	for _, r := range requests {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		input.Write(b)
		input.WriteByte('\n')
	}
	var output bytes.Buffer
	if err := srv.ServeStdio(context.Background(), &input, &output, asUser); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	var out []rpcResponse
	dec := json.NewDecoder(&output)
	for {
		var resp rpcResponse
		if err := dec.Decode(&resp); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode response: %v", err)
		}
		out = append(out, resp)
	}
	return out
}

func TestServeStdio_InitializeAndToolsList(t *testing.T) {
	p := openDB(t)
	srv := newStdioServer(t, p)
	_, owner, _ := seedProject(t, p)

	resps := stdioRoundtrip(t, srv, owner, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
	})
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Errorf("initialize: %+v", resps[0].Error)
	}
	res, _ := resps[0].Result.(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion: %v", res["protocolVersion"])
	}
	res2, _ := resps[1].Result.(map[string]any)
	tools, _ := res2["tools"].([]any)
	// Assert against the canonical registered set (memory.* + wiki.*
	// spliced via init()) so adding a tool never silently flakes this.
	if len(tools) != len(toolSchemas) {
		t.Errorf("expected %d tools, got %d", len(toolSchemas), len(tools))
	}
}

func TestServeStdio_StoreListRecallDelete_Roundtrip(t *testing.T) {
	p := openDB(t)
	srv := newStdioServer(t, p)
	pid, owner, _ := seedProject(t, p)

	// store
	resps := stdioRoundtrip(t, srv, owner, []map[string]any{{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "memory.store",
			"arguments": map[string]any{
				"project_id": pid.String(), "kind": "recall",
				"content": "stdio works",
			},
		},
	}})
	if resps[0].Error != nil {
		t.Fatalf("store: %+v", resps[0].Error)
	}
	res := resps[0].Result.(map[string]any)
	structured := res["structuredContent"].(map[string]any)
	mid := structured["memory"].(map[string]any)["id"].(string)

	// list
	resps = stdioRoundtrip(t, srv, owner, []map[string]any{{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "memory.list",
			"arguments": map[string]any{"project_id": pid.String()},
		},
	}})
	res = resps[0].Result.(map[string]any)
	structured = res["structuredContent"].(map[string]any)
	if got := len(structured["memories"].([]any)); got != 1 {
		t.Errorf("list: want 1, got %d", got)
	}

	// recall
	resps = stdioRoundtrip(t, srv, owner, []map[string]any{{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name": "memory.recall",
			"arguments": map[string]any{
				"project_id": pid.String(), "query": "stdio",
			},
		},
	}})
	res = resps[0].Result.(map[string]any)
	structured = res["structuredContent"].(map[string]any)
	if got := len(structured["memories"].([]any)); got != 1 {
		t.Errorf("recall: want 1 hit, got %d", got)
	}

	// delete
	resps = stdioRoundtrip(t, srv, owner, []map[string]any{{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{
			"name":      "memory.delete",
			"arguments": map[string]any{"id": mid},
		},
	}})
	if resps[0].Error != nil {
		t.Errorf("delete: %+v", resps[0].Error)
	}
}

func TestServeStdio_NotificationsHaveNoResponse(t *testing.T) {
	p := openDB(t)
	srv := newStdioServer(t, p)
	_, owner, _ := seedProject(t, p)

	// id absent → notification → no response written.
	resps := stdioRoundtrip(t, srv, owner, []map[string]any{
		{"jsonrpc": "2.0", "method": "ping"},          // notification
		{"jsonrpc": "2.0", "id": 7, "method": "ping"}, // request
	})
	if len(resps) != 1 {
		t.Errorf("want 1 response (notification dropped), got %d", len(resps))
	}
	if string(resps[0].ID) != "7" {
		t.Errorf("response id mismatch: got %s", resps[0].ID)
	}
}

func TestServeStdio_BadJSONLineProducesParseError(t *testing.T) {
	p := openDB(t)
	srv := newStdioServer(t, p)
	_, owner, _ := seedProject(t, p)

	in := strings.NewReader(`{not valid json}` + "\n")
	var out bytes.Buffer
	if err := srv.ServeStdio(context.Background(), in, &out, owner); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Errorf("expected parse error, got %+v", resp.Error)
	}
}

func TestServeStdio_RejectsBadJSONRPCVersion(t *testing.T) {
	p := openDB(t)
	srv := newStdioServer(t, p)
	_, owner, _ := seedProject(t, p)

	resps := stdioRoundtrip(t, srv, owner, []map[string]any{
		{"jsonrpc": "1.0", "id": 1, "method": "ping"},
	})
	if len(resps) != 1 || resps[0].Error == nil ||
		resps[0].Error.Code != codeInvalidRequest {
		t.Errorf("want invalid_request error, got %+v", resps)
	}
}

func TestServeStdio_RequiresUserID(t *testing.T) {
	p := openDB(t)
	srv := newStdioServer(t, p)
	err := srv.ServeStdio(context.Background(), strings.NewReader(""),
		io.Discard, uuid.Nil)
	if err == nil {
		t.Error("expected error for nil user id")
	}
}

func TestServeStdio_EOFReturnsCleanly(t *testing.T) {
	p := openDB(t)
	srv := newStdioServer(t, p)
	_, owner, _ := seedProject(t, p)

	if err := srv.ServeStdio(context.Background(),
		strings.NewReader(""), io.Discard, owner); err != nil {
		t.Errorf("EOF should not error: %v", err)
	}
}

// ─── ensure stderr stays clean by the binary's logger config ──

func TestServeStdio_DoesNotEscapeHTML(t *testing.T) {
	// Rich text content (markdown links etc.) shouldn't be HTML-
	// escaped on the wire — agents read this verbatim.
	p := openDB(t)
	srv := newStdioServer(t, p)
	pid, owner, _ := seedProject(t, p)

	in := mustEncodeLines(t, []map[string]any{{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "memory.store",
			"arguments": map[string]any{
				"project_id": pid.String(),
				"content":    "see <https://example.com> for details",
			},
		},
	}})
	var out bytes.Buffer
	if err := srv.ServeStdio(context.Background(),
		bytes.NewReader(in), &out, owner); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	// SetEscapeHTML(false) means `<` and `>` reach the wire as
	// single-byte literals instead of < / >. Asserting the
	// literal form is present implies the escape form is absent
	// (a single character can't be encoded both ways).
	raw := out.String()
	if !strings.Contains(raw, `<https://example.com>`) {
		t.Errorf("literal angle brackets missing: %s", raw)
	}
}

// ─── helpers ────────────────────────────────────────────

func mustEncodeLines(t *testing.T, items []map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, it := range items {
		b, _ := json.Marshal(it)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// silence unused-import warning if test file is built without DB.
var _ = os.Stderr
