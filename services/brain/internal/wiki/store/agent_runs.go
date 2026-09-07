package store

// agent_runs.go — wiki maintain agent run 持久化（迁移 00010，
// BiuMind-Agent-Experience-Design §1.2 P2）。
//
// 此前 run 状态只有内存 AgentRuns sync.Map（runID→cancel），dialog 一关
// 审计清单即丢。本文件把 run 落库：handleWikiAgentRun 开始写 running 行，
// 结束/失败/取消更新终态；page_revisions.run_id 把写前快照挂到 run 上，
// 撑「可回看历史」的两个 GET 端点。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AgentRun 终态。
const (
	AgentRunRunning   = "running"
	AgentRunDone      = "done"
	AgentRunFailed    = "failed"
	AgentRunCancelled = "cancelled"
)

// AgentRun —— brain.agent_runs 一行。ChangedPages 仅 ListAgentRuns 聚合填充。
type AgentRun struct {
	RunID        string
	ProjectID    uuid.UUID
	OwnerID      uuid.UUID
	Mode         string
	Model        string
	Instruction  string
	Status       string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Error        string
	ChangedPages int // 该 run 的快照涉及的去重页数（list 聚合）
}

// AgentRunChange —— run 详情的一行改动（= 该 run_id 的写前快照 join pages）。
// Op 是推断值：merge = 页已软删且带 merged_into 提示；其余快照一律 update
// （create 无写前快照，天然不进本清单——撤销新建走删页，客户端 run 内
// 事件流已有覆盖）。
type AgentRunChange struct {
	RevisionID uuid.UUID
	PageID     uuid.UUID
	Title      string // 快照留存的写前标题（页已删也能展示）
	Op         string // "update" | "merge"（推断）
	ChangeType string // page_revisions.change_type（'edit'/'restore'）
	CreatedAt  time.Time
}

// CreateAgentRun —— run 开始写 running 行。run_id 客户端生成，重复 id
// 直接报错（主键冲突），调用方（handleWikiAgentRun）视为 500。
func (s *Store) CreateAgentRun(ctx context.Context, run AgentRun) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO brain.agent_runs (run_id, project_id, owner_id, mode, model, instruction, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'running')
	`, run.RunID, run.ProjectID, run.OwnerID, run.Mode, run.Model, run.Instruction)
	if err != nil {
		return fmt.Errorf("create agent run: %w", err)
	}
	return nil
}

// FinishAgentRun —— 写终态（done/failed/cancelled）。WHERE status='running'
// 保证并发安全：cancel 端点先标 cancelled 后，loop 退出时的 done/failed
// 回写不会覆盖（cancel 语义优先）；重复 finish 幂等 no-op。
func (s *Store) FinishAgentRun(ctx context.Context, runID, status, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE brain.agent_runs
		   SET status = $2, finished_at = now(), error = NULLIF($3, '')
		 WHERE run_id = $1 AND status = 'running'
	`, runID, status, errMsg)
	if err != nil {
		return fmt.Errorf("finish agent run: %w", err)
	}
	return nil
}

// ListAgentRuns —— 项目 run 历史（新→旧），含每 run 改动页数聚合
// （page_revisions.run_id 去重页数；快照被 >512KB 跳过/窗口合并的页可能
// 漏计，见 AgentRunChange.Op 注释的诚实降级语义）。
func (s *Store) ListAgentRuns(ctx context.Context, projectID uuid.UUID, limit int) ([]*AgentRun, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.run_id, r.project_id, r.owner_id, r.mode, r.model, r.instruction,
		       r.status, r.started_at, r.finished_at, COALESCE(r.error, ''),
		       COALESCE(c.changed_pages, 0)
		  FROM brain.agent_runs r
		  LEFT JOIN (
		      SELECT run_id, COUNT(DISTINCT page_id) AS changed_pages
		        FROM brain.page_revisions
		       WHERE run_id IS NOT NULL
		       GROUP BY run_id
		  ) c ON c.run_id = r.run_id
		 WHERE r.project_id = $1
		 ORDER BY r.started_at DESC
		 LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentRun
	for rows.Next() {
		r := &AgentRun{}
		if err := rows.Scan(&r.RunID, &r.ProjectID, &r.OwnerID, &r.Mode, &r.Model,
			&r.Instruction, &r.Status, &r.StartedAt, &r.FinishedAt, &r.Error,
			&r.ChangedPages); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetAgentRun —— 单条 run（详情端点用；不存在 → ErrNotFound）。
func (s *Store) GetAgentRun(ctx context.Context, projectID uuid.UUID, runID string) (*AgentRun, error) {
	r := &AgentRun{}
	err := s.pool.QueryRow(ctx, `
		SELECT run_id, project_id, owner_id, mode, model, instruction,
		       status, started_at, finished_at, COALESCE(error, '')
		  FROM brain.agent_runs
		 WHERE run_id = $1 AND project_id = $2
	`, runID, projectID).Scan(
		&r.RunID, &r.ProjectID, &r.OwnerID, &r.Mode, &r.Model, &r.Instruction,
		&r.Status, &r.StartedAt, &r.FinishedAt, &r.Error,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListAgentRunChanges —— run 的改动页清单：该 run_id 的快照（写前态）按时间
// 正序，LEFT JOIN pages 推断操作类型（merge = 软删 + merged_into 提示）。
// 页被后续硬删时 pages 行不在，标题仍取快照留存的写前标题。
func (s *Store) ListAgentRunChanges(ctx context.Context, runID string) ([]*AgentRunChange, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.page_id, r.title, r.change_type, r.created_at,
		       (p.deleted_at IS NOT NULL AND p.frontmatter ? 'merged_into') AS merged
		  FROM brain.page_revisions r
		  LEFT JOIN brain.pages p ON p.id = r.page_id
		 WHERE r.run_id = $1
		 ORDER BY r.created_at ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentRunChange
	for rows.Next() {
		c := &AgentRunChange{}
		var merged *bool
		if err := rows.Scan(&c.RevisionID, &c.PageID, &c.Title, &c.ChangeType,
			&c.CreatedAt, &merged); err != nil {
			return nil, err
		}
		if merged != nil && *merged {
			c.Op = "merge"
		} else {
			c.Op = "update"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
