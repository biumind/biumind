// embeddings.go — POST /v1/embeddings (OpenAI 兼容).
//
// v0.3 M2: 同步路径 (无 streaming). 工作链路:
//   1. 解 OpenAI body: {model, input, encoding_format?, dimensions?, user?}
//   2. ModeRouter.ResolveForEmbed 拿 ResolveOutput + EmbedAdaptor
//      (mode 必须 == 'embedding', 否则 ErrModeMismatch)
//   3. adaptor.TranslateEmbedRequest → upstream HTTP
//   4. http.Client.Do → 上游 JSON 响应
//   5. adaptor.ParseEmbedResponse → canonical EmbedResponse
//   6. JSON 序列化 → 客户端 (透传 OpenAI shape)
//
// M2 边界:
//   - 无 quota / billing (后续 patch: 按 prompt_tokens 计费, 跟 chat 共用 hub.tpm)
//   - 无 retry / channel failover — 单 attempt
//   - 错误码: mode_mismatch / modality_unsupported / upstream_status

package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/router"
)

// EmbeddingsHandler — 接 /v1/embeddings.
type EmbeddingsHandler struct {
	ModeRouter     *router.ModeRouter
	HTTPClient     *http.Client
	Logger         *slog.Logger
	PlanFromClaims func(r *http.Request) registry.Plan
	// Billing — M5 接入. nil 时跳过计费 (灰度场景).
	Billing *ModalityBilling
}

// openaiEmbedRequest — 对外 OpenAI wire shape. Input 用 json.RawMessage
// 因为 OpenAI 接受 string / []string 两种形态, 我们透传给 adaptor 让它
// 决定怎么传.
type openaiEmbedRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format,omitempty"`
	Dimensions     int             `json:"dimensions,omitempty"`
	User           string          `json:"user,omitempty"`
}

func (h *EmbeddingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.ModeRouter == nil {
		writeJSONErr(w, http.StatusInternalServerError, "no_mode_router", "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	startedAt := time.Now()
	logger := h.logger()

	var req openaiEmbedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Model == "" || len(req.Input) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "missing_field",
			"model + input 必填 (OpenAI 兼容)")
		return
	}
	logger.DebugContext(r.Context(), "embeddings: request",
		"model", req.Model, "inputs", len(req.Input))

	// JWT claims → user id + plan (ModeRouter Resolver 用).
	claims, _ := bauth.ClaimsFrom(r.Context())
	var userID uuid.UUID
	if claims != nil && claims.UserID != "" {
		if id, err := uuid.Parse(claims.UserID); err == nil {
			userID = id
		}
	}
	var userPlan registry.Plan
	if h.PlanFromClaims != nil {
		userPlan = h.PlanFromClaims(r)
	}

	out, embedA, err := h.ModeRouter.ResolveForEmbed(r.Context(), router.ResolveInput{
		ModelCode: req.Model,
		UserID:    userID,
		UserPlan:  userPlan,
		RequestID: r.Header.Get("X-Request-ID"),
	})
	if err != nil {
		status, code := classifyEmbedResolveErr(err)
		writeJSONErr(w, status, code, err.Error())
		logger.Warn("embeddings resolve failed",
			"model", req.Model, "code", code, "err", err)
		return
	}

	creds := &provider.Credentials{
		APIKey:  string(out.Plaintext),
		BaseURL: out.BaseURL,
	}
	if len(out.Header) > 0 {
		creds.Extra = make(map[string]string, len(out.Header))
		for k, v := range out.Header {
			creds.Extra[k] = v
		}
	}

	// 解 input — 支持 string / []string. RawMessage 透传给 adaptor.
	var input any
	if jerr := json.Unmarshal(req.Input, &input); jerr != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_input", jerr.Error())
		return
	}

	embedReq := &provider.EmbedRequest{
		Model:          out.UpstreamModel,
		Input:          input,
		EncodingFormat: req.EncodingFormat,
		Dimensions:     req.Dimensions,
		User:           req.User,
	}

	// ─── M5 计费 preflight ─────────────────────────────────────
	// 估 max prompt tokens: input 字节数 / 4 (跟 chat estimatePromptTokensFromCanon
	// 同算法). batch 形态 ([]string) 用所有元素总长度.
	estTok := estimateEmbedTokens(input)
	var billState *modalityState
	if h.Billing != nil {
		billState = h.startBilling(w, r, creds, req.Model, out.Provider, estTok)
		if billState == nil {
			return // 402 已写
		}
		defer h.finalizeBilling(billState)
	}

	upstream, err := embedA.TranslateEmbedRequest(r.Context(), embedReq, creds)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "translate_failed", err.Error())
		return
	}
	logger.DebugContext(r.Context(), "embeddings: upstream request",
		"model", req.Model, "upstream_url", upstream.URL.String(),
		"body_bytes", upstream.ContentLength)

	httpClient := h.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	upstreamStart := time.Now()
	resp, err := httpClient.Do(upstream)
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()
	logger.DebugContext(r.Context(), "embeddings: upstream response",
		"model", req.Model, "status", resp.StatusCode,
		"latency_ms", time.Since(upstreamStart).Milliseconds())

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))

	if resp.StatusCode >= 400 {
		writeJSONErr(w, resp.StatusCode, "upstream_status",
			truncateForLog(body, 500))
		logger.Warn("embeddings upstream error",
			"model", req.Model, "status", resp.StatusCode,
			"body", truncateForLog(body, 200))
		return
	}

	parsed, perr := embedA.ParseEmbedResponse(body)
	if perr != nil {
		writeJSONErr(w, http.StatusBadGateway, "parse_failed", perr.Error())
		return
	}

	// ─── M5 计费 finalize 数据准备 ──────────────────────────────
	if billState != nil && billState.Pricing != nil {
		billState.Success = true
		billState.ActualAmount = billState.Pricing.CalculateEmbed(int64(parsed.Usage.PromptTokens))
	} else if billState != nil {
		billState.Success = true
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(parsed); encErr != nil {
		logger.Warn("embeddings encode response", "err", encErr)
	}

	dim := 0
	if len(parsed.Data) > 0 {
		dim = len(parsed.Data[0].Embedding)
	}
	logger.Info("embeddings done",
		"model", req.Model, "vectors", len(parsed.Data), "dim", dim,
		"prompt_tokens", parsed.Usage.PromptTokens,
		"latency_ms", time.Since(startedAt).Milliseconds())
}

