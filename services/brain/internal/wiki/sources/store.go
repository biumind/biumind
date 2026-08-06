// Package sources data access —— brain.wiki_sources 表 CRUD + dedup 查询。
//
// wiki_sources 是 wiki 唯一来源表（00057 合并了旧 brain.sources）：
//   - kind='webclip' —— 抓取网页（CreateWebclip，幂等 on content_hash）
//   - kind='upload' —— 上传文件（Upsert，幂等 on project_id+rel_path）
//
// 调用方在 api.go；底层用 pgxpool。所有 mutation 都返回完整 Source 行，
// 调用方再 marshal 成 JSON 响应。
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
)

// Source 是统一来源行。webclip 行：Kind='webclip'，URL/ExtractedText(原 raw)
// 填充，FileID=nil；upload 行：Kind='upload'，FileID/RelPath 填充。
type Source struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	Kind          string // webclip | upload | voice
	URL           string
	UserID        *uuid.UUID
	FileID        *uuid.UUID
	RelPath       string
	Filename      string
	Title         string
	Mime          string
	ByteSize      int64
	ContentHash   []byte // sha256；webclip=sha256(url|content)，upload=sha256(文件字节)
	ExtractedText string // webclip=抓取正文(原 raw)，upload=parser 抽取文本
	ParseStatus   string // queued/processing/done/error；webclip 入库直接 done
	ParseError    string
	ExternalID    string
	PageID        *uuid.UUID
	Metadata      map[string]any
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store {
	return &Store{pool: p}
}

const selectCols = `id, project_id, kind, url, user_id, file_id, rel_path, filename,
    title, mime, byte_size, content_hash, extracted_text, parse_status,
    parse_error, external_id, page_id, metadata, created_at, updated_at`

func scan(row pgx.Row) (*Source, error) {
	var s Source
	var url, title, mime, parseError, externalID, extracted *string
	var userID, fileID, pageID *uuid.UUID
	var mdRaw []byte
	if err := row.Scan(
		&s.ID, &s.ProjectID, &s.Kind, &url, &userID, &fileID,
		&s.RelPath, &s.Filename, &title, &mime, &s.ByteSize,
		&s.ContentHash, &extracted, &s.ParseStatus, &parseError,
		&externalID, &pageID, &mdRaw, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if url != nil {
		s.URL = *url
	}
	if title != nil {
		s.Title = *title
	}
	if mime != nil {
		s.Mime = *mime
	}
	if parseError != nil {
		s.ParseError = *parseError
	}
	if externalID != nil {
		s.ExternalID = *externalID
	}
	if extracted != nil {
		s.ExtractedText = *extracted
	}
	s.UserID = userID
	s.FileID = fileID
	s.PageID = pageID
	if len(mdRaw) > 0 {
		_ = json.Unmarshal(mdRaw, &s.Metadata)
	}
	return &s, nil
}

// emitEvent 写一条 brain.events（与 wiki/store.emitEvent 同 schema）。
// sources 包独立于 wiki/store，事件 schema 保持兼容（scope=wiki:project:<pid>）。
func (s *Store) emitEvent(
	ctx context.Context, tx pgx.Tx,
	projectID uuid.UUID, actorType, actorID, eventType string,
	payload map[string]any,
) error {
	pl, _ := json.Marshal(payload)
	scope := fmt.Sprintf("wiki:project:%s", projectID)
	_, err := tx.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, scope, actorType, actorID, eventType, pl)
	return err
}

// CreateInput is the data needed to upsert an upload source. RelPath is the
// uniqueness key per project; the same path uploaded twice updates
// file_id + content_hash + parse_status in place.
type CreateInput struct {
	ProjectID     uuid.UUID
	UserID        *uuid.UUID // Phase 2：handleCreate 从 JWT 写入
	FileID        *uuid.UUID
	RelPath       string
	Filename      string
	Mime          string
	ByteSize      int64
	ContentHash   []byte
	ExtractedText string
	ParseStatus   string // 默认 "queued"
	ExternalID    string
}

