// jobs.go — Phase 4 段 3 (P4.S3.1) 异步任务入口.
//
// /v1/jobs 是 model-relay 端 dispatch 的第三种 mode (sync chat /
// streaming chat 之外):  async generation. POST 写 aigc.tasks + NATS
// publish, GET 查任务状态.
//
// 与 aigc /v1/generations 并存的过渡:
//   段 3.1 (本任务): /v1/jobs 上线, aigc /v1/generations 仍可用
//   段 3.2: Python worker 从 NATS payload 拿凭证, 不再读 env
//   段 3.6: aigc /v1/generations 切流到 /v1/jobs, 删除前者
//
// 流程:
//   1. authMiddleware 解 JWT → user_id, plan
//   2. ModelResolver: SELECT model_relay.models WHERE code=? AND
//      mode IN (image/video/dh/hotparse) AND status='active'
//   3. Pricing 估算: token strategy 不适用; fixed strategy 按
//      cost_per_image / cost_per_video_second + max duration; parameter
//      strategy 走 pricing_rules 多维乘数
//   4. identity.Hold(MaxAmount=估算) → hold_id
//   5. Provider lookup → 拿 credentials → vault.Reveal → 明文 api_key
//   6. INSERT aigc.tasks (跨 schema 写, 段 3 后 aigc.tasks 仍存活)
//   7. NATS publish aigc.task.submit (含 decrypted_key + base_url +
//      headers + hold_id)
//   8. 返回 {job_id, hold_id, max_credits}
//
// GET /v1/jobs/{id}:
//   读 aigc.tasks WHERE id=? AND user_id=? — 防越权
//   返 {status, progress, cost_credits, error_code/message,
//        outputs (从 aigc.task_outputs 子查询)}.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"

	"github.com/biumind/biumind/services/model-relay/internal/billing"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// JobsHandler 持有 /v1/jobs 端点的依赖.
type JobsHandler struct {
	Pool    *pgxpool.Pool             // 写 aigc.tasks (跨 schema)
	Cache   *registry.Cache           // model 路由 (in-memory + LISTEN/NOTIFY)
	Vault   *registry.CredentialVault // 解密 model_relay.credentials
	Billing *billing.Client           // identity.Hold/Settle/Release
	Bus     bus.Bus                   // NATS, nil 时 publish 为 noop
	Logger  *slog.Logger
}

// jobSubmitRequest 是 POST /v1/jobs 入参.
type jobSubmitRequest struct {
	Model          string         `json:"model"` // model_relay.models.code
	Prompt         string         `json:"prompt"`
	NegativePrompt string         `json:"negative_prompt,omitempty"`
	Params         map[string]any `json:"params,omitempty"` // duration, resolution, aspect_ratio, ...
	ParentSHA      string         `json:"parent_sha,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

// jobSubmitResponse 是 POST /v1/jobs 返回.
type jobSubmitResponse struct {
	JobID      string `json:"job_id"`
	HoldID     string `json:"hold_id,omitempty"`
	MaxCredits int64  `json:"max_credits"` // Hold 上限
	Status     string `json:"status"`      // 总是 "pending" 或 "queued"
}

// jobStatusResponse 是 GET /v1/jobs/{id} 返回.
type jobStatusResponse struct {
	JobID        string         `json:"job_id"`
	Status       string         `json:"status"`
	Progress     int            `json:"progress"`
	Type         string         `json:"type"` // image|video|digital_human|hotparse
	ModelCode    string         `json:"model_code"`
	CostCredits  int64          `json:"cost_credits"`
	ErrorCode    string         `json:"error_code,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	CreatedAt    string         `json:"created_at"`
	CompletedAt  string         `json:"completed_at,omitempty"`
	Outputs      []jobOutput    `json:"outputs,omitempty"`
	ExternalTask string         `json:"external_task_id,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
}

type jobOutput struct {
	Idx        int    `json:"idx"`
	Kind       string `json:"kind"` // image|video|audio|cover
	StorageURL string `json:"storage_url"`
	Sha256     string `json:"sha256"`
	MimeType   string `json:"mime_type,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
	Moderation string `json:"moderation_status,omitempty"`
}

// ServeHTTP 路由分发. 单一 handler 同时处理 POST 和 GET, 因为
// stdlib net/http ServeMux 1.22 不支持 same-pattern multi-method
// 的 hot path 复用 — 上层 main.go 用两个 mux.HandleFunc 注册.
// (这里保留 single-method handlers, 见 SubmitJob / GetJob.)

