// Package mcp implements a Model Context Protocol server exposing
// Brain.Memory and Brain.Wiki operations as MCP tools.
//
// Transports:
//
//	HTTP   POST /v1/mcp           — JWT-authenticated, multi-tenant
//	stdio  ServeStdio             — env-pinned user, single-tenant local
//
// Methods (both transports):
//
//	initialize    — handshake, returns server metadata + capabilities
//	tools/list    — returns the canonical tool list (memory + wiki)
//	tools/call    — dispatches to one of:
//	                  memory.store / memory.list / memory.recall / memory.delete
//	                  wiki.search / wiki.list_pages / wiki.get_page
//	                  wiki.create_page / wiki.update_page / wiki.ingest
//
// Server is built incrementally — base() requires Memory + Wiki + Verifier;
// callers attach optional capabilities (Embedder, search, ingest) via
// WithEmbedder / WithSearch / WithIngest. A tool whose dependencies
// aren't wired returns an internal-error frame at call time, never on
// tools/list, so AI clients see a stable tool palette across deploys.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	"github.com/biumind/biumind/packages/go-sdk/biu/rerank"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/biumind/biumind/services/brain/internal/search/bm25"
	"github.com/biumind/biumind/services/brain/internal/search/vector"
	wikiingest "github.com/biumind/biumind/services/brain/internal/wiki/ingest"
	wikirelevance "github.com/biumind/biumind/services/brain/internal/wiki/relevance"
	wikireviews "github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

const (
	protocolVersion = "2025-03-26" // current MCP spec version we target
	serverName      = "biumind-brain"
	serverVersion   = "0.2.0"

	// JSON-RPC 2.0 reserved error codes.
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type Server struct {
	Memory   *memstore.Store
	Wiki     *wikistore.Store
	Verifier *bauth.Verifier
	// Embedder is optional; when set, memory.recall AND wiki.search
	// enable hybrid (semantic + lexical) ranking using the same
	// query-side provider that filled the index.
	Embedder embed.Embedder
	// BM25 / Vector / Ingest / Publisher are optional capability
	// bundles consumed by wiki.* tools. When unset, the corresponding
	// tools return an internal-error frame on call (and tools/list
	// still advertises them — AI clients tolerate optional deps).
	BM25      *bm25.Searcher
	Vector    *vector.Searcher
	Ingest    *wikiingest.Store
	Publisher publisher.Publisher
	Reviews   *wikireviews.Store
	Relevance *wikirelevance.Store
	// Reranker is optional; when set, wiki.search applies a cross-encoder
	// relevance rerank to the RRF-fused list (P1-2). Mirrors search/api.
	Reranker  rerank.Reranker
	Logger    *slog.Logger
}

func New(m *memstore.Store, w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Memory: m, Wiki: w, Verifier: v, Logger: l}
}

func (s *Server) WithEmbedder(e embed.Embedder) *Server {
	s.Embedder = e
	return s
}

// WithSearch attaches the BM25 + (optional) vector retrievers used by
// wiki.search. Pass `vec=nil` for BM25-only deployments — the tool
// stays available, the result mode just drops to "bm25".
func (s *Server) WithSearch(b *bm25.Searcher, vec *vector.Searcher) *Server {
	s.BM25 = b
	s.Vector = vec
	return s
}

// WithReranker attaches the cross-encoder relevance reranker used by
// wiki.search. Nil ⇒ wiki.search keeps its RRF-fused order.
func (s *Server) WithReranker(r rerank.Reranker) *Server {
	s.Reranker = r
	return s
}

// WithIngest attaches the wiki ingest task store + NATS publisher used
// by wiki.ingest. Both must be set together (a publisher without a
// store, or vice versa, would create unreachable rows or unscheduled
// publishes).
func (s *Server) WithIngest(i *wikiingest.Store, p publisher.Publisher) *Server {
	s.Ingest = i
	s.Publisher = p
	return s
}

// WithReviews attaches the review queue store used by
// wiki.list_reviews / wiki.dismiss_review. Without it the tools return
// an internal-error frame at call time and tools/list still advertises
// them (clients tolerate optional deps).
func (s *Server) WithReviews(r *wikireviews.Store) *Server {
	s.Reviews = r
	return s
}

// WithRelevance attaches the page relatedness store used by
// wiki.related_pages. Same opt-in pattern as the others.
func (s *Server) WithRelevance(r *wikirelevance.Store) *Server {
	s.Relevance = r
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/mcp", s.requireAuth(s.handle))
}

// ─── JSON-RPC envelopes ─────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ─── Dispatcher ─────────────────────────────────────────

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.writeRPC(w, nil, nil, &rpcError{Code: codeParseError, Message: err.Error()})
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeRPC(w, nil, nil, &rpcError{Code: codeParseError, Message: err.Error()})
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeRPC(w, req.ID, nil, &rpcError{
			Code: codeInvalidRequest, Message: `jsonrpc must be "2.0"`,
		})
		return
	}

	result, rerr := s.dispatch(r.Context(), req)
	s.writeRPC(w, req.ID, result, rerr)
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params)
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params)
	case "ping":
		return map[string]any{}, nil
	default:
		return nil, &rpcError{
			Code:    codeMethodNotFound,
			Message: fmt.Sprintf("method %q not supported", req.Method),
		}
	}
}