func (s *Store) Upsert(ctx context.Context, in CreateInput) (*Source, error) {
	if in.ParseStatus == "" {
		in.ParseStatus = "queued"
	}
	q := fmt.Sprintf(`
        INSERT INTO brain.wiki_sources
          (project_id, user_id, file_id, rel_path, filename, mime, byte_size,
           content_hash, extracted_text, parse_status, external_id, kind)
        VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,NULLIF($9,''),$10,NULLIF($11,''),'upload')
        ON CONFLICT (project_id, rel_path) WHERE kind = 'upload' DO UPDATE SET
          user_id         = COALESCE(EXCLUDED.user_id, wiki_sources.user_id),
          file_id         = EXCLUDED.file_id,
          filename        = EXCLUDED.filename,
          mime            = EXCLUDED.mime,
          byte_size       = EXCLUDED.byte_size,
          content_hash    = EXCLUDED.content_hash,
          extracted_text  = EXCLUDED.extracted_text,
          parse_status    = EXCLUDED.parse_status,
          parse_error     = NULL,
          external_id     = EXCLUDED.external_id,
          updated_at      = now()
        RETURNING %s`, selectCols)
	row := s.pool.QueryRow(ctx, q,
		in.ProjectID, in.UserID, in.FileID, in.RelPath, in.Filename, in.Mime,
		in.ByteSize, in.ContentHash, in.ExtractedText, in.ParseStatus,
		in.ExternalID,
	)
	return scan(row)
}

// CreateWebclipInput 是 webclip 抓取建源的入参。ContentHash = sha256(url|content)，
// 由 handler 算好传入（幂等键）。
type CreateWebclipInput struct {
	ProjectID   uuid.UUID
	UserID      uuid.UUID
	URL         string
	Title       string
	Raw         string
	Metadata    map[string]any
	ContentHash []byte
}

// CreateWebclip 幂等建一个 webclip 来源。命中 (project_id, content_hash)
// WHERE kind='webclip' 返回旧行 + dup=true。否则 INSERT（kind='webclip'，
// parse_status='done'，extracted_text=raw）+ emit source.created。
//
// 替代旧 wiki/store.CreateSource（00057 退役）。rel_path/filename NOT NULL
// 用 url 末段/title 兜底（webclip 不靠 rel_path 去重）。
func (s *Store) CreateWebclip(ctx context.Context, in CreateWebclipInput) (*Source, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	// 幂等查：(project_id, content_hash) WHERE kind='webclip'
	row := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM brain.wiki_sources
		WHERE project_id = $1 AND content_hash = $2 AND kind = 'webclip'
		LIMIT 1`, selectCols), in.ProjectID, in.ContentHash)
	if existing, err := scan(row); err == nil {
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, false, cerr
		}
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	mdJSON, _ := json.Marshal(in.Metadata)
	if len(mdJSON) == 0 {
		mdJSON = []byte("{}")
	}
	row = tx.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO brain.wiki_sources
		  (project_id, kind, user_id, url, title, extracted_text, metadata,
		   content_hash, parse_status, rel_path, filename)
		VALUES ($1,'webclip',$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7,'done',
		        COALESCE(NULLIF(substring($3 from '[^/]+$'), ''), 'webclip/' || gen_random_uuid()::text),
		        COALESCE(NULLIF($4,''), 'webclip'))
		RETURNING %s`, selectCols),
		in.ProjectID, in.UserID, in.URL, in.Title, in.Raw, mdJSON, in.ContentHash)
	created, err := scan(row)
	if err != nil {
		return nil, false, err
	}

	if err := s.emitEvent(ctx, tx, in.ProjectID, "user", in.UserID.String(), "source.created", map[string]any{
		"source_id": created.ID, "kind": "webclip", "url": created.URL,
	}); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return created, false, nil
}

