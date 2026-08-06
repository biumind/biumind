// videos.go — POST /v1/videos/generations (BiuMind 自定 OpenAI-style 端点).
//
// v0.3 M4: 视频生成 sync facade. OpenAI 没有官方 /v1/videos/generations
// (sora 闭源), 我们沿用 /v1/images/generations 命名风格新建一个端点.
// 客户端语义:
//   POST → 等待 1-3min → 返 {data:[{url, cover_image_url}]}
//
// 跟 images.go 同结构: AsyncVideoAdaptor (dashscope wanx-video) 走 submit
// + poll, sync VideoAdaptor 走单次 HTTP. 视频默认 poll 间隔 8s, 总超时
// 10min (cosyvoice 5s/5min 不够; wanx 5s 视频通常 30-90s, 10s 视频 1-2min,
// kling 1080p 3-5min).
//
// M4 边界: 无 quota / billing — 单 attempt 直通; 后续跟 image 一起按
// per_video_second 接 hub.tpm + Hold/Settle.

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

// 视频任务比图像长得多, 默认 8s 间隔 + 10min 总超时.
const (
	defaultVideoPollInterval = 8 * time.Second
	defaultVideoPollTimeout  = 10 * time.Minute
)

// VideosHandler — 接 /v1/videos/generations.
type VideosHandler struct {
	ModeRouter     *router.ModeRouter
	HTTPClient     *http.Client
	Logger         *slog.Logger
	PlanFromClaims func(r *http.Request) registry.Plan
	// Billing — M5 接入. nil 时跳过计费.
	Billing *ModalityBilling

	PollInterval time.Duration
	PollTimeout  time.Duration
}

// videoRequest — 对外 wire shape. 综合 OpenAI dall-e 风格 + 视频特有字段
// (duration / first_frame_url / aspect_ratio).
type videoRequest struct {
	Model           string   `json:"model"`
	Prompt          string   `json:"prompt"`
	NegativePrompt  string   `json:"negative_prompt,omitempty"`
	N               int      `json:"n,omitempty"`
	Size            string   `json:"size,omitempty"`
	AspectRatio     string   `json:"aspect_ratio,omitempty"`
	Resolution      string   `json:"resolution,omitempty"`
	Duration        int      `json:"duration,omitempty"` // 秒
	FirstFrameURL   string   `json:"first_frame_url,omitempty"`
	LastFrameURL    string   `json:"last_frame_url,omitempty"`
	ReferenceImages []string `json:"reference_image_urls,omitempty"`
	Seed            int      `json:"seed,omitempty"`
	User            string   `json:"user,omitempty"`
}

