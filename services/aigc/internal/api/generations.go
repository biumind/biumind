package api

// generations.go — 生成任务核心 endpoints (P2-4 / P2-5 / P2-7).
//
// 提交流程 (POST /v1/generations):
//
//   1. JWT 校验 (requireAuth) → 拿 user_id
//   2. authz check → aigc:tasks:create on aigc.Model{code}
//   3. 校验模型 enabled
//   4. 计算 cost_credits (基础价; pricing_rule 加价 P3+ 接入时加)
//   5. billing.Consume(idempotencyKey=task_id 候选) — 4xx 直接拒, 5xx 重试 3 次
//   6. INSERT aigc.tasks (status=pending, cost_credits)
//   7. 有 parent_sha 则 AddLineageEdge (★ 血缘 DAG)
//   8. NATS publish aigc.task.submit  (P2 阶段 NoopBus 静默丢弃; P3 接 worker)
//   9. 任何步骤失败 → billing.Refund(原 log_id) 退款
//  10. 返回 task + estimated_seconds
//
// 失败语义保证:
//   - billing.Consume 成功 + 后续步骤任一失败 → 立即 Refund (best-effort)
//   - DB INSERT 失败时 task_id 没有, idempotency_key 无关联 → 后台 reconcile job
//     兜底 (按 ref_id 匹配未孤儿 log)
//
// 估时 (estimated_seconds): MVP 简单写死按 type 推算; v2 根据上游模型 + 参数预测.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/biumind/biumind/services/aigc/internal/authz"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
)

// SubmitDeps 是 generations 子模块需要的额外依赖 (Server 之外).
// main.go 装配后通过 SetSubmitDeps 注入.
type SubmitDeps struct {
	Bus bus.Bus // NoopBus 在 dev / 早期阶段; 生产用真 NATS

	// Logger — nil 时取 slog.Default().
	Logger *slog.Logger
}

func (s *Server) SetSubmitDeps(d SubmitDeps) { s.submitDeps = d }

// MountGenerations 单独导出便于 main.go / Mount 选择性挂载.
// 必须在 SetSubmitDeps 之后调用.
func (s *Server) MountGenerations(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/generations", s.requireAuth(s.handleSubmitGeneration))
	mux.HandleFunc("GET /v1/generations/mine", s.requireAuth(s.handleListMyTasks))
	mux.HandleFunc("GET /v1/generations/{id}", s.requireAuth(s.handleGetTask))
	// 注意: /others / /cancel 这种字面量片段必须在 /{id} 模板路径之前注册.
	mux.HandleFunc("POST /v1/generations/{id}/cancel", s.requireAuth(s.handleCancelTask))
	mux.HandleFunc("PATCH /v1/generations/{id}/visibility", s.requireAuth(s.handleSetVisibility))
	mux.HandleFunc("DELETE /v1/generations/{id}", s.requireAuth(s.handleDeleteTask))
}

// ─── POST /v1/generations ────────────────────────────

