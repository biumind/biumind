// generations.go — POST /v1/internal/generations (服务间异步生成执行入口).
//
// 段 3.6:让 AIGC 生成真正过 model-relay(I6 单一 egress)。aigc worker 不再
// 直连 dashscope/volcengine,而是把 NATS 任务转成对本端点的调用;model-relay
// 复用对外 /v1/images|videos/generations 的全部逻辑(ResolveModel → 凭证解密
// → 计费 Hold → 真·submit/poll 上游 → Settle),把产物 URL 返回给 worker。
//
// 与对外端点的差异:
//   - 鉴权:内部 bearer token(IDENTITY_INTERNAL_TOKEN),不是终端用户 JWT。
//   - 用户身份:body 显式带 user_id;handler 注入成 claims,让下游计费/plan
//     gating 跟用户直连时完全一致(PlanFromRequest 按 user_id resolve 真实 plan)。
//
// 实现刻意复用现有 *api.ImagesHandler / *api.VideosHandler,而不是抽取共享
// core —— 零改动已验证的对外端点,无回归面。请求体直接转发(image/video 的
// 请求结构体会忽略 user_id/type/idempotency_key 等未知 JSON 字段)。
//
// body(image/video 字段的并集 + 路由字段):
//
//	{
//	  "user_id":   "uuid",          // 必填, 注入 claims
//	  "type":      "image"|"video", // 必填, 路由到对应 handler
//	  "idempotency_key": "task-id", // 可选, 透传成 X-Request-Id 让 Hold 幂等
//	  "model": "...", "prompt": "...",
//	  "n": 1, "size": "1024*1024",          // image
//	  "duration": 5, "resolution": "1080P", // video
//	  ...
//	}
//
// 返回:原样转发内层 handler 的 OpenAI 兼容响应 {created, data:[{url,...}]}
// 或错误体。worker 据此下载产物转存 MinIO。

package internalapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// generateRoutePeek 只挑路由 + 身份字段;其余字段原样转发给内层 handler。
type generateRoutePeek struct {
	UserID         string `json:"user_id"`
	Type           string `json:"type"`
	IdempotencyKey string `json:"idempotency_key"`
}

// handleGenerate 复用对外 image/video handler 执行实际生成。
func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if s.Images == nil || s.Videos == nil {
		http.Error(w, "generation handlers not wired", http.StatusServiceUnavailable)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var peek generateRoutePeek
	if uerr := json.Unmarshal(raw, &peek); uerr != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if peek.UserID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	// 注入 claims — 下游 Preflight 计费按这个 user 走 Hold,PlanFromRequest
	// 按 user_id resolve 真实 plan(plan-gating 与用户直连一致)。
	ctx := bauth.WithClaims(r.Context(), &bauth.Claims{UserID: peek.UserID})
	inner, ierr := http.NewRequestWithContext(ctx, http.MethodPost,
		"/internal/generate", bytes.NewReader(raw))
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
	switch peek.Type {
	case "image":
		s.Images.ServeHTTP(rec, inner)
	case "video":
		s.Videos.ServeHTTP(rec, inner)
	default:
		// digital_human / hotparse 等暂无 relay adaptor — 让 worker 回落直连。
		http.Error(w, "unsupported generation type: "+peek.Type, http.StatusNotImplemented)
		return
	}

	rec.flushTo(w)
}

// bufferRecorder 是一个最小 http.ResponseWriter,缓存内层 handler 的响应再
// 整体转发(image/video 响应是一次性 JSON,无需流式)。避免在生产代码引入
// net/http/httptest。
type bufferRecorder struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func (b *bufferRecorder) Header() http.Header {
	if b.header == nil {
		b.header = http.Header{}
	}
	return b.header
}

func (b *bufferRecorder) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferRecorder) Write(p []byte) (int, error) { return b.body.Write(p) }

func (b *bufferRecorder) flushTo(w http.ResponseWriter) {
	for k, vs := range b.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if b.status == 0 {
		b.status = http.StatusOK
	}
	w.WriteHeader(b.status)
	_, _ = w.Write(b.body.Bytes())
}
