// chat.go — POST /v1/internal/chat (服务间 LLM 调用入口).
//
// 与 /v1/internal/generations 同构(见 generations.go):aigc worker(爆款解析
// HotparseProvider)需要用 LLM 把 STT 转写文本拆解成 文案/钩子/分镜/标签。按 I6
// 不变量,worker 绝不直 import LLM SDK —— 必须经 model-relay。本端点让 worker
// 用内部 bearer token 调用,复用对外 /v1/messages 的 *api.MessagesHandler
// (Anthropic 兼容),计费 Hold/Settle 落到 body 里显式带的 user_id。
//
// body(Anthropic messages 字段的并集 + 路由字段):
//
//	{
//	  "user_id":   "uuid",          // 必填, 注入 claims → 计费归此 user
//	  "idempotency_key": "task-id", // 可选, 透传 X-Request-Id 让 Hold 幂等
//	  "model": "claude-opus-4-8",
//	  "messages": [...], "max_tokens": 2048,
//	  "stream": false               // ★ 必须 false: bufferRecorder 仅一次性 JSON
//	}
//
// 返回:原样转发 MessagesHandler 的 Anthropic 兼容响应 {content:[...], usage:{...}}。

package internalapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// chatRoutePeek 只挑路由 + 身份字段;其余字段原样转发给 MessagesHandler。
type chatRoutePeek struct {
	UserID         string `json:"user_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// handleChat 复用对外 *api.MessagesHandler 执行实际 LLM 调用。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if s.Messages == nil {
		http.Error(w, "chat handler not wired", http.StatusServiceUnavailable)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var peek chatRoutePeek
	if uerr := json.Unmarshal(raw, &peek); uerr != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if peek.UserID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	// 注入 claims — 下游计费 Hold/Settle 按这个 user 走(与用户直连一致)。
	ctx := bauth.WithClaims(r.Context(), &bauth.Claims{UserID: peek.UserID})
	inner, ierr := http.NewRequestWithContext(ctx, http.MethodPost,
		"/internal/chat", bytes.NewReader(raw))
	if ierr != nil {
		http.Error(w, "build inner request", http.StatusInternalServerError)
		return
	}
	inner.Header.Set("Content-Type", "application/json")
	// 幂等:NATS 重投同一任务时,X-Request-Id=task_id 让 Hold 去重不双扣。
	if peek.IdempotencyKey != "" {
		inner.Header.Set("X-Request-Id", peek.IdempotencyKey)
	}

	rec := &bufferRecorder{}
	s.Messages.ServeHTTP(rec, inner)
	rec.flushTo(w)
}
