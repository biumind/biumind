// images.go — POST /v1/images/generations (OpenAI 兼容).
//
// v0.3 M3: 把 OpenAI 同步语义包成 sync facade 给客户端用. 路径选择:
//
//   adaptor implements AsyncImageAdaptor (dashscope wanx)
//     → submit + poll loop, 5s 间隔, 默认总超时 5min
//     → 返客户端时已经是 OSS URL
//
//   adaptor implements ImageAdaptor only (DALL-E / 自部署 SD)
//     → 单次 HTTP, ParseImageResponse 解 URL 透传
//
// 时序图 (async 路径):
//
//   client → POST /v1/images/generations
//     model-relay → ModeRouter.ResolveForImage 拿 ImageAdaptor
//     model-relay → submit (X-DashScope-Async: enable)
//     dashscope ⇒ {output:{task_id:"t1"}}
//     loop:
//       sleep 5s
//       model-relay → GET /api/v1/tasks/t1
//       dashscope ⇒ {task_status:"RUNNING"}    → continue
//       dashscope ⇒ {task_status:"SUCCEEDED",results:[{url}]}  → break
//   model-relay → 200 {created, data:[{url}]} → client
//
// M3 边界:
//   - 无 quota / billing — 单 attempt, 失败 502
//   - 总超时 5min, poll 间隔 5s; 后续按 model.expected_duration 自适应
//   - 错误码: mode_mismatch / modality_unsupported / submit_failed /
//     poll_failed / poll_timeout / upstream_status

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/router"
)

// 默认 poll 节奏 — image 任务通常 10-30s 完成, video 1-3min;
// 5s 间隔 + 5min 超时是 image 的合理默认.
const (
	defaultImagePollInterval = 5 * time.Second
	defaultImagePollTimeout  = 5 * time.Minute
)

// ImagesHandler — 接 /v1/images/generations.
type ImagesHandler struct {
	ModeRouter     *router.ModeRouter
	HTTPClient     *http.Client
	Logger         *slog.Logger
	PlanFromClaims func(r *http.Request) registry.Plan
	// Billing — M5 接入. nil 时跳过计费.
	Billing *ModalityBilling

	// 可选 — 测试 / 加速场景覆盖默认 poll 节奏.
	PollInterval time.Duration
	PollTimeout  time.Duration
}

// openaiImageRequest — 对外 OpenAI /v1/images/generations wire shape.
type openaiImageRequest struct {
	Model              string   `json:"model"`
	Prompt             string   `json:"prompt"`
	NegativePrompt     string   `json:"negative_prompt,omitempty"` // dashscope wanx / SD; dall-e 忽略
	Seed               int      `json:"seed,omitempty"`
	N                  int      `json:"n,omitempty"`
	Size               string   `json:"size,omitempty"`
	AspectRatio        string   `json:"aspect_ratio,omitempty"` // size 空时 adaptor 按它查尺寸表
	Resolution         string   `json:"resolution,omitempty"`
	ReferenceImageURLs []string `json:"reference_image_urls,omitempty"`
	Quality            string   `json:"quality,omitempty"`
	Style              string   `json:"style,omitempty"`
	ResponseFormat     string   `json:"response_format,omitempty"` // url|b64_json
	User               string   `json:"user,omitempty"`
}

