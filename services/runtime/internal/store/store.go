// Package store is the data access layer for Runtime.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type TaskStatus string

const (
	StatusPending  TaskStatus = "pending"
	StatusRunning  TaskStatus = "running"
	StatusDone     TaskStatus = "done"
	StatusFailed   TaskStatus = "failed"
	StatusCanceled TaskStatus = "canceled"
)

type Task struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	ProjectID      *uuid.UUID
	Agent          string
	Prompt         string
	SystemPrompt   string
	Model          string
	PermissionMode string
	Status         TaskStatus
	ErrorMessage   string
	ThreadID       string
	RunID          string
	TokensIn       int64
	TokensOut      int64
	CostUSDMicros  int64
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

type CreateInput struct {
	UserID         uuid.UUID
	ProjectID      *uuid.UUID
	Agent          string
	Prompt         string
	SystemPrompt   string
	Model          string
	PermissionMode string
	ThreadID       string
	RunID          string
}

func (s *Store) Create(ctx context.Context, in CreateInput) (*Task, error) {
	t := &Task{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO runtime.tasks
			(user_id, project_id, agent, prompt, system_prompt, model, permission_mode,
			 status, thread_id, run_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9)
		RETURNING id, user_id, project_id, agent, prompt, system_prompt, model,
		          permission_mode, status, error_message, thread_id, run_id,
		          cost_tokens_in, cost_tokens_out, cost_usd_micros,
		          created_at, started_at, finished_at
	`, in.UserID, in.ProjectID, in.Agent, in.Prompt, in.SystemPrompt, in.Model,
		in.PermissionMode, in.ThreadID, in.RunID).Scan(
		&t.ID, &t.UserID, &t.ProjectID, &t.Agent, &t.Prompt, &t.SystemPrompt, &t.Model,
		&t.PermissionMode, &t.Status, &t.ErrorMessage, &t.ThreadID, &t.RunID,
		&t.TokensIn, &t.TokensOut, &t.CostUSDMicros,
		&t.CreatedAt, &t.StartedAt, &t.FinishedAt,
	)
	return t, err
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Task, error) {
	t := &Task{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, project_id, agent, prompt, system_prompt, model,
		       permission_mode, status, error_message, thread_id, run_id,
		       cost_tokens_in, cost_tokens_out, cost_usd_micros,
		       created_at, started_at, finished_at
		FROM runtime.tasks WHERE id = $1
	`, id).Scan(
		&t.ID, &t.UserID, &t.ProjectID, &t.Agent, &t.Prompt, &t.SystemPrompt, &t.Model,
		&t.PermissionMode, &t.Status, &t.ErrorMessage, &t.ThreadID, &t.RunID,
		&t.TokensIn, &t.TokensOut, &t.CostUSDMicros,
		&t.CreatedAt, &t.StartedAt, &t.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *Store) MarkRunning(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runtime.tasks SET status = 'running', started_at = now()
		WHERE id = $1 AND status = 'pending'
	`, id)
	return err
}

type FinishInput struct {
	TaskID        uuid.UUID
	Status        TaskStatus
	ErrorMessage  string
	TokensIn      int64
	TokensOut     int64
	CostUSDMicros int64
}

func (s *Store) Finish(ctx context.Context, in FinishInput) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runtime.tasks
		SET status = $2, error_message = $3,
		    cost_tokens_in = $4, cost_tokens_out = $5, cost_usd_micros = $6,
		    finished_at = now()
		WHERE id = $1 AND status NOT IN ('done','failed','canceled')
	`, in.TaskID, in.Status, in.ErrorMessage, in.TokensIn, in.TokensOut, in.CostUSDMicros)
	return err
}

func (s *Store) ListByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, project_id, agent, prompt, system_prompt, model,
		       permission_mode, status, error_message, thread_id, run_id,
		       cost_tokens_in, cost_tokens_out, cost_usd_micros,
		       created_at, started_at, finished_at
		FROM runtime.tasks WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.ProjectID, &t.Agent, &t.Prompt, &t.SystemPrompt, &t.Model,
			&t.PermissionMode, &t.Status, &t.ErrorMessage, &t.ThreadID, &t.RunID,
			&t.TokensIn, &t.TokensOut, &t.CostUSDMicros,
			&t.CreatedAt, &t.StartedAt, &t.FinishedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
