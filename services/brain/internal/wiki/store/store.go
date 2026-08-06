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
	CreatedAt     time.Time
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

// UpdatePage applies title/frontmatter changes with If-Match.
type UpdatePageInput struct {
	PageID         uuid.UUID
	IfMatchVersion int
	Title          *string
	Frontmatter    map[string]any
	ActorID        string
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
	if err := snapshotPageRevisionTx(ctx, tx, cur.ID, cur.ProjectID, in.ActorID); err != nil {
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
//  1. duplicate's non-deleted blocks get their page_id rewritten to
//     canonical, with positions shifted past canonical's current tail
//     so the original ordering of each side is preserved
//  2. wiki_chunks rows pointing at duplicate get their page_id
//     rewritten so vector hits resolve to canonical going forward
//     (the embed worker's next rechunk pass replaces them outright,
//     but until then we don't want stale page links in search hits)
//  3. duplicate page is soft-deleted with a `merged_into` frontmatter
//     hint so any UI / wikilink resolver can present a redirect
//  4. canonical's version is bumped + page.merged event emitted on its
//     scope so subscribers see the change and can refresh caches
//
// Both pages must exist, be non-deleted, and live in the same project.
// Caller (reviews API / MCP wiki.merge_pages) is responsible for
// ownership checks; this layer enforces the structural invariants.
//
// Wikilink rewriting (find every "[[duplicate-title]]" and update to
// canonical) is intentionally NOT done here — it's an expensive
// content scan that's better handled as a background pass against the
// merged_into hint. P2-D-extended will add a wikilink-rewriter worker
// once the link-graph index lands.
func (s *Store) MergePages(ctx context.Context, canonicalID, duplicateID uuid.UUID, actor string) error {
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
		SELECT id, project_id, title, frontmatter, version, deleted_at
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
		frontmatter []byte
		version     int
		deletedAt   *time.Time
	}
	pages := map[uuid.UUID]*pageRow{}
	for rows.Next() {
		p := &pageRow{}
		if err := rows.Scan(&p.id, &p.projectID, &p.title,
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
	if err := snapshotPageRevisionTx(ctx, tx, canonical.id, canonical.projectID, actor); err != nil {
		return fmt.Errorf("snapshot canonical pre-merge: %w", err)
	}
	if err := snapshotPageRevisionTx(ctx, tx, duplicate.id, duplicate.projectID, actor); err != nil {
		return fmt.Errorf("snapshot duplicate pre-merge: %w", err)
	}

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

	// Bump canonical version + emit events. version bump invalidates
	// any in-flight If-Match update on the canonical page so an editor
	// session that pre-loaded canonical sees a 409 on save and reloads
	// — which is the correct UX after a merge.
	if _, err := tx.Exec(ctx, `
		UPDATE brain.pages
		   SET version = version + 1, updated_at = now()
		 WHERE id = $1
	`, canonicalID); err != nil {
		return fmt.Errorf("bump canonical version: %w", err)
	}

	if err := emitEvent(ctx, tx, canonical.projectID, "user", actor,
		"page.merged", map[string]any{
			"canonical_id": canonicalID,
			"duplicate_id": duplicateID,
			"moved_blocks": movedBlocks,
			"canonical_v":  canonical.version + 1,
		}); err != nil {
		return err
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
	if err := snapshotPageRevisionTx(ctx, tx, in.PageID, in.ProjectID, in.ActorID); err != nil {
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
	if err := snapshotPageRevisionTx(ctx, tx, cur.PageID, projectID, in.ActorID); err != nil {
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
	if err := snapshotPageRevisionTx(ctx, tx, pageID, projectID, actor); err != nil {
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
const pageRevisionColumns = `id, page_id, project_id, actor_id, title, frontmatter, change_type, change_summary, created_at`

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
}

// UpdatePageBody —— body_md 权威写入口（§⑤ Path C，拍板同步写）。事务内：
//  1. 写前快照旧态（复用 ④ snapshotPageRevisionTx，含 body_md 原文 + blocks_json）
//  2. UPDATE pages.body_md + version+1 OCC
//  3. reconcileBlocksTx 把 live blocks 对账成 mdparse(newBody)（保 block_id）
//  4. emit page.updated（client page 流刷新）
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
	if err := snapshotPageRevisionTx(ctx, tx, cur.ID, cur.ProjectID, in.ActorID); err != nil {
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
	default:
		raw, _ := b.Content["text"].(string)
		return raw
	}
}

// BackfillBodyMd —— 一次性回填 body_md='' 的页（§⑤ 迁移 00066 后启动跑，幂等）。
// body_md='' 且有 live blocks → BlocksToMarkdown 重算 UPDATE；空页保持 ''。
// 跳过已回填（body_md<>'') 确保重复启动无副作用。返回回填页数。
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

// snapshotPageRevisionTx —— 把 page（写前旧态：title/frontmatter + live blocks）
// 存为 edit 版本。距上一条 edit 版本不足 revisionWindow 则跳过（窗口合并）。
// blocks_json 超 MaxBlocksJSONBytes 跳过本条（极大页罕见，保 restore 完整不截断）。
// 内部 tx-scoped 读 page，统一 4 调用点（UpdatePage/CreateBlock/UpdateBlock/SoftDeleteBlock）。
// SoftDeletePage 不快照：restore 无法 un-delete 页，快照成死数据；page 删除属回收站范畴。
func snapshotPageRevisionTx(ctx context.Context, tx pgx.Tx, pageID, projectID uuid.UUID, actorID string) error {
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
		INSERT INTO brain.page_revisions (page_id, project_id, actor_id, title, frontmatter, body_md, blocks_json, change_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'edit')
	`, pageID, projectID, actorID, title, fm, bodyMd, blocksJSON); err != nil {
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
			&r.ChangeType, &r.ChangeSummary, &r.CreatedAt); err != nil {
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
		SELECT id, page_id, project_id, actor_id, title, frontmatter, body_md, blocks_json, change_type, change_summary, created_at
		FROM brain.page_revisions WHERE id = $1 AND page_id = $2
	`, revisionID, pageID).Scan(
		&r.ID, &r.PageID, &r.ProjectID, &r.ActorID, &r.Title, &fm, &r.BodyMd, &blocks,
		&r.ChangeType, &r.ChangeSummary, &r.CreatedAt,
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
func (s *Store) RestorePageRevision(ctx context.Context, pageID, revisionID uuid.UUID, actorID string) (*Page, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. 当前 page（tx-scoped 读，保证一致态）。
	cur := &Page{}
	curFM := []byte("{}")
	err = tx.QueryRow(ctx, `
		SELECT id, project_id, parent_id, title, frontmatter, share_mode, version, created_at, updated_at
		FROM brain.pages WHERE id = $1 AND deleted_at IS NULL
	`, pageID).Scan(
		&cur.ID, &cur.ProjectID, &cur.ParentID, &cur.Title, &curFM, &cur.ShareMode, &cur.Version,
		&cur.CreatedAt, &cur.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(curFM, &cur.Frontmatter)

	// 2. 目标版本（含 blocks_json），严格 page_id 匹配。
	rev := &Revision{}
	var revFM, revBlocks []byte
	err = tx.QueryRow(ctx, `
		SELECT id, page_id, project_id, title, frontmatter, blocks_json
		FROM brain.page_revisions WHERE id = $1 AND page_id = $2
	`, revisionID, pageID).Scan(&rev.ID, &rev.PageID, &rev.ProjectID, &rev.Title, &revFM, &revBlocks)
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
		INSERT INTO brain.page_revisions (page_id, project_id, actor_id, title, frontmatter, blocks_json, change_type, change_summary)
		VALUES ($1, $2, $3, $4, $5, $6, 'restore', $7)
	`, pageID, cur.ProjectID, actorID, cur.Title, curFM, curBlocksJSON, RevisionRestoreSummary); err != nil {
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
		"page_id":   p.ID,
		"revision_id": rev.ID,
		"title":     p.Title,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// SavePageRevisionAsCopy —— 以该版本内容新建页：同 project、同 parent、标题追加
// RevisionCopySuffix、复制 frontmatter + 全部 snap blocks。走 CreatePage + CreateBlock
// （各开自身事务，顺序执行），block 用新生成的 id（副本页无既有 graph 引用，不需保 id）。
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
		ActorID:     actorID,
	})
	if err != nil {
		return nil, err
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