// ─── helpers ────────────────────────────────────────────

func (h *JobsHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// estimateCredits 按 model.pricing_strategy 估算 Hold 上限.
//
//	token     不适用 (image/video 不应该走 token; 设为 0 让 caller 报错)
//	fixed     cost_per_image / cost_per_video_second × duration / 等
//	parameter base × Π(matched multiplier from pricing_rules)
//
// 返回积分整数 (向上取整). 单位与 identity.credits 一致 (毫分对应表
// 见 BiuMind-Billing-Redesign.md §3).
func (h *JobsHandler) estimateCredits(
	ctx context.Context, m *registry.Model, params map[string]any,
) (int64, error) {
	switch m.Mode {
	case "image_generation":
		return h.estimateImage(ctx, m, params)
	case "video_generation":
		return h.estimateVideo(ctx, m, params)
	case "digital_human":
		return h.estimateAudio(ctx, m, params)
	case "audio_speech", "audio_transcription":
		return h.estimateAudio(ctx, m, params)
	case "hotparse":
		// 默认 base, 实际成本由 worker 完成时回报.
		return h.fetchBasePrice(ctx, m, "image")
	default:
		return 0, fmt.Errorf("model %q has mode=%q not supported by /v1/jobs (use /v1/messages for chat)", m.Code, m.Mode)
	}
}

// fetchBasePrice 读 model_relay.pricing 最近一条 cost_per_<kind>.
// 返回积分 (1 元 = 100,000 毫分; 1 积分 = 1000 毫分; 这里直接用 cost
// 字段, 后续 billing 层负责单位换算 — MVP 假设 cost_per_image 是
// 「积分」单位, 与 aigc.models.price_credits 保持口径一致).
func (h *JobsHandler) fetchBasePrice(ctx context.Context, m *registry.Model, kind string) (int64, error) {
	col := "cost_per_image"
	switch kind {
	case "video":
		col = "cost_per_video_second"
	case "audio":
		col = "cost_per_audio_second"
	case "char":
		col = "cost_per_character"
	}
	q := fmt.Sprintf(`
		SELECT COALESCE(%s, 0)::bigint
		FROM model_relay.pricing
		WHERE model_id = $1
		ORDER BY effective_at DESC
		LIMIT 1`, col)
	var v int64
	err := h.Pool.QueryRow(ctx, q, m.ID).Scan(&v)
	if errors.Is(err, errNoRows) {
		return 0, nil // 未配置, caller 决定是 reject 还是 fallback
	}
	return v, err
}

func (h *JobsHandler) estimateImage(ctx context.Context, m *registry.Model, params map[string]any) (int64, error) {
	base, err := h.fetchBasePrice(ctx, m, "image")
	if err != nil {
		return 0, err
	}
	if base <= 0 {
		return 0, fmt.Errorf("model %q has no cost_per_image", m.Code)
	}
	if m.PricingStrategy != "parameter" {
		return base, nil
	}
	mult, err := h.applyRules(ctx, m, params)
	if err != nil {
		return base, err
	}
	return ceilMul(base, mult), nil
}

func (h *JobsHandler) estimateVideo(ctx context.Context, m *registry.Model, params map[string]any) (int64, error) {
	base, err := h.fetchBasePrice(ctx, m, "video")
	if err != nil {
		return 0, err
	}
	if base <= 0 {
		// 兜底回 cost_per_image (aigc 旧 price_credits 迁过来时只有这一个)
		base, err = h.fetchBasePrice(ctx, m, "image")
		if err != nil || base <= 0 {
			return 0, fmt.Errorf("model %q has no cost_per_video_second nor cost_per_image", m.Code)
		}
	}
	mult := 1.0
	if m.PricingStrategy == "parameter" {
		mult, err = h.applyRules(ctx, m, params)
		if err != nil {
			return 0, err
		}
	}
	// 视频估算上限取 max duration (params.duration 或 5)
	durSec, _ := params["duration"].(float64)
	if durSec <= 0 {
		durSec = 5
	}
	return ceilMul(base, mult*durSec), nil
}

func (h *JobsHandler) estimateAudio(ctx context.Context, m *registry.Model, _ map[string]any) (int64, error) {
	base, err := h.fetchBasePrice(ctx, m, "audio")
	if err != nil || base <= 0 {
		// fallback to image-style fixed cost
		base, err = h.fetchBasePrice(ctx, m, "image")
	}
	if err != nil || base <= 0 {
		return 0, fmt.Errorf("model %q has no audio/image base cost", m.Code)
	}
	return base, nil
}

// applyRules 把 model_relay.pricing_rules.rule_jsonb 的 by_duration ×
// by_resolution 乘数应用到当前请求 params. 返回累积 multiplier.
func (h *JobsHandler) applyRules(ctx context.Context, m *registry.Model, params map[string]any) (float64, error) {
	const q = `
		SELECT rule_jsonb FROM model_relay.pricing_rules
		WHERE model_id = $1
		ORDER BY effective_at DESC LIMIT 1`
	var raw []byte
	err := h.Pool.QueryRow(ctx, q, m.ID).Scan(&raw)
	if errors.Is(err, errNoRows) {
		return 1, nil
	}
	if err != nil {
		return 1, err
	}
	var rule struct {
		ByDuration []struct {
			MaxSeconds float64 `json:"max_seconds"`
			Multiplier float64 `json:"multiplier"`
		} `json:"by_duration"`
		ByResolution []struct {
			Resolution string  `json:"resolution"`
			Multiplier float64 `json:"multiplier"`
		} `json:"by_resolution"`
	}
	if err := json.Unmarshal(raw, &rule); err != nil {
		return 1, fmt.Errorf("pricing_rules.rule_jsonb invalid: %w", err)
	}
	mult := 1.0
	if d, ok := params["duration"].(float64); ok && len(rule.ByDuration) > 0 {
		// 找到第一个 max_seconds >= d 的档位
		for _, b := range rule.ByDuration {
			if d <= b.MaxSeconds {
				mult *= b.Multiplier
				break
			}
		}
	}
	if r, ok := params["resolution"].(string); ok && len(rule.ByResolution) > 0 {
		for _, b := range rule.ByResolution {
			if b.Resolution == r {
				mult *= b.Multiplier
				break
			}
		}
	}
	return mult, nil
}

func ceilMul(base int64, factor float64) int64 {
	v := float64(base) * factor
	if v <= 0 {
		return 0
	}
	r := int64(v)
	if v > float64(r) {
		r++
	}
	return r
}

// providerCredential reads model_relay.credentials for the given
// provider code. Picks first 'active' row (admin should have only one
// per AIGC provider during 段 3 transition; multi-cred routing is
// chat-only).
func (h *JobsHandler) providerCredential(ctx context.Context, providerCode string) (*registry.Credential, error) {
	const q = `
		SELECT c.id FROM model_relay.credentials c
		JOIN model_relay.providers p ON p.id = c.provider_id
		WHERE p.code = $1 AND c.status = 'active'
		ORDER BY c.created_at LIMIT 1`
	var id uuid.UUID
	if err := h.Pool.QueryRow(ctx, q, providerCode).Scan(&id); err != nil {
		if errors.Is(err, errNoRows) {
			return nil, fmt.Errorf("no active credential for provider %q", providerCode)
		}
		return nil, err
	}
	// re-fetch through cache for envelope-encrypted bytes
	cred, err := h.Cache.GetCredential(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load credential %s: %w", id, err)
	}
	return cred, nil
}

// errNoRows 转发 pgx.ErrNoRows 让 errors.Is 检查更直观.
var errNoRows = pgx.ErrNoRows

// ─── handlers ────────────────────────────────────────────

// SubmitJob handles POST /v1/jobs.
func (h *JobsHandler) SubmitJob(w http.ResponseWriter, r *http.Request) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}

	var req jobSubmitRequest
	if !decodeJobJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_input", "model required")
		return
	}

	// 1. ModelResolver
	model, err := h.Cache.GetModelByCode(r.Context(), req.Model)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "model_not_found", err.Error())
		return
	}
	if model.Status != "active" {
		writeJSONError(w, http.StatusServiceUnavailable, "model_disabled",
			fmt.Sprintf("model %q status=%s", model.Code, model.Status))
		return
	}
	if !isJobsModeSupported(model.Mode) {
		writeJSONError(w, http.StatusBadRequest, "wrong_endpoint",
			fmt.Sprintf("model mode=%s — use /v1/messages for chat or /v1/embeddings", model.Mode))
		return
	}

	// 2. Pricing 估算
	maxCredits, err := h.estimateCredits(r.Context(), model, req.Params)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pricing_unavailable", err.Error())
		return
	}
	if maxCredits <= 0 {
		writeJSONError(w, http.StatusServiceUnavailable, "pricing_unavailable",
			"estimated cost is 0; check pricing config")
		return
	}

	// 3. Hold (skip when billing client not wired — dev mode)
	var holdID string
	if h.Billing != nil {
		hold, err := h.Billing.Hold(r.Context(), billing.HoldArgs{
			UserID:         claims.UserID,
			MaxAmount:      maxCredits,
			RefType:        "aigc_task",
			IdempotencyKey: req.IdempotencyKey,
			TTLSeconds:     600,       // 10min — 视频任务通常 1-3min, 留余量
			ModelCode:      req.Model, // W3-7: dashboard 按模型分布用
		})
		if err != nil {
			if errors.Is(err, billing.ErrInsufficient) {
				writeJSONError(w, http.StatusPaymentRequired, "insufficient_credits",
					fmt.Sprintf("max_credits=%d", maxCredits))
				return
			}
			writeJSONError(w, http.StatusServiceUnavailable, "hold_failed", err.Error())
			return
		}
		holdID = hold.ID
	}

	// 4. Resolve credentials → decrypted key
	cred, err := h.providerCredential(r.Context(), model.Family)
	if err != nil {
		h.releaseHold(r.Context(), holdID, "credential resolve failed")
		writeJSONError(w, http.StatusServiceUnavailable, "credential_unavailable", err.Error())
		return
	}
	plaintext, err := h.Vault.RevealFromCached(cred)
	if err != nil {
		h.releaseHold(r.Context(), holdID, "credential decrypt failed")
		writeJSONError(w, http.StatusServiceUnavailable, "credential_decrypt_failed", err.Error())
		return
	}

	// 5. INSERT aigc.tasks (跨 schema; 段 3.4 后保留此表)
	jobID, err := h.insertTask(r.Context(), claims.UserID, model, &req, maxCredits)
	if err != nil {
		h.releaseHold(r.Context(), holdID, "task insert failed")
		writeJSONError(w, http.StatusInternalServerError, "task_insert_failed", err.Error())
		return
	}

	// 6. NATS publish
	h.publishSubmit(r.Context(), jobID, claims.UserID, model, &req, plaintext, cred, holdID)
	// 立即清零明文字节
	for i := range plaintext {
		plaintext[i] = 0
	}

	writeJSONOK(w, http.StatusCreated, jobSubmitResponse{
		JobID:      jobID,
		HoldID:     holdID,
		MaxCredits: maxCredits,
		Status:     "queued",
	})
}

