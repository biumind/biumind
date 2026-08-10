// Package chat is the data access + business layer for chat threads.
//
// Schema lives under the `chat` namespace (migration 00006_chat.sql),
// covering threads + messages + message_groups (the last is a
// placeholder for future multi-model parallel; see design doc §14.5).
//
// All operations are owner-scoped: the caller passes a userID derived
// from the JWT subject and every query filters by `user_id =`. A
// missing-or-mismatched user means ErrNotFound (NOT 403) so we don't
// leak the existence of someone else's thread.
package chat

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

var (
	ErrNotFound = errors.New("chat: not found")
	ErrConflict = errors.New("chat: client_id collision")
	ErrInvalid  = errors.New("chat: invalid input")
	// ErrThreadOwnedByOther 是 EnsureThread 在客户端给的 thread id 已被
	// 另一个 user 占用时返回的哨兵 —— 区别于 ErrNotFound 的防探测语义,
	// 这里调用方显式复用了冲突 id,路由层映射 409 让客户端重新生成 id。
	ErrThreadOwnedByOther = errors.New("chat: thread owned by another user")
)

// 6-state machine aligned with cherry-studio (design doc §14.4).
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusStreaming  = "streaming"
	StatusSuccess    = "success"
	StatusError      = "error"
	StatusPaused     = "paused"
)

// Roles a message can take.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"
)

func validRole(r string) bool {
	switch r {
	case RoleUser, RoleAssistant, RoleTool, RoleSystem:
		return true
	}
	return false
}

