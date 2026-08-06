// Wiki tool surface for the brain MCP server.
//
// Six tools expose a stable contract so existing AI-client configurations
// port over with minimal change:
//
//	wiki.search        — hybrid BM25 + (optional) vector retrieval
//	wiki.list_pages    — list pages in a project
//	wiki.get_page      — fetch one page (optionally with blocks)
//	wiki.create_page   — create a page (title + optional frontmatter)
//	wiki.update_page   — update title / frontmatter (If-Match versioned)
//	wiki.ingest        — kick off the multi-page CoT ingest pipeline
//
// All tools share the same checkProject ownership gate that memory.*
// tools use; stdio transport uses the env-pinned user, HTTP transport
// uses the JWT subject. Result envelopes also follow the same MCP
// shape (content + structuredContent) so AI clients can render text
// and consume structured data uniformly.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/services/brain/internal/search/bm25"
	"github.com/biumind/biumind/services/brain/internal/search/rrf"
	"github.com/biumind/biumind/services/brain/internal/search/vector"
	wikiingest "github.com/biumind/biumind/services/brain/internal/wiki/ingest"
	wikirelevance "github.com/biumind/biumind/services/brain/internal/wiki/relevance"
	wikireviews "github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// ─── Schemas ───────────────────────────────────────────────────

// wikiToolSchemas extends the memory tool list. Loaded once at init
// time and concatenated into toolSchemas.
var wikiToolSchemas = []map[string]any{
	{
		"name": "wiki.search",
		"description": "Search wiki pages and blocks by query. " +
			"Returns hybrid BM25 + vector results when an embedder is configured, " +
			"otherwise BM25 only. Scope is the calling user's projects.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string"},
				"project_id": map[string]any{"type": "string", "description": "Optional UUID; omit to search across all owned projects."},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "wiki.list_pages",
		"description": "List pages in a project, newest first.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "string"},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 100},
			},
			"required": []string{"project_id"},
		},
	},
	{
		"name":        "wiki.get_page",
		"description": "Fetch one page by ID, optionally with its blocks.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"page_id":        map[string]any{"type": "string"},
				"include_blocks": map[string]any{"type": "boolean", "default": true},
			},
			"required": []string{"page_id"},
		},
	},
	{
		"name":        "wiki.create_page",
		"description": "Create a new page in a project.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id":  map[string]any{"type": "string"},
				"title":       map[string]any{"type": "string"},
				"parent_id":   map[string]any{"type": "string", "description": "Optional parent page UUID for tree structure."},
				"frontmatter": map[string]any{"type": "object", "description": "Optional YAML-equivalent metadata."},
			},
			"required": []string{"project_id", "title"},
		},
	},
	{
		"name": "wiki.update_page",
		"description": "Update a page's title and/or frontmatter. " +
			"Pass `version` (from a prior get_page) for optimistic concurrency; " +
			"omit to skip the check (last-writer-wins).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"page_id":     map[string]any{"type": "string"},
				"title":       map[string]any{"type": "string"},
				"frontmatter": map[string]any{"type": "object"},
				"version":     map[string]any{"type": "integer", "description": "If-Match version; omit for force update."},
			},
			"required": []string{"page_id"},
		},
	},
	{
		"name": "wiki.ingest",
		"description": "Submit raw text to the wiki LLM ingest pipeline. " +
			"Creates a task that the wiki-llm worker picks up via NATS, " +
			"streams CoT-generated wiki pages back as they finish.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "string"},
				"raw_text":   map[string]any{"type": "string", "description": "Source content (markdown / plain text)."},
				"title":      map[string]any{"type": "string", "description": "Optional task title shown in the UI."},
			},
			"required": []string{"project_id", "raw_text"},
		},
	},
	{
		"name": "wiki.list_reviews",
		"description": "List automated review items in a project (dedup / lint / sweep / merge / suggestion / contradiction). " +
			"Default returns open items only; pass status=resolved or dismissed to inspect history.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "string"},
				"kind":       map[string]any{"type": "string", "enum": []string{"dedup", "lint", "sweep", "merge", "suggestion", "contradiction"}, "description": "Optional filter."},
				"status":     map[string]any{"type": "string", "enum": []string{"open", "resolved", "dismissed"}, "default": "open"},
				"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 100},
			},
			"required": []string{"project_id"},
		},
	},
	{
		"name": "wiki.dismiss_review",
		"description": "Mark a review as dismissed (won't be re-flagged on future scans). " +
			"Use this when the suggestion is wrong — e.g. dedup flagged two pages that are intentionally separate.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "review_item UUID."},
			},
			"required": []string{"id"},
		},
	},
	{
		"name": "wiki.related_pages",
		"description": "List the top-K pages most related to the given page, " +
			"ranked by graph relevance (direct wikilinks + Adamic-Adar shared " +
			"neighbours + type affinity). Use this for 'see also' / 'what " +
			"else covers this topic' queries.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"page_id": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
			},
			"required": []string{"page_id"},
		},
	},
	{
		"name": "wiki.merge_pages",
		"description": "Fold the `duplicate_id` page into `canonical_id`: " +
			"all of duplicate's blocks move to canonical (appended past the existing tail), " +
			"vector chunks are re-linked, and duplicate is soft-deleted with a `merged_into` " +
			"frontmatter hint. Any open dedup review for the pair is auto-resolved. " +
			"Both pages must live in the same project.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"canonical_id": map[string]any{"type": "string", "description": "Surviving page UUID — the one to keep."},
				"duplicate_id": map[string]any{"type": "string", "description": "Page UUID to fold in (will be soft-deleted)."},
			},
			"required": []string{"canonical_id", "duplicate_id"},
		},
	},
}

