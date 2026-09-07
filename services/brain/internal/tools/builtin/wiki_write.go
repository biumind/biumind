package builtin

// wiki write tools (S3 P0-1) — autonomous-maintenance agent loop's write
// surface. Three mutating tools wrapping wikistore.Store, owner-scoped via
// tools.UserIDFromContext (same pattern as WikiSearch). They are the
// brain-side twins of the MCP wiki.* write tools (memory/mcp/wiki_tools.go)
// re-exposed as tools.Tool so the chat AgentLoop framework (which only
// speaks tools.Registry) can dispatch them.
//
// Why a second copy and not sharing the MCP handlers: the MCP server is a
// separate process (cmd/memory-mcp) with its own handler signature
// (ctx, uid, raw) (any, *rpcError); the chat agent loop calls
// tools.Registry.Invoke(ctx, mode, name, input). The two registries are
// intentionally incompatible (chat is in-process, MCP is over HTTP/stdio).
// The shared part is the underlying wikistore.Store + the ownership-check
// pattern (GetProject → OwnerID == uid), which both copies call.
//
// Safety (see docs/BiuMind-S3-AgentLoop-Design.md §7.1): these tools are
// advertised to the LLM only under WikiAgentToolAllowlist (plain chat stays
// read-only). Write safety rests on:
//   - version乐观锁 (wiki_update_page requires If-Match version — agent must
//     get_page first to learn it; OCC rejects stale writes with ErrConflict)
//   - page_revisions rollback (S2 ④ — UpdatePage/UpdatePageBody snapshot
//     pre-write state inside their tx, so any LLM mistake is restorable)
// NOT on biumindkit permission (RunV2 BypassPermissions).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/tools"
	wikireviews "github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
)

// pageOutForTool renders a wiki page into the JSON map returned to the LLM.
// Mirrors the shape of mcp pageOut (wiki_tools.go pageOut) so the agent
// sees consistent page objects across MCP and the chat loop.
func pageOutForTool(p *wikistore.Page) map[string]any {
	out := map[string]any{
		"id":         p.ID.String(),
		"project_id": p.ProjectID.String(),
		"title":      p.Title,
		"version":    p.Version,
		"body_md":    p.BodyMd,
	}
	if p.ParentID != nil {
		out["parent_id"] = p.ParentID.String()
	}
	return out
}

// checkProjectOwned verifies projectID belongs to uid via GetProject. Returns
// the parsed project id on success. Mirrors mcp.checkProject semantics:
// missing project or non-owner both fail (we collapse to a single "not found"
// to avoid leaking existence — same as wiki/api ownsProject).
func checkProjectOwned(ctx context.Context, st *wikistore.Store, uid uuid.UUID, raw string) (uuid.UUID, error) {
	pid, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("project_id must be a UUID")
	}
	p, err := st.GetProject(ctx, pid)
	if err != nil {
		return uuid.Nil, fmt.Errorf("project not found")
	}
	if p.OwnerID != uid {
		return uuid.Nil, fmt.Errorf("project not found")
	}
	return pid, nil
}

// checkPageOwned fetches a page and verifies its project belongs to uid.
// Returns (page, project_id) on success. Collapses missing/forbidden to
// "page not found".
func checkPageOwned(ctx context.Context, st *wikistore.Store, uid uuid.UUID, pageID uuid.UUID) (*wikistore.Page, error) {
	cur, err := st.GetPage(ctx, pageID)
	if err != nil {
		if errors.Is(err, wikistore.ErrNotFound) {
			return nil, fmt.Errorf("page not found")
		}
		return nil, err
	}
	if _, err := checkProjectOwned(ctx, st, uid, cur.ProjectID.String()); err != nil {
		return nil, fmt.Errorf("page not found")
	}
	return cur, nil
}