func (h *VideosHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	var req videoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Model == "" || req.Prompt == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_field",
			"model + prompt 必填")
		return
	}
	logger.DebugContext(r.Context(), "videos.generations: request",
		"model", req.Model, "prompt_len", len(req.Prompt),
		"size", req.Size, "duration", req.Duration,
		"has_first_frame", req.FirstFrameURL != "",
		"has_last_frame", req.LastFrameURL != "")

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

	out, videoA, err := h.ModeRouter.ResolveForVideo(r.Context(), router.ResolveInput{
		ModelCode: req.Model,
		UserID:    userID,
		UserPlan:  userPlan,
		RequestID: r.Header.Get("X-Request-ID"),
	})
	if err != nil {
		status, code := classifyVideoResolveErr(err)
		writeJSONErr(w, status, code, err.Error())
		logger.Warn("videos resolve failed", "model", req.Model, "code", code, "err", err)
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

	videoReq := &provider.VideoRequest{
		Model:              out.UpstreamModel,
		Prompt:             req.Prompt,
		NegativePrompt:     req.NegativePrompt,
		FirstFrameURL:      req.FirstFrameURL,
		LastFrameURL:       req.LastFrameURL,
		ReferenceImageURLs: req.ReferenceImages,
		Size:               req.Size,
		AspectRatio:        req.AspectRatio,
		Resolution:         req.Resolution,
		DurationSeconds:    req.Duration,
		Seed:               req.Seed,
		User:               req.User,
	}

	// ─── M5 计费 preflight ─────────────────────────────────────
	// video 走 aigc_video ref_type, 按秒计费. max amount 用 duration × 单价上限.
	// duration 没填默认 5s; 单价上限粗估 ¥10/秒 (含 markup).
	var billState *modalityState
	if h.Billing != nil {
		dur := int64(req.Duration)
		if dur <= 0 {
			dur = 5
		}
		var cont bool
		billState, cont = h.Billing.Preflight(w, r, creds, PreflightOpts{
			ModelCode:      req.Model,
			ProviderCode:   out.Provider.Code,
			PricingRefType: "aigc_video",
			HoldRefType:    "video_request",
			MaxAmount:      dur * 100000, // 单秒 ¥10 上界 (含 markup, video 比 image 贵 10x)
			RefID:          r.Header.Get("X-Request-Id"),
			// 幂等:内部生成路径 (aigc worker) 传 X-Request-Id=task_id,
			// NATS 重投时 Hold 去重不双扣;对外客户端不带则为空 (维持原行为)。
			IdempotencyKey: r.Header.Get("X-Request-Id"),
			TTLSeconds:     900, // 15min, 超时由 PollTimeout 兜底
		})
		if !cont {
			return
		}
		defer h.Billing.Finalize(billState, "video-request")
	}

	upstream, err := videoA.TranslateVideoRequest(r.Context(), videoReq, creds)
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
		logger.Warn("videos submit upstream error",
			"model", req.Model, "status", submitResp.StatusCode,
			"body", truncateForLog(submitBody, 200))
		return
	}

	asyncA, isAsync := videoA.(provider.AsyncVideoAdaptor)
	if !isAsync {
		// 同步路径 — 假想未来 sync video 模型. 当前所有 provider 都是 async.
		result, perr := videoA.ParseVideoResponse(submitBody)
		if perr != nil {
			writeJSONErr(w, http.StatusBadGateway, "parse_failed", perr.Error())
			return
		}
		writeVideoOK(w, result, logger, req.Model, "sync", startedAt)
		return
	}

	taskID, perr := asyncA.ParseVideoSubmit(submitBody)
	if perr != nil {
		writeJSONErr(w, http.StatusBadGateway, "submit_failed", perr.Error())
		return
	}
	logger.InfoContext(r.Context(), "videos.generations: submitted",
		"model", req.Model, "task_id", taskID,
		"poll_interval", h.pollInterval(),
		"poll_timeout", h.pollTimeout())

	result, perr := h.pollVideoUntilDone(r, asyncA, taskID, creds)
	if perr != nil {
		if errors.Is(perr, r.Context().Err()) {
			logger.Info("videos: client disconnected during poll",
				"model", req.Model, "task_id", taskID)
			return
		}
		writeJSONErr(w, http.StatusBadGateway, classifyVideoPollErr(perr), perr.Error())
		logger.Warn("videos poll failed",
			"model", req.Model, "task_id", taskID, "err", perr)
		return
	}

	// finalize 数据 — duration 真值优先用 result.Data[0].DurationMs
	// (dashscope poll response 可能透传); 否则 fallback 到 caller 传的 req.Duration.
	if billState != nil {
		billState.Success = true
		actualSec := int64(req.Duration)
		if len(result.Data) > 0 && result.Data[0].DurationMs > 0 {
			actualSec = int64(result.Data[0].DurationMs / 1000)
			if actualSec == 0 {
				actualSec = 1
			}
		}
		if actualSec <= 0 {
			actualSec = 5 // 默认 5s
		}
		if billState.Pricing != nil {
			billState.ActualAmount = billState.Pricing.CalculateVideo(actualSec)
		}
	}

	writeVideoOK(w, result, logger, req.Model, "async", startedAt)
}

// pollVideoUntilDone — 跟 pollUntilDone (image) 同结构, 只是间隔 / 超时
// 默认值不同.
func (h *VideosHandler) pollVideoUntilDone(
	r *http.Request, asyncA provider.AsyncVideoAdaptor, taskID string,
	creds *provider.Credentials,
) (*provider.VideoResponse, error) {
	interval := h.pollInterval()
	timeout := h.pollTimeout()
	deadline := time.Now().Add(timeout)

	httpClient := h.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	for time.Now().Before(deadline) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(interval):
		}

		pollReq, err := asyncA.BuildVideoPollRequest(r.Context(), taskID, creds)
		if err != nil {
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

		status, result, err := asyncA.ParseVideoPollResponse(body)
		if err != nil {
			return nil, err
		}
		switch status {
		case "succeeded":
			return result, nil
		case "running":
			continue
		default:
			return nil, fmt.Errorf("unknown poll status %q", status)
		}
	}
	return nil, fmt.Errorf("poll timeout after %v (task_id=%s)", timeout, taskID)
}

func (h *VideosHandler) pollInterval() time.Duration {
	if h.PollInterval > 0 {
		return h.PollInterval
	}
	return defaultVideoPollInterval
}

func (h *VideosHandler) pollTimeout() time.Duration {
	if h.PollTimeout > 0 {
		return h.PollTimeout
	}
	return defaultVideoPollTimeout
}

// writeVideoOK — 序列化 OpenAI 兼容响应 {created, data:[{url, cover_image_url}]}.
func writeVideoOK(
	w http.ResponseWriter, result *provider.VideoResponse,
	logger *slog.Logger, model, path string, startedAt time.Time,
) {
	if result.Created == 0 {
		result.Created = time.Now().Unix()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(result); encErr != nil {
		logger.Warn("videos encode response", "err", encErr)
		return
	}
	logger.Info("videos.generations done",
		"model", model, "path", path,
		"videos", len(result.Data),
		"latency_ms", time.Since(startedAt).Milliseconds())
}

func classifyVideoResolveErr(err error) (int, string) {
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

func classifyVideoPollErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "poll timeout"):
		return "poll_timeout"
	case strings.HasPrefix(msg, "poll status"):
		return "poll_upstream_status"
	case strings.HasPrefix(msg, "build poll") || strings.HasPrefix(msg, "poll http"):
		return "poll_failed"
	default:
		return "task_failed"
	}
}

func (h *VideosHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}
