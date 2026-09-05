// Package ingest is the brain side of the wiki ingest pipeline:
// the API surface plus the NATS coordination layer for tasks consumed
// by workers/wiki-llm.
//
// Single-page source-file parsing is owned by workers/wiki-parse
// (file → extracted_text); this package owns the multi-page CoT path:
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
//	brain.wiki.ingest.cancel     brain → worker  (cancel broadcast, no queue group)
//
// The "update" subscriber is wired in P1-8 once the worker exists; this
// file only owns the table + the requested-side publish.
package ingest

import (
	"context"
	"encoding/json"
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
	// SourceHash 是建任务时 source 的 content_hash（hex），落 progress.source_hash
	// 供下次 POST /ingest 的增量短路比对（content_hash 未变即跳过重跑）。
	// 空串 = 无 source / source 无 hash（老数据），不参与短路。
	SourceHash string
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
	progress := "{}"
	if in.SourceHash != "" {
		b, _ := json.Marshal(map[string]any{"source_hash": in.SourceHash})
		progress = string(b)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	t := &Task{}
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.ingest_tasks
		    (project_id, owner_id, source_id, raw_text, title, status, processor, progress)
		VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7::jsonb)
		RETURNING `+taskCols, in.ProjectID, in.OwnerID, in.SourceID,
		in.RawText, in.Title, in.Processor, progress).Scan(
		&t.ID, &t.ProjectID, &t.OwnerID, &t.SourceID, &t.RawText, &t.Title,
		&t.Status, &t.Error, &t.Progress, &t.ResultPages, &t.Processor,
		&t.CancelRequestedAt, &t.StartedAt, &t.FinishedAt,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}

	payload := map[string]any{
		"task_id":    t.ID.String(),
		"project_id": t.ProjectID.String(),
		"title":      t.Title,
		"status":     t.Status,
		"processor":  t.Processor,
	}
	if t.SourceID != nil {
		payload["source_id"] = t.SourceID.String()
	}
	if err := emitEventTx(ctx, tx, t.ProjectID, "user", t.OwnerID.String(),
		"ingest_task.created", payload); err != nil {
		return nil, fmt.Errorf("emit created event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
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
// writes, by the API layer (client-mirror PATCH), and by the reaper.
// Each one commits (status update, brain.events row) in the same tx —
// the events row is what syncws projects to clients as ingest_task.* ops.
//
// actorType/actorID land on brain.events.actor_*：subscriber 传
// ("worker","wiki-llm-worker")，PATCH 传 ("user",uid)，reaper 传
// ("system","ingest-reaper")。

// MarkRunning transitions pending → running. Idempotent for already-
// running tasks (no-op when status is already running/partial). Emits
// ingest_task.started only on the real pending → running edge — repeat
// "running" updates would otherwise spam a duplicate event per delivery.
func (s *Store) MarkRunning(ctx context.Context, id uuid.UUID, actorType, actorID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var projectID uuid.UUID
	var title, prevStatus string
	err = tx.QueryRow(ctx, `
		WITH old AS (SELECT status FROM brain.ingest_tasks WHERE id = $1)
		UPDATE brain.ingest_tasks
		   SET status = 'running',
		       started_at = COALESCE(started_at, now()),
		       updated_at = now()
		 WHERE id = $1 AND status IN ('pending','running','partial')
		RETURNING project_id, title, (SELECT status FROM old)
	`, id).Scan(&projectID, &title, &prevStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if prevStatus == StatusPending {
		if err := emitEventTx(ctx, tx, projectID, actorType, actorID,
			"ingest_task.started", map[string]any{
				"task_id":    id.String(),
				"project_id": projectID.String(),
				"title":      title,
				"status":     StatusRunning,
			}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AppendResultPage records a page as having been emitted by this
// task and bumps status to "partial" if still running. The worker
// publishes one update per page so the UI sees streaming progress.
// Emits ingest_task.page with page_id + pages_done（客户端进度页按
// op=page 增量收集 result_pages）。
func (s *Store) AppendResultPage(ctx context.Context, id, pageID uuid.UUID, progress map[string]any, actorType, actorID string) error {
	if progress == nil {
		progress = map[string]any{}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var projectID uuid.UUID
	var status string
	var resultPages []uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE brain.ingest_tasks
		   SET result_pages = array_append(result_pages, $2),
		       status = CASE WHEN status IN ('pending','running')
		                     THEN 'partial' ELSE status END,
		       progress = CASE WHEN progress ? 'source_hash'
		                  THEN jsonb_set($3::jsonb, '{source_hash}', progress->'source_hash', true)
		                  ELSE $3::jsonb END,
		       updated_at = now()
		 WHERE id = $1 AND status NOT IN ('done','failed','cancelled')
		RETURNING project_id, status, result_pages
	`, id, pageID, progress).Scan(&projectID, &status, &resultPages)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	payload := map[string]any{
		"task_id":    id.String(),
		"project_id": projectID.String(),
		"page_id":    pageID.String(),
		"status":     status,
		"pages_done": len(resultPages),
	}
	if p, ok := progress["last_path"].(string); ok && p != "" {
		payload["path"] = p
	}
	if err := emitEventTx(ctx, tx, projectID, actorType, actorID,
		"ingest_task.page", payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkTerminal flips the task to a terminal status with optional error
// text. The pre-existing transient status filter prevents late worker
// updates from clobbering a task the user already cancelled. Emits
// ingest_task.done|failed|cancelled（done 带 result_pages，failed 带 error）。
func (s *Store) MarkTerminal(ctx context.Context, id uuid.UUID, status, errMsg, actorType, actorID string) error {
	if !IsTerminal(status) {
		return fmt.Errorf("status %q is not terminal", status)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var projectID uuid.UUID
	var title string
	var resultPages []uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE brain.ingest_tasks
		   SET status = $2, error = $3,
		       finished_at = now(), updated_at = now()
		 WHERE id = $1 AND status NOT IN ('done','failed','cancelled')
		RETURNING project_id, title, result_pages
	`, id, status, errMsg).Scan(&projectID, &title, &resultPages)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	payload := map[string]any{
		"task_id":      id.String(),
		"project_id":   projectID.String(),
		"title":        title,
		"status":       status,
		"result_pages": uuidStrings(resultPages),
	}
	if status == StatusFailed && errMsg != "" {
		payload["error"] = errMsg
	}
	if err := emitEventTx(ctx, tx, projectID, actorType, actorID,
		"ingest_task."+status, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

// Retry resets a failed/cancelled task to pending for a manual retry
// （POST …/tasks/{tid}/retry）。清空 error / cancel 标记 / 起止时间，
// 并归零 requeue_count —— 手动重试是用户显式判断"这次能成"，毒丸计数
// 不应让重试任务一卡就被 reaper 再判死。返回更新后的任务供 API 层重发。
func (s *Store) Retry(ctx context.Context, id uuid.UUID) (*Task, error) {
	t := &Task{}
	err := s.pool.QueryRow(ctx, `
		UPDATE brain.ingest_tasks
		   SET status = 'pending',
		       error = '',
		       progress = progress - 'requeue_count',
		       cancel_requested_at = NULL,
		       started_at = NULL,
		       finished_at = NULL,
		       updated_at = now()
		 WHERE id = $1 AND status IN ('failed','cancelled')
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

// SweepCancelRequested 把取消信号超龄（worker 始终未观测到）的非终态
// 任务标 cancelled。取消广播是 fire-and-forget：广播时无 worker 在线
// 信号即丢，而 ListStuck 又跳过 cancel_requested 的行 —— 没有本清扫，
// 这类任务会永远卡在 pending/running。返回清扫行数。每个被清扫的任务
// 同事务补一条 ingest_task.cancelled 事件（否则客户端进度页永远等不到终态）。
func (s *Store) SweepCancelRequested(ctx context.Context, before time.Time) (int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		UPDATE brain.ingest_tasks
		   SET status = 'cancelled', finished_at = now(), updated_at = now()
		 WHERE cancel_requested_at IS NOT NULL
		   AND cancel_requested_at < $1
		   AND status NOT IN ('done','failed','cancelled')
		RETURNING id, project_id, title
	`, before)
	if err != nil {
		return 0, err
	}
	type swept struct {
		id, projectID uuid.UUID
		title         string
	}
	var list []swept
	for rows.Next() {
		var sw swept
		if err := rows.Scan(&sw.id, &sw.projectID, &sw.title); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, sw)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, sw := range list {
		if err := emitEventTx(ctx, tx, sw.projectID, "system", "ingest-reaper",
			"ingest_task.cancelled", map[string]any{
				"task_id":    sw.id.String(),
				"project_id": sw.projectID.String(),
				"title":      sw.title,
				"status":     StatusCancelled,
			}); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(list)), nil
}

// UpdateProgress replaces progress on a non-terminal task. 客户端镜像
// 任务（processor=client）经 PATCH 调用 —— updated_at 顺带刷新，充当
// reaper 的惰性心跳。同步发 ingest_task.progress 事件：progress 的键
// （phase/stage/percent/...）平铺进 payload，客户端进度页按 op=progress 消费。
// 注：progress 是整体替换语义，但服务端写入的 source_hash（增量短路依据）
// 由 SQL 钉住，调用方替换冲不掉。
func (s *Store) UpdateProgress(ctx context.Context, id uuid.UUID, progress map[string]any, actorType, actorID string) error {
	if progress == nil {
		progress = map[string]any{}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var projectID uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `
		UPDATE brain.ingest_tasks
		   SET progress = CASE WHEN progress ? 'source_hash'
		                  THEN jsonb_set($2::jsonb, '{source_hash}', progress->'source_hash', true)
		                  ELSE $2::jsonb END,
		       updated_at = now()
		 WHERE id = $1 AND status NOT IN ('done','failed','cancelled')
		RETURNING project_id, status
	`, id, progress).Scan(&projectID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	payload := map[string]any{
		"task_id":    id.String(),
		"project_id": projectID.String(),
		"status":     status,
	}
	for k, v := range progress {
		payload[k] = v
	}
	if err := emitEventTx(ctx, tx, projectID, actorType, actorID,
		"ingest_task.progress", payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── content_hash 增量短路（task-level dedup）─────────────────────

// FindLastDoneBySource 返回该 source 最近一次成功（done 且有 result_pages）
// 的 ingest 任务；没有则 ErrNotFound。配合 Task.SourceHash 做 POST /ingest
// 的增量短路：source content_hash 未变且上次已成功 → 直接复用上次结果。
func (s *Store) FindLastDoneBySource(ctx context.Context, sourceID uuid.UUID) (*Task, error) {
	t := &Task{}
	err := s.pool.QueryRow(ctx, `
		SELECT `+taskCols+`
		FROM brain.ingest_tasks
		WHERE source_id = $1
		  AND status = 'done'
		  AND COALESCE(array_length(result_pages, 1), 0) > 0
		ORDER BY created_at DESC
		LIMIT 1
	`, sourceID).Scan(
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

// SourceHash 读 progress.source_hash（建任务时记录的 source content_hash，
// hex）。老任务无此字段 → 空串 → 不参与增量短路（安全兜底走正常 ingest）。
func (t *Task) SourceHash() string {
	if t.Progress == nil {
		return ""
	}
	s, _ := t.Progress["source_hash"].(string)
	return s
}

// ─── brain.events 写入 ───────────────────────────────────────────

// emitEventTx 在业务事务内追加一条 brain.events（与 wiki/store.emitEvent 同
// schema：scope=wiki:project:<pid>）。event_type 形如 "ingest_task.started"，
// syncws 按 "." 拆 (entity, op) 投影给客户端进度页。
func emitEventTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actorType, actorID, eventType string, payload map[string]any) error {
	pl, _ := json.Marshal(payload)
	scope := fmt.Sprintf("wiki:project:%s", projectID)
	_, err := tx.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, scope, actorType, actorID, eventType, pl)
	return err
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}
