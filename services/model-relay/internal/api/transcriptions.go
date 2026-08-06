// transcriptions.go — POST /v1/audio/transcriptions (OpenAI 兼容 ASR).
//
// 双路径设计:
//
// 1. multipart/form-data (M6 同步, OpenAI Whisper / Groq / SiliconFlow):
//    Content-Type: multipart/form-data
//    Form: file=@audio.mp3, model=whisper-1, language?, prompt?,
//          response_format?, temperature?
//    handler 直接 multipart 透传给 sync TranscribeAdaptor.
//
// 2. application/json (M6.5 异步, dashscope paraformer-v2 / sensevoice):
//    Content-Type: application/json
//    Body: {"model":"paraformer-v2", "audio_url":"https://...", "language"?, ...}
//    handler 类型断言 AsyncTranscribeAdaptor → submit + poll + 二次 fetch.
//
// 客户端选哪条:
//   - 已有公网 https URL (例: 用户上传 OSS / brain /v1/files presign-put)
//     → JSON 路径, 适合 paraformer (单价低 + 中文最强)
//   - 客户端只有 audio bytes 不想上传 → multipart 路径, 适合 Whisper
//
// 计费:
//   - 同步: input audio file_size estimation (无上游 duration 透传)
//   - 异步: dashscope poll response 含 properties.original_duration_in_milliseconds
//          → 上游真实秒数计费 (按 per_kchar 暂复用 CalculateSpeech, 单位换算)

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/router"
)

// 音频文件上限 25MB (跟 OpenAI 一致, Whisper API 硬限制).
const maxAudioBytes = 25 * 1024 * 1024

// async ASR poll 节奏 — paraformer 通常 30s-2min 完成.
const (
	defaultTranscribePollInterval = 5 * time.Second
	defaultTranscribePollTimeout  = 10 * time.Minute
)

// TranscriptionsHandler — 接 /v1/audio/transcriptions.
type TranscriptionsHandler struct {
	ModeRouter     *router.ModeRouter
	HTTPClient     *http.Client
	Logger         *slog.Logger
	PlanFromClaims func(r *http.Request) registry.Plan
	Billing        *ModalityBilling

	PollInterval time.Duration
	PollTimeout  time.Duration
}

