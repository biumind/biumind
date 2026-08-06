package store

// lineage.go — aigc.asset_lineage 仓储 (★ MVP 必做差异化能力).
//
// 一条边: child_sha 由 parent_sha 经 op 派生而来.
// op: remix | edit | inpaint | upscale | i2v | extract_frame |
//     style_transfer | first_frame | reference | cache_hit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AssetLineage 是血缘 DAG 的一条边.
type AssetLineage struct {
	ChildSHA    string
	ParentSHA   string
	Op          string
	OpParams    []byte // raw jsonb
	ChildTaskID *uuid.UUID
	CreatedAt   time.Time
}

const lineageColumns = `child_sha, parent_sha, op, op_params, child_task_id, created_at`

func scanLineage(r scanner) (*AssetLineage, error) {
	l := &AssetLineage{}
	err := r.Scan(&l.ChildSHA, &l.ParentSHA, &l.Op, &l.OpParams, &l.ChildTaskID, &l.CreatedAt)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// AddLineageEdge 添加一条血缘边. (child_sha, parent_sha, op) 唯一,
// 重复插入 ON CONFLICT DO NOTHING 静默幂等.
type AddLineageEdgeArgs struct {
	ChildSHA    string
	ParentSHA   string
	Op          string
	OpParams    any
	ChildTaskID *uuid.UUID
}

func (s *Store) AddLineageEdge(ctx context.Context, a AddLineageEdgeArgs) error {
	var paramsJSON []byte
	if a.OpParams != nil {
		var err error
		paramsJSON, err = json.Marshal(a.OpParams)
		if err != nil {
			return err
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO aigc.asset_lineage
			(child_sha, parent_sha, op, op_params, child_task_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (child_sha, parent_sha, op) DO NOTHING
	`, a.ChildSHA, a.ParentSHA, a.Op, nullableJSON(paramsJSON), a.ChildTaskID)
	return err
}

// ListParentEdges 返回直接祖先 (一层): 谁派生了 sha.
// 想拿完整 DAG 由调用方递归 (有圈防护见 §13 风险).
func (s *Store) ListParentEdges(ctx context.Context, sha string) ([]*AssetLineage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+lineageColumns+`
		FROM aigc.asset_lineage
		WHERE child_sha = $1
		ORDER BY created_at
	`, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AssetLineage
	for rows.Next() {
		l, err := scanLineage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListChildEdges 返回直接派生 (一层): 由 sha 衍生出的所有.
func (s *Store) ListChildEdges(ctx context.Context, sha string) ([]*AssetLineage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+lineageColumns+`
		FROM aigc.asset_lineage
		WHERE parent_sha = $1
		ORDER BY created_at
	`, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AssetLineage
	for rows.Next() {
		l, err := scanLineage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// HasAncestor 检查 candidateAncestor 是否在 sha 的祖先链中 (用于建边时防环).
// 实现: BFS 上溯 sha 的祖先, 命中即返回 true. 深度上限 64 防失控.
func (s *Store) HasAncestor(ctx context.Context, sha, candidateAncestor string) (bool, error) {
	if sha == candidateAncestor {
		return true, nil
	}
	const maxDepth = 64
	visited := map[string]bool{sha: true}
	frontier := []string{sha}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		rows, err := s.pool.Query(ctx, `
			SELECT parent_sha FROM aigc.asset_lineage
			WHERE child_sha = ANY($1)
		`, frontier)
		if err != nil {
			return false, err
		}
		var next []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return false, err
			}
			if p == candidateAncestor {
				rows.Close()
				return true, nil
			}
			if !visited[p] {
				visited[p] = true
				next = append(next, p)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, err
		}
		frontier = next
	}
	return false, nil
}
