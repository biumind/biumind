// Package api implements POST /v1/search.
//
// scope:
//
//	"wiki"   pages + blocks (default); fuses wiki+vector+graph via RRF
//	"web"    SearxNG external
//	"all"    wiki + web fused via RRF
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	"github.com/biumind/biumind/packages/go-sdk/biu/rerank"
	notestore "github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/biumind/biumind/services/brain/internal/search/bm25"
	"github.com/biumind/biumind/services/brain/internal/search/decay"
	"github.com/biumind/biumind/services/brain/internal/search/rrf"
	"github.com/biumind/biumind/services/brain/internal/search/searxng"
	"github.com/biumind/biumind/services/brain/internal/search/vector"
	wikirelevance "github.com/biumind/biumind/services/brain/internal/wiki/relevance"
	"github.com/google/uuid"
)

type Server struct {
	BM25      *bm25.Searcher
	SearxNG   *searxng.Client
	Vector    *vector.Searcher     // optional: nil ⇒ no vector path
	Relevance *wikirelevance.Store // optional: nil ⇒ no graph path
	Embedder  embed.Embedder       // optional: nil disables vector path
	Reranker  rerank.Reranker      // optional: nil ⇒ no cross-encoder rerank
	Notes     noteSearcher         // optional: nil ⇒ notes path off
	Decay     *decay.Decay
	Verifier  *bauth.Verifier
	Logger    *slog.Logger
}

func NewServer(s *bm25.Searcher, sx *searxng.Client, d *decay.Decay, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{BM25: s, SearxNG: sx, Decay: d, Verifier: v, Logger: l}
}

// WithVector wires the vector retrieval path. Both the searcher AND an
// embedder are required — a vector store without an embedder can't
// compute query vectors. Either nil ⇒ vector path stays off.
func (s *Server) WithVector(v *vector.Searcher, e embed.Embedder) *Server {
	s.Vector = v
	s.Embedder = e
	return s
}

// WithRelevance wires the graph-relatedness retrieval path. The
// path expands BM25 top-K seed pages into their related neighbours
// via brain.page_relevance and feeds that as a fourth ranked list
// to RRF. Nil ⇒ graph path stays off; the rest of the search keeps
// working unchanged.
func (s *Server) WithRelevance(r *wikirelevance.Store) *Server {
	s.Relevance = r
	return s
}

// noteSearcher is the narrow contract the notes retrieval path needs
// from the note domain — the real implementation is
// *notestore.Store.SearchNotes (zhparser tsvector + ts_headline);
// tests stub it. Search must always carry the caller's user_id
// (personal notes, strict isolation).
type noteSearcher interface {
	SearchNotes(ctx context.Context, userID uuid.UUID, query string, limit int) ([]notestore.SearchHit, error)
}

// WithNotes wires the personal-notes retrieval path (N3). Notes join
// RRF as an extra ranked list (fused kind="note") and also surface as
// a separate "notes" array in the response. The path only fires when
// the request opts in via include_notes=true — privacy default off.
// Nil ⇒ notes path stays off.
//
// TODO(N3 后续): notes 的 embedding/向量召回（现在只有 zhparser 词法一路）。
func (s *Server) WithNotes(n noteSearcher) *Server {
	s.Notes = n
	return s
}

// WithReranker wires the cross-encoder relevance reranker. Sits after
// RRF fusion and before feedback rerank: the model re-scores the fused
// list by query/doc relevance, then feedback rerank applies the user's
// up/down verdicts as a personal nudge on top. Nil ⇒ rerank off; the
// fused list keeps its RRF order.
func (s *Server) WithReranker(r rerank.Reranker) *Server {
	s.Reranker = r
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/search", s.requireAuth(s.handleSearch))
	s.mountFeedback(mux)
}

type searchReq struct {
	Query     string  `json:"query"`
	Scope     string  `json:"scope"`      // "wiki" | "web" | "all"
	ProjectID *string `json:"project_id"` // optional restrict
	Limit     int     `json:"limit"`
	// IncludeNotes —— 把个人笔记纳入检索（独立 "notes" 数组 + RRF 一路）。
	// 默认 false：隐私默认关，由客户端开关控制。
	IncludeNotes bool `json:"include_notes"`
}

