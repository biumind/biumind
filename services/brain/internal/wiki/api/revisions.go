package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// Wiki 页版本历史（迁移 00065），REST 镜像 note/api Revisions。
//
//	GET   /v1/wiki/projects/{pid}/pages/{id}/revisions                版本列表（不含 blocks_json）
//	GET   /v1/wiki/projects/{pid}/pages/{id}/revisions/{rid}          单个版本（含完整 blocks_json）
//	POST  /v1/wiki/projects/{pid}/pages/{id}/revisions/{rid}/restore  覆盖式恢复（先自动备份当前态）
//	POST  /v1/wiki/projects/{pid}/pages/{id}/revisions/{rid}/save-as-copy 以该版本另存新页
//
// restore 二次确认在 client（非后端）；后端 restore 即覆盖。
// user 隔离两层：ownsProject（项目归属）+ 验 page.ProjectID==pid（防跨项目猜 uuid 取历史）。

// requirePageInProject 验 page 归属项目 pid，否则 404 并返回 false。
func (s *Server) requirePageInProject(w http.ResponseWriter, r *http.Request, pid, pageID uuid.UUID) bool {
	page, err := s.Store.GetPage(r.Context(), pageID)
	if err != nil || page.ProjectID != pid {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return false
	}
	return true
}

func (s *Server) handleListPageRevisions(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if !s.requirePageInProject(w, r, pid, pageID) {
		return
	}
	limit := 20
	if l, _ := strconv.Atoi(r.URL.Query().Get("limit")); l > 0 && l <= 100 {
		limit = l
	}
	offset := 0
	if o, _ := strconv.Atoi(r.URL.Query().Get("offset")); o > 0 {
		offset = o
	}
	revs, err := s.Store.ListPageRevisions(r.Context(), pageID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(revs))
	for _, rev := range revs {
		out = append(out, revisionOut(rev, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": out})
}

func (s *Server) handleGetPageRevision(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	rid, err := uuid.Parse(r.PathValue("rid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_revision_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if !s.requirePageInProject(w, r, pid, pageID) {
		return
	}
	rev, err := s.Store.GetPageRevision(r.Context(), pageID, rid)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, revisionOut(rev, true))
}

func (s *Server) handleRestorePageRevision(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	rid, err := uuid.Parse(r.PathValue("rid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_revision_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if !s.requirePageInProject(w, r, pid, pageID) {
		return
	}
	actorID := mustUserID(r).String()
	// §1.2 P2 可选 OCC：body 带 if_match_version（run 结束时刻的 page
	// version）时先比对，不一致 → 409（带当前态供客户端 diff 确认）；
	// 空 body / 不传 → 维持原覆盖式行为（向后兼容）。
	ifMatch := 0
	if r.Body != nil && r.ContentLength != 0 {
		var req struct {
			IfMatchVersion int `json:"if_match_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil &&
			!errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ifMatch = req.IfMatchVersion
	}
	p, err := s.Store.RestorePageRevision(r.Context(), pageID, rid, actorID, ifMatch)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		body := map[string]any{
			"error": map[string]any{
				"code": "version_conflict", "message": "if_match_version mismatch",
			},
		}
		if cur, gerr := s.Store.GetPage(r.Context(), pageID); gerr == nil {
			body["server_version"] = cur.Version
			body["server_payload"] = pageOut(cur)
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pageOut(p))
}

func (s *Server) handleSavePageRevisionAsCopy(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	rid, err := uuid.Parse(r.PathValue("rid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_revision_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if !s.requirePageInProject(w, r, pid, pageID) {
		return
	}
	actorID := mustUserID(r).String()
	p, err := s.Store.SavePageRevisionAsCopy(r.Context(), pageID, rid, actorID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pageOut(p))
}

// revisionOut —— 版本输出。list（includeBlocks=false）省 blocks_json（取内容走 detail）。
// detail（includeBlocks=true）含 frontmatter + blocks_json（反序列化为 generic 数组）。
func revisionOut(r *store.Revision, includeBlocks bool) map[string]any {
	out := map[string]any{
		"id":          r.ID.String(),
		"page_id":     r.PageID.String(),
		"project_id":  r.ProjectID.String(),
		"actor_id":    r.ActorID,
		"title":       r.Title,
		"change_type": r.ChangeType,
		"created_at":  r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.ChangeSummary != nil {
		out["change_summary"] = *r.ChangeSummary
	}
	// §1.2 P2：agent 产生的快照带 run_id（人工为 ""，不下发），客户端审计
	// 面板据此精确匹配本 run 的写前快照（替代 P1 的时间窗启发式）。
	if r.RunID != "" {
		out["run_id"] = r.RunID
	}
	if includeBlocks {
		out["frontmatter"] = r.Frontmatter
		// blocks_json 存的是 store.Block 的 Go 字段名序列化（restore 反序列化靠它，
		// case-insensitive 匹配 Go 字段）；输出给 client 须经 blockOut 转 snake_case，
		// 否则 client 读 b['content'] 落空（存的是 'Content'）。
		var bs []*store.Block
		if len(r.BlocksJSON) > 0 {
			_ = json.Unmarshal(r.BlocksJSON, &bs)
		}
		blocks := make([]map[string]any, len(bs))
		for i, b := range bs {
			blocks[i] = blockOut(b)
		}
		out["blocks_json"] = blocks
	}
	return out
}