// ─── Dispatch entry points (wired into mcp.go's switch) ────────

func (s *Server) callWikiSearch(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		Query     string `json:"query"`
		ProjectID string `json:"project_id"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "query is required"}
	}
	if s.BM25 == nil {
		return nil, &rpcError{Code: codeInternalError,
			Message: "wiki search not configured on this MCP server"}
	}
	if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = 20
	}

	var pid *uuid.UUID
	if a.ProjectID != "" {
		p, perr := s.checkProject(ctx, uid, a.ProjectID)
		if perr != nil {
			return nil, perr
		}
		pid = &p
	}

	bctx, bcancel := context.WithTimeout(ctx, 5*time.Second)
	hits, err := s.BM25.Search(bctx, a.Query, bm25.SearchOptions{
		ProjectID: pid, OwnerID: uid, IncludeBlocks: true, Limit: a.Limit,
	})
	bcancel()
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}

	// Optional vector path. Same provider feeds memory.recall and
	// search/api so query embeddings live in the same space as the
	// indexed chunk embeddings.
	var vhits []vector.Hit
	if s.Vector != nil && s.Embedder != nil {
		ec, ecancel := context.WithTimeout(ctx, 8*time.Second)
		qvec, eerr := s.Embedder.Embed(ec, a.Query)
		ecancel()
		if eerr == nil && len(qvec) > 0 {
			vc, vcancel := context.WithTimeout(ctx, 5*time.Second)
			vhits, _ = s.Vector.Search(vc, vector.SearchOptions{
				OwnerID: uid, ProjectID: pid,
				QueryEmbedding: qvec, Limit: a.Limit,
			})
			vcancel()
		}
	}

	// Build per-source ranked lists for RRF. When only BM25 produced
	// hits the fuse reduces to identity (single list → returns same
	// order). We dedupe vector hits by page so a page with multiple
	// chunk matches doesn't dominate the fused output.
	mode := "bm25"
	var lists [][]rrf.Result
	if len(hits) > 0 {
		l := make([]rrf.Result, 0, len(hits))
		for _, h := range hits {
			l = append(l, rrf.Result{
				ID: "wiki:" + h.Kind + ":" + h.ID, Score: h.Score,
				Meta: map[string]any{
					"kind": h.Kind, "page_id": h.PageID,
					"project_id": h.ProjectID,
					"title":      h.Title, "snippet": h.Snippet,
					"updated_at": h.UpdatedAt.UTC().Format(time.RFC3339),
				},
			})
		}
		lists = append(lists, l)
	}
	if len(vhits) > 0 {
		mode = "hybrid"
		seen := map[string]bool{}
		l := make([]rrf.Result, 0, len(vhits))
		for _, h := range vhits {
			if seen[h.PageID] {
				continue
			}
			seen[h.PageID] = true
			l = append(l, rrf.Result{
				ID: "wiki:page:" + h.PageID, Score: h.Score,
				Meta: map[string]any{
					"kind": "page", "page_id": h.PageID,
					"chunk_id":   h.ChunkID,
					"project_id": h.ProjectID,
					"title":      h.Title, "snippet": h.Snippet,
					"updated_at": h.UpdatedAt.UTC().Format(time.RFC3339),
					"source":     "vector",
				},
			})
		}
		lists = append(lists, l)
	}

	fused := rrf.Fuse(lists, 60, a.Limit)
	// Cross-encoder rerank (P1-2): re-score the fused list by query/doc
	// relevance when a reranker is wired. Mirrors search/api; sits after
	// RRF fusion. The model's score is stashed in Meta["reranked_score"].
	if s.Reranker != nil && len(fused) > 0 {
		fused = s.rerankMCP(ctx, a.Query, fused)
	}
	out := make([]map[string]any, 0, len(fused))
	for _, f := range fused {
		row := map[string]any{"id": f.ID, "score": f.Score}
		for k, v := range f.Meta {
			row[k] = v
		}
		out = append(out, row)
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("%d hits for %q (mode=%s)", len(out), a.Query, mode),
	}}, map[string]any{"hits": out, "mode": mode, "query": a.Query}), nil
}

// rerankMCP re-scores the RRF-fused list through the cross-encoder.
// Docs come from each result's snippet (Meta["snippet"]) or title. On
// error we keep the RRF order — a model blip must never blank results.
// The cross-encoder score is stashed in Meta["reranked_score"] so the
// MCP consumer sees the model's relevance alongside the RRF score.
func (s *Server) rerankMCP(ctx context.Context, query string, fused []rrf.Result) []rrf.Result {
	docs := make([]string, len(fused))
	for i, f := range fused {
		doc, _ := f.Meta["snippet"].(string)
		if doc == "" {
			doc, _ = f.Meta["title"].(string)
		}
		docs[i] = doc
	}
	scores, err := s.Reranker.Rerank(ctx, query, docs, len(docs))
	if err != nil {
		return fused
	}
	out := make([]rrf.Result, 0, len(fused))
	seen := make([]bool, len(fused))
	for _, sc := range scores {
		if sc.Index < 0 || sc.Index >= len(fused) || seen[sc.Index] {
			continue
		}
		seen[sc.Index] = true
		f := fused[sc.Index]
		f.Meta["reranked_score"] = sc.Score
		out = append(out, f)
	}
	for i, f := range fused {
		if !seen[i] {
			out = append(out, f)
		}
	}
	return out
}

func (s *Server) callWikiListPages(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		ProjectID string `json:"project_id"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	pid, perr := s.checkProject(ctx, uid, a.ProjectID)
	if perr != nil {
		return nil, perr
	}
	limit := a.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	pages, err := s.Wiki.ListPages(ctx, pid, limit)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	out := make([]map[string]any, len(pages))
	for i, p := range pages {
		out[i] = pageOut(p, false)
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("%d pages in project", len(out)),
	}}, map[string]any{"pages": out}), nil
}

