// Package api implements Brain.Graph HTTP endpoints.
//
//	GET    /v1/graph/projects/{pid}/nodes               list/search nodes
//	GET    /v1/graph/projects/{pid}/nodes/{id}          node detail + 1-hop neighbors
//	GET    /v1/graph/projects/{pid}/related?node_id=&depth=&relations=
//	POST   /v1/graph/projects/{pid}/extract             apply heuristic extractor to a block
//
// JWT-verified per the existing Brain pattern (delegated to the same
// Verifier used by Wiki / Search).
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/graph/extract"
	"github.com/biumind/biumind/services/brain/internal/graph/store"
	"github.com/google/uuid"
)

type Server struct {
	Store    *store.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(s *store.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/graph/projects/{pid}/nodes",
		s.requireAuth(s.handleListNodes))
	mux.HandleFunc("GET /v1/graph/projects/{pid}/nodes/{id}",
		s.requireAuth(s.handleGetNode))
	mux.HandleFunc("GET /v1/graph/projects/{pid}/related",
		s.requireAuth(s.handleRelated))
	mux.HandleFunc("POST /v1/graph/projects/{pid}/extract",
		s.requireAuth(s.handleExtract))
}

// ─── List / search nodes ──────────────────────────────────

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	pid, ok := parseUUID(w, r.PathValue("pid"))
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	nodes, err := s.Store.ListNodes(r.Context(), store.ListNodesInput{
		ProjectID: pid,
		Kind:      q.Get("kind"),
		Search:    q.Get("q"),
		Limit:     limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeOut(&n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out})
}

// ─── Node detail (+ immediate edges + backlinks) ──────────

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	pid, ok := parseUUID(w, r.PathValue("pid"))
	if !ok {
		return
	}
	id, ok := parseUUID(w, r.PathValue("id"))
	if !ok {
		return
	}
	n, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if n.ProjectID != pid {
		writeErr(w, http.StatusForbidden, "wrong_project", "")
		return
	}
	edges, err := s.Store.ListEdges(r.Context(), pid, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	backlinks, err := s.Store.Backlinks(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	edgeOuts := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		edgeOuts = append(edgeOuts, edgeOut(&e))
	}
	bl := make([]string, 0, len(backlinks))
	for _, id := range backlinks {
		bl = append(bl, id.String())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node":      nodeOut(n),
		"edges":     edgeOuts,
		"backlinks": bl,
	})
}

// ─── BFS traversal ────────────────────────────────────────

func (s *Server) handleRelated(w http.ResponseWriter, r *http.Request) {
	pid, ok := parseUUID(w, r.PathValue("pid"))
	if !ok {
		return
	}
	q := r.URL.Query()
	seedID, ok := parseUUID(w, q.Get("node_id"))
	if !ok {
		return
	}
	depth, _ := strconv.Atoi(q.Get("depth"))
	if depth == 0 {
		depth = 2
	}
	maxNodes, _ := strconv.Atoi(q.Get("limit"))

	var relations []string
	if rs := strings.TrimSpace(q.Get("relations")); rs != "" {
		for _, p := range strings.Split(rs, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				relations = append(relations, p)
			}
		}
	}
	if relations == nil {
		relations = []string{}
	}

	neighbors, err := s.Store.NeighborsBFS(r.Context(), pid, seedID, depth, relations, maxNodes)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "graph api: related",
			"project_id", pid, "seed_id", seedID, "depth", depth,
			"relations", len(relations), "max_nodes", maxNodes,
			"hits", len(neighbors))
	}
	out := make([]map[string]any, 0, len(neighbors))
	for _, n := range neighbors {
		m := nodeOut(&n.Node)
		m["depth"] = n.Depth
		m["relation"] = n.Relation
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"neighbors": out})
}

// ─── Manual extract trigger ───────────────────────────────

type extractReq struct {
	BlockID *string         `json:"block_id"`
	Content json.RawMessage `json:"content"`
}

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	pid, ok := parseUUID(w, r.PathValue("pid"))
	if !ok {
		return
	}
	var req extractReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var content map[string]any
	if len(req.Content) > 0 {
		if err := json.Unmarshal(req.Content, &content); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_content", err.Error())
			return
		}
	}
	candidates := extract.FromBlockContent(content)

	var blockID *uuid.UUID
	if req.BlockID != nil {
		bid, err := uuid.Parse(*req.BlockID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_block_id", err.Error())
			return
		}
		blockID = &bid
	}

	created := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		n, err := s.Store.UpsertNode(r.Context(), store.UpsertNodeInput{
			ProjectID: pid,
			Kind:      c.Kind,
			Name:      c.Name,
			Weight:    c.Weight,
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		if blockID != nil {
			_ = s.Store.LinkBlock(r.Context(), *blockID, n.ID, c.Weight)
		}
		created = append(created, map[string]any{
			"node":     nodeOut(n),
			"relation": c.Relation,
			"surface":  c.Original,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"extracted": created,
		"count":     len(created),
	})
}

// ─── helpers ──────────────────────────────────────────────

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(auth[7:])
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func parseUUID(w http.ResponseWriter, raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_uuid", raw)
		return uuid.Nil, false
	}
	return id, true
}

func nodeOut(n *store.Node) map[string]any {
	out := map[string]any{
		"id":         n.ID.String(),
		"project_id": n.ProjectID.String(),
		"kind":       n.Kind,
		"name":       n.Name,
		"aliases":    n.Aliases,
		"summary":    n.Summary,
		"weight":     n.Weight,
		"created_at": n.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": n.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if n.Path != nil && *n.Path != "" {
		out["path"] = *n.Path
	}
	return out
}

func edgeOut(e *store.Edge) map[string]any {
	out := map[string]any{
		"id":       e.ID.String(),
		"src_id":   e.SrcID.String(),
		"dst_id":   e.DstID.String(),
		"relation": e.Relation,
		"weight":   e.Weight,
	}
	if e.EvidenceBlockID != nil {
		out["evidence_block_id"] = e.EvidenceBlockID.String()
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