// transcribeJSONRequest — async 路径 JSON body.
type transcribeJSONRequest struct {
	Model          string `json:"model"`
	AudioURL       string `json:"audio_url"`
	Language       string `json:"language,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

func (h *TranscriptionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.ModeRouter == nil {
		writeJSONErr(w, http.StatusInternalServerError, "no_mode_router", "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 按 Content-Type 分发. multipart 走老路径, JSON 走异步路径.
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		h.serveMultipart(w, r)
		return
	}
	if strings.HasPrefix(ct, "application/json") {
		h.serveJSON(w, r)
		return
	}
	writeJSONErr(w, http.StatusUnsupportedMediaType, "bad_content_type",
		"expected multipart/form-data (sync Whisper) or application/json (async paraformer)")
}

// ─── sync multipart 路径 (Whisper / Groq / 自部署 faster-whisper) ─────

func (h *TranscriptionsHandler) serveMultipart(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	logger := h.logger()

	if err := r.ParseMultipartForm(maxAudioBytes); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_multipart", err.Error())
		return
	}

	model := r.FormValue("model")
	if model == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_field", "model 必填")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "missing_file",
			"file (multipart) 必填: -F file=@audio.mp3")
		return
	}
	defer file.Close()
	if header.Size > maxAudioBytes {
		writeJSONErr(w, http.StatusRequestEntityTooLarge, "file_too_large",
			"audio file > 25MB (Whisper hard limit)")
		return
	}

	audioBytes, rerr := io.ReadAll(io.LimitReader(file, maxAudioBytes))
	if rerr != nil {
		writeJSONErr(w, http.StatusBadRequest, "audio_read_failed", rerr.Error())
		return
	}

	language := r.FormValue("language")
	prompt := r.FormValue("prompt")
	respFormat := r.FormValue("response_format")
	temperature := 0.0
	if t := r.FormValue("temperature"); t != "" {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			temperature = v
		}
	}

	logger.DebugContext(r.Context(), "transcriptions: multipart request",
		"model", model, "file_size", len(audioBytes),
		"filename", header.Filename, "language", language, "format", respFormat)

	out, transcribeA, creds, billState, ok := h.resolveAndPreflight(w, r, model, int64(len(audioBytes)))
	if !ok {
		return
	}
	if billState != nil {
		defer h.Billing.Finalize(billState, "audio-transcription-request")
	}

	transcribeReq := &provider.TranscribeRequest{
		Model:          out.UpstreamModel,
		Audio:          bytes.NewReader(audioBytes),
		AudioFilename:  header.Filename,
		Language:       language,
		Prompt:         prompt,
		ResponseFormat: respFormat,
		Temperature:    temperature,
	}

	upstream, err := transcribeA.TranslateTranscribeRequest(r.Context(), transcribeReq, creds)
	if err != nil {
		// 如果 adaptor 不支持 multipart (例: dashscope), 引导客户端切 JSON 路径.
		if errors.Is(err, provider.ErrNotImplemented) {
			writeJSONErr(w, http.StatusBadRequest, "multipart_not_supported",
				"this model requires async file_url path; POST application/json with audio_url instead")
			return
		}
		writeJSONErr(w, http.StatusBadRequest, "translate_failed", err.Error())
		return
	}

	httpClient := h.httpClient()
	resp, err := httpClient.Do(upstream)
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))

	if resp.StatusCode >= 400 {
		writeJSONErr(w, resp.StatusCode, "upstream_status",
			truncateForLog(body, 500))
		logger.Warn("transcriptions upstream error",
			"model", model, "status", resp.StatusCode,
			"body", truncateForLog(body, 200))
		return
	}

	// response_format=text 直接 plain 透传.
	if respFormat == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		setTranscribeBilling(billState, int64(len(audioBytes))/16384, 0)
		logger.Info("transcriptions done (text)",
			"model", model, "audio_bytes", len(audioBytes),
			"text_len", len(body),
			"latency_ms", time.Since(startedAt).Milliseconds())
		return
	}

	parsed, perr := transcribeA.ParseTranscribeResponse(body)
	if perr != nil {
		writeJSONErr(w, http.StatusBadGateway, "parse_failed", perr.Error())
		return
	}
	setTranscribeBilling(billState, int64(parsed.Duration), int64(len(audioBytes))/16384)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(parsed); encErr != nil {
		logger.Warn("transcriptions encode response", "err", encErr)
	}
	logger.Info("transcriptions done",
		"model", model, "audio_bytes", len(audioBytes),
		"text_len", len(parsed.Text), "duration_s", parsed.Duration,
		"language", parsed.Language,
		"latency_ms", time.Since(startedAt).Milliseconds())
}

// ─── async JSON 路径 (paraformer-v2 / sensevoice) ─────

func (h *TranscriptionsHandler) serveJSON(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	logger := h.logger()

	var req transcribeJSONRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Model == "" || req.AudioURL == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_field",
			"model + audio_url 必填 (audio_url 必须公网 https URL)")
		return
	}
	logger.DebugContext(r.Context(), "transcriptions: json request",
		"model", req.Model, "audio_url", req.AudioURL,
		"language", req.Language, "format", req.ResponseFormat)

	// async 路径 maxAmount 估算: audio_url 文件大小未知, 给 1h 上界 = 3600s × ¥0.001/秒
	const asyncMaxAmount = int64(36000)
	out, transcribeA, creds, billState, ok := h.resolveAndPreflightAsync(w, r, req.Model, asyncMaxAmount)
	if !ok {
		return
	}
	if billState != nil {
		defer h.Billing.Finalize(billState, "audio-transcription-request")
	}

	asyncA, isAsync := transcribeA.(provider.AsyncTranscribeAdaptor)
	if !isAsync {
		writeJSONErr(w, http.StatusBadRequest, "json_not_supported",
			"this model only supports multipart upload; use POST multipart/form-data with file=@audio.mp3 instead")
		return
	}

	transcribeReq := &provider.TranscribeRequest{
		Model:          out.UpstreamModel,
		FileURLs:       []string{req.AudioURL},
		Language:       req.Language,
		Prompt:         req.Prompt,
		ResponseFormat: req.ResponseFormat,
	}

	upstream, err := asyncA.TranslateTranscribeRequest(r.Context(), transcribeReq, creds)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "translate_failed", err.Error())
		return
	}

	httpClient := h.httpClient()
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
		logger.Warn("transcriptions submit error",
			"model", req.Model, "status", submitResp.StatusCode,
			"body", truncateForLog(submitBody, 200))
		return
	}

	taskID, perr := asyncA.ParseTranscribeSubmit(submitBody)
	if perr != nil {
		writeJSONErr(w, http.StatusBadGateway, "submit_failed", perr.Error())
		return
	}
	logger.InfoContext(r.Context(), "transcriptions: submitted",
		"model", req.Model, "task_id", taskID, "audio_url", req.AudioURL)

	result, perr := h.pollTranscribeUntilDone(r, asyncA, taskID, creds, httpClient)
	if perr != nil {
		if errors.Is(perr, r.Context().Err()) {
			logger.Info("transcriptions: client disconnected during poll",
				"model", req.Model, "task_id", taskID)
			return
		}
		writeJSONErr(w, http.StatusBadGateway, classifyTranscribePollErr(perr), perr.Error())
		logger.Warn("transcriptions poll failed",
			"model", req.Model, "task_id", taskID, "err", perr)
		return
	}

	setTranscribeBilling(billState, int64(result.Duration), 0)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(result); encErr != nil {
		logger.Warn("transcriptions encode response", "err", encErr)
	}
	logger.Info("transcriptions done (async)",
		"model", req.Model, "task_id", taskID,
		"text_len", len(result.Text), "duration_s", result.Duration,
		"latency_ms", time.Since(startedAt).Milliseconds())
}

// pollTranscribeUntilDone — submit 后循环 poll 直到 succeeded/failed/超时.
// 处理 dashscope 的 redirect 形态: succeeded 时拿到 transcription_url 再
// 二次 GET 拉真正的 ASR JSON.
func (h *TranscriptionsHandler) pollTranscribeUntilDone(
	r *http.Request, asyncA provider.AsyncTranscribeAdaptor,
	taskID string, creds *provider.Credentials, httpClient *http.Client,
) (*provider.TranscribeResponse, error) {
	interval := h.PollInterval
	if interval <= 0 {
		interval = defaultTranscribePollInterval
	}
	timeout := h.PollTimeout
	if timeout <= 0 {
		timeout = defaultTranscribePollTimeout
	}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(interval):
		}

		pollReq, err := asyncA.BuildTranscribePollRequest(r.Context(), taskID, creds)
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

		status, inline, resultURL, err := asyncA.ParseTranscribePollResponse(body)
		if err != nil {
			return nil, err
		}
		switch status {
		case "running":
			continue
		case "succeeded":
			if inline != nil {
				return inline, nil
			}
			// dashscope redirect 形态: 二次 GET transcription_url 拿真 JSON.
			if resultURL == "" {
				return nil, fmt.Errorf("succeeded but neither inline result nor resultURL")
			}
			return h.fetchTranscriptionResult(r, asyncA, resultURL, httpClient)
		default:
			return nil, fmt.Errorf("unknown poll status %q", status)
		}
	}
	return nil, fmt.Errorf("poll timeout after %v (task_id=%s)", timeout, taskID)
}

// fetchTranscriptionResult — GET transcription_url, 转 canonical TranscribeResponse.
// dashscope OSS public URL 不需要 Authorization header.
func (h *TranscriptionsHandler) fetchTranscriptionResult(
	r *http.Request, asyncA provider.AsyncTranscribeAdaptor,
	resultURL string, httpClient *http.Client,
) (*provider.TranscribeResponse, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resultURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build fetch: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch transcription: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch transcription status %d: %s",
			resp.StatusCode, truncateForLog(body, 200))
	}
	return asyncA.ParseTranscriptionResult(body)
}

// ─── 共享 helpers ─────────────────────────────────────────────────────

func (h *TranscriptionsHandler) resolveAndPreflight(
	w http.ResponseWriter, r *http.Request, model string, audioBytes int64,
) (*router.ResolveOutput, provider.TranscribeAdaptor, *provider.Credentials, *modalityState, bool) {
	out, transcribeA, creds, ok := h.resolve(w, r, model)
	if !ok {
		return nil, nil, nil, nil, false
	}
	billState := h.preflightSync(w, r, creds, model, out.Provider.Code, audioBytes)
	if billState == nil && h.Billing != nil {
		// preflight 失败已写 402, caller 直接 return.
		return nil, nil, nil, nil, false
	}
	return out, transcribeA, creds, billState, true
}

func (h *TranscriptionsHandler) resolveAndPreflightAsync(
	w http.ResponseWriter, r *http.Request, model string, maxAmount int64,
) (*router.ResolveOutput, provider.TranscribeAdaptor, *provider.Credentials, *modalityState, bool) {
	out, transcribeA, creds, ok := h.resolve(w, r, model)
	if !ok {
		return nil, nil, nil, nil, false
	}
	billState := h.preflightAsync(w, r, creds, model, out.Provider.Code, maxAmount)
	if billState == nil && h.Billing != nil {
		return nil, nil, nil, nil, false
	}
	return out, transcribeA, creds, billState, true
}

func (h *TranscriptionsHandler) resolve(
	w http.ResponseWriter, r *http.Request, model string,
) (*router.ResolveOutput, provider.TranscribeAdaptor, *provider.Credentials, bool) {
	logger := h.logger()
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

	out, transcribeA, err := h.ModeRouter.ResolveForTranscribe(r.Context(), router.ResolveInput{
		ModelCode: model,
		UserID:    userID,
		UserPlan:  userPlan,
		RequestID: r.Header.Get("X-Request-ID"),
	})
	if err != nil {
		status, code := classifyTranscribeResolveErr(err)
		writeJSONErr(w, status, code, err.Error())
		logger.Warn("transcriptions resolve failed",
			"model", model, "code", code, "err", err)
		return nil, nil, nil, false
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
	return out, transcribeA, creds, true
}

func (h *TranscriptionsHandler) preflightSync(
	w http.ResponseWriter, r *http.Request, creds *provider.Credentials,
	model, providerCode string, audioBytes int64,
) *modalityState {
	if h.Billing == nil {
		return nil
	}
	estDurationSec := audioBytes / 4096
	if estDurationSec < 1 {
		estDurationSec = 1
	}
	st, cont := h.Billing.Preflight(w, r, creds, PreflightOpts{
		ModelCode:      model,
		ProviderCode:   providerCode,
		PricingRefType: "audio_transcription",
		HoldRefType:    "audio_speech_request",
		MaxAmount:      estDurationSec * 1000,
		RefID:          r.Header.Get("X-Request-Id"),
		TTLSeconds:     120,
	})
	if !cont {
		return nil
	}
	return st
}

func (h *TranscriptionsHandler) preflightAsync(
	w http.ResponseWriter, r *http.Request, creds *provider.Credentials,
	model, providerCode string, maxAmount int64,
) *modalityState {
	if h.Billing == nil {
		return nil
	}
	st, cont := h.Billing.Preflight(w, r, creds, PreflightOpts{
		ModelCode:      model,
		ProviderCode:   providerCode,
		PricingRefType: "audio_transcription",
		HoldRefType:    "audio_speech_request",
		MaxAmount:      maxAmount,
		RefID:          r.Header.Get("X-Request-Id"),
		TTLSeconds:     900, // async 任务长, 给 15min
	})
	if !cont {
		return nil
	}
	return st
}

func (h *TranscriptionsHandler) httpClient() *http.Client {
	if h.HTTPClient != nil {
		return h.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// setTranscribeBilling — 计费 finalize 数据填. duration 真值优先, 0 时
// fallback 到 estDurationSec.
func setTranscribeBilling(st *modalityState, durationSec, estDurationSec int64) {
	if st == nil {
		return
	}
	st.Success = true
	if durationSec < 1 {
		durationSec = estDurationSec
	}
	if durationSec < 1 {
		durationSec = 1
	}
	if st.Pricing != nil {
		// 复用 CalculateSpeech (pricing seed 用 per_second cost_basis 时
		// CostInputPerUnit 即 millicents/s, 这里 chars 当 seconds 算).
		st.ActualAmount = st.Pricing.CalculateSpeech(durationSec * 1000)
	}
}

func classifyTranscribeResolveErr(err error) (int, string) {
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

func classifyTranscribePollErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "poll timeout"):
		return "poll_timeout"
	case strings.HasPrefix(msg, "poll status"):
		return "poll_upstream_status"
	case strings.HasPrefix(msg, "build poll") || strings.HasPrefix(msg, "poll http"):
		return "poll_failed"
	case strings.HasPrefix(msg, "fetch transcription"):
		return "fetch_failed"
	default:
		return "task_failed"
	}
}

func (h *TranscriptionsHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}