func (s *Server) callWikiGetPage(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		PageID        string `json:"page_id"`
		IncludeBlocks *bool  `json:"include_blocks"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	pageID, err := uuid.Parse(a.PageID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "page_id must be a UUID"}
	}
	page, err := s.Wiki.GetPage(ctx, pageID)
	if err != nil {
		if errors.Is(err, wikistore.ErrNotFound) {
			return nil, &rpcError{Code: codeInvalidParams, Message: "page not found"}
		}
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	// Owner check: project ownership gates page access. The wiki store
	// doesn't denormalise owner_id onto pages, so we look it up.
	if _, perr := s.checkProject(ctx, uid, page.ProjectID.String()); perr != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "page not found"}
	}

	out := pageOut(page, true)
	includeBlocks := true
	if a.IncludeBlocks != nil {
		includeBlocks = *a.IncludeBlocks
	}
	if includeBlocks {
		blocks, berr := s.Wiki.ListBlocks(ctx, pageID)
		if berr != nil {
			return nil, &rpcError{Code: codeInternalError, Message: berr.Error()}
		}
		// Cap to first 200 blocks so a runaway page doesn't blow the
		// MCP response budget. Callers needing more should paginate
		// via the REST endpoint.
		const blockCap = 200
		if len(blocks) > blockCap {
			blocks = blocks[:blockCap]
		}
		bs := make([]map[string]any, len(blocks))
		for i, b := range blocks {
			bs[i] = blockOut(b)
		}
		out["blocks"] = bs
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("page %q (v%d)", page.Title, page.Version),
	}}, map[string]any{"page": out}), nil
}

func (s *Server) callWikiCreatePage(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		ProjectID   string         `json:"project_id"`
		Title       string         `json:"title"`
		ParentID    string         `json:"parent_id"`
		Frontmatter map[string]any `json:"frontmatter"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	pid, perr := s.checkProject(ctx, uid, a.ProjectID)
	if perr != nil {
		return nil, perr
	}
	if strings.TrimSpace(a.Title) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "title is required"}
	}
	var parent *uuid.UUID
	if a.ParentID != "" {
		v, err := uuid.Parse(a.ParentID)
		if err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "parent_id must be a UUID"}
		}
		parent = &v
	}
	page, err := s.Wiki.CreatePage(ctx, wikistore.CreatePageInput{
		ProjectID: pid, ParentID: parent, Title: a.Title,
		ActorID: uid.String(),
	})
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	// Apply frontmatter as a follow-up update so the create stays atomic
	// in the simple case; on FM failure the page still exists.
	if len(a.Frontmatter) > 0 {
		updated, uerr := s.Wiki.UpdatePage(ctx, wikistore.UpdatePageInput{
			PageID: page.ID, IfMatchVersion: page.Version,
			Frontmatter: a.Frontmatter, ActorID: uid.String(),
		})
		if uerr == nil {
			page = updated
		}
		// If frontmatter update fails we still return the created page —
		// caller can retry the FM via wiki.update_page. Surfacing a
		// soft warning would require an MCP-level "warnings" channel
		// the spec doesn't define.
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("created page %s (%q)", page.ID, page.Title),
	}}, map[string]any{"page": pageOut(page, true)}), nil
}

