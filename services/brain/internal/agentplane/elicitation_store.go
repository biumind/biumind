// agent_elicitations store（P3-c durable resume）—— elicitation 提问的
// 持久化读写 + chat 僵尸 session 的 boot sweep。
//
// 写入方唯一入口是 ElicitationObserver（Queue.PublishSessionFrame 的
// FrameObserver 链）：control_request{subtype:elicitation, mode:form} 帧
// 落 pending 行；form_answer 终态帧 CAS 置 answered。迟到作答（loop 已死）
// 由 ingress.maybeRoutePermissionResponse 第二级走 ResolveElicitationCAS
// 原子认领 —— CAS 的 WHERE status='pending' 防 replay×sweep×resume 三方
// 并发双写。
//
// 设计：docs/BiuMind-Agent-Experience-Design.md §6.3。

package agentplane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ElicitationTTL 是 elicitation 行的存活窗口：创建后 7 天未答 → janitor
// 置 expired。durable 后表单等待以天计（设计 §6.3 时间窗重定；30min
// session_token 由 refresh 链续期，不限制作答窗口）。
const ElicitationTTL = 7 * 24 * time.Hour

// Elicitation 是 agent_elicitations 行的 Go 表示。
type Elicitation struct {
	RequestID  uuid.UUID
	SessionID  uuid.UUID
	ThreadID   *uuid.UUID
	UserID     uuid.UUID
	Payload    []byte // JSONB {question, header, multi_select, options}
	Status     string
	Answer     []byte // JSONB {action, content}；未答为 nil
	CreatedAt  time.Time
	AnsweredAt *time.Time
	ConsumedAt *time.Time
	ExpiresAt  time.Time
}

// InsertElicitation 落一条 pending 提问。同事务把该 session 旧的 pending
// 行批量作废（status='cancelled'）—— daemon work redeliver 重跑会用新
// request_id 重新提问，不作废则新旧两张表单并存（拍板 §6.3-5）。
//
// session 元数据（thread_id/user_id）经 INSERT...SELECT 从 agent_sessions
// 一次带入，调用方只需给 session_id。session 不存在 → ErrNotFound（0 行
// 插入）。request_id 冲突（同帧 replay / 重发）→ ON CONFLICT 作废语义：
// 不顶行、不报错（帧至少经 NATS 投递一次，replay 合法）。
func (s *Store) InsertElicitation(ctx context.Context, requestID, sessionID uuid.UUID, payload []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("insert elicitation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE agent_elicitations
		   SET status = 'cancelled'
		 WHERE session_id = $1 AND status = 'pending'
	`, sessionID); err != nil {
		return fmt.Errorf("insert elicitation: cancel stale pending: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO agent_elicitations
			(request_id, session_id, thread_id, user_id, payload, expires_at)
		SELECT $1, s.session_id, s.thread_id, s.user_id, $3, $4
		  FROM agent_sessions s
		 WHERE s.session_id = $2
		ON CONFLICT (request_id) DO NOTHING
	`, requestID, sessionID, payload, time.Now().Add(ElicitationTTL))
	if err != nil {
		return fmt.Errorf("insert elicitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// ON CONFLICT 命中（replay）与 session 不存在都是 0 行；分开判别 —
		// replay 合法静默过，session 不存在是调用方 bug / 数据错乱。
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM agent_elicitations WHERE request_id = $1)`,
			requestID).Scan(&exists); err != nil {
			return fmt.Errorf("insert elicitation: check conflict: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return tx.Commit(ctx)
}

// ResolveElicitationCAS 原子认领一条 pending 提问：pending → answered。
// 命中返回 (sessionID, payload, true)；行不存在或已被认领（replay / 并发
// resume / janitor 已动过）→ ok=false 无错误。payload 带回提问快照，
// 调用方（ingress 迟到作答路径）据此补发 form_answer 帧。
func (s *Store) ResolveElicitationCAS(ctx context.Context, requestID uuid.UUID, answer []byte) (sessionID uuid.UUID, payload []byte, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `
		UPDATE agent_elicitations
		   SET status = 'answered', answer = $2, answered_at = now()
		 WHERE request_id = $1 AND status = 'pending'
		RETURNING session_id, payload
	`, requestID, answer).Scan(&sessionID, &payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil, false, nil
		}
		return uuid.Nil, nil, false, fmt.Errorf("resolve elicitation CAS: %w", err)
	}
	return sessionID, payload, true, nil
}

// GetElicitation 按 request_id 取一行（observer 落 form 行进 chat.messages
// 时取 session 元数据）。不存在 → ErrNotFound。
func (s *Store) GetElicitation(ctx context.Context, requestID uuid.UUID) (*Elicitation, error) {
	const q = `
		SELECT request_id, session_id, thread_id, user_id, payload, status,
		       answer, created_at, answered_at, consumed_at, expires_at
		  FROM agent_elicitations
		 WHERE request_id = $1
	`
	var e Elicitation
	err := s.pool.QueryRow(ctx, q, requestID).Scan(
		&e.RequestID, &e.SessionID, &e.ThreadID, &e.UserID, &e.Payload, &e.Status,
		&e.Answer, &e.CreatedAt, &e.AnsweredAt, &e.ConsumedAt, &e.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get elicitation: %w", err)
	}
	return &e, nil
}

// ListPendingElicitations 列出 session 的待答提问（GET elicitations 端点 +
// resume 的 409 提示）。按创建时间升序（先问的先答）。
func (s *Store) ListPendingElicitations(ctx context.Context, sessionID uuid.UUID) ([]Elicitation, error) {
	return s.listElicitations(ctx, `
		SELECT request_id, session_id, thread_id, user_id, payload, status,
		       answer, created_at, answered_at, consumed_at, expires_at
		  FROM agent_elicitations
		 WHERE session_id = $1 AND status = 'pending'
		 ORDER BY created_at ASC
	`, sessionID)
}

// ListUnconsumedAnswers 列出 session 已答但未被 resume 注入过的答案
// （resume 端点组 synthetic user 消息用）。
func (s *Store) ListUnconsumedAnswers(ctx context.Context, sessionID uuid.UUID) ([]Elicitation, error) {
	return s.listElicitations(ctx, `
		SELECT request_id, session_id, thread_id, user_id, payload, status,
		       answer, created_at, answered_at, consumed_at, expires_at
		  FROM agent_elicitations
		 WHERE session_id = $1 AND status = 'answered' AND consumed_at IS NULL
		 ORDER BY created_at ASC
	`, sessionID)
}

func (s *Store) listElicitations(ctx context.Context, q string, sessionID uuid.UUID) ([]Elicitation, error) {
	rows, err := s.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list elicitations: %w", err)
	}
	defer rows.Close()
	var out []Elicitation
	for rows.Next() {
		var e Elicitation
		if err := rows.Scan(
			&e.RequestID, &e.SessionID, &e.ThreadID, &e.UserID, &e.Payload, &e.Status,
			&e.Answer, &e.CreatedAt, &e.AnsweredAt, &e.ConsumedAt, &e.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan elicitation: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkAnswersConsumed 把 session 所有已答未消费的答案打上 consumed_at
// （resume 注入 synthetic user 消息后调，防下次 resume 重复注入）。
func (s *Store) MarkAnswersConsumed(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agent_elicitations
		   SET consumed_at = now()
		 WHERE session_id = $1 AND status = 'answered' AND consumed_at IS NULL
	`, sessionID)
	if err != nil {
		return fmt.Errorf("mark answers consumed: %w", err)
	}
	return nil
}