func (h *ImagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	var req openaiImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Model == "" || req.Prompt == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_field",
			"model + prompt 必填 (OpenAI 兼容)")
		return
	}
	logger.DebugContext(r.Context(), "images.generations: request",
		"model", req.Model, "prompt_len", len(req.Prompt),
		"size", req.Size, "n", req.N)

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

	out, imageA, err := h.ModeRouter.ResolveForImage(r.Context(), router.ResolveInput{
		ModelCode: req.Model,
		UserID:    userID,
		UserPlan:  userPlan,
		RequestID: r.Header.Get("X-Request-ID"),
	})
	if err != nil {
		status, code := classifyImageResolveErr(err)
		writeJSONErr(w, status, code, err.Error())
		logger.Warn("images resolve failed", "model", req.Model, "code", code, "err", err)
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

	imageReq := &provider.ImageRequest{
		Model:              out.UpstreamModel,
		Prompt:             req.Prompt,
		NegativePrompt:     req.NegativePrompt,
		Seed:               req.Seed,
		N:                  req.N,
		Size:               req.Size,
		AspectRatio:        req.AspectRatio,
		Resolution:         req.Resolution,
		ReferenceImageURLs: req.ReferenceImageURLs,
		Quality:            req.Quality,
		Style:              req.Style,
		ResponseFormat:     req.ResponseFormat,
		User:               req.User,
	}

	// ─── M5 计费 preflight ─────────────────────────────────────
	// image 走 aigc_image ref_type 复用现有定价表 (跟 /v1/jobs 共享).
	// max amount 用 N×单价上限粗估 (单张 ¥1 cost × markup ≈ 30000 millicents).
	var billState *modalityState
	if h.Billing != nil {
		n := int64(req.N)
		if n <= 0 {
			n = 1
		}
		var cont bool
		billState, cont = h.Billing.Preflight(w, r, creds, PreflightOpts{
			ModelCode:      req.Model,
			ProviderCode:   out.Provider.Code,
			PricingRefType: "aigc_image",
			HoldRefType:    "image_request",
			MaxAmount:      n * 30000, // 单张 ¥3 上界 (含 markup)
			RefID:          r.Header.Get("X-Request-Id"),
			// 幂等:内部生成路径 (aigc worker) 传 X-Request-Id=task_id,
			// NATS 重投时 Hold 去重不双扣;对外客户端不带则为空 (维持原行为)。
			IdempotencyKey: r.Header.Get("X-Request-Id"),
			TTLSeconds:     300, // image 通常 10-30s, 给 5min 余量
		})
		if !cont {
			return
		}
		defer h.Billing.Finalize(billState, "image-request")
	}

	// submit
	upstream, err := imageA.TranslateImageRequest(r.Context(), imageReq, creds)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "translate_failed", err.Error())
		return
	}

	httpClient := h.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	submitResp, err := httpClient.Do(upstream)
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer submitResp.Body.Close()
	submitBody, _ := io.ReadAll(io.LimitReader(submitResp.Body, 4*1024*1024))

	if submitResp.StatusCode >= 400 {
		writeJSONErr(w, submitResp.StatusCode, "upstream_status",
			truncateForLog(submitBody, 500))
		logger.Warn("images submit upstream error",
			"model", req.Model, "status", submitResp.StatusCode,
			"body", truncateForLog(submitBody, 200))
		return
	}

	// 路径选择: AsyncImageAdaptor 走 submit + poll, 否则同步 ParseImageResponse.
	asyncA, isAsync := imageA.(provider.AsyncImageAdaptor)
	if !isAsync {
		// 同步路径 (DALL-E / 自部署 SD): submit body 已经是最终响应.
		result, perr := imageA.ParseImageResponse(submitBody)
		if perr != nil {
			writeJSONErr(w, http.StatusBadGateway, "parse_failed", perr.Error())
			return
		}
		writeImageOK(w, result, logger, req.Model, "sync", startedAt)
		return
	}

	// 异步路径: 解 task_id, 然后 poll.
	taskID, perr := asyncA.ParseImageSubmit(submitBody)
	if perr != nil {
		writeJSONErr(w, http.StatusBadGateway, "submit_failed", perr.Error())
		return
	}
	logger.InfoContext(r.Context(), "images.generations: submitted",
		"model", req.Model, "task_id", taskID)

	result, perr := h.pollUntilDone(r, asyncA, taskID, creds)
	if perr != nil {
		// 客户端断开时不写 (broken pipe), 否则 502 透传 err.
		if errors.Is(perr, r.Context().Err()) {
			logger.Info("images: client disconnected during poll",
				"model", req.Model, "task_id", taskID)
			return
		}
		writeJSONErr(w, http.StatusBadGateway, classifyPollErr(perr), perr.Error())
		logger.Warn("images poll failed",
			"model", req.Model, "task_id", taskID, "err", perr)
		return
	}

	// finalize 数据
	if billState != nil {
		billState.Success = true
		if billState.Pricing != nil {
			billState.ActualAmount = billState.Pricing.CalculateImage(int64(len(result.Data)))
		}
	}

	writeImageOK(w, result, logger, req.Model, "async", startedAt)
}

