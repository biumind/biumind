// Package ingest is the brain side of the wiki ingest pipeline:
// the API surface plus the NATS coordination layer for tasks consumed
// by workers/wiki-llm.
//
// This package complements (does not replace) services/brain/internal/ingestbus,
// which still serves single-page direct ingest from biu CLI / channels.
// What's new here:
//
//   - brain.ingest_tasks table — durable task with progress, cancel,
//     and result_pages tracking
//   - Multi-page CoT output (one source → many wiki pages)
//   - Streaming partial-save (UI sees pages land as the LLM emits them)
//   - Cooperative cancellation
//
// Subject layout (env-prefixed via biu/bus):
//
//	brain.wiki.ingest.requested  brain → worker  (task start)
//	brain.wiki.ingest.update     worker → brain  (status/progress/page)
//
// The "update" subscriber is wired in P1-8 once the worker exists; this
// file only owns the table + the requested-side publish.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// Status values mirror the migration's CHECK constraint.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusPartial   = "partial" // streaming: some pages already landed
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

func ValidStatus(s string) bool {
	switch s {
	case StatusPending, StatusRunning, StatusPartial,
		StatusDone, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// IsTerminal reports whether `s` is an end state. Terminal tasks no
// longer accept worker updates (worker callbacks for terminal tasks are
// silently ignored — typically a late event arriving after cancel).
func IsTerminal(s string) bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled
}

// Task is the projection of one brain.ingest_tasks row.
type Task struct {
	ID                uuid.UUID
	ProjectID         uuid.UUID
	OwnerID           uuid.UUID
	SourceID          *uuid.UUID
	RawText           string
	Title             string
	Status            string
	Error             string
	Progress          map[string]any
	ResultPages       []uuid.UUID
	Processor         string // server（worker 驱动）| client（客户端镜像，00007）
	CancelRequestedAt *time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// CreateInput is what the API handler hands the store. Either SourceID
// or RawText must be non-empty (the handler validates; this layer
// enforces nothing beyond NOT NULL — keeps store dumb).
type CreateInput struct {
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	SourceID  *uuid.UUID
	RawText   string
	Title     string
	Processor string // 默认 "server"；"client" = 客户端镜像任务（W2）
}

func (s *Store) Create(ctx context.Context, in CreateInput) (*Task, error) {
	if in.ProjectID == uuid.Nil || in.OwnerID == uuid.Nil {
		return nil, fmt.Errorf("project_id and owner_id required")
	}
	if in.SourceID == nil && strings.TrimSpace(in.RawText) == "" {
		return nil, fmt.Errorf("source_id or raw_text required")
	}
	if in.Processor == "" {
		in.Processor = "server"
	}
	t := &Task{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO brain.ingest_tasks
		    (project_id, owner_id, source_id, raw_text, title, status, processor)
		VALUES ($1, $2, $3, $4, $5, 'pending', $6)
		RETURNING `+taskCols, in.ProjectID, in.OwnerID, in.SourceID,
		in.RawText, in.Title, in.Processor).Scan(
		&t.ID, &t.ProjectID, &t.OwnerID, &t.SourceID, &t.RawText, &t.Title,
		&t.Status, &t.Error, &t.Progress, &t.ResultPages, &t.Processor,
		&t.CancelRequestedAt, &t.StartedAt, &t.FinishedAt,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}
	return t, nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Task, error) {
	t := &Task{}
	err := s.pool.QueryRow(ctx, `
		SELECT `+taskCols+`
		FROM brain.ingest_tasks WHERE id = $1
	`, id).Scan(
		&t.ID, &t.ProjectID, &t.OwnerID, &t.SourceID, &t.RawText, &t.Title,
		&t.Status, &t.Error, &t.Progress, &t.ResultPages, &t.Processor,
		&t.CancelRequestedAt, &t.StartedAt, &t.FinishedAt,
		&t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ListByProject returns recent tasks for a project, newest first.
// The owner check is the API layer's job (it already runs ownsProject).
func (s *Store) ListByProject(ctx context.Context, projectID uuid.UUID, limit int) ([]*Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskCols+`
		FROM brain.ingest_tasks
		WHERE project_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.OwnerID, &t.SourceID, &t.RawText, &t.Title,
			&t.Status, &t.Error, &t.Progress, &t.ResultPages, &t.Processor,
			&t.CancelRequestedAt, &t.StartedAt, &t.FinishedAt,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RequestCancel sets cancel_requested_at on a non-terminal task. It does
// NOT flip status to "cancelled" — that's the worker's job once it
// observes the flag at a chunk boundary, so we don't lose track of work
// the worker has already partially saved.
//
// Returns ErrNotFound when no row matches OR the task is already
// terminal (so the API layer surfaces the same 404 in both cases).
func (s *Store) RequestCancel(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.ingest_tasks
		   SET cancel_requested_at = now(), updated_at = now()
		 WHERE id = $1
		   AND status NOT IN ('done','failed','cancelled')
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Worker callback path ──────────────────────────────────────
// These methods are invoked by the brain-side NATS subscriber that
// translates worker `brain.wiki.ingest.update` messages into table
// writes. The API layer never calls them directly.

// MarkRunning transitions pending → running. Idempotent for already-
// running tasks (no-op when status is already running/partial).
func (s *Store) MarkRunning(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.ingest_tasks
		   SET status = 'running',
		       started_at = COALESCE(started_at, now()),
		       updated_at = now()
		 WHERE id = $1 AND status IN ('pending','running','partial')
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AppendResultPage records a page as having been emitted by this
// task and bumps status to "partial" if still running. The worker
// publishes one update per page so the UI sees streaming progress.
func (s *Store) AppendResultPage(ctx context.Context, id, pageID uuid.UUID, progress map[string]any) error {
	if progress == nil {
		progress = map[string]any{}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.ingest_tasks
		   SET result_pages = array_append(result_pages, $2),
		       status = CASE WHEN status IN ('pending','running')
		                     THEN 'partial' ELSE status END,
		       progress = $3::jsonb,
		       updated_at = now()
		 WHERE id = $1 AND status NOT IN ('done','failed','cancelled')
	`, id, pageID, progress)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkTerminal flips the task to a terminal status with optional error
// text. The pre-existing transient status filter prevents late worker
// updates from clobbering a task the user already cancelled.
func (s *Store) MarkTerminal(ctx context.Context, id uuid.UUID, status, errMsg string) error {
	if !IsTerminal(status) {
		return fmt.Errorf("status %q is not terminal", status)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.ingest_tasks
		   SET status = $2, error = $3,
		       finished_at = now(), updated_at = now()
		 WHERE id = $1 AND status NOT IN ('done','failed','cancelled')
	`, id, status, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Reaper path ─────────────────────────────────────────────────
// The reaper (reaper.go) recovers tasks that will never make progress
// on their own: 'pending' rows whose publish failed or message was lost,
// and 'running'/'partial' rows whose worker died mid-task.

const taskCols = `id, project_id, owner_id, source_id, raw_text, title,
       status, error, progress, result_pages, processor,
       cancel_requested_at, started_at, finished_at,
       created_at, updated_at`

// ListStuck returns non-terminal tasks that have gone quiet:
//   - 'pending'   with updated_at < pendingBefore（publish 失败/消息丢失）
//   - 'running'/'partial' with updated_at < activeBefore（worker/客户端中途死亡）
//
// processor 参数区分两类处置：'server' 走重发（Requeue + publish），
// 'client' 走接管（reaper.takeOverClient，不 publish）。updated_at 即惰性
// 心跳：worker/客户端每次推进都会刷新它。
func (s *Store) ListStuck(ctx context.Context, processor string, pendingBefore, activeBefore time.Time, limit int) ([]*Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskCols+`
		FROM brain.ingest_tasks
		WHERE processor = $1
		  AND cancel_requested_at IS NULL
		  AND (
		    (status = 'pending' AND updated_at < $2)
		    OR (status IN ('running','partial') AND updated_at < $3)
		  )
		ORDER BY updated_at ASC
		LIMIT $4
	`, processor, pendingBefore, activeBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.OwnerID, &t.SourceID, &t.RawText, &t.Title,
			&t.Status, &t.Error, &t.Progress, &t.ResultPages, &t.Processor,
			&t.CancelRequestedAt, &t.StartedAt, &t.FinishedAt,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RequeueCount reads progress.requeue_count（无则 0）。
func (t *Task) RequeueCount() int {
	if t.Progress == nil {
		return 0
	}
	if v, ok := t.Progress["requeue_count"].(float64); ok {
		return int(v)
	}
	return 0
}

// Requeue resets a stuck task to pending and bumps progress.requeue_count，
// 返回更新后的任务供 reaper 重组 publish payload。已终态/已取消的行返回
// ErrNotFound（并发下 worker 可能刚好完结，不再重发）。
func (s *Store) Requeue(ctx context.Context, id uuid.UUID) (*Task, error) {
	t := &Task{}
	err := s.pool.QueryRow(ctx, `
		UPDATE brain.ingest_tasks
		   SET status = 'pending',
		       progress = jsonb_set(
		         progress, '{requeue_count}',
		         to_jsonb(COALESCE((progress->>'requeue_count')::int, 0) + 1)),
		       updated_at = now()
		 WHERE id = $1 AND status IN ('pending','running','partial')
		RETURNING `+taskCols, id).Scan(
		&t.ID, &t.ProjectID, &t.OwnerID, &t.SourceID, &t.RawText, &t.Title,
		&t.Status, &t.Error, &t.Progress, &t.ResultPages, &t.Processor,
		&t.CancelRequestedAt, &t.StartedAt, &t.FinishedAt,
		&t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateProgress replaces progress on a non-terminal task. 客户端镜像
// 任务（processor=client）经 PATCH 调用 —— updated_at 顺带刷新，充当
// reaper 的惰性心跳。
func (s *Store) UpdateProgress(ctx context.Context, id uuid.UUID, progress map[string]any) error {
	if progress == nil {
		progress = map[string]any{}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.ingest_tasks
		   SET progress = $2::jsonb, updated_at = now()
		 WHERE id = $1 AND status NOT IN ('done','failed','cancelled')
	`, id, progress)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