// PauseZombieChatSessions 是 brain 启动时的 boot sweep（拍板 §6.3-3）：
// 所有 environment_id IS NULL（chat 模式，loop 在 brain 进程内）且还卡在
// active 的 session 置 paused —— brain 重启即这些 loop 已死，无需猜时间窗。
// daemon 模式（environment_id 非空）不动：work redeliver 已天然重跑重问。
// 返回置 paused 的行数。启动时同步调一次（HTTP 开始服务前），天然幂等。
func (s *Store) PauseZombieChatSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_sessions
		   SET state = 'paused', updated_at = now()
		 WHERE environment_id IS NULL AND state = 'active'
	`)
	if err != nil {
		return 0, fmt.Errorf("pause zombie chat sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ResumeSessionCAS 把 paused session 原子翻回 active（resume 端点的并发
// 守卫：两个并发 resume 只有第一个成功）。返回是否翻牌成功。
func (s *Store) ResumeSessionCAS(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_sessions
		   SET state = 'active', updated_at = now()
		 WHERE session_id = $1 AND state = 'paused'
	`, sessionID)
	if err != nil {
		return false, fmt.Errorf("resume session CAS: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// GetSessionResult 取 task 模式最终态摘要（/v1/agent/sessions/{id}/result
// 端点）。无结果行（chat/agent 模式不写结果表 / 未 finalize）→ ErrNotFound。
func (s *Store) GetSessionResult(ctx context.Context, sessionID uuid.UUID) (*SessionResult, error) {
	const q = `
		SELECT session_id, status, COALESCE(final_text, ''), final_parts,
		       tool_calls_summary, COALESCE(cost_usd, 0), COALESCE(prompt_tokens, 0),
		       COALESCE(completion_tokens, 0), COALESCE(duration_ms, 0),
		       COALESCE(error_message, '')
		  FROM agent_session_results
		 WHERE session_id = $1
	`
	var r SessionResult
	err := s.pool.QueryRow(ctx, q, sessionID).Scan(
		&r.SessionID, &r.Status, &r.FinalText, &r.FinalParts, &r.ToolCallsSummary,
		&r.CostUSD, &r.PromptTokens, &r.CompletionTokens, &r.DurationMs, &r.ErrorMessage,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get session result: %w", err)
	}
	return &r, nil
}