// GetByID returns one source row by ID. 给 ingest internal_api 反查 raw 用
// （替代旧 wiki/store.GetSource）。
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*Source, error) {
	q := fmt.Sprintf(`SELECT %s FROM brain.wiki_sources WHERE id=$1`, selectCols)
	src, err := scan(s.pool.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return src, nil
}

func (s *Store) ListByProject(ctx context.Context, pid uuid.UUID, limit int) ([]*Source, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := fmt.Sprintf(`
        SELECT %s FROM brain.wiki_sources
        WHERE project_id = $1
        ORDER BY created_at DESC
        LIMIT $2`, selectCols)
	rows, err := s.pool.Query(ctx, q, pid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Source, 0, limit)
	for rows.Next() {
		src, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Source, error) {
	q := fmt.Sprintf(`SELECT %s FROM brain.wiki_sources WHERE id=$1`, selectCols)
	src, err := scan(s.pool.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return src, nil
}

// Delete removes the source row. Caller is responsible for cascading
// downstream cleanup (page detachment etc.) in higher layers if needed.
// Returns ErrNotFound if no row matched.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM brain.wiki_sources WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Phase 3: parse worker 回写 + dedup + blob 解析 ───────────────

// UpdateParseInput is the wiki-parse worker's write-back payload. The
// worker sets parse_status to processing on pickup, then done (with
// extracted_text + content_hash) or error (with parse_error + bumped
// retries). Empty ExtractedText/ContentHash preserve the existing
// column via COALESCE so the error path doesn't clobber a partial run.
type UpdateParseInput struct {
	ID            uuid.UUID
	ParseStatus   string // processing | done | error
	ExtractedText string // done 时填（parser 输出）
	ContentHash   []byte // done 时填（sha256(extracted_text)）
	ParseError    string // error 时填异常信息
	BumpRetries   bool   // error 时 true → retries++（ListParseQueue 按 retries<3 收）
}

// UpdateParseStatus mutates one row's parse lifecycle columns and
// returns the updated row. Returns ErrNotFound if no row matched.
func (s *Store) UpdateParseStatus(ctx context.Context, in UpdateParseInput) (*Source, error) {
	q := fmt.Sprintf(`
        UPDATE brain.wiki_sources SET
            parse_status   = $2,
            parse_error    = NULLIF($3, ''),
            extracted_text = COALESCE(NULLIF($4, ''), extracted_text),
            content_hash   = COALESCE($5, content_hash),
            retries        = retries + CASE WHEN $6 THEN 1 ELSE 0 END,
            updated_at     = now()
        WHERE id = $1
        RETURNING %s`, selectCols)
	row := s.pool.QueryRow(ctx, q,
		in.ID, in.ParseStatus, in.ParseError, in.ExtractedText,
		in.ContentHash, in.BumpRetries)
	src, err := scan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return src, nil
}

// ListParseQueue returns upload rows awaiting parse — either freshly
// queued or errored with retries left. The wiki-parse worker calls
// this on startup + each tick to backstop the NATS trigger (covers
// messages lost to subject mismatch, worker downtime, or NoopBus dev).
// Ordered oldest-first so a backlog drains in arrival order.
func (s *Store) ListParseQueue(ctx context.Context, maxRetries, limit int) ([]*Source, error) {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := fmt.Sprintf(`
        SELECT %s FROM brain.wiki_sources
        WHERE kind = 'upload'
          AND file_id IS NOT NULL
          AND parse_status IN ('queued', 'error')
          AND retries < $1
        ORDER BY created_at ASC
        LIMIT $2`, selectCols)
	rows, err := s.pool.Query(ctx, q, maxRetries, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Source, 0, limit)
	for rows.Next() {
		src, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// FindSourceDupes returns same-project siblings sharing the given
// content_hash, excluding the source itself. The parse-result handler
// calls this after a successful parse to surface project-internal
// duplicates as review_items. content_hash is sha256(extracted_text)
// so different file formats with the same text content also match
// (user-confirmed decision: cross-format dedup is desired).
func (s *Store) FindSourceDupes(
	ctx context.Context, projectID uuid.UUID, contentHash []byte, excludeID uuid.UUID,
) ([]*Source, error) {
	if len(contentHash) == 0 {
		return nil, nil
	}
	q := fmt.Sprintf(`
        SELECT %s FROM brain.wiki_sources
        WHERE project_id = $1
          AND content_hash = $2
          AND id <> $3
        ORDER BY created_at ASC`, selectCols)
	rows, err := s.pool.Query(ctx, q, projectID, contentHash, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Source, 0)
	for rows.Next() {
		src, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// FileObjectKey resolves the MinIO object_key for a source's file via
// files.objects. The blob-presign handler calls this to mint a presigned
// GET URL for the wiki-parse worker. Returns ErrNotFound if the source
// has no file_id or the file row is gone.
func (s *Store) FileObjectKey(ctx context.Context, sourceID uuid.UUID) (string, error) {
	var objectKey string
	err := s.pool.QueryRow(ctx, `
        SELECT o.object_key
          FROM brain.wiki_sources ws
          JOIN files.objects o ON o.id = ws.file_id
         WHERE ws.id = $1`, sourceID).Scan(&objectKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return objectKey, nil
}

// ExternalIDsInProject returns all external_id values currently
// recorded for a project. Connector/import dialogs use this to avoid
// re-importing the same upstream item.
func (s *Store) ExternalIDsInProject(ctx context.Context, pid uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT external_id FROM brain.wiki_sources
         WHERE project_id=$1 AND external_id IS NOT NULL`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