// GetJob handles GET /v1/jobs/{id}.
func (h *JobsHandler) GetJob(w http.ResponseWriter, r *http.Request) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	jobID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	const q = `
		SELECT id, type, model_code, status, progress, cost_credits,
		       COALESCE(error_code, ''), COALESCE(error_message, ''),
		       created_at,
		       COALESCE(completed_at, '0001-01-01'::timestamptz),
		       COALESCE(external_task_id, ''),
		       COALESCE(params, '{}'::jsonb)
		FROM aigc.tasks WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	var (
		out         jobStatusResponse
		completedAt time.Time
		paramsRaw   []byte
	)
	err = h.Pool.QueryRow(r.Context(), q, jobID, claims.UserID).Scan(
		&out.JobID, &out.Type, &out.ModelCode, &out.Status, &out.Progress,
		&out.CostCredits, &out.ErrorCode, &out.ErrorMessage,
		&out.CreatedAt, /* time.Time auto-formats; will overwrite below */
		&completedAt, &out.ExternalTask, &paramsRaw,
	)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "")
		return
	}
	// 上面 CreatedAt 实际上 Scan 到 time.Time 而不是 string,需要二次格式化.
	// 简化:重新查 created_at 单独格式化, 或用 to_char. 这里直接重写:
	var ca time.Time
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT created_at FROM aigc.tasks WHERE id=$1`, jobID,
	).Scan(&ca)
	out.CreatedAt = ca.UTC().Format(time.RFC3339)
	if !completedAt.IsZero() && completedAt.Year() > 1 {
		out.CompletedAt = completedAt.UTC().Format(time.RFC3339)
	}
	if len(paramsRaw) > 0 {
		_ = json.Unmarshal(paramsRaw, &out.Params)
	}

	// outputs
	outRows, err := h.Pool.Query(r.Context(), `
		SELECT idx, kind, storage_url, sha256, COALESCE(mime_type,''),
		       COALESCE(width, 0), COALESCE(height, 0),
		       COALESCE(duration_ms, 0), moderation_status
		FROM aigc.task_outputs WHERE task_id = $1 ORDER BY idx`, jobID)
	if err == nil {
		defer outRows.Close()
		for outRows.Next() {
			var o jobOutput
			if err := outRows.Scan(&o.Idx, &o.Kind, &o.StorageURL, &o.Sha256,
				&o.MimeType, &o.Width, &o.Height, &o.DurationMs, &o.Moderation); err == nil {
				out.Outputs = append(out.Outputs, o)
			}
		}
	}

	writeJSONOK(w, http.StatusOK, out)
}

