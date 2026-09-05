// Package research stores deep-research tasks: topic → web search →
// LLM synthesis → wiki page.
//
// The store is intentionally thin — Create / Get / List / Update — so
// the orchestrator (research.go) can drive transitions without
// re-implementing SQL conventions.
package research

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values written to research_tasks.status.
const (
	StatusQueued       = "queued"
	StatusSearching    = "searching"
	StatusSynthesizing = "synthesizing"
	StatusSaving       = "saving"
	StatusDone         = "done"
	StatusError        = "error"
)

type WebHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Source  string `json:"source"`
}

type Task struct {
	ID           uuid.UUID
	ProjectID    uuid.UUID
	OwnerID      uuid.UUID
	Topic        string
	Queries      []string
	Status       string
	PageID       *uuid.UUID
	WebResults   []WebHit
	Synthesis    string
	ErrorMessage string
	// SourceReviewID links the task back to the review queue entry it
	// was spawned from (reviews_page「研究」action). Nil for manually
	// started research. The orchestrator auto-resolves this review once
	// the research page lands.
	SourceReviewID *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time // nil until the task first leaves 'queued'
	FinishedAt     *time.Time // nil until Complete/Fail stamps it
}

var ErrNotFound = errors.New("research task not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a queued task and returns the row. sourceReviewID is
// nil for manually started research; non-nil marks the task as spawned
// from a review queue entry (auto-resolved on completion).
func (s *Store) Create(ctx context.Context, projectID, ownerID uuid.UUID, topic string, queries []string, sourceReviewID *uuid.UUID) (*Task, error) {
	if queries == nil {
		queries = []string{}
	}
	var t Task
	err := s.pool.QueryRow(ctx, `
		INSERT INTO brain.research_tasks (project_id, owner_id, topic, queries, status, source_review_id)
		VALUES ($1, $2, $3, $4, 'queued', $5)
		RETURNING id, created_at, updated_at
	`, projectID, ownerID, topic, queries, sourceReviewID).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.ProjectID = projectID
	t.OwnerID = ownerID
	t.Topic = topic
	t.Queries = queries
	t.Status = StatusQueued
	t.SourceReviewID = sourceReviewID
	return &t, nil
}

// Get returns a task by id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Task, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, project_id, owner_id, topic, queries, status,
		       page_id, web_results, synthesis,
		       COALESCE(error_message, ''), source_review_id, created_at, updated_at,
		       started_at, finished_at
		FROM brain.research_tasks WHERE id = $1
	`, id)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListByProject returns the project's tasks, newest first. Limit caps
// the result set; 0 → 50.
func (s *Store) ListByProject(ctx context.Context, projectID uuid.UUID, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, owner_id, topic, queries, status,
		       page_id, web_results, synthesis,
		       COALESCE(error_message, ''), source_review_id, created_at, updated_at,
		       started_at, finished_at
		FROM brain.research_tasks
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
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetStatus is a single-shot update. Used between pipeline phases to
// emit progress without touching unrelated columns.
//
// started_at is stamped exactly once — the first time the task enters
// 'searching' (the queued → searching transition marks "work began").
// Re-entering searching on a recover re-run leaves the original stamp
// intact so in-flight wall-clock stays honest.
func (s *Store) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE brain.research_tasks
		SET status = $1,
		    updated_at = now(),
		    started_at = CASE
		        WHEN $1 = 'searching' AND started_at IS NULL THEN now()
		        ELSE started_at
		    END
		WHERE id = $2`,
		status, id)
	return err
}

// SaveWebResults persists the dedup'd hit list once /searching/ is done.
func (s *Store) SaveWebResults(ctx context.Context, id uuid.UUID, hits []WebHit) error {
	b, err := json.Marshal(hits)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE brain.research_tasks SET web_results = $1, updated_at = now() WHERE id = $2`,
		b, id)
	return err
}

// AppendSynthesis incrementally updates the running synthesis text.
// Used when we want to expose streaming progress to the UI; the
// orchestrator may also call SetStatus around this.
func (s *Store) AppendSynthesis(ctx context.Context, id uuid.UUID, full string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE brain.research_tasks SET synthesis = $1, updated_at = now() WHERE id = $2`,
		full, id)
	return err
}

// Complete marks the task done with the resulting page id.
func (s *Store) Complete(ctx context.Context, id, pageID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE brain.research_tasks
		SET status = 'done', page_id = $1, finished_at = now(), updated_at = now()
		WHERE id = $2
	`, pageID, id)
	return err
}

// Fail marks the task errored with a message.
func (s *Store) Fail(ctx context.Context, id uuid.UUID, msg string) error {
	if len(msg) > 4000 {
		msg = msg[:4000]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE brain.research_tasks
		SET status = 'error', error_message = $1, finished_at = now(), updated_at = now()
		WHERE id = $2
	`, msg, id)
	return err
}