// ─── initialize ─────────────────────────────────────────

func (s *Server) handleInitialize(_ json.RawMessage) (any, *rpcError) {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false, // tool set is static for this server
			},
		},
	}, nil
}

// ─── tools/list ─────────────────────────────────────────

func (s *Server) handleToolsList() (any, *rpcError) {
	return map[string]any{"tools": toolSchemas}, nil
}

// toolSchemas — the canonical advertised tools. Schemas are MCP-flavoured
// JSON Schema (subset of draft 2020-12). Keep these in sync with the
// dispatch table in handleToolsCall.
//
// Wiki tool schemas are concatenated from wiki_tools.go via init() so
// each surface area lives next to its implementation.
var toolSchemas = []map[string]any{
	{
		"name":        "memory.store",
		"description": "Persist a fact, preference, or habit the agent should remember.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "string", "description": "Owning project (UUID)."},
				"kind":       map[string]any{"type": "string", "enum": []string{"recall", "preference", "habit"}, "default": "recall"},
				"content":    map[string]any{"type": "string", "description": "The text to remember."},
				"salience":   map[string]any{"type": "number", "minimum": 0, "maximum": 1, "default": 0.5},
			},
			"required": []string{"project_id", "content"},
		},
	},
	{
		"name":        "memory.list",
		"description": "List recent memories in a project.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "string"},
				"kind":       map[string]any{"type": "string", "enum": []string{"recall", "preference", "habit"}},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 100},
			},
			"required": []string{"project_id"},
		},
	},
	{
		"name":        "memory.recall",
		"description": "Search memories by query (semantic + lexical hybrid when embeddings are configured).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "string"},
				"query":      map[string]any{"type": "string"},
				"kind":       map[string]any{"type": "string", "enum": []string{"recall", "preference", "habit"}},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 10},
			},
			"required": []string{"project_id", "query"},
		},
	},
	{
		"name":        "memory.delete",
		"description": "Delete a memory by ID. Caller must own the row.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Memory UUID."},
			},
			"required": []string{"id"},
		},
	},
}

// init splices the wiki tool schemas onto the canonical tool list.
// Implementations live in wiki_tools.go alongside their dispatch
// helpers — keeping the vars separate lets each surface evolve
// independently.
func init() {
	toolSchemas = append(toolSchemas, wikiToolSchemas...)
}

// ─── tools/call ─────────────────────────────────────────

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p toolCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	uid := uidFromCtx(ctx)
	if uid == uuid.Nil {
		return nil, &rpcError{Code: codeInternalError, Message: "missing user id"}
	}

	switch p.Name {
	case "memory.store":
		return s.callStore(ctx, uid, p.Arguments)
	case "memory.list":
		return s.callList(ctx, uid, p.Arguments)
	case "memory.recall":
		return s.callRecall(ctx, uid, p.Arguments)
	case "memory.delete":
		return s.callDelete(ctx, uid, p.Arguments)
	case "wiki.search":
		return s.callWikiSearch(ctx, uid, p.Arguments)
	case "wiki.list_pages":
		return s.callWikiListPages(ctx, uid, p.Arguments)
	case "wiki.get_page":
		return s.callWikiGetPage(ctx, uid, p.Arguments)
	case "wiki.create_page":
		return s.callWikiCreatePage(ctx, uid, p.Arguments)
	case "wiki.update_page":
		return s.callWikiUpdatePage(ctx, uid, p.Arguments)
	case "wiki.ingest":
		return s.callWikiIngest(ctx, uid, p.Arguments)
	case "wiki.list_reviews":
		return s.callWikiListReviews(ctx, uid, p.Arguments)
	case "wiki.dismiss_review":
		return s.callWikiDismissReview(ctx, uid, p.Arguments)
	case "wiki.merge_pages":
		return s.callWikiMergePages(ctx, uid, p.Arguments)
	case "wiki.related_pages":
		return s.callWikiRelatedPages(ctx, uid, p.Arguments)
	default:
		return nil, &rpcError{
			Code:    codeMethodNotFound,
			Message: fmt.Sprintf("unknown tool %q", p.Name),
		}
	}
}

// ─── tool implementations ───────────────────────────────

type storeArgs struct {
	ProjectID string  `json:"project_id"`
	Kind      string  `json:"kind"`
	Content   string  `json:"content"`
	Salience  float32 `json:"salience"`
}

func (s *Server) callStore(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a storeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	pid, perr := s.checkProject(ctx, uid, a.ProjectID)
	if perr != nil {
		return nil, perr
	}
	m, err := s.Memory.Create(ctx, memstore.StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: a.Kind,
		Content: a.Content, Salience: a.Salience,
	})
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("stored memory %s (kind=%s)", m.ID, m.Kind),
	}}, map[string]any{"memory": memOut(m)}), nil
}