func (s *Server) callWikiUpdatePage(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		PageID      string         `json:"page_id"`
		Title       *string        `json:"title"`
		Frontmatter map[string]any `json:"frontmatter"`
		Version     int            `json:"version"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	pageID, err := uuid.Parse(a.PageID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "page_id must be a UUID"}
	}
	cur, err := s.Wiki.GetPage(ctx, pageID)
	if err != nil {
		if errors.Is(err, wikistore.ErrNotFound) {
			return nil, &rpcError{Code: codeInvalidParams, Message: "page not found"}
		}
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	if _, perr := s.checkProject(ctx, uid, cur.ProjectID.String()); perr != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "page not found"}
	}
	updated, err := s.Wiki.UpdatePage(ctx, wikistore.UpdatePageInput{
		PageID: pageID, IfMatchVersion: a.Version,
		Title: a.Title, Frontmatter: a.Frontmatter,
		ActorID: uid.String(),
	})
	if err != nil {
		if errors.Is(err, wikistore.ErrConflict) {
			return nil, &rpcError{
				Code:    codeInvalidParams,
				Message: fmt.Sprintf("version conflict (current v%d)", cur.Version),
				Data:    map[string]any{"current_version": cur.Version},
			}
		}
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("updated page %s to v%d", updated.ID, updated.Version),
	}}, map[string]any{"page": pageOut(updated, true)}), nil
}

func (s *Server) callWikiIngest(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		ProjectID string `json:"project_id"`
		RawText   string `json:"raw_text"`
		Title     string `json:"title"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	// Cheap validation runs first — empty raw_text and missing
	// dependencies don't need a DB round trip to detect, so failing
	// fast here keeps misconfigured callers from chewing on store
	// queries that would only error later.
	if strings.TrimSpace(a.RawText) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "raw_text is required"}
	}
	if s.Ingest == nil || s.Publisher == nil {
		return nil, &rpcError{Code: codeInternalError,
			Message: "wiki ingest not configured on this MCP server"}
	}
	pid, perr := s.checkProject(ctx, uid, a.ProjectID)
	if perr != nil {
		return nil, perr
	}
	task, err := s.Ingest.Create(ctx, wikiingest.CreateInput{
		ProjectID: pid, OwnerID: uid,
		RawText: a.RawText, Title: a.Title,
	})
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	// Publish to NATS so workers/wiki-llm picks it up. Mirror the HTTP
	// handler's policy: don't roll back the row on publish failure —
	// the row is still useful (visible as pending in the UI).
	payload := map[string]any{
		"task_id":    task.ID.String(),
		"project_id": task.ProjectID.String(),
		"owner_id":   task.OwnerID.String(),
		"title":      task.Title,
		"raw_text":   task.RawText,
	}
	pubCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.Publisher.Publish(pubCtx,
		"wiki.ingest.requested", "wiki.ingest.requested", payload); err != nil {
		// Soft-warn via the text content; structured field still has
		// the task id so callers know what to poll.
		return mcpResult([]map[string]any{{
			"type": "text",
			"text": fmt.Sprintf("ingest task %s created but NATS publish failed: %v",
				task.ID, err),
		}}, map[string]any{"task": ingestTaskOut(task), "publish_error": err.Error()}), nil
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("ingest task %s queued for project %s",
			task.ID, task.ProjectID),
	}}, map[string]any{"task": ingestTaskOut(task)}), nil
}

