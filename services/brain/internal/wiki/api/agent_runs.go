package api

// agent_runs.go — wiki maintain agent run 历史查询端点
// （BiuMind-Agent-Experience-Design §1.2 P2 审计「可回看历史」）。
//
//	GET /v1/wiki/projects/{pid}/agent/runs           run 列表（含改动页数聚合）
//	GET /v1/wiki/projects/{pid}/agent/runs/{runId}   run 详情 + 改动页清单
//
// 改动清单 = 该 run_id 的 page_revisions 写前快照（join pages 推断操作类型）。
// 诚实降级：create 无快照不进清单（客户端 run 内事件流已覆盖）；快照被
// >512KB 跳过 / 5min 窗口合并的页可能缺席——宁可少列也不猜。

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/wiki/store"
)

func (s *Server) handleListAgentRuns(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	limit := 50
	if l, _ := strconv.Atoi(r.URL.Query().Get("limit")); l > 0 && l <= 200 {
		limit = l
	}
	runs, err := s.Store.ListAgentRuns(r.Context(), pid, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		out = append(out, agentRunOut(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (s *Server) handleGetAgentRun(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	runID := r.PathValue("runId")
	if runID == "" {
		writeErr(w, http.StatusBadRequest, "bad_run_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	run, err := s.Store.GetAgentRun(r.Context(), pid, runID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	changes, err := s.Store.ListAgentRunChanges(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		out = append(out, map[string]any{
			"revision_id": c.RevisionID.String(),
			"page_id":     c.PageID.String(),
			"title":       c.Title,
			"op":          c.Op,
			"change_type": c.ChangeType,
			"created_at":  c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":     agentRunOut(run),
		"changes": out,
	})
}

// agentRunOut —— run 输出。changed_pages 仅 list 端点有值（详情端点另行
// 下发完整 changes 数组，无需聚合数）。
func agentRunOut(r *store.AgentRun) map[string]any {
	out := map[string]any{
		"run_id":        r.RunID,
		"project_id":    r.ProjectID.String(),
		"mode":          r.Mode,
		"model":         r.Model,
		"instruction":   r.Instruction,
		"status":        r.Status,
		"started_at":    r.StartedAt.UTC().Format(time.RFC3339),
		"error":         r.Error,
		"changed_pages": r.ChangedPages,
	}
	if r.FinishedAt != nil {
		out["finished_at"] = r.FinishedAt.UTC().Format(time.RFC3339)
	}
	return out
}