// startBilling — 包装 ModalityBilling.Preflight.
// embedding 单价: ¥0.4-2/M token cost (4000-20000 millicents/M),
// markup ≤ 5x, 上界一个 token 算 100000 millicents/M = 0.1 millicent/token.
// 用 estPromptTok × 0.1 + 100 millicents 兜底, 4x safety.
func (h *EmbeddingsHandler) startBilling(
	w http.ResponseWriter, r *http.Request, creds *provider.Credentials,
	modelCode string, prov *registry.Provider, estPromptTok int64,
) *modalityState {
	if estPromptTok < 100 {
		estPromptTok = 100
	}
	// 4x safety 确保 max ≥ actual
	maxAmount := estPromptTok*100000/1_000_000*4 + 100

	st, cont := h.Billing.Preflight(w, r, creds, PreflightOpts{
		ModelCode:      modelCode,
		ProviderCode:   prov.Code,
		PricingRefType: "embedding",
		HoldRefType:    "embedding_request",
		MaxAmount:      maxAmount,
		RefID:          r.Header.Get("X-Request-Id"),
		TTLSeconds:     60,
	})
	if !cont {
		return nil
	}
	return st
}

func (h *EmbeddingsHandler) finalizeBilling(st *modalityState) {
	h.Billing.Finalize(st, "embedding-request")
}

// estimateEmbedTokens — 字节数 / 4 粗估 (跟 chat 同算法).
// input 是 string 或 []string; RawMessage 解出 any 后兜底.
func estimateEmbedTokens(input any) int64 {
	if input == nil {
		return 1024
	}
	var n int64
	switch v := input.(type) {
	case string:
		n = int64(len(v))
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				n += int64(len(s))
			}
		}
	}
	if n <= 0 {
		return 1024
	}
	return n / 4
}

// classifyEmbedResolveErr — ModeRouter 错误 → HTTP status + errcode.
func classifyEmbedResolveErr(err error) (int, string) {
	switch {
	case errors.Is(err, router.ErrModeMismatch):
		return http.StatusBadRequest, "mode_mismatch"
	case errors.Is(err, router.ErrModalityNotSupported):
		return http.StatusServiceUnavailable, "modality_unsupported"
	case errors.Is(err, provider.ErrNotImplemented):
		return http.StatusNotImplemented, "not_implemented"
	default:
		return http.StatusBadGateway, "resolve_failed"
	}
}

func (h *EmbeddingsHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}
