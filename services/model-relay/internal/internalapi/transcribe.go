// transcribe.go — POST /v1/internal/transcribe (服务间 STT 调用入口).
//
// 与 /v1/internal/generations / /v1/internal/chat 同构。aigc worker(爆款解析
// HotparseProvider)需要把视频音轨转写成文字。按 I6,worker 不直 import STT SDK
// —— 经 model-relay。本端点复用对外 /v1/audio/transcriptions 的
// *api.TranscriptionsHandler,计费落到 user。
//
// 与 chat/generations 的差异:STT 请求体多为 multipart/form-data(音频文件
// bytes,Whisper 同步路径),无法像 JSON 那样 peek user_id。故 user_id 走请求头
// X-Internal-User-Id 传入。Content-Type(含 multipart boundary)原样透传给内层
// dispatch —— 这样 multipart(Whisper 同步)与 application/json(paraformer 异步,
// audio_url)两条路径都能透明转发。
//
// 请求:
//
//	Header: Authorization: Bearer <internal token>
//	        X-Internal-User-Id: <uuid>        // 必填, 注入 claims → 计费归此 user
//	        X-Request-Id: <task-id>           // 可选, Hold 幂等
//	        Content-Type: multipart/form-data | application/json
//	Body:   multipart(file=@audio, model=whisper-1, ...) 或 JSON({model, audio_url})
//
// 返回:原样转发 {text, language, duration}。

package internalapi

import (
	"bytes"
	"io"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// handleTranscribe 复用对外 *api.TranscriptionsHandler 执行实际 STT。
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.Transcriptions == nil {
		http.Error(w, "transcription handler not wired", http.StatusServiceUnavailable)
		return
	}
	userID := r.Header.Get("X-Internal-User-Id")
	if userID == "" {
		http.Error(w, "X-Internal-User-Id required", http.StatusBadRequest)
		return
	}
	// 音频文件可达 25MB(Whisper 上限),留 26MB buffer。
	raw, err := io.ReadAll(io.LimitReader(r.Body, 26*1024*1024))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// 注入 claims — 下游计费按这个 user 走(audio_transcription ref_type)。
	ctx := bauth.WithClaims(r.Context(), &bauth.Claims{UserID: userID})
	inner, ierr := http.NewRequestWithContext(ctx, http.MethodPost,
		"/internal/transcribe", bytes.NewReader(raw))
	if ierr != nil {
		http.Error(w, "build inner request", http.StatusInternalServerError)
		return
	}
	// 透传 Content-Type(含 multipart boundary)让内层按 multipart/JSON 分发。
	inner.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	if rid := r.Header.Get("X-Request-Id"); rid != "" {
		inner.Header.Set("X-Request-Id", rid)
	}

	rec := &bufferRecorder{}
	s.Transcriptions.ServeHTTP(rec, inner)
	rec.flushTo(w)
}