// ListStuck returns active tasks whose updated_at is older than the
// cutoff — i.e. in-flight when the process died and never adopted on
// boot. Backed by research_tasks_active_idx (partial, active-only).
// Ordered oldest-first so a long-stuck queue drains fairly.
func (s *Store) ListStuck(ctx context.Context, olderThan time.Duration) ([]*Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, owner_id, topic, queries, status,
		       page_id, web_results, synthesis,
		       COALESCE(error_message, ''), source_review_id, created_at, updated_at,
		       started_at, finished_at
		FROM brain.research_tasks
		WHERE status IN ('queued', 'searching', 'synthesizing', 'saving')
		  AND updated_at < now() - make_interval(secs => $1)
		ORDER BY updated_at ASC
		LIMIT 200
	`, int(olderThan.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindPageByTaskID looks up an existing wiki page previously written for
// this research task (frontmatter marker research_taskid). Used by the
// orchestrator's savePage to stay idempotent across a crash-recover: if
// the page was created but the task row's page_id wasn't stamped before
// the crash, the recover re-run reuses the page instead of creating a
// duplicate. Returns nil (no error) when none exists. Soft-deleted pages
// are excluded — a deleted research page must not block a re-run.
func (s *Store) FindPageByTaskID(ctx context.Context, projectID, taskID uuid.UUID) (*uuid.UUID, error) {
	var pageID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM brain.pages
		WHERE project_id = $1
		  AND frontmatter->>'research_taskid' = $2
		  AND deleted_at IS NULL
		LIMIT 1
	`, projectID, taskID.String()).Scan(&pageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pageID, nil
}

// IsActiveStatus reports whether a task status is non-terminal (work may
// still be in flight). Delete/rerun both refuse active tasks — research
// has no cancel signal (unlike ingest's cancel_requested_at), so the
// state-machine-aligned answer is to reject and let the user wait for a
// terminal state first.
func IsActiveStatus(status string) bool {
	switch status {
	case StatusQueued, StatusSearching, StatusSynthesizing, StatusSaving:
		return true
	}
	return false
}

// Delete hard-deletes a terminal task row (the table has no soft-delete
// column). Returns false when the row doesn't exist or is still active —
// callers map that to 404/409 from their own Get-first check. The wiki
// page the task produced (if any) is user content and is NOT touched.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM brain.research_tasks
		WHERE id = $1
		  AND status NOT IN ('queued', 'searching', 'synthesizing', 'saving')
	`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ResetForRerun rewinds a terminal task to 'queued' so Orch.Run re-executes
// the full pipeline: phase outputs (page_id / web_results / synthesis /
// error / started/finished stamps) are cleared; topic + queries survive.
//
// Two guards in one tx:
//   - the WHERE status IN ('done','error') clause makes the reset
//     conditional — a concurrent rerun (or a rerun of an active task)
//     affects 0 rows and the caller returns 409, so the same task can
//     never be re-run twice at once
//   - the previous page's research_taskid frontmatter marker is stripped
//     (page itself is kept — it may hold user edits) so savePage's
//     crash-recover dup guard doesn't reattach the rerun to the old page
//     and a fresh "Research: <topic>" page is written instead
//
// Returns (true, nil) when the reset landed.
func (s *Store) ResetForRerun(ctx context.Context, id uuid.UUID) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE brain.research_tasks
		SET status = 'queued',
		    page_id = NULL,
		    web_results = '[]'::jsonb,
		    synthesis = '',
		    error_message = NULL,
		    started_at = NULL,
		    finished_at = NULL,
		    updated_at = now()
		WHERE id = $1
		  AND status IN ('done', 'error')
	`, id)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE brain.pages
		SET frontmatter = frontmatter - 'research_taskid', updated_at = now()
		WHERE frontmatter->>'research_taskid' = $1
		  AND deleted_at IS NULL
	`, id.String()); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	var queries []string
	var hitsJSON []byte
	if err := row.Scan(
		&t.ID, &t.ProjectID, &t.OwnerID, &t.Topic, &queries, &t.Status,
		&t.PageID, &hitsJSON, &t.Synthesis, &t.ErrorMessage,
		&t.SourceReviewID, &t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.FinishedAt,
	); err != nil {
		return nil, err
	}
	t.Queries = queries
	if len(hitsJSON) > 0 {
		_ = json.Unmarshal(hitsJSON, &t.WebResults)
	}
	return &t, nil
}