func (s *Server) callWikiListReviews(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		ProjectID string `json:"project_id"`
		Kind      string `json:"kind"`
		Status    string `json:"status"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if s.Reviews == nil {
		return nil, &rpcError{Code: codeInternalError,
			Message: "wiki reviews not configured on this MCP server"}
	}
	pid, perr := s.checkProject(ctx, uid, a.ProjectID)
	if perr != nil {
		return nil, perr
	}
	status := a.Status
	if status == "" {
		status = wikireviews.StatusOpen
	}
	items, err := s.Reviews.List(ctx, wikireviews.ListInput{
		ProjectID: pid, Kind: a.Kind, Status: status, Limit: a.Limit,
	})
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, reviewOut(it))
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("%d %s reviews (kind=%s)",
			len(out), status, orAll(a.Kind)),
	}}, map[string]any{"reviews": out, "status": status}), nil
}

func (s *Server) callWikiDismissReview(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if s.Reviews == nil {
		return nil, &rpcError{Code: codeInternalError,
			Message: "wiki reviews not configured on this MCP server"}
	}
	id, err := uuid.Parse(a.ID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "id must be a UUID"}
	}
	it, err := s.Reviews.Get(ctx, id)
	if err != nil {
		if errors.Is(err, wikireviews.ErrNotFound) {
			return nil, &rpcError{Code: codeInvalidParams, Message: "review not found"}
		}
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	if it.OwnerID != uid {
		return nil, &rpcError{Code: codeInvalidParams, Message: "review not found"}
	}
	if err := s.Reviews.SetStatus(ctx, id, wikireviews.StatusDismissed); err != nil {
		if errors.Is(err, wikireviews.ErrNotFound) {
			return nil, &rpcError{Code: codeInvalidParams, Message: "review not found"}
		}
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("review %s dismissed", id),
	}}, map[string]any{"id": id.String(), "status": wikireviews.StatusDismissed}), nil
}

func (s *Server) callWikiMergePages(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		CanonicalID string `json:"canonical_id"`
		DuplicateID string `json:"duplicate_id"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	canonicalID, err := uuid.Parse(a.CanonicalID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "canonical_id must be a UUID"}
	}
	duplicateID, err := uuid.Parse(a.DuplicateID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "duplicate_id must be a UUID"}
	}
	if canonicalID == duplicateID {
		return nil, &rpcError{Code: codeInvalidParams,
			Message: "canonical_id and duplicate_id must differ"}
	}

	// Both pages: fetch, ownership check via project ownership.
	canonical, err := s.Wiki.GetPage(ctx, canonicalID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "canonical page not found"}
	}
	if _, perr := s.checkProject(ctx, uid, canonical.ProjectID.String()); perr != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "canonical page not found"}
	}
	duplicate, err := s.Wiki.GetPage(ctx, duplicateID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "duplicate page not found"}
	}
	if duplicate.ProjectID != canonical.ProjectID {
		return nil, &rpcError{Code: codeInvalidParams,
			Message: "both pages must belong to the same project"}
	}

	if err := s.Wiki.MergePages(ctx, canonicalID, duplicateID, uid.String()); err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}

	// Best-effort auto-resolve of the matching dedup review.
	if s.Reviews != nil {
		key := wikireviews.DedupKeyForPair(canonicalID, duplicateID)
		if it := lookupReviewByKey(ctx, s.Reviews, key); it != nil &&
			it.Status == wikireviews.StatusOpen {
			_ = s.Reviews.SetStatus(ctx, it.ID, wikireviews.StatusResolved)
		}
	}

	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("merged %s into %s", duplicateID, canonicalID),
	}}, map[string]any{
		"canonical_id": canonicalID.String(),
		"duplicate_id": duplicateID.String(),
		"merged":       true,
	}), nil
}