// WikiCreatePage returns the wiki page-creation write tool. Agent loop uses
// it to materialize new pages from ingested sources. ReadOnly=false →
// biumindkit treats as side-effecting.
func WikiCreatePage(st *wikistore.Store) tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "project_id": {"type": "string", "description": "UUID of the project to create the page in."},
    "title": {"type": "string", "description": "Page title. Required."},
    "parent_id": {"type": "string", "description": "Optional UUID of the parent page (tree structure)."},
    "frontmatter": {"type": "object", "description": "Optional frontmatter map (e.g. type, tags, sources)."},
    "body_md": {"type": "string", "description": "Optional authoritative markdown body. When set, the store projects it into blocks inside the create tx."}
  },
  "required": ["project_id", "title"]
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "wiki_create_page",
			Description: "Create a new wiki page in a project you own. Use to materialize knowledge from sources. The page is created atomically with its markdown body projected into blocks. Does NOT overwrite existing pages — to change an existing page use wiki_update_page with its current version.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeCloud,
		},
		ReadOnly: false,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			uid := tools.UserIDFromContext(ctx)
			if uid == uuid.Nil {
				return nil, errors.New("wiki_create_page: missing user identity in context")
			}
			var a struct {
				ProjectID   string         `json:"project_id"`
				Title       string         `json:"title"`
				ParentID    string         `json:"parent_id"`
				Frontmatter map[string]any `json:"frontmatter"`
				BodyMd      string         `json:"body_md"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("wiki_create_page: bad input: %w", err)
			}
			pid, err := checkProjectOwned(ctx, st, uid, a.ProjectID)
			if err != nil {
				return nil, fmt.Errorf("wiki_create_page: %w", err)
			}
			if a.Title == "" {
				return nil, errors.New("wiki_create_page: title is required")
			}
			var parent *uuid.UUID
			if a.ParentID != "" {
				v, err := uuid.Parse(a.ParentID)
				if err != nil {
					return nil, errors.New("wiki_create_page: parent_id must be a UUID")
				}
				parent = &v
			}
			page, err := st.CreatePage(ctx, wikistore.CreatePageInput{
				ProjectID:   pid,
				ParentID:    parent,
				Title:       a.Title,
				Frontmatter: a.Frontmatter,
				BodyMd:      a.BodyMd,
				ActorID:     uid.String(),
			})
			if err != nil {
				return nil, fmt.Errorf("wiki_create_page: %w", err)
			}
			return map[string]any{
				"created": true,
				"page":    pageOutForTool(page),
			}, nil
		},
	}
}

// WikiUpdatePage returns the wiki page-update write tool. Supports title /
// frontmatter (metadata) and body_md (authoritative body) in one call — at
// least one must be set. version (If-Match) is required so the agent must
// get_page first (OCC乐观锁 gate).
func WikiUpdatePage(st *wikistore.Store) tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "page_id": {"type": "string", "description": "UUID of the page to update."},
    "version": {"type": "integer", "description": "Current page version (If-Match乐观锁). Get the page first to learn it; stale versions are rejected."},
    "title": {"type": "string", "description": "Optional new title. Omit to leave unchanged."},
    "frontmatter": {"type": "object", "description": "Optional frontmatter merge map. Omit to leave unchanged."},
    "body_md": {"type": "string", "description": "Optional new authoritative markdown body. When set, the store reconciles blocks (preserving block_id continuity) inside the update tx."}
  },
  "required": ["page_id", "version"]
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "wiki_update_page",
			Description: "Update an existing wiki page's metadata (title / frontmatter) and/or body (body_md). The version field is an optimistic-concurrency gate — get the page first to learn its current version. At least one of title / frontmatter / body_md must be set. Every update snapshots the pre-write state into page_revisions so mistakes are restorable.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeCloud,
		},
		ReadOnly: false,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			uid := tools.UserIDFromContext(ctx)
			if uid == uuid.Nil {
				return nil, errors.New("wiki_update_page: missing user identity in context")
			}
			var a struct {
				PageID      string         `json:"page_id"`
				Version     int            `json:"version"`
				Title       *string        `json:"title"`
				Frontmatter map[string]any `json:"frontmatter"`
				BodyMd      string         `json:"body_md"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("wiki_update_page: bad input: %w", err)
			}
			pageID, err := uuid.Parse(a.PageID)
			if err != nil {
				return nil, errors.New("wiki_update_page: page_id must be a UUID")
			}
			cur, err := checkPageOwned(ctx, st, uid, pageID)
			if err != nil {
				return nil, fmt.Errorf("wiki_update_page: %w", err)
			}
			if a.Title == nil && a.Frontmatter == nil && a.BodyMd == "" {
				return nil, errors.New("wiki_update_page: at least one of title / frontmatter / body_md must be set")
			}

			// Metadata phase (title/frontmatter) — only if requested.
			page := cur
			runID := tools.RunIDFromContext(ctx) // agent run 审计归属；"" = NULL
			if a.Title != nil || a.Frontmatter != nil {
				updated, err := st.UpdatePage(ctx, wikistore.UpdatePageInput{
					PageID:         pageID,
					IfMatchVersion: a.Version,
					Title:          a.Title,
					Frontmatter:    a.Frontmatter,
					ActorID:        uid.String(),
					RunID:          runID,
				})
				if err != nil {
					if errors.Is(err, wikistore.ErrConflict) {
						return nil, fmt.Errorf("wiki_update_page: version conflict (current v%d)", cur.Version)
					}
					return nil, fmt.Errorf("wiki_update_page: %w", err)
				}
				page = updated
			}

			// Body phase — uses the latest version (post-metadata if that ran,
			// else the original If-Match). Reconciliation preserves block_id
			// continuity so graph edges / chunks / backlinks don't dangle.
			if a.BodyMd != "" {
				updated, err := st.UpdatePageBody(ctx, wikistore.UpdatePageBodyInput{
					PageID:         pageID,
					BodyMd:         a.BodyMd,
					IfMatchVersion: page.Version,
					ActorID:        uid.String(),
					RunID:          runID,
				})
				if err != nil {
					if errors.Is(err, wikistore.ErrConflict) {
						return nil, fmt.Errorf("wiki_update_page: version conflict during body update (current v%d)", page.Version)
					}
					return nil, fmt.Errorf("wiki_update_page: body: %w", err)
				}
				page = updated
			}
			return map[string]any{
				"updated": true,
				"page":    pageOutForTool(page),
			}, nil
		},
	}
}

