package api

// merge_undo.go —— wiki merge undo（撤销页面合并）HTTP 端点
// （BiuMind-Agent-Experience-Design §6.1 P3-a）。仅用户 UI 使用，
// 不暴露给 agent 工具（A3）。
//
//	POST /v1/wiki/projects/{pid}/pages/{id}/unmerge   撤销一次 merge（{id} = canonical 页）
//
// body：{"duplicate_id": "<uuid>", "if_match_version": <int>?}
//
// 响应：
//
//	200 {"unmerged": true, "canonical": <page>, "duplicate": <page>}
//	409 version_conflict   if_match_version 与 canonical 当前 version 不符
//	                       （带 server_version / server_payload，同 restore 端点）
//	409 undo_unavailable   merge 前快照缺失（5min 窗口合并 / >512KB / Prune /
//	                       00011 前老 merge 无 merge_id），诚实降级不猜（A1）
//	409 merge_state_invalid 链式 merge（canonical 又被 merge 走）或 duplicate
//	                       已不指向该 canonical（A2）

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

type unmergeReq struct {
	DuplicateID    string `json:"duplicate_id"` // 被合并走（软删中）的那页
	IfMatchVersion int    `json:"if_match_version"`
}

func (s *Server) handleUnmergePage(w http.ResponseWriter, r *http.Request) {
	pid, _ := uuid.Parse(r.PathValue("pid"))
	canonicalID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if !s.requirePageInProject(w, r, pid, canonicalID) {
		return
	}
	var req unmergeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	duplicateID, err := uuid.Parse(req.DuplicateID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_duplicate_id", err.Error())
		return
	}
	actorID := mustUserID(r).String()

	canonical, duplicate, err := s.Store.UnmergePages(r.Context(),
		canonicalID, duplicateID, actorID, req.IfMatchVersion)
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
		if cur, gerr := s.Store.GetPage(r.Context(), canonicalID); gerr == nil {
			body["server_version"] = cur.Version
			body["server_payload"] = pageOut(cur)
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	if errors.Is(err, store.ErrMergeUndoUnavailable) {
		writeErr(w, http.StatusConflict, "undo_unavailable",
			"pre-merge snapshot missing (revision window merge / oversized page / pruned); cannot undo precisely")
		return
	}
	if errors.Is(err, store.ErrMergeStateInvalid) {
		writeErr(w, http.StatusConflict, "merge_state_invalid",
			"page pair is no longer in an undoable merged state (chained merge or changed duplicate)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"unmerged":  true,
		"canonical": pageOut(canonical),
		"duplicate": pageOut(duplicate),
	})
}