// lookupReviewByKey is a best-effort helper. Reviews is package-private
// so we add a small adapter here rather than exposing the inner pool.
// On any error we return nil — the merge already succeeded; review
// auto-resolve is just UX cleanup.
func lookupReviewByKey(ctx context.Context, store *wikireviews.Store, key string) *wikireviews.Item {
	id, err := store.IDByDedupeKey(ctx, key)
	if err != nil || id == uuid.Nil {
		return nil
	}
	it, err := store.Get(ctx, id)
	if err != nil {
		return nil
	}
	return it
}

func (s *Server) callWikiRelatedPages(ctx context.Context, uid uuid.UUID, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		PageID string `json:"page_id"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if s.Relevance == nil {
		return nil, &rpcError{Code: codeInternalError,
			Message: "wiki relevance not configured on this MCP server"}
	}
	pageID, err := uuid.Parse(a.PageID)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "page_id must be a UUID"}
	}
	page, err := s.Wiki.GetPage(ctx, pageID)
	if err != nil {
		if errors.Is(err, wikistore.ErrNotFound) {
			return nil, &rpcError{Code: codeInvalidParams, Message: "page not found"}
		}
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	if _, perr := s.checkProject(ctx, uid, page.ProjectID.String()); perr != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "page not found"}
	}
	rows, err := s.Relevance.ListRelated(ctx, pageID, a.Limit)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"page_id": r.OtherPageID.String(),
			"title":   r.Title,
			"score":   r.Score,
			"signals": r.Signals,
		})
	}
	return mcpResult([]map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("%d related pages for %s", len(out), pageID),
	}}, map[string]any{"page_id": pageID.String(), "related": out}), nil
}

// _wikirelevance keeps the relevance import live even if the only
// consumer (callWikiRelatedPages) gets dead-coded by future refactors.
var _ = wikirelevance.WeightDirectLink

func reviewOut(it *wikireviews.Item) map[string]any {
	pages := make([]string, 0, len(it.PageIDs))
	for _, p := range it.PageIDs {
		pages = append(pages, p.String())
	}
	out := map[string]any{
		"id":          it.ID.String(),
		"project_id":  it.ProjectID.String(),
		"kind":        it.Kind,
		"status":      it.Status,
		"title":       it.Title,
		"description": it.Description,
		"page_ids":    pages,
		"payload":     it.Payload,
		"created_at":  it.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":  it.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if it.ResolvedAt != nil {
		out["resolved_at"] = it.ResolvedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func orAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

// ─── Output projections ────────────────────────────────────────

func pageOut(p *wikistore.Page, includeFM bool) map[string]any {
	out := map[string]any{
		"id":         p.ID.String(),
		"project_id": p.ProjectID.String(),
		"title":      p.Title,
		"version":    p.Version,
		"created_at": p.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": p.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if p.ParentID != nil {
		out["parent_id"] = p.ParentID.String()
	}
	out["body_md"] = p.BodyMd
	if includeFM {
		out["frontmatter"] = p.Frontmatter
	}
	return out
}

func blockOut(b *wikistore.Block) map[string]any {
	out := map[string]any{
		"id":       b.ID.String(),
		"page_id":  b.PageID.String(),
		"position": b.Position,
		"type":     b.Type,
		"content":  b.Content,
		"version":  b.Version,
	}
	if b.ParentID != nil {
		out["parent_id"] = b.ParentID.String()
	}
	return out
}

func ingestTaskOut(t *wikiingest.Task) map[string]any {
	out := map[string]any{
		"id":         t.ID.String(),
		"project_id": t.ProjectID.String(),
		"status":     t.Status,
		"title":      t.Title,
		"created_at": t.CreatedAt.UTC().Format(time.RFC3339),
	}
	if t.SourceID != nil {
		out["source_id"] = t.SourceID.String()
	}
	return out
}