// ─── inserts / publishes ────────────────────────────────

func (h *JobsHandler) insertTask(
	ctx context.Context, userID string, m *registry.Model,
	req *jobSubmitRequest, maxCredits int64,
) (string, error) {
	taskType := jobsTypeFromMode(m.Mode)
	paramsJSON, _ := json.Marshal(req.Params)
	if len(paramsJSON) == 0 {
		paramsJSON = []byte("{}")
	}
	var jobID string
	const q = `
		INSERT INTO aigc.tasks
			(user_id, type, model_code, provider_code, prompt, negative_prompt,
			 params, status, cost_credits, parent_sha)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, 'queued', $8, NULLIF($9,''))
		RETURNING id`
	err := h.Pool.QueryRow(ctx, q,
		userID, taskType, m.Code, m.Family, req.Prompt, req.NegativePrompt,
		paramsJSON, maxCredits, req.ParentSHA,
	).Scan(&jobID)
	return jobID, err
}

func (h *JobsHandler) publishSubmit(
	ctx context.Context, jobID, userID string, m *registry.Model,
	req *jobSubmitRequest, plaintext []byte, cred *registry.Credential, holdID string,
) {
	if h.Bus == nil {
		return
	}
	payload := map[string]any{
		"task_id":             jobID,
		"user_id":             userID,
		"type":                jobsTypeFromMode(m.Mode),
		"model_code":          m.Code,
		"provider_code":       m.Family,
		"prompt":              req.Prompt,
		"negative_prompt":     req.NegativePrompt,
		"params":              req.Params,
		"parent_sha":          req.ParentSHA,
		"hold_id":             holdID,
		"credential_api_key":  string(plaintext),
		"credential_base_url": cred.BaseURL,
		"credential_headers":  cred.HeaderOverride,
		"credential_last4":    last4OfBytes(plaintext),
	}
	if err := h.Bus.Publish(ctx, "aigc.task.submit", payload); err != nil {
		h.logger().Warn("model-relay /v1/jobs publish failed",
			"job_id", jobID, "err", err)
	}
}