type submitReq struct {
	Type           string         `json:"type"`
	ModelCode      string         `json:"model_code"`
	Prompt         string         `json:"prompt"`
	NegativePrompt string         `json:"negative_prompt,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	IsPublic       bool           `json:"is_public,omitempty"`
	ParentSHA      string         `json:"parent_sha,omitempty"`
	LineageOp      string         `json:"lineage_op,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"` // 客户端去重
}

func (s *Server) handleSubmitGeneration(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		writeErr(w, http.StatusServiceUnavailable, "billing_not_wired", "")
		return
	}
	uid, claims, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req submitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	switch req.Type {
	case "image", "video", "digital_human", "hotparse":
	default:
		writeErr(w, http.StatusBadRequest, "bad_type", "type required")
		return
	}
	// 门禁: digital_human 暂无可用生成链路 —— model-relay 无对应 adaptor,
	// worker 直连 provider 段3.6 已删。客户端已对该 tab 做"即将上线"门禁;此处
	// 服务端兜底防绕过。上线时连同 adaptor 一起放开。
	//
	// hotparse 已放开(爆款解析): worker HotparseProvider 经 model-relay
	// /v1/internal/transcribe + /v1/internal/chat 执行 STT + LLM 拆解。
	switch req.Type {
	case "digital_human":
		writeErr(w, http.StatusNotImplemented, "type_not_available",
			req.Type+" generation is coming soon")
		return
	}
	if req.ModelCode == "" {
		writeErr(w, http.StatusBadRequest, "bad_model", "model_code required")
		return
	}
	if req.Prompt == "" && req.Type != "hotparse" {
		writeErr(w, http.StatusBadRequest, "bad_prompt", "prompt required")
		return
	}

	// 校验模型存在 + enabled
	model, err := s.Store.GetModel(r.Context(), req.ModelCode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "model_not_found", req.ModelCode)
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !model.Enabled {
		writeErr(w, http.StatusForbidden, "model_disabled", req.ModelCode)
		return
	}
	if model.Type != req.Type {
		writeErr(w, http.StatusBadRequest, "type_model_mismatch",
			fmt.Sprintf("model %s is type=%s but request type=%s", model.Code, model.Type, req.Type))
		return
	}

	// authz: aigc:tasks:create on Model
	allowed, err := authz.Authorize(r.Context(), s.decider(),
		authz.PrincipalUser(uid, claims.Plan, firstRole(claims.Roles)),
		authz.ActionCreateTask,
		authz.ResourceModelByCode(req.ModelCode))
	if err != nil {
		writeErr(w, http.StatusForbidden, "authz_unavailable", err.Error())
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}

	// 段3.6: aigc 不再在提交时扣费。计费已归一到 model-relay 单一 egress —
	// worker 调 /v1/internal/generations 时由 model-relay 同步 Hold/Settle
	// (按 model_relay.pricing 单一 SoT)。这里只算一个展示用估值 (前端显示
	// "约 N 积分"), 不做任何 identity 扣减, 避免与 relay 双扣。
	cost := computeCostCredits(model, req.Params)

	// 入库
	task, err := s.Store.CreateTask(r.Context(), store.CreateTaskArgs{
		UserID:         uid,
		Type:           req.Type,
		ModelCode:      req.ModelCode,
		ProviderCode:   model.ProviderCode,
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		Params:         req.Params,
		IsPublic:       req.IsPublic,
		CostCredits:    cost,
		ParentSHA:      req.ParentSHA,
		LineageOp:      req.LineageOp,
	})
	if err != nil {
		// 段3.6: 无 Consume 不需退款 (relay 在生成时才扣)。
		writeErr(w, http.StatusInternalServerError, "create_task", err.Error())
		return
	}

	// 血缘 DAG: parent_sha + lineage_op 已写到 aigc.tasks (CreateTaskArgs).
	// asset_lineage 表里那条边由 worker 转存 task_outputs 拿到 child_sha 后再补
	// (避免 P2 阶段构造 child_sha 占位违反 PK).

	// publish NATS aigc.task.submit — 带完整 payload 让 worker 无需查 PG.
	_ = s.publishSubmit(r.Context(), task, model.ProviderCode)

	// balance_after: 当前余额 (展示用)。段3.6 后提交不扣费, 真正扣减在生成时
	// 由 model-relay 完成, 这里读一下现值给前端 (best-effort, 失败返 0)。
	var balanceAfter int64
	if s.Billing != nil {
		if bal, berr := s.Billing.GetBalanceTotal(r.Context(), uid); berr == nil {
			balanceAfter = bal
		}
	}

	s.submitLogger().DebugContext(r.Context(), "aigc: task submitted",
		"task_id", task.ID, "user_id", uid, "type", req.Type,
		"model", req.ModelCode, "provider", model.ProviderCode,
		"prompt_bytes", len(req.Prompt), "cost_credits", cost)
	writeJSON(w, http.StatusOK, map[string]any{
		"task":              projectTask(task, nil),
		"estimated_seconds": estimateSeconds(req.Type),
		"cost_credits":      cost,
		"balance_after":     balanceAfter,
	})
}

// ─── GET /v1/generations/mine ────────────────────────

func (s *Server) handleListMyTasks(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := requireUserID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, offset := paginationFromQuery(q)

	statuses := splitCSV(firstQ(q, "statuses"))
	types := splitCSV(firstQ(q, "type"))

	tasks, err := s.Store.ListMyTasks(r.Context(), store.ListMyTasksArgs{
		UserID:   uid,
		Statuses: statuses,
		Types:    types,
		Limit:    limit,
		Offset:   offset,
	})
	if writeStoreErr(w, err) {
		return
	}

	// 一次性批量拉 outputs
	taskIDs := make([]uuid.UUID, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	outsBatch, err := s.Store.ListTaskOutputsBatch(r.Context(), taskIDs)
	if writeStoreErr(w, err) {
		return
	}

	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, projectTask(t, outsBatch[t.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// ─── GET /v1/generations/{id} ────────────────────────

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	uid, claims, ok := requireUserID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	task, err := s.Store.GetTask(r.Context(), id)
	if writeStoreErr(w, err) {
		return
	}

	// authz: 自己的可读, 公开任务任何登录用户可读, 否则 deny
	allowed, err := authz.Authorize(r.Context(), s.decider(),
		authz.PrincipalUser(uid, claims.Plan, firstRole(claims.Roles)),
		authz.ActionReadTask,
		authz.ResourceTask(task.ID, task.UserID, task.IsPublic))
	if err != nil {
		writeErr(w, http.StatusForbidden, "authz_unavailable", err.Error())
		return
	}
	// 即使 Cedar policy 没配, owner 自己也必须可读 (兜底)
	if !allowed && task.UserID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}

	outs, err := s.Store.ListTaskOutputs(r.Context(), task.ID)
	if writeStoreErr(w, err) {
		return
	}

	resp := map[string]any{"task": projectTask(task, outs)}

	// 血缘 (可选, ?include_lineage=1)
	if firstQ(r.URL.Query(), "include_lineage") == "1" && task.ParentSHA != "" {
		// 仅一层: 父 + 子. 多层由前端递归调.
		parents, _ := s.Store.ListParentEdges(r.Context(), task.ParentSHA)
		resp["lineage_parents"] = projectLineage(parents)
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─── PATCH /v1/generations/{id}/visibility ───────────

type visibilityReq struct {
	IsPublic bool `json:"is_public"`
}

func (s *Server) handleSetVisibility(w http.ResponseWriter, r *http.Request) {
	uid, claims, ok := requireUserID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req visibilityReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// 先取 task 校验 owner; 也让 authz policy 拿到 owner_id
	task, err := s.Store.GetTask(r.Context(), id)
	if writeStoreErr(w, err) {
		return
	}
	allowed, err := authz.Authorize(r.Context(), s.decider(),
		authz.PrincipalUser(uid, claims.Plan, firstRole(claims.Roles)),
		authz.ActionUpdateTaskVis,
		authz.ResourceTask(task.ID, task.UserID, task.IsPublic))
	if err != nil {
		writeErr(w, http.StatusForbidden, "authz_unavailable", err.Error())
		return
	}
	if !allowed && task.UserID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}
	n, err := s.Store.SetTaskVisibility(r.Context(), uid, []uuid.UUID{id}, req.IsPublic)
	if writeStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated_count": n})
}

// ─── DELETE /v1/generations/{id} ─────────────────────

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	uid, claims, ok := requireUserID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	task, err := s.Store.GetTask(r.Context(), id)
	if writeStoreErr(w, err) {
		return
	}
	allowed, err := authz.Authorize(r.Context(), s.decider(),
		authz.PrincipalUser(uid, claims.Plan, firstRole(claims.Roles)),
		authz.ActionDeleteTask,
		authz.ResourceTask(task.ID, task.UserID, task.IsPublic))
	if err != nil {
		writeErr(w, http.StatusForbidden, "authz_unavailable", err.Error())
		return
	}
	if !allowed && task.UserID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}
	n, err := s.Store.SoftDeleteTasks(r.Context(), uid, []uuid.UUID{id})
	if writeStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted_count": n})
}

// ─── POST /v1/generations/{id}/cancel ────────────────

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	uid, claims, ok := requireUserID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	task, err := s.Store.GetTask(r.Context(), id)
	if writeStoreErr(w, err) {
		return
	}
	allowed, err := authz.Authorize(r.Context(), s.decider(),
		authz.PrincipalUser(uid, claims.Plan, firstRole(claims.Roles)),
		authz.ActionCancelTask,
		authz.ResourceTask(task.ID, task.UserID, task.IsPublic))
	if err != nil {
		writeErr(w, http.StatusForbidden, "authz_unavailable", err.Error())
		return
	}
	if !allowed && task.UserID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}
	if !task.IsActive() {
		writeErr(w, http.StatusConflict, "not_active",
			"task is "+task.Status+", cannot cancel")
		return
	}

	now := time.Now().UTC()
	if err := s.Store.UpdateTaskStatus(r.Context(), store.UpdateTaskStatusArgs{
		ID: task.ID, Status: "cancelled", CompletedAt: &now,
	}); err != nil {
		writeStoreErr(w, err)
		return
	}

	// 退款 (best-effort; 真退款金额由后台 reconcile 兜底; 失败不阻塞用户)
	if s.Billing != nil && task.CostCredits > 0 {
		// 这里需要原 billing log_id; P2 阶段未持久化, 退款用 ref_id (idempotency_key
		// 兜底). v2 把 log_id 写进 aigc.tasks.billing_log_id.
		// 现阶段简化: 不做实际 refund (worker 完成态切 cancelled 时由 worker 处理).
	}

	// publish NATS aigc.task.cancel 让 worker 中断上游 (P3)
	_ = s.publishCancel(r.Context(), task.ID)

	writeJSON(w, http.StatusOK, map[string]any{"task_id": task.ID, "status": "cancelled"})
}

// ─── 投影 / NATS / 估时 / 计价 helpers ───────────────

// projectTask 把 store.Task + outputs 投影成 client JSON.
// outputs 可空 (列表场景就不一定带; 详情场景一定带).
func projectTask(t *store.Task, outs []*store.TaskOutput) map[string]any {
	out := map[string]any{
		"id":               t.ID,
		"user_id":          t.UserID,
		"type":             t.Type,
		"model_code":       t.ModelCode,
		"provider_code":    t.ProviderCode,
		"prompt":           t.Prompt,
		"params":           rawJSONOrEmpty(t.Params),
		"status":           t.Status,
		"progress":         t.Progress,
		"cost_credits":     t.CostCredits,
		"refunded_credits": t.RefundedCredits,
		"is_public":        t.IsPublic,
		"created_at":       t.CreatedAt,
	}
	if t.NegativePrompt != "" {
		out["negative_prompt"] = t.NegativePrompt
	}
	if t.ErrorCode != "" {
		out["error_code"] = t.ErrorCode
	}
	if t.ErrorMessage != "" {
		out["error_message"] = t.ErrorMessage
	}
	if t.QueuedAt != nil {
		out["queued_at"] = *t.QueuedAt
	}
	if t.StartedAt != nil {
		out["started_at"] = *t.StartedAt
	}
	if t.CompletedAt != nil {
		out["completed_at"] = *t.CompletedAt
	}
	if outs != nil {
		out["outputs"] = projectOutputs(outs)
	}
	return out
}

func projectLineage(edges []*store.AssetLineage) []map[string]any {
	out := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		out = append(out, map[string]any{
			"child_sha":  e.ChildSHA,
			"parent_sha": e.ParentSHA,
			"op":         e.Op,
			"op_params":  rawJSONOrEmpty(e.OpParams),
			"created_at": e.CreatedAt,
		})
	}
	return out
}

// computeCostCredits — MVP 走基础价. v2 按 pricing_rule + params (duration /
// resolution) 加价.
func computeCostCredits(m *store.Model, _ map[string]any) int64 {
	return m.PriceCredits
}

func estimateSeconds(typ string) int64 {
	switch typ {
	case "image":
		return 15
	case "video":
		return 90
	case "digital_human":
		return 120
	case "hotparse":
		return 30
	}
	return 30
}

// publishSubmit / publishCancel — 通过 bus 发 NATS subject.
// SubmitDeps.Bus 为 nil 时静默 (dev / 早期阶段); 错误只 log 不阻塞响应.
//
// Subjects:
//
//	aigc.task.submit   服务端 → workers/aigc (含完整 payload, worker 无需查 PG)
//	aigc.task.cancel   服务端 → workers/aigc (中断上游)
func (s *Server) publishSubmit(ctx context.Context, t *store.Task, providerCode string) error {
	if s.submitDeps.Bus == nil {
		return nil
	}
	// payload 字段对齐 workers/aigc/biumind_aigc/event.py SubmitTask
	payload := map[string]any{
		"task_id":         t.ID.String(),
		"user_id":         t.UserID.String(),
		"type":            t.Type,
		"model_code":      t.ModelCode,
		"provider_code":   providerCode,
		"prompt":          t.Prompt,
		"negative_prompt": t.NegativePrompt,
		"params":          rawJSONOrEmpty(t.Params),
		"cost_credits":    t.CostCredits,
		"parent_sha":      t.ParentSHA,
	}
	// 段3.6: 不再注入 credential_*。生成经 model-relay 单一 egress
	// (worker RelayProvider 调 /v1/internal/generations, 凭证由 model-relay
	// 自己从 vault 解密), worker 不再用 payload 凭证。
	return s.submitDeps.Bus.Publish(ctx, "aigc.task.submit", payload)
}

// submitLogger returns the configured logger or slog.Default() if nil.
// Used in publishSubmit so the Warn line goes to the right sink.
func (s *Server) submitLogger() *slog.Logger {
	if s.submitDeps.Logger != nil {
		return s.submitDeps.Logger
	}
	return slog.Default()
}

func (s *Server) publishCancel(ctx context.Context, taskID uuid.UUID) error {
	if s.submitDeps.Bus == nil {
		return nil
	}
	return s.submitDeps.Bus.Publish(ctx, "aigc.task.cancel",
		map[string]any{"task_id": taskID.String()})
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	for _, p := range splitOn(s, ',') {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitOn(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
