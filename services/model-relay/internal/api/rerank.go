// rerank.go — POST /v1/rerank (Cohere 兼容).
//
// v0.3 M2.5: 同步路径. 工作链路:
//   1. 解 Cohere body: {model, query, documents:[], top_n?, return_documents?}
//   2. ModeRouter.ResolveForRerank 拿 ResolveOutput + RerankAdaptor
//      (mode 必须 == 'rerank', 否则 ErrModeMismatch)
//   3. adaptor.TranslateRerankRequest → upstream HTTP
//   4. http.Client.Do → 上游 JSON
//   5. adaptor.ParseRerankResponse → canonical RerankResponse
//   6. JSON 序列化 → 客户端 (透传 Cohere shape)
//
// 客户端用法 (跟 Cohere SDK / SiliconFlow 文档一致):
//   curl POST /v1/rerank \
//     -H "Authorization: Bearer <jwt>" \
//     -d '{"model":"BAAI/bge-reranker-v2-m3","query":"...","documents":["a","b"]}'
//
// M2.5 边界: 无 quota / billing — 单 attempt 透传; 后续跟 embedding/speech
// 一起统一接 hub.tpm + Hold/Settle.

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

// RerankHandler — 接 /v1/rerank.
type RerankHandler struct {
	ModeRouter     *router.ModeRouter
	HTTPClient     *http.Client
	Logger         *slog.Logger
	PlanFromClaims func(r *http.Request) registry.Plan
	// Billing — M5 接入. nil 时跳过计费.
	Billing *ModalityBilling
}

// cohereRerankRequest — Cohere 标准 wire shape (SiliconFlow / Jina /
// Voyage / 新-API / TEI 都遵循).
type cohereRerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
}

func (h *RerankHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	var req cohereRerankRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Model == "" || req.Query == "" || len(req.Documents) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "missing_field",
			"model + query + documents 必填")
		return
	}
	logger.DebugContext(r.Context(), "rerank: request",
		"model", req.Model, "docs", len(req.Documents),
		"top_n", req.TopN, "return_documents", req.ReturnDocuments)

	// JWT claims → user id + plan.
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

	out, rerankA, err := h.ModeRouter.ResolveForRerank(r.Context(), router.ResolveInput{
		ModelCode: req.Model,
		UserID:    userID,
		UserPlan:  userPlan,
		RequestID: r.Header.Get("X-Request-ID"),
	})
	if err != nil {
		status, code := classifyRerankResolveErr(err)
		writeJSONErr(w, status, code, err.Error())
		logger.Warn("rerank resolve failed",
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

	rerankReq := &provider.RerankRequest{
		Model:           out.UpstreamModel,
		Query:           req.Query,
		Documents:       req.Documents,
		TopN:            req.TopN,
		ReturnDocuments: req.ReturnDocuments,
	}

	// ─── M5 计费 preflight ─────────────────────────────────────
	// rerank 单 query 实际成本 < ¥1 (Cohere unit ¥0.05, dashscope token
	// 几百 × ¥0.0001). 给 ¥5 = 50000 millicents 硬上限保留充足余地, 不必
	// 按文本长度精确估 — 上下界差太大反而易触发 insufficient_credits.
	const rerankMaxHold = int64(50000)
	var billState *modalityState
	if h.Billing != nil {
		var cont bool
		billState, cont = h.Billing.Preflight(w, r, creds, PreflightOpts{
			ModelCode:      req.Model,
			ProviderCode:   out.Provider.Code,
			PricingRefType: "rerank",
			HoldRefType:    "rerank_request",
			MaxAmount:      rerankMaxHold,
			RefID:          r.Header.Get("X-Request-Id"),
			TTLSeconds:     60,
		})
		if !cont {
			return // 402 已写
		}
		defer h.Billing.Finalize(billState, "rerank-request")
	}

	upstream, err := rerankA.TranslateRerankRequest(r.Context(), rerankReq, creds)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "translate_failed", err.Error())
		return
	}

	httpClient := h.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpClient.Do(upstream)
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))

	if resp.StatusCode >= 400 {
		writeJSONErr(w, resp.StatusCode, "upstream_status",
			truncateForLog(body, 500))
		logger.Warn("rerank upstream error",
			"model", req.Model, "status", resp.StatusCode,
			"body", truncateForLog(body, 200))
		return
	}

	parsed, perr := rerankA.ParseRerankResponse(body)
	if perr != nil {
		writeJSONErr(w, http.StatusBadGateway, "parse_failed", perr.Error())
		return
	}

	// finalize 数据
	if billState != nil {
		billState.Success = true
		if billState.Pricing != nil {
			billState.ActualAmount = billState.Pricing.CalculateRerank(
				int64(parsed.Meta.BilledUnits.SearchUnits))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(parsed); encErr != nil {
		logger.Warn("rerank encode response", "err", encErr)
	}

	logger.Info("rerank done",
		"model", req.Model, "docs", len(req.Documents),
		"results", len(parsed.Results),
		"search_units", parsed.Meta.BilledUnits.SearchUnits,
		"latency_ms", time.Since(startedAt).Milliseconds())
}

func classifyRerankResolveErr(err error) (int, string) {
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

func (h *RerankHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}