func (h *JobsHandler) releaseHold(ctx context.Context, holdID, reason string) {
	if h.Billing == nil || holdID == "" {
		return
	}
	if err := h.Billing.Release(ctx, holdID); err != nil {
		h.logger().Warn("hold release failed",
			"hold_id", holdID, "reason", reason, "err", err)
	}
}

// ─── enums / utils ────────────────────────────────────

// isJobsModeSupported 判断哪些 model.mode 走 /v1/jobs.
func isJobsModeSupported(m string) bool {
	switch m {
	case "image_generation", "video_generation", "digital_human",
		"audio_speech", "audio_transcription", "hotparse":
		return true
	}
	return false
}

// last4OfBytes returns the last 4 chars of a key for safe logging.
// Empty when key is shorter than 4 bytes.
func last4OfBytes(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	return string(b[len(b)-4:])
}

// jobsTypeFromMode 把 model.mode 翻译成 aigc.tasks.type 列表里的字面量.
func jobsTypeFromMode(mode string) string {
	switch mode {
	case "image_generation":
		return "image"
	case "video_generation":
		return "video"
	case "digital_human":
		return "digital_human"
	case "hotparse":
		return "hotparse"
	default:
		// audio_speech/audio_transcription 暂走 image type (aigc.tasks.type
		// 列只接 image|video|digital_human|hotparse, 段 3.4 后扩枚举).
		return "image"
	}
}

// ─── tiny JSON helpers (本文件局部, 不污染 messages.go) ───

func decodeJobJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_json", err.Error())
		return false
	}
	return true
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

func writeJSONOK(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
