package api

// gallery.go — 公开作品瀑布流 endpoint (P2-8).
//
//   GET /v1/gallery?type=image&keyword=...&limit=50&offset=0
//
// 公开端点 (无需登录), 返回 is_public=true && status=completed && deleted_at IS NULL
// 的任务. 含 outputs 元数据让前端直接渲染缩略图 + blurhash 占位.
//
// MVP: type 过滤 + prompt ILIKE 关键词搜索. v2 接 CLIP 语义搜索.

import (
	"net/http"

	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
)

func (s *Server) handleListGallery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ := firstQ(q, "type")
	switch typ {
	case "", "image", "video", "digital_human", "hotparse":
	default:
		writeErr(w, http.StatusBadRequest, "bad_type", "")
		return
	}
	limit, offset := paginationFromQuery(q)

	items, err := s.Store.ListGallery(r.Context(), store.ListGalleryArgs{
		Type:    typ,
		Keyword: firstQ(q, "keyword"),
		Limit:   limit,
		Offset:  offset,
	})
	if writeStoreErr(w, err) {
		return
	}

	// 一次性批量拉所有 outputs (避免 N+1 查询).
	taskIDs := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		taskIDs = append(taskIDs, it.ID)
	}
	outsBatch, err := s.Store.ListTaskOutputsBatch(r.Context(), taskIDs)
	if writeStoreErr(w, err) {
		return
	}

	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, projectGalleryItem(it, outsBatch[it.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func projectGalleryItem(it *store.GalleryItem, outputs []*store.TaskOutput) map[string]any {
	return map[string]any{
		"task_id":    it.ID,
		"creator_id": it.UserID, // creator_display_name 由前端二次拉 identity (可缓存)
		"type":       it.Type,
		"prompt":     it.Prompt,
		"model_code": it.ModelCode,
		"created_at": it.CreatedAt,
		"outputs":    projectOutputs(outputs),
	}
}

// projectOutputs 把 TaskOutput 切片投影. 与 my_works 详情共用.
func projectOutputs(outs []*store.TaskOutput) []map[string]any {
	res := make([]map[string]any, 0, len(outs))
	for _, o := range outs {
		entry := map[string]any{
			"idx":         o.Idx,
			"kind":        o.Kind,
			"sha256":      o.SHA256,
			"url":         o.StorageURL, // "cas:<sha>" 或 https://cdn...
			"width":       o.Width,
			"height":      o.Height,
			"duration_ms": o.DurationMs,
			"file_size":   o.FileSize,
			"mime_type":   o.MimeType,
		}
		if o.Blurhash != "" {
			entry["blurhash"] = o.Blurhash
		}
		if o.CoverSHA != "" {
			entry["cover_sha"] = o.CoverSHA
		}
		res = append(res, entry)
	}
	return res
}