type searchResp struct {
	Query  string      `json:"query"`
	Scope  string      `json:"scope"`
	Wiki   []wikiHit   `json:"wiki,omitempty"`
	Web    []webHit    `json:"web,omitempty"`
	Vector []vectorHit `json:"vector,omitempty"`
	Graph  []graphHit  `json:"graph,omitempty"`
	Notes  []noteHit   `json:"notes,omitempty"`
	Images []imageHit  `json:"images,omitempty"`
	Fused  []fusedHit  `json:"fused,omitempty"`
}

// noteHit is one personal-note hit from the notes retrieval path
// (include_notes=true). Snippet is ts_headline-wrapped with
// <mark> highlights; Score is the raw ts_rank.
type noteHit struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet,omitempty"`
	Score     float64 `json:"score"`
	UpdatedAt string  `json:"updated_at"`
}

// imageHit is one markdown image reference extracted from the blocks
// of a page that survived BM25/vector/graph fusion. Mirrors the
// llm_wiki ImageHit shape so the Flutter / web client UI can present
// a "matching" + "supporting" two-tier image grid (alt-matches-query
// images first, then images from matched pages whose alt didn't match).
//
// We intentionally don't run a separate full-text search on alt text
// — that would over-emit decorative images (logos, icons, page
// chrome) that happen to share a query token. Instead we restrict
// candidates to images appearing on already-matched pages and
// classify each by whether its alt overlaps the query tokens.
type imageHit struct {
	URL             string `json:"url"`
	Alt             string `json:"alt"`
	PageID          string `json:"page_id"`
	BlockID         string `json:"block_id,omitempty"`
	PageTitle       string `json:"page_title"`
	AltMatchesQuery bool   `json:"alt_matches_query"`
}

// graphHit is one wiki page surfaced by the graph-relevance path —
// "BM25 found page X, the relevance graph says Y is strongly related
// to X, so Y is also a candidate". Score is the relevance edge score
// from page_relevance; meta.via_seed records which BM25 hit caused
// the expansion (debugging + UI provenance).
type graphHit struct {
	PageID  string  `json:"page_id"`
	Title   string  `json:"title"`
	ViaSeed string  `json:"via_seed_page_id"`
	Score   float64 `json:"score"`
}

type vectorHit struct {
	ChunkID   string  `json:"chunk_id"`
	PageID    string  `json:"page_id"`
	BlockID   string  `json:"block_id,omitempty"`
	ProjectID string  `json:"project_id"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet,omitempty"`
	Score     float64 `json:"score"`
	UpdatedAt string  `json:"updated_at"`
}

type wikiHit struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	PageID    string  `json:"page_id"`
	ProjectID string  `json:"project_id"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet,omitempty"`
	Score     float64 `json:"score"`
	UpdatedAt string  `json:"updated_at"`
	// Feedback echoes the user's stored verdict ("up"|"down") for this
	// (query, page) pair when one exists — set during feedback rerank so
	// the UI can badge promoted/demoted results. Empty when no verdict.
	Feedback string `json:"feedback,omitempty"`
}

type webHit struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet string  `json:"snippet"`
	Engine  string  `json:"engine"`
	Score   float64 `json:"score"`
}

