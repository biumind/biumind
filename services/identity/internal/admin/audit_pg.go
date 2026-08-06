// audit_pg — Postgres 持久化的 audit log 实现.
//
// schema 在 services/identity/migrations/00003_rbac.sql 已建好:
//   audit.events (id, at, actor_id, actor_email, actor_role, actor_ip,
//                 actor_ua, action, resource, target_id, target_type,
//                 detail jsonb, success, error_code, error_message)
//
// 当前 admin.AuditEvent 字段较少 (At/ActorID/Action/Target/Detail), 大部分
// 字段留 NULL. 后续 commit 扩 AuditEvent 加 actor_email/actor_ip 等.

package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgAudit struct {
	pool *pgxpool.Pool
}

// NewPGAudit 构造 PG audit store.
func NewPGAudit(pool *pgxpool.Pool) *pgAudit {
	return &pgAudit{pool: pool}
}

// Append 写入 audit.events. detail 字段当前是 string (FreeText),
// 用 to_jsonb 包成 JSONB 存. 后续可以改成结构化 jsonb.
func (p *pgAudit) Append(ev AuditEvent) error {
	ctx, cancel := contextWithTimeout(2)
	defer cancel()

	var actorUUID *uuid.UUID
	if ev.ActorID != "" {
		if u, err := uuid.Parse(ev.ActorID); err == nil {
			actorUUID = &u
		}
	}

	// inet 列对非 IP 字符串会抛错; 拿不到合法 IP 时传 NULL.
	var actorIP any
	if ev.ActorIP != "" {
		actorIP = ev.ActorIP
	}

	_, err := p.pool.Exec(ctx, `
		INSERT INTO audit.events
		    (at, actor_id, actor_email, actor_role, actor_ip, actor_ua,
		     action, resource, target_id, target_type, detail,
		     success, error_code, error_message)
		VALUES (
			COALESCE(NULLIF($1, '0001-01-01 00:00:00+00'::timestamptz), now()),
			$2, NULLIF($3, ''), NULLIF($4, ''), $5::inet, NULLIF($6, ''),
			$7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''),
			CASE WHEN $11::text = '' THEN NULL ELSE to_jsonb($11::text) END,
			$12, NULLIF($13, ''), NULLIF($14, '')
		)
	`,
		ev.At, actorUUID, ev.ActorEmail, ev.ActorRole, actorIP, ev.ActorUA,
		ev.Action, ev.Resource, ev.Target, ev.TargetType, ev.Detail,
		ev.Success, ev.ErrorCode, ev.ErrorMessage,
	)
	return err
}

// Recent 返回最近 n 条 audit, 倒序 (最新在前). 跟 *AuditLog.Recent 行为一致.
func (p *pgAudit) Recent(n int) ([]AuditEvent, error) {
	if n <= 0 {
		n = 100
	}
	ctx, cancel := contextWithTimeout(5)
	defer cancel()

	rows, err := p.pool.Query(ctx, `
		SELECT at,
		       COALESCE(actor_id::text, '')    AS actor_id,
		       COALESCE(actor_email, '')       AS actor_email,
		       COALESCE(actor_role, '')        AS actor_role,
		       COALESCE(host(actor_ip), '')    AS actor_ip,
		       COALESCE(actor_ua, '')          AS actor_ua,
		       action,
		       COALESCE(resource, '')          AS resource,
		       COALESCE(target_id, '')         AS target,
		       COALESCE(target_type, '')       AS target_type,
		       COALESCE(detail #>> '{}', '')   AS detail,
		       success,
		       COALESCE(error_code, '')        AS error_code,
		       COALESCE(error_message, '')     AS error_message
		FROM audit.events
		ORDER BY at DESC
		LIMIT $1
	`, n)
	if err != nil {
		return nil, fmt.Errorf("audit.events query: %w", err)
	}
	defer rows.Close()

	var out []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		if err := rows.Scan(
			&ev.At,
			&ev.ActorID, &ev.ActorEmail, &ev.ActorRole, &ev.ActorIP, &ev.ActorUA,
			&ev.Action, &ev.Resource, &ev.Target, &ev.TargetType, &ev.Detail,
			&ev.Success, &ev.ErrorCode, &ev.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("audit.events scan: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit.events rows: %w", err)
	}
	return out, nil
}

// AuditSummary 是 dashboard 卡片的预聚合数据. 一次 SQL 拉一行,
// 避免前端连发 6 个 count 查询.
type AuditSummary struct {
	WindowSeconds       int   `json:"window_seconds"`
	FailedLogins        int64 `json:"failed_logins"`
	BruteForceHits      int64 `json:"brute_force_hits"`
	RoleChanges         int64 `json:"role_changes"`
	SystemConfigChanges int64 `json:"system_config_changes"`
	EmailVerifications  int64 `json:"email_verifications"`
	PasswordResets      int64 `json:"password_resets"`
	TotalEvents         int64 `json:"total_events"`
	TotalFailures       int64 `json:"total_failures"`
}

// Summary 按 window 聚合 audit.events. 用 FILTER 一次过, PG 在 audit_events_at_idx
// 上加 action 过滤是 cheap 的 (action 列基数低).
func (p *pgAudit) Summary(window time.Duration) (*AuditSummary, error) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	ctx, cancel := contextWithTimeout(5)
	defer cancel()

	var s AuditSummary
	s.WindowSeconds = int(window.Seconds())
	err := p.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE action='auth.login.failed')                     AS failed_logins,
		  COUNT(*) FILTER (WHERE action='auth.login.brute_force')                AS brute_force,
		  COUNT(*) FILTER (WHERE action LIKE 'admin.user.role%' OR action='roles.permissions.update') AS role_changes,
		  COUNT(*) FILTER (WHERE action='system.config.update' OR action='system.email_test') AS system_config,
		  COUNT(*) FILTER (WHERE action LIKE 'auth.email.%')                     AS email_verifications,
		  COUNT(*) FILTER (WHERE action LIKE 'auth.password_reset.%')            AS password_resets,
		  COUNT(*)                                                                AS total_events,
		  COUNT(*) FILTER (WHERE NOT success)                                    AS total_failures
		FROM audit.events
		WHERE at > now() - ($1 || ' seconds')::interval
	`, fmt.Sprintf("%d", s.WindowSeconds)).Scan(
		&s.FailedLogins, &s.BruteForceHits, &s.RoleChanges, &s.SystemConfigChanges,
		&s.EmailVerifications, &s.PasswordResets, &s.TotalEvents, &s.TotalFailures,
	)
	if err != nil {
		return nil, fmt.Errorf("audit.summary: %w", err)
	}
	return &s, nil
}

// 短超时, 避免 audit 慢拖业务.
func contextWithTimeout(seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}

// quiet — keep imports
var _ = errors.New
