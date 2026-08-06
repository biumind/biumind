// audio_speech.go — POST /v1/audio/speech (OpenAI 兼容 TTS).
//
// 工作链路:
//   1. 解 OpenAI body: {model, input, voice, response_format, speed, ...}
//   2. ModeRouter.ResolveForSpeech 拿 ResolveOutput + SpeechAdaptor
//      (mode 必须 == 'audio_speech', 否则 ErrModeMismatch)
//   3. adaptor.TranslateSpeechRequest → upstream HTTP request
//   4. http.Client.Do 拿 chunked SSE 响应
//   5. adaptor.StreamAudioFrames 解 SSE → AudioFrame channel
//   6. 流式 chunked transfer 把 AudioFrame.Data 推给客户端 (audio/mpeg
//      / audio/wav / audio/L16 等), Content-Type 跟 ResponseFormat 走
//
// M1 边界:
//   - 无 quota / billing / retry — 单 attempt, 失败直接 502
//   - 无 BYOK — 走标准 channel 解析
//   - 仅支持 HTTP 路径 (cosyvoice 非实时); WS 路径 (cosyvoice-v3.5-flash 等)
//     OpenSpeechWebSocket 当前返 ErrNotImplemented → handler 返 501
//
// 计费 / quota / 重试在 M2 加 (按字符数计费而非 token).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// SpeechHandler 接 /v1/audio/speech.
//
// PlanFromClaims — 可选, 从 claims 拿用户 plan (用 ModeRouter 走 plan gate);
// 不传时所有用户当 free.
type SpeechHandler struct {
	ModeRouter *router.ModeRouter
	HTTPClient *http.Client
	Logger     *slog.Logger
	// PlanFromClaims — model-relay main.go 注入, 桥接 plan.PlanFromRequest.
	// 函数签名简单, 避免 api 包反向依赖 plan.
	PlanFromClaims func(r *http.Request) registry.Plan
	// Billing — M5 接入. nil 时跳过计费.
	Billing *ModalityBilling
}

// openaiSpeechRequest — 对外 OpenAI 兼容形态. 非 OpenAI 字段 (sample_rate)
// 跟 OpenAI 现有 /v1/audio/speech 也兼容 (Azure / Together 等扩展过).
type openaiSpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format,omitempty"` // mp3/opus/aac/flac/wav/pcm
	Speed          float64 `json:"speed,omitempty"`
	SampleRate     int     `json:"sample_rate,omitempty"`
}

