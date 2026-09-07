// Package store is the data access layer for Brain.Wiki.
//
// Every mutation:
//
//  1. Updates the row (with version check for If-Match)
//  2. Inserts an `events` row (which triggers pg_notify)
//
// Both happen in a single tx so observers see consistent state.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/services/brain/internal/wiki/mdparse"
	"github.com/biumind/biumind/services/brain/internal/wiki/templates"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("version conflict")
)

type Project struct {
	ID         uuid.UUID
	OwnerID    uuid.UUID
	Name       string
	TemplateID string // "" when the project was created without a template
	CreatedAt  time.Time
}

type Page struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	ParentID    *uuid.UUID
	Title       string
	Frontmatter map[string]any
	BodyMd      string // §⑤ Path C 权威正文；blocks 为其 mdparse 派生投影
	ShareMode   string
	Version     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Block struct {
	ID        uuid.UUID
	PageID    uuid.UUID
	ParentID  *uuid.UUID
	Position  float64
	Type      string
	Content   map[string]any
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Revision —— Wiki 页版本历史快照（迁移 00065）。edit = 5 写入口写前旧态
// 全量快照（5 分钟窗口合并 + Prune 可清）；restore = 恢复前自动备份（永久）。
// 镜像 note/store Revision（00059），差异：wiki 无 user 概念用 actor_id；
// 正文非单字段，frontmatter + 全部 live blocks 序列化为 blocks_json。
type Revision struct {
	ID            uuid.UUID
	PageID        uuid.UUID
	ProjectID     uuid.UUID
	ActorID       string
	Title         string
	Frontmatter   map[string]any
	BodyMd        string // §⑤ 写前 body_md 原文（restore 无损恢复用）
	BlocksJSON    json.RawMessage
	ChangeType    string
	ChangeSummary *string
	// RunID —— 产生该快照的 agent run id（迁移 00010，§1.2 P2 变更审计）；
	// 人工编辑 / MCP 路径无 run 上下文，为 ""。
	RunID     string
	CreatedAt time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// ─── Projects ───────────────────────────────────────────

// CreateProjectWithTemplate inserts a project (recording template_id for
// traceability) and, when seed is non-empty, writes the template's seed
// pages + blocks in the SAME transaction. An empty templateID stores NULL
// (old clients / "general" template); an empty seed creates an empty
// project. Atomicity matters: a half-seeded project (pages without blocks,
// or a project row without its schema page) would confuse the reader and
// the chunker, so everything commits together or not at all.
//
// Seed blocks are produced by the templates package via mdparse, so seeded
// pages are real heading/text/code blocks and flow through the normal
// embedworker pipeline with headingPath — no special-casing downstream.
func (s *Store) CreateProjectWithTemplate(ctx context.Context, owner uuid.UUID, name, templateID string, seed []templates.SeedPage) (*Project, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	p := &Project{}
	// NULL when templateID is "" (keeps the column nullable for old rows +
	// the general/blank case). *string nil → NULL via pgx.
	var tid *string
	if templateID != "" {
		tid = &templateID
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.projects (owner_id, name, template_id) VALUES ($1, $2, $3)
		RETURNING id, owner_id, name, COALESCE(template_id, '') AS template_id, created_at
	`, owner, name, tid).Scan(&p.ID, &p.OwnerID, &p.Name, &p.TemplateID, &p.CreatedAt)
	if err != nil {
		return nil, err
	}

	for _, sp := range seed {
		frontmatter := []byte("{}")
		if len(sp.Frontmatter) > 0 {
			if b, err := json.Marshal(sp.Frontmatter); err == nil {
				frontmatter = b
			}
		}
		var pageID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO brain.pages (project_id, title, frontmatter, body_md)
			VALUES ($1, $2, $3, $4) RETURNING id
		`, p.ID, sp.Title, frontmatter, sp.BodyMd).Scan(&pageID); err != nil {
			return nil, fmt.Errorf("seed page %q: %w", sp.Title, err)
		}
		if err := emitEvent(ctx, tx, p.ID, "user", "template", "page.created", map[string]any{
			"page_id": pageID, "title": sp.Title,
		}); err != nil {
			return nil, err
		}
		for i, b := range sp.Blocks {
			contentJSON, _ := json.Marshal(b.Content)
			var blockID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO brain.blocks (page_id, position, type, content)
				VALUES ($1, $2, $3, $4) RETURNING id
			`, pageID, float64(i+1), b.Type, contentJSON).Scan(&blockID); err != nil {
				return nil, fmt.Errorf("seed block %d on %q: %w", i, sp.Title, err)
			}
			if err := emitEvent(ctx, tx, p.ID, "user", "template", "block.created", map[string]any{
				"page_id":    pageID,
				"block_id":   blockID,
				"project_id": p.ID,
				"type":       b.Type,
				"content":    b.Content,
			}); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (*Project, error) {
	p := &Project{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, COALESCE(template_id, '') AS template_id, created_at
		FROM brain.projects WHERE id = $1
	`, id).Scan(&p.ID, &p.OwnerID, &p.Name, &p.TemplateID, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProjects(ctx context.Context, owner uuid.UUID, limit int) ([]*Project, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, name, COALESCE(template_id, '') AS template_id, created_at
		FROM brain.projects
		WHERE owner_id = $1 ORDER BY created_at DESC LIMIT $2
	`, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.OwnerID, &p.Name, &p.TemplateID, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─── Pages ──────────────────────────────────────────────

type CreatePageInput struct {
	ProjectID uuid.UUID
	ParentID  *uuid.UUID
	Title     string
	// Frontmatter, when non-nil, seeds the page's frontmatter jsonb at
	// creation. nil keeps the existing default ('{}'). Used by feature
	// callers (deep research, ingest) to mark provenance / type so the
	// UI can later filter by origin without a separate column.
	Frontmatter map[string]any
	// BodyMd is the authoritative markdown body (§⑤ Path C). When non-empty
	// the store projects it into brain.blocks via mdparse inside the same tx
	// so chunks/graph/embed see real blocks. Empty = blank page.
	BodyMd  string
	ActorID string
}

func (s *Store) CreatePage(ctx context.Context, in CreatePageInput) (*Page, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	p := &Page{}
	frontmatter := []byte("{}")
	if len(in.Frontmatter) > 0 {
		if b, err := json.Marshal(in.Frontmatter); err == nil {
			frontmatter = b
		}
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.pages (project_id, parent_id, title, frontmatter, body_md)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
	`, in.ProjectID, in.ParentID, in.Title, frontmatter, in.BodyMd).Scan(
		&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &frontmatter, &p.BodyMd, &p.ShareMode, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert page: %w", err)
	}
	_ = json.Unmarshal(frontmatter, &p.Frontmatter)

	if err := emitEvent(ctx, tx, in.ProjectID, "user", in.ActorID, "page.created", map[string]any{
		"page_id": p.ID, "title": p.Title,
	}); err != nil {
		return nil, err
	}
	// §⑤ Path C：body_md 权威。创建带正文时事务内 mdparse 投影 blocks（在 page.created 之后，
	// 事件顺序合理；下游 chunks/graph/embed 无感，吃 blocks 投影）。
	if in.BodyMd != "" {
		if err := insertBlocksTx(ctx, tx, p.ID, in.ProjectID, in.ActorID, mdparse.ParseBlocks(in.BodyMd)); err != nil {
			return nil, fmt.Errorf("project body blocks: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) GetPage(ctx context.Context, id uuid.UUID) (*Page, error) {
	p := &Page{}
	frontmatter := []byte("{}")
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
		FROM brain.pages WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(
		&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &frontmatter, &p.BodyMd, &p.ShareMode, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(frontmatter, &p.Frontmatter)
	return p, nil
}

// BacklinkCandidate is one block-with-its-page row, returned by
// ListBacklinkCandidates. The caller (wiki/api/backlinks) walks the
// list and decides which rows actually contain a [[wikilink]] to
// the target page.
type BacklinkCandidate struct {
	PageID    uuid.UUID
	PageTitle string
	BlockID   uuid.UUID
	Text      string
	UpdatedAt time.Time
}

// ListBacklinkCandidates returns all alive blocks in `projectID`
// (excluding blocks of `excludePageID`) whose text contains a `[[`,
// newest-page first. Caller does the regex filtering since the
// matching rules (case-insensitive title match, alias-tolerant) live
// next to the API surface where they're easier to evolve.
func (s *Store) ListBacklinkCandidates(ctx context.Context, projectID, excludePageID uuid.UUID, limit int) ([]BacklinkCandidate, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.title, b.id, COALESCE(b.content->>'text', ''), p.updated_at
		FROM brain.pages p
		JOIN brain.blocks b ON b.page_id = p.id
		WHERE p.project_id = $1
		  AND p.deleted_at IS NULL
		  AND b.deleted_at IS NULL
		  AND p.id <> $2
		  AND b.content->>'text' LIKE '%[[%'
		ORDER BY p.updated_at DESC
		LIMIT $3
	`, projectID, excludePageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BacklinkCandidate, 0, 32)
	for rows.Next() {
		var c BacklinkCandidate
		if err := rows.Scan(&c.PageID, &c.PageTitle, &c.BlockID, &c.Text, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListPages(ctx context.Context, projectID uuid.UUID, limit int) ([]*Page, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, parent_id, title, frontmatter, share_mode, version, created_at, updated_at
		FROM brain.pages WHERE project_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Page
	for rows.Next() {
		p := &Page{}
		fm := []byte("{}")
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &fm, &p.ShareMode,
			&p.Version, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(fm, &p.Frontmatter)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PageIndexEntry is one row of the lightweight page index consumed by
// the ingest-context internal endpoint: title + frontmatter type only,
// no body — the worker feeds this list into the stage-1 analysis prompt
// so the LLM can link to (and avoid duplicating) existing pages.
type PageIndexEntry struct {
	Title string
	Type  string // COALESCE(frontmatter->>'type', '') — "" when untyped
}

// ListPageIndex returns up to `limit` alive pages of a project (oldest
// first, so the original — usually most canonical — pages survive
// truncation) plus the total alive-page count regardless of the limit.
func (s *Store) ListPageIndex(ctx context.Context, projectID uuid.UUID, limit int) ([]PageIndexEntry, int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT title, COALESCE(frontmatter->>'type', ''), COUNT(*) OVER ()
		FROM brain.pages
		WHERE project_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]PageIndexEntry, 0, 32)
	total := 0
	for rows.Next() {
		var e PageIndexEntry
		if err := rows.Scan(&e.Title, &e.Type, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// GetPageByType returns the oldest alive page of a project whose
// frontmatter `type` equals `typ` (e.g. "purpose" / "schema" seeded by
// templates). Oldest-first because template seed pages are written at
// project creation; later user pages of the same type are derivatives.
// ErrNotFound when the project has no such page (e.g. blank template).
func (s *Store) GetPageByType(ctx context.Context, projectID uuid.UUID, typ string) (*Page, error) {
	p := &Page{}
	frontmatter := []byte("{}")
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
		FROM brain.pages
		WHERE project_id = $1 AND deleted_at IS NULL
		  AND frontmatter->>'type' = $2
		ORDER BY created_at ASC
		LIMIT 1
	`, projectID, typ).Scan(
		&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &frontmatter, &p.BodyMd, &p.ShareMode, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(frontmatter, &p.Frontmatter)
	return p, nil
}

// UpdatePage applies title/frontmatter changes with If-Match.
type UpdatePageInput struct {
	PageID         uuid.UUID
	IfMatchVersion int
	Title          *string
	Frontmatter    map[string]any
	ActorID        string
	// RunID —— agent run 归属（tools.WithRunID 透传；"" = 人工/无 run → NULL）。
	RunID string
}

func (s *Store) UpdatePage(ctx context.Context, in UpdatePageInput) (*Page, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := s.GetPage(ctx, in.PageID)
	if err != nil {
		return nil, err
	}
	if in.IfMatchVersion != 0 && cur.Version != in.IfMatchVersion {
		return nil, ErrConflict
	}
	// 写前快照旧态（title/frontmatter 变化触发；窗口合并由 snapshotPageRevisionTx 内部处理）。
	if err := snapshotPageRevisionTx(ctx, tx, cur.ID, cur.ProjectID, in.ActorID, in.RunID); err != nil {
		return nil, err
	}
	title := cur.Title
	if in.Title != nil {
		title = *in.Title
	}
	frontmatter := cur.Frontmatter
	if in.Frontmatter != nil {
		frontmatter = in.Frontmatter
	}
	fmJSON, _ := json.Marshal(frontmatter)
	p := &Page{}
	got := []byte("{}")
	err = tx.QueryRow(ctx, `
		UPDATE brain.pages SET title = $2, frontmatter = $3,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND version = $4
		RETURNING id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
	`, in.PageID, title, fmJSON, cur.Version).Scan(
		&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &got, &p.BodyMd, &p.ShareMode, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("update page: %w", err)
	}
	_ = json.Unmarshal(got, &p.Frontmatter)
	if err := emitEvent(ctx, tx, p.ProjectID, "user", in.ActorID, "page.updated", map[string]any{
		"page_id": p.ID, "version": p.Version, "title": p.Title,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) SoftDeletePage(ctx context.Context, id uuid.UUID, actor string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var projectID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE brain.pages SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL
		RETURNING project_id
	`, id).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := emitEvent(ctx, tx, projectID, "user", actor, "page.deleted", map[string]any{
		"page_id": id,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkEnrichStale nulls pages.enriched_at so the wiki enrich worker's
// self-healing scan (enriched_at IS NULL OR enriched_at < updated_at)
// picks this page up on its next tick. Scoped to project_id so a stale
// pageID can't touch another project. Used by the manual
// POST /pages/{id}/enrich trigger (B-4).
func (s *Store) MarkEnrichStale(ctx context.Context, pageID, projectID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE brain.pages SET enriched_at = NULL
		WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
	`, pageID, projectID)
	return err
}

// MergePages folds `duplicate` into `canonical` atomically:
//
//  1. duplicate's body_md is folded into canonical's body_md
//     (body_md 权威，见 mergedBodyForMerge：`\n\n---\n\n` 分隔 +
//     `> 合并自「title」` 来源标注；两页正文规范化后相同则不追加，
//     duplicate 正文为空时回退用其 live blocks 的 markdown 投影，
//     避免 block-only 内容静默丢失）
//  2. duplicate's non-deleted blocks get their page_id rewritten to
//     canonical, with positions shifted past canonical's current tail
//     so block ids (and chunk block_id pointers) survive
//  3. wiki_chunks rows pointing at duplicate get their page_id
//     rewritten so vector hits resolve to canonical going forward
//     (the embed worker's next rechunk pass replaces them outright,
//     but until then we don't want stale page links in search hits)
//  4. canonical's blocks are re-projected from the MERGED body via
//     reconcileBlocksTx — body_md stays authoritative and the blocks
//     projection never drifts from what readers/retrieval see;
//     content-duplicate blocks (identical-body merge) collapse to one
//  5. duplicate page is soft-deleted with a `merged_into` frontmatter
//     hint so any UI / wikilink resolver can present a redirect
//  6. every OTHER live page in the project gets its `[[duplicate-title]]`
//     wikilinks rewritten to `[[canonical-title]]` (exact-target match,
//     alias-preserving — see wikilink.go), each through the same
//     pipeline as UpdatePageBody: revision snapshot → body_md update →
//     blocks re-projection → page.updated event. The merged canonical
//     body itself gets the same rewrite inline (step 1) so appending
//     duplicate's body can't introduce fresh dead `[[duplicate-title]]`
//     links
//  7. canonical's version is bumped + frontmatter union written back
//     (数组字段并集去重、duplicate 独有标量补齐、canonical 已有标量不
//     覆盖 — 见 mergedFrontmatterForMerge) + page.merged event emitted
//     on its scope (plus a page.updated when the body changed) so
//     subscribers see the change and can refresh caches
//
// Retry-safe: a second merge of the same pair fails fast on the
// soft-deleted duplicate before any write, and identical bodies are
// never appended twice.
//
// Both pages must exist, be non-deleted, and live in the same project.
// Caller (reviews API / MCP wiki.merge_pages) is responsible for
// ownership checks; this layer enforces the structural invariants.
func (s *Store) MergePages(ctx context.Context, canonicalID, duplicateID uuid.UUID, actor, runID string) error {
	if canonicalID == duplicateID {
		return fmt.Errorf("canonical and duplicate must differ")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Lock both pages FOR UPDATE so a concurrent edit / second merge
	// can't race us. We lock by ascending UUID order so two merges of
	// the same pair from opposite directions don't deadlock.
	first, second := canonicalID, duplicateID
	if first.String() > second.String() {
		first, second = second, first
	}
	rows, err := tx.Query(ctx, `
		SELECT id, project_id, title, body_md, frontmatter, version, deleted_at
		  FROM brain.pages
		 WHERE id IN ($1, $2)
		 ORDER BY id
		 FOR UPDATE
	`, first, second)
	if err != nil {
		return err
	}
	type pageRow struct {
		id          uuid.UUID
		projectID   uuid.UUID
		title       string
		bodyMd      string
		frontmatter []byte
		version     int
		deletedAt   *time.Time
	}
	pages := map[uuid.UUID]*pageRow{}
	for rows.Next() {
		p := &pageRow{}
		if err := rows.Scan(&p.id, &p.projectID, &p.title, &p.bodyMd,
			&p.frontmatter, &p.version, &p.deletedAt); err != nil {
			rows.Close()
			return err
		}
		pages[p.id] = p
	}
	rows.Close()

	canonical, ok := pages[canonicalID]
	if !ok {
		return ErrNotFound
	}
	duplicate, ok := pages[duplicateID]
	if !ok {
		return ErrNotFound
	}
	if canonical.deletedAt != nil || duplicate.deletedAt != nil {
		return fmt.Errorf("cannot merge a deleted page")
	}
	if canonical.projectID != duplicate.projectID {
		return fmt.Errorf("canonical and duplicate must belong to the same project")
	}

	// S3 P0-1 / S2 ④ —— merge 是破坏性重组（canonical 吞 duplicate 的 blocks，
	// duplicate soft-delete），写前快照两页态，事后可经 page_revisions restore
	// 分别恢复（canonical 回到合并前、duplicate 回到删除前）。同 tx、同 actor。
	// snapshotPageRevisionTx 内部 5min 窗口合并避免高频快照膨胀。
	if err := snapshotPageRevisionTx(ctx, tx, canonical.id, canonical.projectID, actor, runID); err != nil {
		return fmt.Errorf("snapshot canonical pre-merge: %w", err)
	}
	if err := snapshotPageRevisionTx(ctx, tx, duplicate.id, duplicate.projectID, actor, runID); err != nil {
		return fmt.Errorf("snapshot duplicate pre-merge: %w", err)
	}

	// Compute canonical's post-merge body BEFORE touching blocks: the
	// reconcile below re-projects from this body, so it is the single
	// source of truth for the merge result. duplicate 正文为空但有 live
	// blocks（CreateBlock 直写路径）时回退 BlocksToMarkdown 投影，
	// 不让 block-only 内容在 reconcile 时被静默清掉。
	var dupBlocks []*Block
	if strings.TrimSpace(duplicate.bodyMd) == "" {
		dupBlocks, err = listBlocksTx(ctx, tx, duplicateID)
		if err != nil {
			return fmt.Errorf("list duplicate blocks: %w", err)
		}
	}
	mergedBody, bodyChanged := mergedBodyForMerge(
		canonical.bodyMd, duplicate.bodyMd, duplicate.title, canonical.title, dupBlocks)

	// Frontmatter union: duplicate 的数组字段（tags/related 类）并集去重进
	// canonical，duplicate 独有的标量字段补上，canonical 已有标量不覆盖。
	// 合并结果与 canonical 现有值一起写回（见下方 canonical UPDATE）。
	mergedFM := mergedFrontmatterForMerge(canonical.frontmatter, duplicate.frontmatter)

	// Compute the position offset to append duplicate's blocks past
	// canonical's tail. We use COALESCE(MAX(position), 0) + 1 so the
	// first migrated block sits cleanly above the last existing one.
	var canonicalMax float64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(position), 0)
		  FROM brain.blocks
		 WHERE page_id = $1 AND deleted_at IS NULL
	`, canonicalID).Scan(&canonicalMax); err != nil {
		return fmt.Errorf("max position lookup: %w", err)
	}

	// Reassign duplicate's live blocks → canonical, shifting positions
	// by (canonicalMax + 1). Soft-deleted blocks stay attached to the
	// duplicate page id (they're invisible to all callers anyway).
	tag, err := tx.Exec(ctx, `
		UPDATE brain.blocks
		   SET page_id = $1,
		       position = position + $2,
		       updated_at = now()
		 WHERE page_id = $3 AND deleted_at IS NULL
	`, canonicalID, canonicalMax+1.0, duplicateID)
	if err != nil {
		return fmt.Errorf("reassign blocks: %w", err)
	}
	movedBlocks := tag.RowsAffected()

	// Rewrite chunks page_id so vector search results resolve to the
	// surviving page. block_id pointers stay valid (we kept block ids).
	if _, err := tx.Exec(ctx, `
		UPDATE brain.wiki_chunks
		   SET page_id = $1, updated_at = now()
		 WHERE page_id = $2
	`, canonicalID, duplicateID); err != nil {
		return fmt.Errorf("rewrite chunks: %w", err)
	}

	// Inject a `merged_into` hint into duplicate's frontmatter then
	// soft-delete it. We round-trip JSON because the column is jsonb;
	// merge keys preserve any pre-existing user-managed YAML.
	var fm map[string]any
	if len(duplicate.frontmatter) > 0 {
		_ = json.Unmarshal(duplicate.frontmatter, &fm)
	}
	if fm == nil {
		fm = map[string]any{}
	}
	fm["merged_into"] = canonicalID.String()
	fm["merged_at"] = time.Now().UTC().Format(time.RFC3339)
	dupFM, _ := json.Marshal(fm)
	if _, err := tx.Exec(ctx, `
		UPDATE brain.pages
		   SET frontmatter = $1::jsonb,
		       deleted_at = now(),
		       version = version + 1,
		       updated_at = now()
		 WHERE id = $2
	`, dupFM, duplicateID); err != nil {
		return fmt.Errorf("soft-delete duplicate: %w", err)
	}

	// Rewrite `[[duplicate-title]]` → `[[canonical-title]]` in every other
	// live page of the project (P2 #20 ③). Same tx, full body_md pipeline
	// per page (snapshot + reconcile + event) so body_md 权威与 blocks 投影
	// 不漂移。在 duplicate soft-delete 之后跑，改写扫描天然排除它。
	rewrittenPages, err := rewriteMergeBacklinksTx(ctx, tx,
		canonical.projectID, canonicalID, duplicateID,
		duplicate.title, canonical.title, actor, runID)
	if err != nil {
		return fmt.Errorf("rewrite merge backlinks: %w", err)
	}

	// Bump canonical version + write the merged body in the same UPDATE,
	// then re-project blocks from it. version bump invalidates
	// any in-flight If-Match update on the canonical page so an editor
	// session that pre-loaded canonical sees a 409 on save and reloads
	// — which is the correct UX after a merge.
	if _, err := tx.Exec(ctx, `
		UPDATE brain.pages
		   SET body_md = $2,
		       frontmatter = $3::jsonb,
		       version = version + 1, updated_at = now()
		 WHERE id = $1
	`, canonicalID, mergedBody, mergedFM); err != nil {
		return fmt.Errorf("update canonical merged body: %w", err)
	}

	// Re-project canonical's blocks from the merged body. This runs
	// unconditionally — even when the body did not change (identical /
	// empty duplicate body) — so the moved duplicate blocks collapse
	// onto canonical's content-identical ones instead of doubling up,
	// and any pre-existing projection drift on canonical self-heals.
	// Idempotent: reconcile is a pure function of (live blocks, body).
	if err := reconcileBlocksTx(ctx, tx, canonicalID, mdparse.ParseBlocks(mergedBody)); err != nil {
		return fmt.Errorf("reproject canonical blocks: %w", err)
	}

	if err := emitEvent(ctx, tx, canonical.projectID, "user", actor,
		"page.merged", map[string]any{
			"canonical_id":    canonicalID,
			"duplicate_id":    duplicateID,
			"moved_blocks":    movedBlocks,
			"rewritten_pages": rewrittenPages,
			"body_merged":     bodyChanged,
			"canonical_v":     canonical.version + 1,
		}); err != nil {
		return err
	}
	if bodyChanged {
		// 与 rewriteMergeBacklinksTx 同一约定：body 变了就发 page.updated，
		// client page 流 / 检索缓存据此刷新（page.merged 语义是结构事件）。
		if err := emitEvent(ctx, tx, canonical.projectID, "user", actor,
			"page.updated", map[string]any{
				"page_id": canonicalID, "version": canonical.version + 1,
				"title": canonical.title, "body": true, "cause": "merge_body",
			}); err != nil {
			return err
		}
	}
	if err := emitEvent(ctx, tx, canonical.projectID, "user", actor,
		"page.deleted", map[string]any{
			"page_id":     duplicateID,
			"reason":      "merged",
			"merged_into": canonicalID,
		}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// mergedBodyForMerge computes canonical's post-merge body_md. Returns
// (merged, changed); changed=false means canonical's body_md must be
// written back unchanged (no duplicate content folded in).
//
// 分隔语义：
//   - duplicate 正文（TrimSpace 后）为空且无 live blocks → 不追加
//   - canonical 正文为空 → 直接采用 duplicate 正文（无分隔符）
//   - 两页正文 TrimSpace 后完全相同 → 不追加（去重 / 幂等底座）
//   - 否则 `canonical + "\n\n---\n\n" + "> 合并自「<dup-title>」" + "\n\n" + duplicate`；
//     mdparse 会把 `---`（thematic break）丢弃、把 blockquote 压成 text block，
//     所以 blocks 投影恰好是 正文块×2 + 标注块，无幽灵分隔块
//
// duplicateBody 为空但 dupBlocks 非空时，dupBlocks 的 markdown 投影顶替
// duplicate 正文参与合并（block-only 内容不静默丢失）。
//
// 合并结果再过一遍 RewriteWikilinks(dup→canonical)：duplicate 正文里带的
// `[[duplicate-title]]` 自引 / canonical 原有的同名链接在合并后都会成为
// 死链，内联改写掉（与 rewriteMergeBacklinksTx 对其他页的处理同规则；
// 该函数明确排除 canonical 自身，故这里补它）。
func mergedBodyForMerge(canonicalBody, duplicateBody, duplicateTitle, canonicalTitle string, dupBlocks []*Block) (string, bool) {
	canon := strings.TrimSpace(canonicalBody)
	dup := strings.TrimSpace(duplicateBody)
	if dup == "" && len(dupBlocks) > 0 {
		dup = strings.TrimSpace(BlocksToMarkdown(dupBlocks))
	}
	var merged string
	switch {
	case dup == "":
		merged = canonicalBody
	case canon == "":
		merged = dup
	case canon == dup:
		merged = canonicalBody
	default:
		annotation := ""
		if t := strings.TrimSpace(duplicateTitle); t != "" {
			annotation = "> 合并自「" + t + "」\n\n"
		}
		merged = canon + "\n\n---\n\n" + annotation + dup
	}
	// 标题仅大小写差异时 [[title]] 本就解析到同一页，跳过改写，
	// 避免把 canonical 正文里的合法大小写形态刷成 canonical 字面。
	if !strings.EqualFold(strings.TrimSpace(duplicateTitle), strings.TrimSpace(canonicalTitle)) {
		merged, _ = RewriteWikilinks(merged, duplicateTitle, canonicalTitle)
	}
	return merged, merged != canonicalBody
}

// mergedFrontmatterForMerge computes canonical's post-merge frontmatter as a
// union of both pages' frontmatter (jsonb round-trip, same convention as the
// duplicate merged_into hint above):
//
//   - 两边都是数组的字段（tags / related 类）取并集去重（按 JSON 归一形态
//     判等，顺序保持 canonical 在前、duplicate 新增项在后）
//   - duplicate 独有、canonical 没有的字段直接补上
//   - canonical 已有的标量（或类型不一致的）字段不覆盖 —— canonical 权威
//
// 返回的 []byte 一定可写回（canonical 无 frontmatter 且 duplicate 也没有时
// 为 "{}"）；调用方无条件写入，免判 changed。
func mergedFrontmatterForMerge(canonicalFM, duplicateFM []byte) []byte {
	var canon, dup map[string]any
	if len(canonicalFM) > 0 {
		_ = json.Unmarshal(canonicalFM, &canon)
	}
	if len(duplicateFM) > 0 {
		_ = json.Unmarshal(duplicateFM, &dup)
	}
	if len(dup) == 0 {
		if len(canonicalFM) > 0 {
			return canonicalFM
		}
		return []byte("{}")
	}
	if canon == nil {
		canon = map[string]any{}
	}
	for k, dv := range dup {
		cv, ok := canon[k]
		if !ok {
			canon[k] = dv
			continue
		}
		cArr, cIsArr := cv.([]any)
		dArr, dIsArr := dv.([]any)
		if !cIsArr || !dIsArr {
			continue // canonical 标量权威，不覆盖
		}
		seen := make(map[string]struct{}, len(cArr))
		for _, item := range cArr {
			seen[jsonKey(item)] = struct{}{}
		}
		for _, item := range dArr {
			key := jsonKey(item)
			if _, dupItem := seen[key]; dupItem {
				continue
			}
			seen[key] = struct{}{}
			cArr = append(cArr, item)
		}
		canon[k] = cArr
	}
	out, err := json.Marshal(canon)
	if err != nil {
		return []byte("{}")
	}
	return out
}

// jsonKey renders a frontmatter array item to a canonical string form for
// dedup. Scalar items dominate in practice (tags / related are string
// lists); Marshal keeps the odd object item deterministic too.
func jsonKey(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// ─── Blocks ─────────────────────────────────────────────

type CreateBlockInput struct {
	PageID    uuid.UUID
	ProjectID uuid.UUID // for events scope
	ParentID  *uuid.UUID
	Position  float64
	Type      string
	Content   map[string]any
	ActorID   string
}

func (s *Store) CreateBlock(ctx context.Context, in CreateBlockInput) (*Block, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// 写前快照（加 block 前的页态；窗口合并抑 agent 批量加 block 风暴）。
	if err := snapshotPageRevisionTx(ctx, tx, in.PageID, in.ProjectID, in.ActorID, ""); err != nil {
		return nil, err
	}
	contentJSON, _ := json.Marshal(in.Content)
	b := &Block{}
	got := []byte("{}")
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.blocks (page_id, parent_id, position, type, content)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, page_id, parent_id, position, type, content, version, created_at, updated_at
	`, in.PageID, in.ParentID, in.Position, in.Type, contentJSON).Scan(
		&b.ID, &b.PageID, &b.ParentID, &b.Position, &b.Type, &got, &b.Version,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(got, &b.Content)
	if err := emitEvent(ctx, tx, in.ProjectID, "user", in.ActorID, "block.created", map[string]any{
		"page_id":    b.PageID,
		"block_id":   b.ID,
		"project_id": in.ProjectID,
		"type":       b.Type,
		"content":    b.Content,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Store) ListBlocks(ctx context.Context, pageID uuid.UUID) ([]*Block, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, page_id, parent_id, position, type, content, version, created_at, updated_at
		FROM brain.blocks WHERE page_id = $1 AND deleted_at IS NULL
		ORDER BY position
	`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Block
	for rows.Next() {
		b := &Block{}
		got := []byte("{}")
		if err := rows.Scan(&b.ID, &b.PageID, &b.ParentID, &b.Position, &b.Type, &got,
			&b.Version, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(got, &b.Content)
		out = append(out, b)
	}
	return out, rows.Err()
}

type UpdateBlockInput struct {
	BlockID        uuid.UUID
	IfMatchVersion int
	Content        map[string]any
	Position       *float64
	ActorID        string
}

func (s *Store) UpdateBlock(ctx context.Context, in UpdateBlockInput) (*Block, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Read current to know project_id (for events) + version.
	cur := &Block{}
	curContent := []byte("{}")
	var projectID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT b.id, b.page_id, b.parent_id, b.position, b.type, b.content, b.version,
		       b.created_at, b.updated_at, p.project_id
		FROM brain.blocks b JOIN brain.pages p ON p.id = b.page_id
		WHERE b.id = $1 AND b.deleted_at IS NULL
	`, in.BlockID).Scan(
		&cur.ID, &cur.PageID, &cur.ParentID, &cur.Position, &cur.Type, &curContent, &cur.Version,
		&cur.CreatedAt, &cur.UpdatedAt, &projectID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.IfMatchVersion != 0 && cur.Version != in.IfMatchVersion {
		return nil, ErrConflict
	}
	// 写前快照（改 block 前的页态）。
	if err := snapshotPageRevisionTx(ctx, tx, cur.PageID, projectID, in.ActorID, ""); err != nil {
		return nil, err
	}

	content := in.Content
	if content == nil {
		_ = json.Unmarshal(curContent, &content)
	}
	contentJSON, _ := json.Marshal(content)
	pos := cur.Position
	if in.Position != nil {
		pos = *in.Position
	}
	b := &Block{}
	got := []byte("{}")
	err = tx.QueryRow(ctx, `
		UPDATE brain.blocks SET content = $2, position = $3,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND version = $4
		RETURNING id, page_id, parent_id, position, type, content, version, created_at, updated_at
	`, in.BlockID, contentJSON, pos, cur.Version).Scan(
		&b.ID, &b.PageID, &b.ParentID, &b.Position, &b.Type, &got, &b.Version,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(got, &b.Content)
	if err := emitEvent(ctx, tx, projectID, "user", in.ActorID, "block.updated", map[string]any{
		"page_id":    b.PageID,
		"block_id":   b.ID,
		"project_id": projectID,
		"version":    b.Version,
		"type":       b.Type,
		"content":    b.Content,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Store) SoftDeleteBlock(ctx context.Context, id uuid.UUID, actor string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// 先取 page_id + project_id 用于写前快照（删 block 前的页态）。
	var pageID, projectID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT b.page_id, p.project_id
		FROM brain.blocks b JOIN brain.pages p ON p.id = b.page_id
		WHERE b.id = $1 AND b.deleted_at IS NULL
	`, id).Scan(&pageID, &projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := snapshotPageRevisionTx(ctx, tx, pageID, projectID, actor, ""); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE brain.blocks SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL
	`, id); err != nil {
		return err
	}
	if err := emitEvent(ctx, tx, projectID, "user", actor, "block.deleted", map[string]any{
		"page_id": pageID, "block_id": id,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Page → Source linkage ─────────────────────────────

// LinkPageSource records that a page was derived from a source (webclip
// 抓取 / upload 文件 / ...). Idempotent on (page_id, source_id). 给
// relevance source-overlap 信号（P1-4）提供 page↔source 多对多归属。
func (s *Store) LinkPageSource(ctx context.Context, pageID, sourceID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO brain.page_sources (page_id, source_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, pageID, sourceID)
	return err
}

// ─── Revisions ──────────────────────────────────────────
//
// Wiki 页版本历史（迁移 00065），镜像 note/store Revisions（00059）。
// edit = 5 写入口写前旧态全量快照（窗口合并 + Prune 可清）；
// restore = 恢复前自动备份（永久保留，不参与窗口合并与 Prune）。
// 与 note 差异：wiki 正文 = N 行 blocks，页级快照序列化 frontmatter + 全部
// live blocks 为 blocks_json；restore 须 block 对账 in-place（保 block_id 连续
// 性，使 graph/backlinks/wiki_chunks 引用不致 dangle）。

const (
	// revisionWindow —— edit 快照的窗口合并间隔：距该页上一条
	// change_type='edit' 的版本不足 5 分钟则不再快照（抑 agent 快写风暴）。
	revisionWindow = 5 * time.Minute
	// PrunePageRevisions 的默认清理门槛（Nowen 惯例，同 note）。
	PruneDefaultKeepRecent = 50
	PruneDefaultKeepDays   = 30
	// RevisionRestoreSummary —— restore 自动备份版本的固定摘要。
	RevisionRestoreSummary = "恢复前自动备份"
	// RevisionCopySuffix —— save-as-copy 新页的标题后缀。
	RevisionCopySuffix = "（历史副本）"
	// MaxBlocksJSONBytes —— 单条快照 blocks_json 字节上限。
	// 超限跳过本条快照（同 llm_wiki file_history 的 512KB 上限思路）：
	// 极大页（≈500+ block）罕见，Prune 控条数；不截断以保证 restore 完整性。
	MaxBlocksJSONBytes = 512 * 1024
)

// pageRevisionColumns —— 不含 blocks_json（list 用）；detail 走 GetPageRevision 单列追加。
const pageRevisionColumns = `id, page_id, project_id, actor_id, title, frontmatter, change_type, change_summary, COALESCE(run_id, ''), created_at`

// listBlocksTx —— 事务内取一页全部 live blocks（写前快照 / restore 对账用）。
// 与 ListBlocks 同 SELECT，但 tx-scoped 保证读到本事务一致态。
func listBlocksTx(ctx context.Context, tx pgx.Tx, pageID uuid.UUID) ([]*Block, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, page_id, parent_id, position, type, content, version, created_at, updated_at
		FROM brain.blocks WHERE page_id = $1 AND deleted_at IS NULL
		ORDER BY position
	`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Block
	for rows.Next() {
		b := &Block{}
		got := []byte("{}")
		if err := rows.Scan(&b.ID, &b.PageID, &b.ParentID, &b.Position, &b.Type, &got,
			&b.Version, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(got, &b.Content)
		out = append(out, b)
	}
	return out, rows.Err()
}

// insertBlocksTx —— 事务内为全新页批量 INSERT blocks（CreatePage 带正文用；无 old blocks，
// 直接按序 INSERT）。每个 block emit block.created（syncws 实时流 + changelog 依赖）。
func insertBlocksTx(ctx context.Context, tx pgx.Tx, pageID, projectID uuid.UUID, actor string, blocks []mdparse.ParsedBlock) error {
	for i, b := range blocks {
		contentJSON, _ := json.Marshal(b.Content)
		var blockID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO brain.blocks (page_id, position, type, content)
			VALUES ($1, $2, $3, $4) RETURNING id
		`, pageID, float64(i+1), b.Type, contentJSON).Scan(&blockID); err != nil {
			return fmt.Errorf("insert block %d: %w", i, err)
		}
		if err := emitEvent(ctx, tx, projectID, "user", actor, "block.created", map[string]any{
			"page_id":    pageID,
			"block_id":   blockID,
			"project_id": projectID,
			"type":       b.Type,
			"content":    b.Content,
		}); err != nil {
			return err
		}
	}
	return nil
}

// blockContentKey —— block 身份键（type + content jsonb 规范化），reconcile 时在无 id 的
// mdparse 产出与有 id 的 live blocks 间 greedy 匹配，保 block_id 连续。
func blockContentKey(typ string, content map[string]any) string {
	b, _ := json.Marshal(content)
	return typ + ":" + string(b)
}

// reconcileBlocksTx —— PUT body_md 时把 live blocks 对账成 newBlocks（mdparse 产出，无 id）。
// greedy content-match：同 (type,content) 旧 block 原地复用 id（刷 position/content/version，
// 复活已软删）；匹配不上的旧 block 软删；newBlocks 多余的 INSERT 新 id。保 block_id 连续 →
// graph_edges/wiki_chunks/page_revisions 引用不 dangle。不 emit block 事件（client 吃 page 流）。
func reconcileBlocksTx(ctx context.Context, tx pgx.Tx, pageID uuid.UUID, newBlocks []mdparse.ParsedBlock) error {
	old, err := listBlocksTx(ctx, tx, pageID)
	if err != nil {
		return fmt.Errorf("list blocks for reconcile: %w", err)
	}
	pool := map[string][]*Block{}
	for _, b := range old {
		k := blockContentKey(b.Type, b.Content)
		pool[k] = append(pool[k], b)
	}
	used := map[uuid.UUID]bool{}
	for i, nb := range newBlocks {
		k := blockContentKey(nb.Type, nb.Content)
		contentJSON, _ := json.Marshal(nb.Content)
		pos := float64(i + 1)
		matched := false
		for _, c := range pool[k] {
			if used[c.ID] {
				continue
			}
			used[c.ID] = true
			matched = true
			if _, err := tx.Exec(ctx, `
				UPDATE brain.blocks SET parent_id = NULL, position = $2, type = $3, content = $4,
				    deleted_at = NULL, version = version + 1, updated_at = now()
				WHERE id = $1
			`, c.ID, pos, nb.Type, contentJSON); err != nil {
				return fmt.Errorf("reconcile update block: %w", err)
			}
			break
		}
		if matched {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO brain.blocks (page_id, position, type, content)
			VALUES ($1, $2, $3, $4)
		`, pageID, pos, nb.Type, contentJSON); err != nil {
			return fmt.Errorf("reconcile insert block: %w", err)
		}
	}
	for _, b := range old {
		if !used[b.ID] {
			if _, err := tx.Exec(ctx, `
				UPDATE brain.blocks SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL
			`, b.ID); err != nil {
				return fmt.Errorf("reconcile soft-delete block: %w", err)
			}
		}
	}
	return nil
}

// UpdatePageBodyInput —— PUT body_md（Milkdown 整篇写）入参。
type UpdatePageBodyInput struct {
	PageID         uuid.UUID
	BodyMd         string
	IfMatchVersion int
	ActorID        string
	// RunID —— agent run 归属（"" = 人工/无 run → 快照 run_id 落 NULL）。
	RunID string
}

// UpdatePageBody —— body_md 权威写入口（§⑤ Path C，拍板同步写）。事务内：
//  1. 写前快照旧态（复用 ④ snapshotPageRevisionTx，含 body_md 原文 + blocks_json）
//  2. UPDATE pages.body_md + version+1 OCC
//  3. reconcileBlocksTx 把 live blocks 对账成 mdparse(newBody)（保 block_id）
//  4. emit page.updated（client page 流刷新）
//
// 不经 UpdatePage（那是 title/frontmatter 入口）；body 改写走本方法避免叠加快照/事件噪声。
func (s *Store) UpdatePageBody(ctx context.Context, in UpdatePageBodyInput) (*Page, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := s.GetPage(ctx, in.PageID)
	if err != nil {
		return nil, err
	}
	if in.IfMatchVersion != 0 && cur.Version != in.IfMatchVersion {
		return nil, ErrConflict
	}
	if err := snapshotPageRevisionTx(ctx, tx, cur.ID, cur.ProjectID, in.ActorID, in.RunID); err != nil {
		return nil, err
	}
	p := &Page{}
	got := []byte("{}")
	err = tx.QueryRow(ctx, `
		UPDATE brain.pages SET body_md = $2,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND version = $3
		RETURNING id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
	`, in.PageID, in.BodyMd, cur.Version).Scan(
		&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &got, &p.BodyMd, &p.ShareMode, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("update page body: %w", err)
	}
	_ = json.Unmarshal(got, &p.Frontmatter)
	if err := reconcileBlocksTx(ctx, tx, p.ID, mdparse.ParseBlocks(in.BodyMd)); err != nil {
		return nil, err
	}
	if err := emitEvent(ctx, tx, p.ProjectID, "user", in.ActorID, "page.updated", map[string]any{
		"page_id": p.ID, "version": p.Version, "title": p.Title, "body": true,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// BlocksToMarkdown —— server 端 blocks → markdown 投影（§⑤ 回填 + 一致性用）。
// 镜像 client reader/block_to_markdown.dart，但不重写 wikilink：body_md 存原始 [[Page]]
// 字面（渲染期才 wiki:// 重写），保 mdparse(BlocksToMarkdown(blocks)) 往返稳定。
func BlocksToMarkdown(blocks []*Block) string {
	var buf strings.Builder
	for _, b := range blocks {
		chunk := blockToMarkdownLine(b)
		if chunk == "" {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(chunk)
	}
	return buf.String()
}

func blockToMarkdownLine(b *Block) string {
	switch b.Type {
	case "heading":
		raw, _ := b.Content["text"].(string)
		if strings.TrimSpace(raw) == "" {
			return ""
		}
		lvl := 2
		switch v := b.Content["level"].(type) {
		case float64:
			lvl = int(v)
		case int:
			lvl = v
		}
		if lvl < 1 {
			lvl = 1
		}
		if lvl > 6 {
			lvl = 6
		}
		return strings.Repeat("#", lvl) + " " + raw
	case "list":
		items, _ := b.Content["items"].([]any)
		var lines []string
		for _, it := range items {
			s := fmt.Sprintf("%v", it)
			if strings.TrimSpace(s) != "" {
				lines = append(lines, "- "+s)
			}
		}
		return strings.Join(lines, "\n")
	case "code":
		raw, _ := b.Content["text"].(string)
		if raw == "" {
			return ""
		}
		lang, _ := b.Content["lang"].(string)
		return "```" + lang + "\n" + raw + "\n```"
	case "table":
		// content.text 是原始 GFM 表格 markdown（mdparse 保留 verbatim），
		// 原样回吐即完成往返保真——重解析会再得到同一个 table block。
		// 与 default 行为一致，单列以钉死 table 类型的输出契约。
		raw, _ := b.Content["text"].(string)
		return raw
	default:
		raw, _ := b.Content["text"].(string)
		return raw
	}
}

// BackfillBodyMd —— 一次性回填 body_md=” 的页（§⑤ 迁移 00066 后启动跑，幂等）。
// body_md=” 且有 live blocks → BlocksToMarkdown 重算 UPDATE；空页保持 ”。
// 跳过已回填（body_md<>”) 确保重复启动无副作用。返回回填页数。
func (s *Store) BackfillBodyMd(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM brain.pages
		WHERE body_md = '' AND deleted_at IS NULL
	`)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		blocks, err := s.ListBlocks(ctx, id)
		if err != nil {
			return n, err
		}
		md := BlocksToMarkdown(blocks)
		if md == "" {
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE brain.pages SET body_md = $1 WHERE id = $2`, md, id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// BackfillFrontmatter —— 一次性剥离历史上误写进 body_md 开头的 YAML
// frontmatter（2026-09-04 串味事故：2026-09-03 前 ingest 路径不拆
// frontmatter，goldmark 把 ---…--- 误判成 setext H2 投成正文标题块，
// pages.frontmatter 恒为 {}）。修复 = frontmatter 入 jsonb 列 + body_md
// 只留正文 + 重投影 blocks。幂等：剥离后 body_md 不再以 --- 开头，重复
// 启动无副作用。返回修复页数。
//
// 已有 jsonb 键优先于剥离值（不覆盖模板 / research 路径已写入的键）；
// 非法 YAML 或正文以分隔线开头的页不动（保守原则，宁可遗留不可误拆）。
func (s *Store) BackfillFrontmatter(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, frontmatter, body_md FROM brain.pages
		WHERE body_md LIKE '---%' AND deleted_at IS NULL
	`)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id   uuid.UUID
		fm   []byte
		body string
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.fm, &c.body); err != nil {
			rows.Close()
			return 0, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, c := range cands {
		fm, body := mdparse.SplitFrontmatter(c.body)
		if fm == nil {
			continue // 非法 YAML / 正文以分隔线开头 —— 不动
		}
		// 合并：已有 jsonb 键优先。
		existing := map[string]any{}
		_ = json.Unmarshal(c.fm, &existing)
		for k, v := range existing {
			fm[k] = v
		}
		fmJSON, err := json.Marshal(fm)
		if err != nil {
			return n, err
		}
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return n, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE brain.pages SET frontmatter = $2, body_md = $3, updated_at = now()
			WHERE id = $1
		`, c.id, fmJSON, body); err != nil {
			tx.Rollback(ctx)
			return n, fmt.Errorf("backfill frontmatter update page: %w", err)
		}
		if err := reconcileBlocksTx(ctx, tx, c.id, mdparse.ParseBlocks(body)); err != nil {
			tx.Rollback(ctx)
			return n, fmt.Errorf("backfill frontmatter reconcile blocks: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// snapshotPageRevisionTx —— 把 page（写前旧态：title/frontmatter + live blocks）
// 存为 edit 版本。距上一条 edit 版本不足 revisionWindow 则跳过（窗口合并）。
// blocks_json 超 MaxBlocksJSONBytes 跳过本条（极大页罕见，保 restore 完整不截断）。
// 内部 tx-scoped 读 page，统一 4 调用点（UpdatePage/CreateBlock/UpdateBlock/SoftDeleteBlock）。
// SoftDeletePage 不快照：restore 无法 un-delete 页，快照成死数据；page 删除属回收站范畴。
// runID 非空时快照落 run_id（agent run 审计）；窗口合并命中时不新增行，
// 既有行的 run_id 保持首写归属、不被覆盖/清空（§1.2 P2）。
func snapshotPageRevisionTx(ctx context.Context, tx pgx.Tx, pageID, projectID uuid.UUID, actorID, runID string) error {
	var lastEdit time.Time
	err := tx.QueryRow(ctx, `
		SELECT created_at FROM brain.page_revisions
		WHERE page_id = $1 AND change_type = 'edit'
		ORDER BY created_at DESC LIMIT 1
	`, pageID).Scan(&lastEdit)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && time.Since(lastEdit) < revisionWindow {
		return nil
	}
	var title, bodyMd string
	var fm []byte
	err = tx.QueryRow(ctx, `
		SELECT title, frontmatter, body_md FROM brain.pages WHERE id = $1 AND deleted_at IS NULL
	`, pageID).Scan(&title, &fm, &bodyMd)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // 页已不存在（删），跳过
	}
	if err != nil {
		return err
	}
	blocks, err := listBlocksTx(ctx, tx, pageID)
	if err != nil {
		return fmt.Errorf("list blocks for snapshot: %w", err)
	}
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		return fmt.Errorf("marshal snapshot blocks: %w", err)
	}
	if len(blocksJSON) > MaxBlocksJSONBytes {
		return nil // 超限跳过（不截断，保 restore 完整）
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO brain.page_revisions (page_id, project_id, actor_id, title, frontmatter, body_md, blocks_json, change_type, run_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'edit', NULLIF($8, ''))
	`, pageID, projectID, actorID, title, fm, bodyMd, blocksJSON, runID); err != nil {
		return fmt.Errorf("snapshot page revision: %w", err)
	}
	return nil
}

// ListPageRevisions —— 该页版本列表（新→旧），不含 blocks_json（取内容走 GetPageRevision）。
// user 隔离在 api 层 ownsProject；store 按 page_id 过滤。
func (s *Store) ListPageRevisions(ctx context.Context, pageID uuid.UUID, limit, offset int) ([]*Revision, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+pageRevisionColumns+`
		FROM brain.page_revisions WHERE page_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, pageID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Revision
	for rows.Next() {
		r := &Revision{}
		var fm []byte
		if err := rows.Scan(&r.ID, &r.PageID, &r.ProjectID, &r.ActorID, &r.Title, &fm,
			&r.ChangeType, &r.ChangeSummary, &r.RunID, &r.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(fm, &r.Frontmatter)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetPageRevision —— 单条版本（含完整 blocks_json），严格 page_id 匹配防跨页取。
func (s *Store) GetPageRevision(ctx context.Context, pageID, revisionID uuid.UUID) (*Revision, error) {
	r := &Revision{}
	var fm, blocks []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, page_id, project_id, actor_id, title, frontmatter, body_md, blocks_json, change_type, change_summary, COALESCE(run_id, ''), created_at
		FROM brain.page_revisions WHERE id = $1 AND page_id = $2
	`, revisionID, pageID).Scan(
		&r.ID, &r.PageID, &r.ProjectID, &r.ActorID, &r.Title, &fm, &r.BodyMd, &blocks,
		&r.ChangeType, &r.ChangeSummary, &r.RunID, &r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(fm, &r.Frontmatter)
	r.BlocksJSON = blocks // 保留 raw，caller（api/restore）按需反序列化
	return r, nil
}

// RestorePageRevision —— 覆盖式恢复：事务内先把当前态（page + live blocks）存为
// change_type='restore' 自动备份，再把页对账回写到该版本内容。block 对账 in-place
// 保 block_id 连续性（update/revive/soft-delete/insert 四分支）。page.version OCC +1，
// emit page.restored（自动进 changelog）。内部 block 改写不走 snapshotPageRevisionTx
// （已显式存 restore 备份，避免递归/多余 edit 快照，同 note RestoreRevision 不经 UpdateNote）。
//
// ifMatchVersion > 0 时先比对当前 page.version（§1.2 P2 客户端 undo OCC：
// 传 run 结束时刻的 version，run 之后页面又被改过则 ErrConflict → 409，
// 不覆盖人工修改）；0 = 不校验（向后兼容既有调用）。
func (s *Store) RestorePageRevision(ctx context.Context, pageID, revisionID uuid.UUID, actorID string, ifMatchVersion int) (*Page, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. 当前 page（tx-scoped 读，保证一致态；body_md 供恢复前备份用）。
	cur := &Page{}
	curFM := []byte("{}")
	err = tx.QueryRow(ctx, `
		SELECT id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
		FROM brain.pages WHERE id = $1 AND deleted_at IS NULL
	`, pageID).Scan(
		&cur.ID, &cur.ProjectID, &cur.ParentID, &cur.Title, &curFM, &cur.BodyMd, &cur.ShareMode, &cur.Version,
		&cur.CreatedAt, &cur.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(curFM, &cur.Frontmatter)

	// 客户端期望版本比对（undo OCC）：run 之后页面被改过 → 冲突，不动任何数据。
	if ifMatchVersion > 0 && cur.Version != ifMatchVersion {
		return nil, ErrConflict
	}

	// 2. 目标版本（含 body_md + blocks_json），严格 page_id 匹配。
	// body_md 必须读出：步骤 5 以 rev.BodyMd 覆盖 pages.body_md，漏读会零值清空权威列。
	rev := &Revision{}
	var revFM, revBlocks []byte
	err = tx.QueryRow(ctx, `
		SELECT id, page_id, project_id, title, frontmatter, body_md, blocks_json
		FROM brain.page_revisions WHERE id = $1 AND page_id = $2
	`, revisionID, pageID).Scan(&rev.ID, &rev.PageID, &rev.ProjectID, &rev.Title, &revFM, &rev.BodyMd, &revBlocks)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(revFM, &rev.Frontmatter)
	var snap []*Block
	if len(revBlocks) > 0 {
		if err := json.Unmarshal(revBlocks, &snap); err != nil {
			return nil, fmt.Errorf("unmarshal revision blocks: %w", err)
		}
	}

	// 3. 恢复前自动备份：当前 page + 当前 live blocks 存为 restore 版本（永久）。
	curBlocks, err := listBlocksTx(ctx, tx, pageID)
	if err != nil {
		return nil, fmt.Errorf("list current blocks: %w", err)
	}
	curBlocksJSON, err := json.Marshal(curBlocks)
	if err != nil {
		return nil, fmt.Errorf("marshal current blocks: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO brain.page_revisions (page_id, project_id, actor_id, title, frontmatter, body_md, blocks_json, change_type, change_summary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'restore', $8)
	`, pageID, cur.ProjectID, actorID, cur.Title, curFM, cur.BodyMd, curBlocksJSON, RevisionRestoreSummary); err != nil {
		return nil, fmt.Errorf("backup before restore: %w", err)
	}

	// 4. block 对账 in-place：让 live blocks 集合精确等于 snap。
	live := make(map[uuid.UUID]*Block, len(curBlocks))
	for _, b := range curBlocks {
		live[b.ID] = b
	}
	snapIDs := make(map[uuid.UUID]bool, len(snap))
	for _, sb := range snap {
		snapIDs[sb.ID] = true
		contentJSON, _ := json.Marshal(sb.Content)
		if _, exists := live[sb.ID]; exists {
			// live 且在 snap → 原地更新（保 id）。
			if _, err := tx.Exec(ctx, `
				UPDATE brain.blocks SET parent_id = $2, position = $3, type = $4, content = $5,
				    deleted_at = NULL, version = version + 1, updated_at = now()
				WHERE id = $1
			`, sb.ID, sb.ParentID, sb.Position, sb.Type, contentJSON); err != nil {
				return nil, fmt.Errorf("reconcile update block: %w", err)
			}
		} else {
			// 不在 live：可能已软删（复活）或不存在（新增）。
			tag, err := tx.Exec(ctx, `
				UPDATE brain.blocks SET parent_id = $2, position = $3, type = $4, content = $5,
				    deleted_at = NULL, version = version + 1, updated_at = now()
				WHERE id = $1
			`, sb.ID, sb.ParentID, sb.Position, sb.Type, contentJSON)
			if err != nil {
				return nil, fmt.Errorf("reconcile revive block: %w", err)
			}
			if tag.RowsAffected() == 0 {
				// 不存在 → 用 snap 的 id 新增（保 block_id 连续性）。
				if _, err := tx.Exec(ctx, `
					INSERT INTO brain.blocks (id, page_id, parent_id, position, type, content)
					VALUES ($1, $2, $3, $4, $5, $6)
				`, sb.ID, pageID, sb.ParentID, sb.Position, sb.Type, contentJSON); err != nil {
					return nil, fmt.Errorf("reconcile insert block: %w", err)
				}
			}
		}
	}
	// live 但不在 snap → 软删（保留行，不硬删，审计 + block_id 不复用）。
	for _, lb := range curBlocks {
		if !snapIDs[lb.ID] {
			if _, err := tx.Exec(ctx, `
				UPDATE brain.blocks SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL
			`, lb.ID); err != nil {
				return nil, fmt.Errorf("reconcile soft-delete block: %w", err)
			}
		}
	}

	// 5. page title/frontmatter 覆盖 + OCC version+1。
	p := &Page{}
	gotFM := []byte("{}")
	err = tx.QueryRow(ctx, `
		UPDATE brain.pages SET title = $2, frontmatter = $3, body_md = $4,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND version = $5
		RETURNING id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
	`, pageID, rev.Title, revFM, rev.BodyMd, cur.Version).Scan(
		&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &gotFM, &p.BodyMd, &p.ShareMode, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("restore page: %w", err)
	}
	_ = json.Unmarshal(gotFM, &p.Frontmatter)

	// 6. emit page.restored（自动进 changelog：ListPageEvents 按 page_id 选不挑 event_type）。
	if err := emitEvent(ctx, tx, cur.ProjectID, "user", actorID, "page.restored", map[string]any{
		"page_id":     p.ID,
		"revision_id": rev.ID,
		"title":       p.Title,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// SavePageRevisionAsCopy —— 以该版本内容新建页：同 project、同 parent、标题追加
// RevisionCopySuffix、复制 frontmatter + body_md + 全部 snap blocks。body_md 非空时
// 交给 CreatePage 事务内 mdparse 自动投影 blocks（§⑤ Path C 权威派生，副本块 id 全新）；
// 仅当旧版本无 body_md（迁移前快照）才退化逐块复制 snap。
func (s *Store) SavePageRevisionAsCopy(ctx context.Context, pageID, revisionID uuid.UUID, actorID string) (*Page, error) {
	rev, err := s.GetPageRevision(ctx, pageID, revisionID)
	if err != nil {
		return nil, err
	}
	var snap []*Block
	if len(rev.BlocksJSON) > 0 {
		if err := json.Unmarshal(rev.BlocksJSON, &snap); err != nil {
			return nil, fmt.Errorf("unmarshal revision blocks: %w", err)
		}
	}
	orig, err := s.GetPage(ctx, pageID)
	if err != nil {
		return nil, err
	}
	p, err := s.CreatePage(ctx, CreatePageInput{
		ProjectID:   rev.ProjectID,
		ParentID:    orig.ParentID,
		Title:       rev.Title + RevisionCopySuffix,
		Frontmatter: rev.Frontmatter,
		BodyMd:      rev.BodyMd,
		ActorID:     actorID,
	})
	if err != nil {
		return nil, err
	}
	if rev.BodyMd != "" {
		// CreatePage 已从事权威 body_md 投影 blocks，逐块复制会翻倍。
		return p, nil
	}
	for _, b := range snap {
		if _, err := s.CreateBlock(ctx, CreateBlockInput{
			PageID:    p.ID,
			ProjectID: rev.ProjectID,
			ParentID:  b.ParentID,
			Position:  b.Position,
			Type:      b.Type,
			Content:   b.Content,
			ActorID:   actorID,
		}); err != nil {
			return nil, fmt.Errorf("copy block: %w", err)
		}
	}
	return p, nil
}

// PrunePageRevisions —— 清理历史版本：只删 change_type='edit' 且超过 keepDays 天、
// 且不在该页最近 keepRecent 条 edit 版本内的行；restore 版本永久保留。只提供函数
// （同 orphan GC），由调用方决定周期。返回删除行数。
func (s *Store) PrunePageRevisions(ctx context.Context, keepRecent, keepDays int) (int64, error) {
	if keepRecent <= 0 {
		keepRecent = PruneDefaultKeepRecent
	}
	if keepDays <= 0 {
		keepDays = PruneDefaultKeepDays
	}
	tag, err := s.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY page_id ORDER BY created_at DESC, id DESC) AS rn
			FROM brain.page_revisions
			WHERE change_type = 'edit'
		)
		DELETE FROM brain.page_revisions r
		USING ranked k
		WHERE r.id = k.id
		  AND k.rn > $1
		  AND r.created_at < now() - make_interval(days => $2)
	`, keepRecent, keepDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ─── Events ─────────────────────────────────────────────

func emitEvent(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actorType, actorID, eventType string, payload map[string]any) error {
	pl, _ := json.Marshal(payload)
	scope := fmt.Sprintf("wiki:project:%s", projectID)
	_, err := tx.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, scope, actorType, actorID, eventType, pl)
	return err
}

// ListPageEvents returns events scoped to one page, newest-first.
//
// brain.events is project-scoped (`wiki:project:{pid}`) but every wiki
// event payload carries the page_id, so filtering on payload->>'page_id'
// gives us a clean per-page changelog without a parallel table. Same
// indexing concern as backlinks: payload field probe is a seq-scan over
// the project's events; cap at 200 rows for the worst case.
func (s *Store) ListPageEvents(ctx context.Context, projectID, pageID uuid.UUID, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	scope := fmt.Sprintf("wiki:project:%s", projectID)
	rows, err := s.pool.Query(ctx, `
		SELECT id, scope, actor_type, actor_id, event_type, payload, schema_ver, created_at
		FROM brain.events
		WHERE scope = $1
		  AND payload->>'page_id' = $2
		ORDER BY id DESC
		LIMIT $3
	`, scope, pageID.String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.Scope, &e.ActorType, &e.ActorID,
			&e.EventType, &payload, &e.SchemaVer, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payload, &e.Payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventsSince fetches events for catchup. Caller passes lastID = 0 for "from beginning".
func (s *Store) EventsSince(ctx context.Context, scope string, lastID int64, limit int) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, scope, actor_type, actor_id, event_type, payload, schema_ver, created_at
		FROM brain.events WHERE scope = $1 AND id > $2 ORDER BY id LIMIT $3
	`, scope, lastID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.Scope, &e.ActorType, &e.ActorID, &e.EventType,
			&payload, &e.SchemaVer, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payload, &e.Payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

type Event struct {
	ID        int64
	Scope     string
	ActorType string
	ActorID   string
	EventType string
	Payload   map[string]any
	SchemaVer int
	CreatedAt time.Time
}