// Thread is one persistent conversation (sidebar entry).
//
// Runtime v3 D9: thread.execution_mode 已废弃删除（migration 00040）。loop
// 位置由 agent_sessions.mode 表达；工具执行环境由 agent_sessions.runtime_env_mode
// 表达（轴 B）。chat 模式工具集恒走 tools.ExecutionCloud（loop 在 brain）。
type Thread struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	ProjectID       *uuid.UUID
	Title           string
	LastMsgPreview  string
	Model           *string
	SystemPrompt    *string
	Pinned          bool
	Archived        bool
	Summary         *string
	SummaryUntilPos *int64
	AgentID         *uuid.UUID
	AgentChain      []byte // raw JSON
	ParentThreadID  *uuid.UUID
	SyncEnabled     bool
	Metadata        []byte // raw JSON
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Message is one turn inside a thread.
type Message struct {
	ID               uuid.UUID
	ThreadID         uuid.UUID
	UserID           uuid.UUID
	Role             string
	Content          string
	Parts            []byte // raw JSON array
	ToolCallID       *string
	ParentID         *uuid.UUID
	Model            *string
	PromptTokens     *int
	CompletionTokens *int
	Status           string
	ErrorMsg         *string
	ClientID         *string
	AgentID          *uuid.UUID
	MessageGroupID   *uuid.UUID
	Position         int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ─── Threads ─────────────────────────────────────────

type CreateThreadInput struct {
	UserID         uuid.UUID
	ProjectID      *uuid.UUID
	Title          string
	Model          *string
	SystemPrompt   *string
	AgentID        *uuid.UUID
	ParentThreadID *uuid.UUID
	SyncEnabled    bool
}

func (s *Store) CreateThread(ctx context.Context, in CreateThreadInput) (*Thread, error) {
	if in.UserID == uuid.Nil {
		return nil, fmt.Errorf("%w: user_id required", ErrInvalid)
	}
	t := &Thread{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chat.threads
		    (user_id, project_id, title, model, system_prompt,
		     agent_id, parent_thread_id, sync_enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, user_id, project_id, title, last_msg_preview,
		          model, system_prompt, pinned, archived,
		          summary, summary_until_position,
		          agent_id, agent_chain, parent_thread_id, sync_enabled,
		          metadata, created_at, updated_at
	`, in.UserID, in.ProjectID, in.Title, in.Model, in.SystemPrompt,
		in.AgentID, in.ParentThreadID, in.SyncEnabled,
	).Scan(
		&t.ID, &t.UserID, &t.ProjectID, &t.Title, &t.LastMsgPreview,
		&t.Model, &t.SystemPrompt, &t.Pinned, &t.Archived,
		&t.Summary, &t.SummaryUntilPos,
		&t.AgentID, &t.AgentChain, &t.ParentThreadID, &t.SyncEnabled,
		&t.Metadata, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return t, nil
}

func (s *Store) GetThread(ctx context.Context, userID, threadID uuid.UUID) (*Thread, error) {
	t := &Thread{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, project_id, title, last_msg_preview,
		       model, system_prompt, pinned, archived,
		       summary, summary_until_position,
		       agent_id, agent_chain, parent_thread_id, sync_enabled,
		       metadata, created_at, updated_at
		FROM chat.threads
		WHERE id = $1 AND user_id = $2
	`, threadID, userID).Scan(
		&t.ID, &t.UserID, &t.ProjectID, &t.Title, &t.LastMsgPreview,
		&t.Model, &t.SystemPrompt, &t.Pinned, &t.Archived,
		&t.Summary, &t.SummaryUntilPos,
		&t.AgentID, &t.AgentChain, &t.ParentThreadID, &t.SyncEnabled,
		&t.Metadata, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

type ListThreadsInput struct {
	UserID   uuid.UUID
	Archived *bool // nil = both; true/false to filter
	// Cursor-based pagination using updated_at + id (id breaks ties).
	BeforeUpdatedAt *time.Time
	// UpdatedAfter restricts to threads with updated_at > value —
	// incremental pulls for cross-device sync. Archived threads are
	// included (filter explicitly via Archived if unwanted); deleted
	// threads are hard-deleted and simply absent.
	UpdatedAfter *time.Time
	Limit        int
}

// ListThreads returns the user's threads sorted by updated_at desc,
// with pinned ones surfaced first if PinnedFirst is set in metadata.
// Cursor: pass the smallest updated_at from the previous page as
// BeforeUpdatedAt. Limit defaults to 50, max 200.
func (s *Store) ListThreads(ctx context.Context, in ListThreadsInput) ([]*Thread, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	q := strings.Builder{}
	q.WriteString(`
		SELECT id, user_id, project_id, title, last_msg_preview,
		       model, system_prompt, pinned, archived,
		       summary, summary_until_position,
		       agent_id, agent_chain, parent_thread_id, sync_enabled,
		       metadata, created_at, updated_at
		FROM chat.threads
		WHERE user_id = $1
	`)
	args := []any{in.UserID}
	if in.Archived != nil {
		args = append(args, *in.Archived)
		fmt.Fprintf(&q, " AND archived = $%d", len(args))
	}
	if in.BeforeUpdatedAt != nil {
		args = append(args, *in.BeforeUpdatedAt)
		fmt.Fprintf(&q, " AND updated_at < $%d", len(args))
	}
	if in.UpdatedAfter != nil {
		args = append(args, *in.UpdatedAfter)
		fmt.Fprintf(&q, " AND updated_at > $%d", len(args))
	}
	args = append(args, limit)
	fmt.Fprintf(&q, " ORDER BY pinned DESC, updated_at DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Thread
	for rows.Next() {
		t := &Thread{}
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.ProjectID, &t.Title, &t.LastMsgPreview,
			&t.Model, &t.SystemPrompt, &t.Pinned, &t.Archived,
			&t.Summary, &t.SummaryUntilPos,
			&t.AgentID, &t.AgentChain, &t.ParentThreadID, &t.SyncEnabled,
			&t.Metadata, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type UpdateThreadInput struct {
	UserID       uuid.UUID
	ThreadID     uuid.UUID
	Title        *string
	Model        *string
	SystemPrompt *string
	Pinned       *bool
	Archived     *bool
	AgentID      *uuid.UUID
	SyncEnabled  *bool
	// ModelParams replaces metadata.model_params wholesale (not a
	// deep merge). Pass an empty struct via &json.RawMessage("{}")
	// to clear it. nil = leave the existing value alone.
	ModelParams json.RawMessage
}

func (s *Store) UpdateThread(ctx context.Context, in UpdateThreadInput) (*Thread, error) {
	// Build dynamic SET clause.
	sets := []string{}
	args := []any{in.ThreadID, in.UserID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Title != nil {
		add("title", *in.Title)
	}
	if in.Model != nil {
		add("model", *in.Model)
	}
	if in.SystemPrompt != nil {
		add("system_prompt", *in.SystemPrompt)
	}
	if in.Pinned != nil {
		add("pinned", *in.Pinned)
	}
	if in.Archived != nil {
		add("archived", *in.Archived)
	}
	if in.AgentID != nil {
		add("agent_id", *in.AgentID)
	}
	if in.SyncEnabled != nil {
		add("sync_enabled", *in.SyncEnabled)
	}
	if in.ModelParams != nil {
		// Merge into the metadata jsonb without touching other
		// metadata keys: jsonb_set replaces (or creates) just
		// `metadata.model_params`. Postgres jsonb_set takes a path
		// array as text[]; we hard-code it to {model_params}.
		args = append(args, []byte(in.ModelParams))
		sets = append(sets,
			fmt.Sprintf("metadata = jsonb_set(coalesce(metadata, '{}'::jsonb), "+
				"'{model_params}', $%d::jsonb, true)", len(args)))
	}
	if len(sets) == 0 {
		// No-op: just return the current state.
		return s.GetThread(ctx, in.UserID, in.ThreadID)
	}
	sets = append(sets, "updated_at = now()")
	q := fmt.Sprintf(`
		UPDATE chat.threads SET %s
		WHERE id = $1 AND user_id = $2
		RETURNING id, user_id, project_id, title, last_msg_preview,
		          model, system_prompt, pinned, archived,
		          summary, summary_until_position,
		          agent_id, agent_chain, parent_thread_id, sync_enabled,
		          metadata, created_at, updated_at
	`, strings.Join(sets, ", "))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	t := &Thread{}
	err = tx.QueryRow(ctx, q, args...).Scan(
		&t.ID, &t.UserID, &t.ProjectID, &t.Title, &t.LastMsgPreview,
		&t.Model, &t.SystemPrompt, &t.Pinned, &t.Archived,
		&t.Summary, &t.SummaryUntilPos,
		&t.AgentID, &t.AgentChain, &t.ParentThreadID, &t.SyncEnabled,
		&t.Metadata, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Same-tx outbox event so other devices learn the metadata change.
	if t.SyncEnabled {
		if err := emitEvent(ctx, tx, t.UserID, EventThreadUpdated, map[string]any{
			"thread_id": t.ID.String(),
		}); err != nil {
			return nil, fmt.Errorf("thread event: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) DeleteThread(ctx context.Context, userID, threadID uuid.UUID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Collect message ids before the CASCADE wipes them — each one gets
	// its own tombstone so offline devices can converge per-message.
	msgRows, err := tx.Query(ctx, `
		SELECT id FROM chat.messages WHERE thread_id = $1 AND user_id = $2
	`, threadID, userID)
	if err != nil {
		return err
	}
	msgIDs, err := pgx.CollectRows(msgRows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}

	var syncEnabled bool
	err = tx.QueryRow(ctx, `
		DELETE FROM chat.threads WHERE id = $1 AND user_id = $2
		RETURNING sync_enabled
	`, threadID, userID).Scan(&syncEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	// Same-tx tombstones: online devices get the outbox event below,
	// offline devices learn "deleted" (vs "never uploaded") via
	// GET /v1/chat/tombstones on their next sync.
	if err := recordTombstonesTx(ctx, tx, userID, "thread",
		[]uuid.UUID{threadID}); err != nil {
		return err
	}
	if err := recordTombstonesTx(ctx, tx, userID, "message", msgIDs); err != nil {
		return err
	}
	if err := pruneTombstonesTx(ctx, tx); err != nil {
		return err
	}

	// Same-tx outbox event so other devices drop the thread.
	if syncEnabled {
		if err := emitEvent(ctx, tx, userID, EventThreadDeleted, map[string]any{
			"thread_id": threadID.String(),
		}); err != nil {
			return fmt.Errorf("thread event: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// SetSummary updates the long-context summary cache. Used by the
// summarizer task once token threshold is hit.
func (s *Store) SetSummary(ctx context.Context, threadID uuid.UUID,
	summary string, untilPos int64,
) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE chat.threads
		   SET summary = $2, summary_until_position = $3, updated_at = now()
		 WHERE id = $1
	`, threadID, summary, untilPos)
	return err
}

// ─── Messages ─────────────────────────────────────────

type CreateMessageInput struct {
	// ID 可选：client 生成并透传的 message uuid。非空 → INSERT 用之作 PK
	// （方案3：本地 message.id == brain chat.messages.id，编辑/删除上行直连）。
	// 空 → 走列默认 gen_random_uuid（向后兼容旧 client / brain 内部落库）。
	ID             *uuid.UUID
	ThreadID       uuid.UUID
	UserID         uuid.UUID
	Role           string
	Content        string
	Parts          []byte // raw JSON; may be empty
	ToolCallID     *string
	ParentID       *uuid.UUID
	Model          *string
	Status         string // defaults to StatusSuccess
	ClientID       *string
	AgentID        *uuid.UUID
	MessageGroupID *uuid.UUID
}

// CreateMessage inserts a new message. Honors client_id dedup: a
// duplicate within the same thread returns the existing row (no error)
// so retries are idempotent.
func (s *Store) CreateMessage(ctx context.Context, in CreateMessageInput) (*Message, error) {
	if !validRole(in.Role) {
		return nil, fmt.Errorf("%w: invalid role %q", ErrInvalid, in.Role)
	}
	if in.ThreadID == uuid.Nil || in.UserID == uuid.Nil {
		return nil, fmt.Errorf("%w: thread_id and user_id required", ErrInvalid)
	}
	status := in.Status
	if status == "" {
		status = StatusSuccess
	}
	parts := in.Parts
	if len(parts) == 0 {
		parts = []byte("[]")
	}

	// First try insert; on dedup collision, fetch existing. The insert
	// runs in a tx so the cross-device sync event (brain.events) commits
	// atomically with the message row (transactional outbox).
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	m := &Message{}
	// 动态拼 INSERT 列：client 透传 in.ID 时显式插 id 列（方案3，本地 id=brain
	// id）；否则不插 id 列走 DEFAULT gen_random_uuid（向后兼容）。
	cols := []string{
		"thread_id", "user_id", "role", "content", "parts",
		"tool_call_id", "parent_id", "model", "status", "client_id",
		"agent_id", "message_group_id",
	}
	args := []any{
		in.ThreadID, in.UserID, in.Role, in.Content, parts,
		in.ToolCallID, in.ParentID, in.Model, status, in.ClientID,
		in.AgentID, in.MessageGroupID,
	}
	if in.ID != nil {
		cols = append([]string{"id"}, cols...)
		args = append([]any{*in.ID}, args...)
	}
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO chat.messages (%s)
		VALUES (%s)
		RETURNING id, thread_id, user_id, role, content, parts,
		          tool_call_id, parent_id, model,
		          prompt_tokens, completion_tokens,
		          status, error, client_id,
		          agent_id, message_group_id,
		          position, created_at, updated_at
	`, strings.Join(cols, ", "), strings.Join(placeholders, ", ")), args...,
	).Scan(
		&m.ID, &m.ThreadID, &m.UserID, &m.Role, &m.Content, &m.Parts,
		&m.ToolCallID, &m.ParentID, &m.Model,
		&m.PromptTokens, &m.CompletionTokens,
		&m.Status, &m.ErrorMsg, &m.ClientID,
		&m.AgentID, &m.MessageGroupID,
		&m.Position, &m.CreatedAt, &m.UpdatedAt,
	)
	if err == nil {
		if err := emitMessageCreatedTx(ctx, tx, m); err != nil {
			return nil, fmt.Errorf("message event: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return m, nil
	}
	// Detect unique-violation on (thread_id, client_id): return existing.
	if in.ClientID != nil && isUniqueViolation(err) {
		row := s.pool.QueryRow(ctx, `
			SELECT id, thread_id, user_id, role, content, parts,
			       tool_call_id, parent_id, model,
			       prompt_tokens, completion_tokens,
			       status, error, client_id,
			       agent_id, message_group_id,
			       position, created_at, updated_at
			FROM chat.messages
			WHERE thread_id = $1 AND client_id = $2
		`, in.ThreadID, *in.ClientID)
		ex := &Message{}
		if e := row.Scan(
			&ex.ID, &ex.ThreadID, &ex.UserID, &ex.Role, &ex.Content, &ex.Parts,
			&ex.ToolCallID, &ex.ParentID, &ex.Model,
			&ex.PromptTokens, &ex.CompletionTokens,
			&ex.Status, &ex.ErrorMsg, &ex.ClientID,
			&ex.AgentID, &ex.MessageGroupID,
			&ex.Position, &ex.CreatedAt, &ex.UpdatedAt,
		); e == nil {
			return ex, nil
		}
	}
	return nil, fmt.Errorf("create message: %w", err)
}

type ListMessagesInput struct {
	ThreadID uuid.UUID
	UserID   uuid.UUID
	// Pagination by position (more reliable than created_at on
	// concurrent inserts).
	AfterPosition  *int64 // > position
	BeforePosition *int64 // < position
	Limit          int
}

func (s *Store) ListMessages(ctx context.Context, in ListMessagesInput) ([]*Message, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := strings.Builder{}
	q.WriteString(`
		SELECT id, thread_id, user_id, role, content, parts,
		       tool_call_id, parent_id, model,
		       prompt_tokens, completion_tokens,
		       status, error, client_id,
		       agent_id, message_group_id,
		       position, created_at, updated_at
		FROM chat.messages
		WHERE thread_id = $1 AND user_id = $2
	`)
	args := []any{in.ThreadID, in.UserID}
	if in.AfterPosition != nil {
		args = append(args, *in.AfterPosition)
		fmt.Fprintf(&q, " AND position > $%d", len(args))
	}
	if in.BeforePosition != nil {
		args = append(args, *in.BeforePosition)
		fmt.Fprintf(&q, " AND position < $%d", len(args))
	}
	args = append(args, limit)
	fmt.Fprintf(&q, " ORDER BY position ASC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(
			&m.ID, &m.ThreadID, &m.UserID, &m.Role, &m.Content, &m.Parts,
			&m.ToolCallID, &m.ParentID, &m.Model,
			&m.PromptTokens, &m.CompletionTokens,
			&m.Status, &m.ErrorMsg, &m.ClientID,
			&m.AgentID, &m.MessageGroupID,
			&m.Position, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMessage(ctx context.Context, userID, msgID uuid.UUID) (*Message, error) {
	m := &Message{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, thread_id, user_id, role, content, parts,
		       tool_call_id, parent_id, model,
		       prompt_tokens, completion_tokens,
		       status, error, client_id,
		       agent_id, message_group_id,
		       position, created_at, updated_at
		FROM chat.messages
		WHERE id = $1 AND user_id = $2
	`, msgID, userID).Scan(
		&m.ID, &m.ThreadID, &m.UserID, &m.Role, &m.Content, &m.Parts,
		&m.ToolCallID, &m.ParentID, &m.Model,
		&m.PromptTokens, &m.CompletionTokens,
		&m.Status, &m.ErrorMsg, &m.ClientID,
		&m.AgentID, &m.MessageGroupID,
		&m.Position, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ListSiblings returns assistant messages that share the same
// parent_id as the given message — i.e. the regeneration siblings.
// Ordered by position ASC so callers can pick "the latest" with the
// last element. Returns the full set including the input message.
//
// owner-scoping: results are filtered by the userID; cross-tenant
// reads behave like ErrNotFound (zero-length slice).
func (s *Store) ListSiblings(ctx context.Context, userID, parentID uuid.UUID) ([]*Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, thread_id, user_id, role, content, parts,
		       tool_call_id, parent_id, model,
		       prompt_tokens, completion_tokens,
		       status, error, client_id,
		       agent_id, message_group_id,
		       position, created_at, updated_at
		FROM chat.messages
		WHERE user_id = $1
		  AND parent_id = $2
		  AND role = 'assistant'
		ORDER BY position ASC
	`, userID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(
			&m.ID, &m.ThreadID, &m.UserID, &m.Role, &m.Content, &m.Parts,
			&m.ToolCallID, &m.ParentID, &m.Model,
			&m.PromptTokens, &m.CompletionTokens,
			&m.Status, &m.ErrorMsg, &m.ClientID,
			&m.AgentID, &m.MessageGroupID,
			&m.Position, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMessageInput collects all the fields that change during +
// after streaming. Pointers everywhere because partial updates are
// the norm (e.g. setting status=success without touching content).
type UpdateMessageInput struct {
	UserID           uuid.UUID
	MessageID        uuid.UUID
	Content          *string
	Parts            []byte // empty → no change
	Status           *string
	ErrorMsg         *string
	PromptTokens     *int
	CompletionTokens *int
}

func (s *Store) UpdateMessage(ctx context.Context, in UpdateMessageInput) (*Message, error) {
	sets := []string{}
	args := []any{in.MessageID, in.UserID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Content != nil {
		add("content", *in.Content)
	}
	if len(in.Parts) > 0 {
		add("parts", in.Parts)
	}
	if in.Status != nil {
		add("status", *in.Status)
	}
	if in.ErrorMsg != nil {
		add("error", *in.ErrorMsg)
	}
	if in.PromptTokens != nil {
		add("prompt_tokens", *in.PromptTokens)
	}
	if in.CompletionTokens != nil {
		add("completion_tokens", *in.CompletionTokens)
	}
	if len(sets) == 0 {
		return s.GetMessage(ctx, in.UserID, in.MessageID)
	}
	sets = append(sets, "updated_at = now()")
	q := fmt.Sprintf(`
		UPDATE chat.messages SET %s
		WHERE id = $1 AND user_id = $2
		RETURNING id, thread_id, user_id, role, content, parts,
		          tool_call_id, parent_id, model,
		          prompt_tokens, completion_tokens,
		          status, error, client_id,
		          agent_id, message_group_id,
		          position, created_at, updated_at
	`, strings.Join(sets, ", "))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	m := &Message{}
	err = tx.QueryRow(ctx, q, args...).Scan(
		&m.ID, &m.ThreadID, &m.UserID, &m.Role, &m.Content, &m.Parts,
		&m.ToolCallID, &m.ParentID, &m.Model,
		&m.PromptTokens, &m.CompletionTokens,
		&m.Status, &m.ErrorMsg, &m.ClientID,
		&m.AgentID, &m.MessageGroupID,
		&m.Position, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// A transition into a terminal status is what announces a completed
	// message (e.g. the streaming placeholder's final UPDATE) — emit the
	// sync event in the same tx. Content/token-only edits stay silent.
	if in.Status != nil && terminalStatus(*in.Status) {
		if err := emitMessageCreatedTx(ctx, tx, m); err != nil {
			return nil, fmt.Errorf("message event: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) DeleteMessage(ctx context.Context, userID, msgID uuid.UUID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var threadID uuid.UUID
	err = tx.QueryRow(ctx, `
		DELETE FROM chat.messages WHERE id = $1 AND user_id = $2
		RETURNING thread_id
	`, msgID, userID).Scan(&threadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	// thread 仍在（删的是 message）—— 查 sync_enabled 决定是否同 tx 发事件，
	// 让他端按服务端为准删本地副本（镜像 DeleteThread）。
	var syncEnabled bool
	if err := tx.QueryRow(ctx,
		`SELECT sync_enabled FROM chat.threads WHERE id = $1`,
		threadID).Scan(&syncEnabled); err != nil {
		return err
	}
	if syncEnabled {
		if err := emitEvent(ctx, tx, userID, EventMessageDeleted, map[string]any{
			"thread_id":  threadID.String(),
			"message_id": msgID.String(),
		}); err != nil {
			return fmt.Errorf("message event: %w", err)
		}
	}
	// Same-tx tombstone so offline devices learn this message is gone
	// (mirror DeleteThread).
	if err := recordTombstonesTx(ctx, tx, userID, "message",
		[]uuid.UUID{msgID}); err != nil {
		return err
	}
	if err := pruneTombstonesTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CleanupOrphanStreaming marks any message stuck in `streaming` for
// more than `staleAfter` as failed. Run on Brain startup and as a
// periodic job — covers the case of a service crash mid-stream.
func (s *Store) CleanupOrphanStreaming(ctx context.Context, staleAfter time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE chat.messages
		   SET status = $1,
		       error = 'orphaned: server crashed mid-stream',
		       updated_at = now()
		 WHERE status = $2
		   AND updated_at < now() - $3::interval
	`, StatusError, StatusStreaming, staleAfter)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ─── Tombstones ───────────────────────────────────────

// Tombstone marks a hard-deleted thread/message so offline devices can
// tell "deleted elsewhere" apart from "never uploaded" and converge
// (design doc BiuMind-Local-Data-Isolation-Design.md §4.1). Rows are
// retained 30 days and pruned lazily on the write path.
type Tombstone struct {
	ID        uuid.UUID `db:"id"`
	Kind      string    `db:"kind"` // "thread" | "message"
	UserID    uuid.UUID `db:"user_id"`
	DeletedAt time.Time `db:"deleted_at"`
}

// ListTombstones returns the user's tombstones with deleted_at > since,
// oldest first, capped at limit.
func (s *Store) ListTombstones(ctx context.Context, userID uuid.UUID,
	since time.Time, limit int,
) ([]Tombstone, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, user_id, deleted_at
		  FROM chat.tombstones
		 WHERE user_id = $1 AND deleted_at > $2
		 ORDER BY deleted_at ASC, kind ASC, id ASC
		 LIMIT $3
	`, userID, since, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[Tombstone])
	if err != nil {
		return nil, err
	}
	return out, nil
}

// recordTombstonesTx writes tombstones for the given ids in the current
// tx. ON CONFLICT keeps it idempotent if a delete is ever replayed.
func recordTombstonesTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID,
	kind string, ids []uuid.UUID,
) error {
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `
			INSERT INTO chat.tombstones (kind, id, user_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (kind, id) DO NOTHING
		`, kind, id, userID); err != nil {
			return fmt.Errorf("tombstone: %w", err)
		}
	}
	return nil
}

// pruneTombstonesTx lazily enforces the 30-day retention window; called
// on every tombstone-writing delete so no separate janitor is needed.
func pruneTombstonesTx(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM chat.tombstones
		 WHERE deleted_at < now() - interval '30 days'
	`); err != nil {
		return fmt.Errorf("tombstone gc: %w", err)
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────

// PartsJSON marshals a parts slice for storage. Caller-side helper
// since the API decodes JSON itself, but tests + unit code sometimes
// build parts in Go.
func PartsJSON(parts any) ([]byte, error) {
	if parts == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(parts)
}

// pgx unique-violation is a SQLSTATE 23505. We don't want to import
// the pgconn package across the package boundary, so we sniff the
// error string instead.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "23505") ||
		strings.Contains(err.Error(), "duplicate key")
}
