package store

// gallery.go — 公开作品 (is_public=true && status=completed && deleted_at IS NULL).
// MVP 仅支持 type 过滤 + prompt ILIKE 关键词. v2 接 CLIP 语义搜索.

import (
	"context"
	"fmt"
	"strings"
)

// GalleryItem 是 ListGallery 的扁平结果 (含 task 头部信息;
// outputs 由调用方按需 ListTaskOutputsBatch 二次拉, 避免聚合查询复杂化).
type GalleryItem struct {
	*Task
	CreatorDisplayName string // 留空, services/aigc 通过 identity 反查 (可选, 缓存)
}

// ListGalleryArgs.
type ListGalleryArgs struct {
	Type    string // 空 = 全部
	Keyword string // 空 = 不过滤; 非空走 prompt ILIKE
	Limit   int
	Offset  int
}

// ListGallery 返回公开作品分页. 默认按 created_at DESC.
func (s *Store) ListGallery(ctx context.Context, a ListGalleryArgs) ([]*GalleryItem, error) {
	args := []any{}
	q := strings.Builder{}
	q.WriteString(`SELECT ` + taskColumns + ` FROM aigc.tasks ` +
		`WHERE is_public = true AND status = 'completed' AND deleted_at IS NULL`)

	if a.Type != "" {
		q.WriteString(fmt.Sprintf(` AND type = $%d`, len(args)+1))
		args = append(args, a.Type)
	}
	if kw := strings.TrimSpace(a.Keyword); kw != "" {
		q.WriteString(fmt.Sprintf(` AND prompt ILIKE $%d`, len(args)+1))
		args = append(args, "%"+kw+"%")
	}

	limit := a.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q.WriteString(fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		len(args)+1, len(args)+2))
	args = append(args, limit, max0(a.Offset))

	rows, err := s.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*GalleryItem
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, &GalleryItem{Task: t})
	}
	return out, rows.Err()
}