// WikiMergePages returns the wiki page-merge write tool. Folds duplicate into
// canonical (migrates blocks, relinks vectors, soft-deletes duplicate).
// ReadOnly=false.
//
// After a successful merge it best-effort auto-resolves the matching
// kind=dedup review — mirrors mcp.callWikiMergePages (S3 §7.4). rv may be
// nil (tests / unwired); the merge itself succeeds without it.
func WikiMergePages(st *wikistore.Store, rv *wikireviews.Store) tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "canonical_id": {"type": "string", "description": "UUID of the page to keep (the survivor)."},
    "duplicate_id": {"type": "string", "description": "UUID of the page to fold in and soft-delete."}
  },
  "required": ["canonical_id", "duplicate_id"]
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "wiki_merge_pages",
			Description: "Merge two wiki pages: fold duplicate_id into canonical_id (migrate blocks, relink chunks/vectors, rewrite [[duplicate-title]] wikilinks in other pages to the canonical title, soft-delete the duplicate). Both pages must belong to the same project you own. Use to resolve duplicate / near-duplicate pages flagged by the dedup review queue.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeCloud,
		},
		ReadOnly: false,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			uid := tools.UserIDFromContext(ctx)
			if uid == uuid.Nil {
				return nil, errors.New("wiki_merge_pages: missing user identity in context")
			}
			var a struct {
				CanonicalID string `json:"canonical_id"`
				DuplicateID string `json:"duplicate_id"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("wiki_merge_pages: bad input: %w", err)
			}
			canonicalID, err := uuid.Parse(a.CanonicalID)
			if err != nil {
				return nil, errors.New("wiki_merge_pages: canonical_id must be a UUID")
			}
			duplicateID, err := uuid.Parse(a.DuplicateID)
			if err != nil {
				return nil, errors.New("wiki_merge_pages: duplicate_id must be a UUID")
			}
			if canonicalID == duplicateID {
				return nil, errors.New("wiki_merge_pages: canonical_id and duplicate_id must differ")
			}
			canonical, err := checkPageOwned(ctx, st, uid, canonicalID)
			if err != nil {
				return nil, fmt.Errorf("wiki_merge_pages: canonical: %w", err)
			}
			duplicate, err := checkPageOwned(ctx, st, uid, duplicateID)
			if err != nil {
				return nil, fmt.Errorf("wiki_merge_pages: duplicate: %w", err)
			}
			if duplicate.ProjectID != canonical.ProjectID {
				return nil, errors.New("wiki_merge_pages: both pages must belong to the same project")
			}
			if err := st.MergePages(ctx, canonicalID, duplicateID, uid.String(),
				tools.RunIDFromContext(ctx)); err != nil {
				return nil, fmt.Errorf("wiki_merge_pages: %w", err)
			}
			// Best-effort auto-resolve of the matching dedup review
			// (same shape as mcp.callWikiMergePages): failure here must
			// not fail the merge — the review can be resolved by hand.
			if rv != nil {
				key := wikireviews.DedupKeyForPair(canonicalID, duplicateID)
				if id, err := rv.IDByDedupeKey(ctx, key); err == nil && id != uuid.Nil {
					if it, err := rv.Get(ctx, id); err == nil && it.Status == wikireviews.StatusOpen {
						_ = rv.SetStatus(ctx, it.ID, wikireviews.StatusResolved)
					}
				}
			}
			return map[string]any{
				"canonical_id": canonicalID.String(),
				"duplicate_id": duplicateID.String(),
				"merged":       true,
			}, nil
		},
	}
}