// classifyPollErr — 把 pollUntilDone 的错误归类成稳定 errcode 让客户端
// 区分超时 / 上游失败 / 协议错误.
func classifyPollErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "poll timeout"):
		return "poll_timeout"
	case strings.HasPrefix(msg, "poll status"):
		return "poll_upstream_status"
	case strings.HasPrefix(msg, "build poll") || strings.HasPrefix(msg, "poll http"):
		return "poll_failed"
	default:
		// dashscope 业务错误 (e.g. "dashscope InternalError: 内部异常") 也走这里
		return "task_failed"
	}
}

// pollUntilDone — 循环 poll 直到 succeeded/failed/超时. 失败/超时时直接
// 写 response 并返 err 让 caller 知道; 成功返 (result, nil).
func (h *ImagesHandler) pollUntilDone(
	r *http.Request, asyncA provider.AsyncImageAdaptor, taskID string,
	creds *provider.Credentials,
) (*provider.ImageResponse, error) {
	interval := h.PollInterval
	if interval <= 0 {
		interval = defaultImagePollInterval
	}
	timeout := h.PollTimeout
	if timeout <= 0 {
		timeout = defaultImagePollTimeout
	}
	deadline := time.Now().Add(timeout)

	httpClient := h.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	for time.Now().Before(deadline) {
		select {
		case <-r.Context().Done():
			// 客户端断开 — 不写 response (已经 broken pipe), 但留日志
			return nil, r.Context().Err()
		case <-time.After(interval):
		}

		pollReq, err := asyncA.BuildPollRequest(r.Context(), taskID, creds)
		if err != nil {
			// 客户端响应没写过, 写错误.
			// 注意: 这里 r 是原 ServeHTTP 的 r, w 在 caller. 我们这里只
			// 返 err, caller 没拿到 ResponseWriter, 所以这层不能写. 改设计:
			// pollUntilDone 不写 response, 返 err 让 caller 写.
			return nil, fmt.Errorf("build poll: %w", err)
		}
		pollResp, err := httpClient.Do(pollReq)
		if err != nil {
			return nil, fmt.Errorf("poll http: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(pollResp.Body, 4*1024*1024))
		_ = pollResp.Body.Close()

		if pollResp.StatusCode >= 400 {
			return nil, fmt.Errorf("poll status %d: %s",
				pollResp.StatusCode, truncateForLog(body, 200))
		}

		status, result, err := asyncA.ParsePollResponse(body)
		if err != nil {
			// failed: 上游业务错误
			return nil, err
		}
		switch status {
		case "succeeded":
			return result, nil
		case "running":
			continue // poll 下一轮
		default:
			return nil, fmt.Errorf("unknown poll status %q", status)
		}
	}
	return nil, fmt.Errorf("poll timeout after %v (task_id=%s)", timeout, taskID)
}

// writeImageOK — 序列化 OpenAI 兼容响应 {created, data:[]}.
func writeImageOK(
	w http.ResponseWriter, result *provider.ImageResponse,
	logger *slog.Logger, model, path string, startedAt time.Time,
) {
	if result.Created == 0 {
		result.Created = time.Now().Unix()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(result); encErr != nil {
		logger.Warn("images encode response", "err", encErr)
		return
	}
	logger.Info("images.generations done",
		"model", model, "path", path,
		"images", len(result.Data),
		"latency_ms", time.Since(startedAt).Milliseconds())
}

func classifyImageResolveErr(err error) (int, string) {
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

func (h *ImagesHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}