func (h *SpeechHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	var req openaiSpeechRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Model == "" || req.Input == "" || req.Voice == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_field",
			"model, input, voice 必填 (OpenAI 兼容)")
		return
	}
	logger.DebugContext(r.Context(), "audio.speech: request",
		"model", req.Model, "voice", req.Voice,
		"input_bytes", len(req.Input), "format", req.ResponseFormat)

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

	out, speechA, err := h.ModeRouter.ResolveForSpeech(r.Context(), router.ResolveInput{
		ModelCode: req.Model,
		UserID:    userID,
		UserPlan:  userPlan,
		RequestID: r.Header.Get("X-Request-ID"),
	})
	if err != nil {
		status, code := classifySpeechResolveErr(err)
		writeJSONErr(w, status, code, err.Error())
		logger.Warn("audio.speech resolve failed",
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

	speechReq := &provider.SpeechRequest{
		Model:          out.UpstreamModel,
		Input:          req.Input,
		Voice:          req.Voice,
		ResponseFormat: req.ResponseFormat,
		Speed:          req.Speed,
		SampleRate:     req.SampleRate,
	}

	// ─── M5 计费 preflight ─────────────────────────────────────
	// 按字符数计费. max amount 用 input 字符数 × ¥10/千字符上界 (1000 millicents/char).
	// utf8 中文字符占 3 字节, 但 dashscope characters 字段以"字符"为单位,
	// 用 utf8.RuneCountInString 准确数字符.
	var billState *modalityState
	if h.Billing != nil {
		estChars := int64(len([]rune(req.Input)))
		var cont bool
		billState, cont = h.Billing.Preflight(w, r, creds, PreflightOpts{
			ModelCode:      req.Model,
			ProviderCode:   out.Provider.Code,
			PricingRefType: "audio_speech",
			HoldRefType:    "audio_speech_request",
			MaxAmount:      estChars * 100, // ¥10/千字符 × markup 粗估上界
			RefID:          r.Header.Get("X-Request-Id"),
			TTLSeconds:     120,
		})
		if !cont {
			return
		}
		defer h.Billing.Finalize(billState, "audio-speech-request")
	}

	upstream, err := speechA.TranslateSpeechRequest(r.Context(), speechReq, creds)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "translate_failed", err.Error())
		return
	}

	// 上游 cosyvoice SSE 可能跑很长 — 单段长文本可达数十秒. 用 cancellable
	// ctx, 客户端断开时上游也会 reset.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	upstream = upstream.WithContext(ctx)

	httpClient := h.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 0} // streaming, no timeout
	}
	resp, err := httpClient.Do(upstream)
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		writeJSONErr(w, resp.StatusCode, "upstream_status", string(body))
		logger.Warn("audio.speech upstream error",
			"model", req.Model, "status", resp.StatusCode, "body", truncateForLog(body, 200))
		return
	}

	frames, err := speechA.StreamAudioFrames(ctx, resp.Body)
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "stream_open_failed", err.Error())
		return
	}

	// 流式响应: chunked transfer, audio/mpeg (mp3 默认) 或对应 mime.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONErr(w, http.StatusInternalServerError,
			"no_streaming", "ResponseWriter does not support flush")
		return
	}
	// header 推迟到第一帧真有 Data 时才写, 这样上游错误(adaptor 通过
	// AudioFrame.Err 透传)还能转成 502 + 错误体, 而不是用户看到 200 +
	// 0 字节的伪成功.
	w.Header().Set("Content-Type", contentTypeForFormat(req.ResponseFormat))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // 让 nginx 不要 buffer 流

	var totalBytes int64
	var totalChars int64
	var lastErr error
	headerWritten := false
	for f := range frames {
		if f.Err != nil {
			lastErr = f.Err
			if !headerWritten {
				// 第一帧就是错 — 还能改 status, 502 + 真错误体.
				writeJSONErr(w, http.StatusBadGateway,
					"upstream_stream_error", f.Err.Error())
				return
			}
			// 已经在流中了, header 200 发了, 只能截断流. 客户端会得到
			// 不完整的 mp3, 由 lastErr 进入 audio.speech done 日志告警.
			break
		}
		if len(f.Data) == 0 {
			// AudioFrame 有 Final 标记但 Data 空, 仍要看 Characters
			if f.Characters > 0 {
				totalChars = int64(f.Characters)
			}
			continue
		}
		if !headerWritten {
			w.WriteHeader(http.StatusOK)
			headerWritten = true
		}
		if _, werr := w.Write(f.Data); werr != nil {
			lastErr = werr
			break
		}
		totalBytes += int64(len(f.Data))
		// dashscope SSE 在最终帧带 usage.characters; M5.E 让 adaptor 把它
		// 填到 AudioFrame.Characters. 多帧累计取最后值 (适配未来累加形态).
		if f.Characters > 0 {
			totalChars = int64(f.Characters)
		}
		flusher.Flush()
	}
	// 上游 SSE 流空跑完没产出任何 audio (上游静默 / 计费走 0) — 之前会
	// 退化成 200 OK + 0 字节, 现在转 502 让用户立刻看到上游异常.
	if !headerWritten && lastErr == nil {
		writeJSONErr(w, http.StatusBadGateway, "upstream_no_audio",
			"stream finished without any audio frames")
		lastErr = errors.New("upstream_no_audio")
	}

	// finalize 数据 — characters 真实值由 adaptor 透传 (M5.E); 没拿到时
	// fallback 到 input 字符数 (粗估, 跟 prompt 长度差不多).
	if billState != nil {
		billState.Success = lastErr == nil
		if totalChars == 0 {
			totalChars = int64(len([]rune(req.Input)))
		}
		if billState.Pricing != nil {
			billState.ActualAmount = billState.Pricing.CalculateSpeech(totalChars)
		}
	}

	logger.Info("audio.speech done",
		"model", req.Model, "voice", req.Voice,
		"format", req.ResponseFormat, "bytes", totalBytes,
		"chars", totalChars,
		"latency_ms", time.Since(startedAt).Milliseconds(),
		"err", lastErr)
}

// classifySpeechResolveErr 把 ModeRouter 错误映射到 HTTP status + 稳定 errcode.
func classifySpeechResolveErr(err error) (int, string) {
	switch {
	case errors.Is(err, router.ErrModeMismatch):
		// model.mode != audio_speech — 客户端选错模型.
		return http.StatusBadRequest, "mode_mismatch"
	case errors.Is(err, router.ErrModalityNotSupported):
		// adaptor 不实现 SpeechAdaptor (例如把 openai chat-only adaptor 绑到
		// audio_speech 模型上) — 配置错误, 503.
		return http.StatusServiceUnavailable, "modality_unsupported"
	case errors.Is(err, provider.ErrNotImplemented):
		return http.StatusNotImplemented, "not_implemented"
	default:
		// resolver 错误 (model 不存在 / plan 门槛 / 无可用 channel) → 502.
		return http.StatusBadGateway, "resolve_failed"
	}
}

// contentTypeForFormat — OpenAI / dashscope 的 format 字段 → HTTP MIME.
// dashscope 默认 mp3 = audio/mpeg.
func contentTypeForFormat(fmt string) string {
	switch fmt {
	case "wav":
		return "audio/wav"
	case "pcm":
		return "audio/L16" // 16-bit linear PCM, OpenAI 也用这个 mime
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "mp3", "":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}

func truncateForLog(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

func (h *SpeechHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// 防 fmt 包未引用 (Errorf 没用):
var _ = fmt.Sprintf