type listArgs struct {
	ProjectID string `json:"project_id"`
	Kind      string `json:"kind"`
	Limit     int    `json:"limit"`
}

func (s *Server) callList(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a listArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	pid, perr := s.checkProject(ctx, uid, a.ProjectID)
	if perr != nil {
		return nil, perr
	}
	ms, err := s.Memory.List(ctx, memstore.ListInput{
		ProjectID: pid, OwnerID: uid, Kind: a.Kind, Limit: a.Limit,
	})
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	out := make([]map[string]any, len(ms))
	for i, m := range ms {
		out[i] = memOut(m)
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("%d memories", len(out)),
	}}, map[string]any{"memories": out}), nil
}

type recallArgs struct {
	ProjectID string `json:"project_id"`
	Query     string `json:"query"`
	Kind      string `json:"kind"`
	Limit     int    `json:"limit"`
}

func (s *Server) callRecall(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a recallArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	pid, perr := s.checkProject(ctx, uid, a.ProjectID)
	if perr != nil {
		return nil, perr
	}
	var qvec []float32
	mode := "lexical"
	if s.Embedder != nil {
		ec, cancel := context.WithTimeout(ctx, 5*time.Second)
		v, err := s.Embedder.Embed(ec, a.Query)
		cancel()
		if err == nil {
			qvec = v
			mode = "hybrid"
		}
	}
	ms, err := s.Memory.Recall(ctx, memstore.RecallInput{
		ProjectID: pid, OwnerID: uid, Query: a.Query,
		QueryEmbedding: qvec, Kind: a.Kind, Limit: a.Limit,
	})
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	out := make([]map[string]any, len(ms))
	for i, m := range ms {
		row := memOut(m)
		row["score"] = m.Score
		out[i] = row
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("%d hits for %q (mode=%s)", len(out), a.Query, mode),
	}}, map[string]any{"memories": out, "mode": mode, "query": a.Query}), nil
}

type deleteArgs struct {
	ID string `json:"id"`
}

func (s *Server) callDelete(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a deleteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	id, err := uuid.Parse(a.ID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "id must be a UUID"}
	}
	if err := s.Memory.Delete(ctx, uid, id); err != nil {
		if errors.Is(err, memstore.ErrNotFound) {
			return nil, &rpcError{Code: codeInvalidParams, Message: "not found"}
		}
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	return mcpResult([]map[string]any{{
		"type": "text", "text": "deleted",
	}}, map[string]any{"deleted": id.String()}), nil
}

// ─── helpers ────────────────────────────────────────────

func (s *Server) checkProject(ctx context.Context, uid uuid.UUID, raw string) (uuid.UUID, *rpcError) {
	pid, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, &rpcError{Code: codeInvalidParams, Message: "project_id must be a UUID"}
	}
	p, err := s.Wiki.GetProject(ctx, pid)
	if err != nil {
		return uuid.Nil, &rpcError{Code: codeInvalidParams, Message: "project not found"}
	}
	if p.OwnerID != uid {
		return uuid.Nil, &rpcError{Code: codeInvalidParams, Message: "forbidden"}
	}
	return pid, nil
}

// mcpResult builds the canonical MCP tool-call result envelope.
// `content` is the human-readable payload (used by chat UIs); the
// optional `structured` field is a machine-readable map agents can
// consume directly without parsing the text blob.
func mcpResult(content []map[string]any, structured map[string]any) map[string]any {
	out := map[string]any{
		"content": content,
		"isError": false,
	}
	if structured != nil {
		out["structuredContent"] = structured
	}
	return out
}

func memOut(m *memstore.Memory) map[string]any {
	return map[string]any{
		"id":               m.ID.String(),
		"project_id":       m.ProjectID.String(),
		"kind":             m.Kind,
		"content":          m.Content,
		"salience":         m.Salience,
		"last_accessed_at": m.LastAccessedAt.UTC().Format(time.RFC3339),
		"created_at":       m.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) writeRPC(w http.ResponseWriter, id json.RawMessage, result any, rerr *rpcError) {
	w.Header().Set("Content-Type", "application/json")
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rerr}
	if rerr != nil {
		resp.Result = nil
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			s.writeRPC(w, nil, nil, &rpcError{
				Code: codeInvalidRequest, Message: "missing bearer token",
			})
			return
		}
		claims, err := s.Verifier.Verify(auth[7:])
		if err != nil {
			s.writeRPC(w, nil, nil, &rpcError{
				Code: codeInvalidRequest, Message: "invalid token: " + err.Error(),
			})
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func uidFromCtx(ctx context.Context) uuid.UUID {
	c, ok := bauth.ClaimsFrom(ctx)
	if !ok {
		return uuid.Nil
	}
	id, _ := uuid.Parse(c.UserID)
	return id
}