type fusedHit struct {
	ID    string         `json:"id"`
	Score float64        `json:"score"`
	Meta  map[string]any `json:"meta"`
	// RerankedScore is the cross-encoder relevance score when rerank ran
	// (P1-2); nil when no reranker is wired. Kept alongside the raw RRF
	// Score so debug/badge UI can show both.
	RerankedScore *float64 `json:"reranked_score,omitempty"`
	// Feedback echoes the user's stored verdict on the underlying page
	// (wiki/vector/graph items carry page_id in Meta; web items have none
	// and are never badged). Set during feedback rerank.
	Feedback string `json:"feedback,omitempty"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		writeErr(w, http.StatusBadRequest, "missing_query", "")
		return
	}
	if req.Scope == "" {
		req.Scope = "wiki"
	}
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 20
	}
	claims := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}

	resp := searchResp{Query: req.Query, Scope: req.Scope}

	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "search api: request",
			"user_id", uid, "query_bytes", len(req.Query),
			"scope", req.Scope, "limit", req.Limit, "project_id", req.ProjectID)
	}

	wantWiki := req.Scope == "wiki" || req.Scope == "all"
	// BM25 nil 防御：正常装配永远非 nil，这个判断只让无 DB 的单测
	// 能走 web/notes 路径。
	wantWiki = wantWiki && s.BM25 != nil
	wantWeb := req.Scope == "web" || req.Scope == "all"
	// Vector fires for the same scopes as wiki — it's a third lens onto
	// the same wiki content, just embedding-based instead of tsvector.
	// Skip when no embedder is wired (unit/CI environments without
	// EMBED_PROVIDER set).
	wantVector := wantWiki && s.Vector != nil && s.Embedder != nil
	// Graph relevance path needs BM25 hits as seeds, so it shares the
	// wiki gate. Off when no relevance store is wired (boot-only
	// environments before the relevance worker has populated rows).
	wantGraph := wantWiki && s.Relevance != nil

	// Feedback rerank (P1-1/B-12): pull this user's stored verdicts for
	// the query once, then re-rank the wiki + fused lists so pages they
	// upvoted surface higher and downvoted ones sink. One indexed query;
	// skipped for web-only scope where no page-bearing list exists.
	verdicts := map[string]string{}
	if wantWiki {
		if vs, err := s.loadFeedback(r.Context(), uid, strings.ToLower(req.Query)); err != nil {
			if s.Logger != nil {
				s.Logger.Warn("search feedback load failed", "err", err)
			}
		} else {
			verdicts = vs
		}
	}

	var wikiResults []bm25.Hit
	if wantWiki {
		var pid *uuid.UUID
		if req.ProjectID != nil && *req.ProjectID != "" {
			if u, err := uuid.Parse(*req.ProjectID); err == nil {
				pid = &u
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		hits, err := s.BM25.Search(ctx, req.Query, bm25.SearchOptions{
			ProjectID: pid, OwnerID: uid, IncludeBlocks: true, Limit: req.Limit,
		})
		cancel()
		if err != nil {
			s.Logger.Warn("bm25 search failed", "err", err)
		} else {
			wikiResults = hits
		}
		// Apply time decay
		for i := range wikiResults {
			wikiResults[i].Score = s.Decay.Apply(wikiResults[i].Score, wikiResults[i].UpdatedAt)
		}
		resp.Wiki = make([]wikiHit, 0, len(wikiResults))
		for _, h := range wikiResults {
			resp.Wiki = append(resp.Wiki, wikiHit{
				ID: h.ID, Kind: h.Kind, PageID: h.PageID, ProjectID: h.ProjectID,
				Title: h.Title, Snippet: h.Snippet, Score: h.Score,
				UpdatedAt: h.UpdatedAt.UTC().Format(time.RFC3339),
			})
		}
	}

	var vectorResults []vector.Hit
	if wantVector {
		var pid *uuid.UUID
		if req.ProjectID != nil && *req.ProjectID != "" {
			if u, err := uuid.Parse(*req.ProjectID); err == nil {
				pid = &u
			}
		}
		// Embed the query. We bound the embed call separately from the
		// ANN call so a slow embedder doesn't burn the search budget on
		// the DB call as well.
		ec, ecancel := context.WithTimeout(r.Context(), 8*time.Second)
		qvec, err := s.Embedder.Embed(ec, req.Query)
		ecancel()
		if err != nil {
			s.Logger.Warn("vector embed failed", "err", err)
		} else if len(qvec) > 0 {
			// Over-fetch chunks (limit×3, floor 30) then collapse to page
			// granularity with a blended max+tail score — a bare top-K
			// chunk window starves recall when one page's chunks crowd
			// out other pages. See vector.OverFetchLimit/CollapsePages.
			vc, vcancel := context.WithTimeout(r.Context(), 5*time.Second)
			vhits, verr := s.Vector.Search(vc, vector.SearchOptions{
				OwnerID: uid, ProjectID: pid,
				QueryEmbedding: qvec, Limit: vector.OverFetchLimit(req.Limit),
			})
			vcancel()
			if verr != nil {
				s.Logger.Warn("vector search failed", "err", verr)
			} else {
				vectorResults = vector.CollapsePages(vhits, req.Limit)
				resp.Vector = make([]vectorHit, 0, len(vhits))
				for _, h := range vhits {
					resp.Vector = append(resp.Vector, vectorHit{
						ChunkID: h.ChunkID, PageID: h.PageID,
						BlockID: h.BlockID, ProjectID: h.ProjectID,
						Title: h.Title, Snippet: h.Snippet,
						Score:     h.Score,
						UpdatedAt: h.UpdatedAt.UTC().Format(time.RFC3339),
					})
				}
			}
		}
	}

	var graphResults []graphHit
	if wantGraph && len(wikiResults) > 0 {
		graphResults = s.expandGraph(r.Context(), wikiResults, req.Limit)
		// Community boost (P2-tail-4): pages sharing a Louvain
		// community with the BM25 top seeds get appended. Complements
		// the per-pair relevance edges from expandGraph by surfacing
		// same-cluster siblings even without direct strong edges.
		if extra := s.expandCommunity(r.Context(), wikiResults, graphResults); len(extra) > 0 {
			graphResults = append(graphResults, extra...)
		}
		resp.Graph = graphResults
	}

	// Personal notes path (N3). Opt-in only (include_notes=true) and
	// independent of scope — notes are personal, not wiki content.
	// Always carries the caller's uid (strict isolation); archived and
	// trashed notes are excluded inside SearchNotes.
	var noteResults []notestore.SearchHit
	if req.IncludeNotes && s.Notes != nil {
		nc, ncancel := context.WithTimeout(r.Context(), 5*time.Second)
		nhits, nerr := s.Notes.SearchNotes(nc, uid, req.Query, req.Limit)
		ncancel()
		if nerr != nil {
			s.Logger.Warn("notes search failed", "err", nerr)
		} else {
			noteResults = nhits
			resp.Notes = make([]noteHit, 0, len(nhits))
			for _, h := range nhits {
				resp.Notes = append(resp.Notes, noteHit{
					ID: h.ID.String(), Title: h.Title, Snippet: h.Snippet,
					Score:     h.Rank,
					UpdatedAt: h.UpdatedAt.UTC().Format(time.RFC3339),
				})
			}
		}
	}

	// Image extraction (P2-H-search-1). Mines markdown image refs
	// (`![alt](url)`) from the blocks of pages already surfaced by
	// the wiki + vector + graph paths. We don't search images
	// independently; the image grid is meant to be a multimodal
	// augmentation of text hits, not a separate retrieval lane.
	if wantWiki {
		imgs := s.extractImages(r.Context(), req.Query,
			wikiResults, vectorResults, graphResults)
		if len(imgs) > 0 {
			resp.Images = imgs
		}
	}

	var webResults []searxng.WebResult
	if wantWeb && s.SearxNG != nil && s.SearxNG.BaseURL != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		web, err := s.SearxNG.Search(ctx, req.Query, req.Limit)
		cancel()
		if err != nil {
			s.Logger.Warn("searxng failed", "err", err)
		} else {
			webResults = web
			resp.Web = make([]webHit, 0, len(web))
			for _, h := range web {
				resp.Web = append(resp.Web, webHit{
					Title: h.Title, URL: h.URL, Snippet: h.Snippet,
					Engine: h.Engine, Score: h.Score,
				})
			}
		}
	}

	// RRF fusion now runs for wiki scope too (not just all) so the
	// default project-search path can enjoy cross-encoder rerank (P1-2).
	// wiki fuses wiki+vector+graph (no web); all adds web. scope=web
	// never reaches here (wantWiki=false).
	if wantWiki && (len(wikiResults) > 0 || len(webResults) > 0 || len(vectorResults) > 0 || len(graphResults) > 0 || len(noteResults) > 0) {
		var lists [][]rrf.Result
		if len(wikiResults) > 0 {
			l := make([]rrf.Result, 0, len(wikiResults))
			for _, h := range wikiResults {
				l = append(l, rrf.Result{
					ID: "wiki:" + h.Kind + ":" + h.ID, Score: h.Score,
					Meta: map[string]any{
						"kind": h.Kind, "page_id": h.PageID, "project_id": h.ProjectID,
						"title": h.Title, "snippet": h.Snippet, "source": "wiki",
					},
				})
			}
			lists = append(lists, l)
		}
		if len(vectorResults) > 0 {
			// Vector hits already collapsed to page granularity upstream
			// (vector.CollapsePages, blended max+tail score) so a search
			// query matching several chunks of the same page doesn't let
			// that page dominate the fused list with many slots. The
			// seen-map dedup stays as a defensive no-op. The chunk_id
			// stays in Meta so callers can deep-link to the matching
			// slice when desired.
			seen := map[string]bool{}
			l := make([]rrf.Result, 0, len(vectorResults))
			for _, h := range vectorResults {
				if seen[h.PageID] {
					continue
				}
				seen[h.PageID] = true
				l = append(l, rrf.Result{
					ID: "wiki:page:" + h.PageID, Score: h.Score,
					Meta: map[string]any{
						"kind": "page", "page_id": h.PageID,
						"chunk_id":   h.ChunkID,
						"block_id":   h.BlockID,
						"project_id": h.ProjectID,
						"title":      h.Title,
						"snippet":    h.Snippet,
						"source":     "vector",
					},
				})
			}
			lists = append(lists, l)
		}
		if len(graphResults) > 0 {
			// Graph hits are already de-duplicated to one row per page
			// in expandGraph; we trust that here. ID prefix matches
			// the vector path so identical pages collapse in RRF (a
			// page surfaced via both vector and graph counts as one
			// item with combined ranks across both lists).
			l := make([]rrf.Result, 0, len(graphResults))
			for _, h := range graphResults {
				l = append(l, rrf.Result{
					ID: "wiki:page:" + h.PageID, Score: h.Score,
					Meta: map[string]any{
						"kind":          "page",
						"page_id":       h.PageID,
						"title":         h.Title,
						"via_seed_page": h.ViaSeed,
						"source":        "graph",
					},
				})
			}
			lists = append(lists, l)
		}
		if len(webResults) > 0 {
			l := make([]rrf.Result, 0, len(webResults))
			for _, h := range webResults {
				l = append(l, rrf.Result{
					ID: "web:" + h.URL, Score: h.Score,
					Meta: map[string]any{
						"url": h.URL, "title": h.Title, "snippet": h.Snippet,
						"engine": h.Engine, "source": "web",
					},
				})
			}
			lists = append(lists, l)
		}
		if len(noteResults) > 0 {
			// Notes 一路：ID 前缀 "note:" 与 wiki/web 命名空间隔离，
			// fused 里 kind="note" 供客户端分桶展示。
			l := make([]rrf.Result, 0, len(noteResults))
			for _, h := range noteResults {
				l = append(l, rrf.Result{
					ID: "note:" + h.ID.String(), Score: h.Rank,
					Meta: map[string]any{
						"kind": "note", "note_id": h.ID.String(),
						"title": h.Title, "snippet": h.Snippet, "source": "note",
					},
				})
			}
			lists = append(lists, l)
		}
		fused := rrf.Fuse(lists, 60, req.Limit)
		resp.Fused = make([]fusedHit, 0, len(fused))
		for _, f := range fused {
			resp.Fused = append(resp.Fused, fusedHit{ID: f.ID, Score: f.Score, Meta: f.Meta})
		}
	}

	// Cross-encoder rerank (P1-2): re-score the fused list by query/doc
	// relevance. Sits before feedback rerank so the user's verdicts nudge
	// the model's ranking, not the other way round. Skipped when no
	// reranker is wired (CI/dev) or the fused list is empty.
	if s.Reranker != nil && len(resp.Fused) > 0 {
		resp.Fused = s.rerankFused(r.Context(), req.Query, resp.Fused)
	}

	// Apply feedback rerank to the page-bearing ranked lists. Both Wiki
	// (scope=wiki) and Fused (scope=all) carry page_id; web-only results
	// have none and are left untouched. Raw Score is preserved — only
	// order changes, and a Feedback badge marks the rows the user moved.
	if len(verdicts) > 0 {
		if len(resp.Wiki) > 0 {
			resp.Wiki = reorderWiki(resp.Wiki, verdicts)
		}
		if len(resp.Fused) > 0 {
			resp.Fused = reorderFused(resp.Fused, verdicts)
		}
	}

	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "search api: result",
			"user_id", uid, "scope", req.Scope,
			"wiki_hits", len(resp.Wiki), "web_hits", len(resp.Web),
			"notes_hits", len(resp.Notes),
			"fused_hits", len(resp.Fused))
	}
	writeJSON(w, http.StatusOK, resp)
}

// rerankFused re-scores the fused list through the cross-encoder. Docs
// are built from each hit's snippet (blocks/web) or title (page-only).
// On rerank error we degrade gracefully and keep the RRF order — a
// model blip must never blank the result list.
func (s *Server) rerankFused(ctx context.Context, query string, hits []fusedHit) []fusedHit {
	docs := make([]string, len(hits))
	for i, h := range hits {
		doc, _ := h.Meta["snippet"].(string)
		if doc == "" {
			doc, _ = h.Meta["title"].(string)
		}
		docs[i] = doc
	}
	scores, err := s.Reranker.Rerank(ctx, query, docs, len(docs))
	if err != nil {
		if s.Logger != nil {
			s.Logger.Warn("rerank failed; keeping RRF order", "err", err)
		}
		return hits
	}
	out := make([]fusedHit, 0, len(hits))
	seen := make([]bool, len(hits))
	for _, sc := range scores {
		if sc.Index < 0 || sc.Index >= len(hits) || seen[sc.Index] {
			continue
		}
		seen[sc.Index] = true
		h := hits[sc.Index]
		rs := sc.Score
		h.RerankedScore = &rs
		out = append(out, h)
	}
	// Append any hits the reranker dropped (e.g. provider capped top_n)
	// in original order so we never lose results.
	for i, h := range hits {
		if !seen[i] {
			out = append(out, h)
		}
	}
	return out
}

// ─── auth + helpers ─────────────────────────────────────

// relatedLister is the narrow contract expandGraph needs from the
// relevance store — keeps the helper testable with a stub without
// dragging the full *wikirelevance.Store into every test fixture.
type relatedLister interface {
	ListRelated(ctx context.Context, pageID uuid.UUID, limit int) ([]wikirelevance.Related, error)
}

// markdownImageRE matches `![alt](url)` — the only image syntax
// biumind's wiki ingest currently emits. We deliberately don't accept
// HTML <img> tags: ingest-llm is instructed to use markdown only, and
// HTML in user-pasted content is rare enough that the noise from
// false positives (matching `<img>` inside code blocks etc.) doesn't
// pay off. Tested against ingest_parse fixtures.
var markdownImageRE = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// extractImages mines markdown image references out of every block on
// every page surfaced by the wiki/vector/graph paths, dedupes by URL
// (first sighting wins — that's almost always the page with the
// highest BM25 rank), and tags each image with whether its alt text
// contains any of the query's tokens.
//
// llm_wiki separates "matching" (alt overlap) from "supporting"
// (alt didn't match but the page did) — the client uses that flag
// to render a default-collapsed "Show all (+N)" toggle. We compute
// the boolean here so the client doesn't reimplement the tokenizer.
func (s *Server) extractImages(
	ctx context.Context,
	query string,
	wikiHits []bm25.Hit,
	vecHits []vector.Hit,
	graphHits []graphHit,
) []imageHit {
	if s == nil || s.BM25 == nil || s.BM25.Pool == nil {
		return nil
	}

	// Collect distinct page IDs in rank order (wiki first — those are
	// the highest-confidence text matches — then vector, then graph).
	type pageRef struct {
		id    uuid.UUID
		title string
	}
	seen := map[uuid.UUID]struct{}{}
	pages := make([]pageRef, 0, 16)
	addPage := func(idStr, title string) {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		pages = append(pages, pageRef{id: id, title: title})
	}
	for _, h := range wikiHits {
		addPage(h.PageID, h.Title)
	}
	for _, h := range vecHits {
		addPage(h.PageID, h.Title)
	}
	for _, h := range graphHits {
		addPage(h.PageID, h.Title)
	}
	if len(pages) == 0 {
		return nil
	}

	// Cap the page set to keep the per-search image-extraction cost
	// bounded. Beyond ~30 pages the user-perceived value of the
	// image grid drops fast (more decorative noise) while DB cost
	// rises linearly.
	const maxPages = 30
	if len(pages) > maxPages {
		pages = pages[:maxPages]
	}
	pageIDs := make([]uuid.UUID, len(pages))
	titleByID := make(map[uuid.UUID]string, len(pages))
	for i, p := range pages {
		pageIDs[i] = p.id
		titleByID[p.id] = p.title
	}

	// One round trip — block content + page id, only live blocks.
	qctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	rows, err := s.BM25.Pool.Query(qctx, `
		SELECT b.page_id, b.id, b.content->>'text', b.content->>'caption'
		  FROM brain.blocks b
		 WHERE b.page_id = ANY($1) AND b.deleted_at IS NULL
	`, pageIDs)
	if err != nil {
		s.Logger.Warn("image extract: block query failed", "err", err)
		return nil
	}
	defer rows.Close()

	tokens := tokenizeQuery(query)
	dedupe := map[string]bool{}
	var out []imageHit
	for rows.Next() {
		var (
			pid, bid uuid.UUID
			text     *string
			caption  *string
		)
		if err := rows.Scan(&pid, &bid, &text, &caption); err != nil {
			s.Logger.Warn("image extract: scan failed", "err", err)
			continue
		}
		body := ""
		if text != nil {
			body = *text
		}
		if caption != nil {
			body += "\n" + *caption
		}
		if !strings.Contains(body, "![") {
			continue
		}
		matches := markdownImageRE.FindAllStringSubmatch(body, -1)
		for _, m := range matches {
			alt := strings.TrimSpace(m[1])
			url := strings.TrimSpace(m[2])
			if url == "" || dedupe[url] {
				continue
			}
			dedupe[url] = true
			out = append(out, imageHit{
				URL:             url,
				Alt:             alt,
				PageID:          pid.String(),
				BlockID:         bid.String(),
				PageTitle:       titleByID[pid],
				AltMatchesQuery: altMatchesQuery(alt, tokens),
			})
		}
	}
	if err := rows.Err(); err != nil {
		s.Logger.Warn("image extract: rows err", "err", err)
	}

	// Sort: alt-matches first, preserving the page-rank order within
	// each bucket. The slice is small (typically <100); a stable
	// insertion sort beats sort.SliceStable for branch-predictor
	// friendliness in this size range.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if !a.AltMatchesQuery && b.AltMatchesQuery {
				out[j-1], out[j] = b, a
				continue
			}
			break
		}
	}
	return out
}

// tokenizeQuery splits a query into lowercase tokens. ASCII runs split
// on whitespace + punctuation; CJK characters become individual one-rune
// tokens. The tokenizer's job is purely to drive substring matching
// against image alt text — full BM25 query analysis lives in Postgres
// (the biumind_zhcn config). This helper only exists because the alt
// match needs to be coherent with what the user typed: a query like
// "总资产。" must still flag an image captioned "图：总资产合计".
func tokenizeQuery(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var (
		tokens []string
		buf    strings.Builder
	)
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		tokens = append(tokens, buf.String())
		buf.Reset()
	}
	for _, r := range q {
		switch {
		case isCJK(r):
			flush()
			tokens = append(tokens, string(r))
		case unicode.IsSpace(r) || unicode.IsPunct(r):
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3040 && r <= 0x30FF) || // Hiragana/Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul
}

func altMatchesQuery(alt string, tokens []string) bool {
	if len(tokens) == 0 || alt == "" {
		return false
	}
	altLower := strings.ToLower(alt)
	for _, tok := range tokens {
		if strings.Contains(altLower, tok) {
			return true
		}
	}
	return false
}

// expandCommunity widens the graph path with pages sharing a Louvain
// community with the BM25 top seeds. Light-touch query: take up to 3
// seeds, look up their community_id, fetch other live pages with the
// same community_id, exclude pages already in the seed/graph result
// set, return as graphHit rows tagged with `via_seed_page` for UI
// provenance. The contribution ranks below the per-pair edges (it's a
// weaker signal — "same broad topic" rather than "directly related"),
// which RRF naturally encodes via list ordering.
func (s *Server) expandCommunity(
	ctx context.Context,
	wikiHits []bm25.Hit,
	already []graphHit,
) []graphHit {
	if s == nil || s.BM25 == nil || s.BM25.Pool == nil || len(wikiHits) == 0 {
		return nil
	}
	const seedCap = 3
	const perCommunity = 8

	seenSeed := map[string]struct{}{}
	seeds := make([]bm25.Hit, 0, seedCap)
	for _, h := range wikiHits {
		if _, dup := seenSeed[h.PageID]; dup {
			continue
		}
		seenSeed[h.PageID] = struct{}{}
		seeds = append(seeds, h)
		if len(seeds) >= seedCap {
			break
		}
	}
	exclude := map[string]struct{}{}
	for k := range seenSeed {
		exclude[k] = struct{}{}
	}
	for _, h := range already {
		exclude[h.PageID] = struct{}{}
	}

	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var out []graphHit
	for _, seed := range seeds {
		seedID, err := uuid.Parse(seed.PageID)
		if err != nil {
			continue
		}
		// Two-step: first get the seed's community_id + project_id,
		// then fetch siblings. Inlined as a single CTE-style query
		// so we get one round trip per seed.
		rows, err := s.BM25.Pool.Query(cctx, `
			WITH seed AS (
			  SELECT community_id, project_id
			    FROM brain.pages
			   WHERE id = $1 AND deleted_at IS NULL
			     AND community_id IS NOT NULL
			)
			SELECT p.id, p.title
			  FROM brain.pages p, seed s
			 WHERE p.community_id = s.community_id
			   AND p.project_id = s.project_id
			   AND p.id <> $1
			   AND p.deleted_at IS NULL
			 ORDER BY p.updated_at DESC
			 LIMIT $2
		`, seedID, perCommunity)
		if err != nil {
			s.Logger.Warn("community expand failed",
				"seed", seed.PageID, "err", err)
			continue
		}
		for rows.Next() {
			var (
				id    uuid.UUID
				title string
			)
			if err := rows.Scan(&id, &title); err != nil {
				continue
			}
			pid := id.String()
			if _, has := exclude[pid]; has {
				continue
			}
			exclude[pid] = struct{}{}
			out = append(out, graphHit{
				PageID:  pid,
				Title:   title,
				ViaSeed: seed.PageID,
				// No relevance score for community-only hits; we set
				// a small fixed weight so RRF puts them below the
				// per-pair graph hits but above arbitrary tail.
				Score: 0.1,
			})
		}
		rows.Close()
	}
	return out
}

// expandGraph turns BM25 wiki hits into a graph-relevance list by
// taking the top-N page-kind hits as seeds and pulling their related
// pages from brain.page_relevance. The result is deduplicated to one
// row per page (highest score wins across seeds), capped to limit,
// and excludes any page that's already a wiki seed (don't double-rank
// the same page in two paths — RRF already handles that via the
// shared ID prefix).
func (s *Server) expandGraph(ctx context.Context, wikiHits []bm25.Hit, limit int) []graphHit {
	if s.Relevance == nil || len(wikiHits) == 0 {
		return nil
	}
	return expandGraphImpl(ctx, s.Relevance, wikiHits, limit, s.Logger)
}

// expandGraphImpl is the testable seam — same logic as expandGraph
// but takes the relatedLister directly so unit tests can pass a stub
// without setting up a real Store + DB.
func expandGraphImpl(
	ctx context.Context,
	rel relatedLister,
	wikiHits []bm25.Hit,
	limit int,
	logger *slog.Logger,
) []graphHit {
	if rel == nil || len(wikiHits) == 0 {
		return nil
	}
	const seedCap = 5 // top-K seeds — beyond this the graph blooms too wide
	const perSeed = 10

	// Collect distinct page IDs from the BM25 hits, preserving rank.
	// Block hits collapse to their owning page; that's fine because we
	// only need page-id seeds.
	seenSeed := make(map[string]struct{})
	seeds := make([]bm25.Hit, 0, seedCap)
	for _, h := range wikiHits {
		if _, dup := seenSeed[h.PageID]; dup {
			continue
		}
		seenSeed[h.PageID] = struct{}{}
		seeds = append(seeds, h)
		if len(seeds) >= seedCap {
			break
		}
	}

	// Per-page best score across all seeds — same page surfaced by
	// multiple seeds keeps the strongest evidence.
	type best struct {
		hit graphHit
	}
	pool := map[string]*best{}
	for _, seed := range seeds {
		seedID, err := uuid.Parse(seed.PageID)
		if err != nil {
			continue
		}
		gctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		rows, err := rel.ListRelated(gctx, seedID, perSeed)
		cancel()
		if err != nil {
			if logger != nil {
				logger.Warn("graph expand failed",
					"seed", seed.PageID, "err", err)
			}
			continue
		}
		for _, r := range rows {
			pid := r.OtherPageID.String()
			// Skip pages already in the seed set — RRF would just see
			// them twice with the same ID.
			if _, isSeed := seenSeed[pid]; isSeed {
				continue
			}
			cur, exists := pool[pid]
			if !exists || float64(r.Score) > cur.hit.Score {
				pool[pid] = &best{
					hit: graphHit{
						PageID:  pid,
						Title:   r.Title,
						ViaSeed: seed.PageID,
						Score:   float64(r.Score),
					},
				}
			}
		}
	}
	out := make([]graphHit, 0, len(pool))
	for _, b := range pool {
		out = append(out, b.hit)
	}
	// Sort by score DESC for deterministic output. The whole list goes
	// to RRF which only cares about rank, but a stable order makes the
	// `resp.Graph` field meaningful for direct consumers.
	sortGraphHits(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sortGraphHits(hs []graphHit) {
	// Insertion sort — the slice is bounded by seedCap × perSeed (50)
	// so a comparator-based sort would be over-engineered.
	for i := 1; i < len(hs); i++ {
		for j := i; j > 0 && hs[j].Score > hs[j-1].Score; j-- {
			hs[j-1], hs[j] = hs[j], hs[j-1]
		}
	}
}

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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg, "len": strconv.Itoa(len(msg))},
	})
}
