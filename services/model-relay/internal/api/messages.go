// Package api implements the HTTP handlers exposed by model-relay.
//
// Currently:
//
//	POST /v1/messages         Anthropic-compatible (streaming + non-streaming)
//
// (OpenAI compat / embeddings / admin API land in M2.)
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/biumind/biumind/packages/go-sdk/biu/quota"
	"github.com/biumind/biumind/services/model-relay/internal/billing"
	mrbyok "github.com/biumind/biumind/services/model-relay/internal/byok"
	"github.com/biumind/biumind/services/model-relay/internal/pricing"
	"github.com/biumind/biumind/services/model-relay/internal/relay/files"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/router"
)

// parseRetryAfter 解析上游 Retry-After header（R4-B）。支持整数秒（RFC 7231）
// 和 HTTP-date 两种形式；缺失/非法/过去时刻 → 0（让调用方回退默认 cooldown）。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// MessagesHandler is the AnthropicMessages-compatible endpoint.
// Caller must inject providerName + creds via context (auth + routing layer).
type MessagesHandler struct {
	Registry   *provider.Registry
	HTTPClient *http.Client
	// CredsResolver returns (provider, creds) given the request claims.
	// May also stamp router state onto r.Context() (e.g. selected channel)
	// so OnRequestComplete can read it back.
	CredsResolver func(r *http.Request, modelName string) (string, *provider.Credentials, *http.Request, error)
	// Limiter is optional. When set, the handler reports real token
	// usage to the `hub.tpm` bucket after each request finishes —
	// post-hoc accounting that gates *future* calls without aborting
	// the in-flight one. nil → no token accounting.
	Limiter quota.Limiter
	// OnRequestComplete fires after every relay attempt — success or
	// failure. main.go wires it to: supervisor.RecordSuccess/Failure on
	// the resolved channel + write a usage_log row with dual-currency
	// settlement. nil = no-op (preserves the env-driven test path).
	//
	// errCode is "" on success; populated with a stable token on failure
	// (e.g. "upstream_status", "translate_failed"). latency is wall-clock
	// from handler entry to last byte / first error.
	OnRequestComplete func(
		r *http.Request,
		modelName, providerName string,
		usage provider.Usage,
		latency time.Duration,
		success bool,
		errCode string,
		creditsCharged int64,
	)

	// Billing — chat 计费 (Hold/Settle/Release). nil 时跳过计费, 与 W0 行为一致.
	// 见 messages_billing.go preflightBilling / finalizeBilling.
	Billing *billing.Client
	// BYOK — 用户自带 Key. 命中时跳过 Billing 路径, 平台不扣费.
	BYOK *mrbyok.Client

	Logger *slog.Logger
}

func (h *MessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	startedAt := time.Now()
	var canon provider.Request
	if err := json.NewDecoder(r.Body).Decode(&canon); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}
	if h.Logger != nil {
		h.Logger.DebugContext(r.Context(), "messages: request",
			"model", canon.Model, "stream", canon.Stream,
			"max_tokens", canon.MaxTokens, "messages", len(canon.Messages))
	}

	providerName, creds, scoped, err := h.CredsResolver(r, canon.Model)
	if err != nil {
		// P1: catalog 失败 (model_not_found 等) → fallback identity BYOK match.
		// 用户配了 custom BYOK 声明该 model 时, 用 identity 返回的 protocol
		// 选 adaptor + key + base_url, 标记 byok_matched 让 preflightBilling
		// 跳过 Get/Hold. 不命中回退原 err 处理 (quota / model_not_found 等).
		if h.BYOK != nil {
			if claims, ok := bauth.ClaimsFrom(r.Context()); ok && claims.UserID != "" {
				if k, merr := h.BYOK.Match(r.Context(), claims.UserID, canon.Model); merr == nil && k != nil {
					providerName = byokAdaptorName(k.Protocol)
					creds = &provider.Credentials{APIKey: k.APIKey, BaseURL: k.BaseURL}
					r = r.WithContext(ctxWithBYOKMatched(r.Context()))
					scoped = nil
					err = nil
				}
			}
		}
	}
	if err != nil {
		// 错误码区分:RPM/TPM 配额耗尽 vs 真实凭证问题。前者用 429 让
		// 客户端可以 retry,后者 502 是非可恢复。
		// "all channels failed" + caller buildCredsResolver 把 quota 错误
		// 归因到 ErrAllChannelsFailed,所以这里靠错误文本判别。
		errMsg := err.Error()
		isQuotaExhausted := strings.Contains(errMsg, "all channels failed") ||
			strings.Contains(errMsg, "quota exhausted") ||
			strings.Contains(errMsg, "rpm_exhausted")
		if isQuotaExhausted {
			writeJSONErr(w, http.StatusTooManyRequests, "channel_quota_exhausted",
				"upstream channel rpm/tpm exhausted; retry after a minute (check channel rpm_limit / tpm_limit in admin)")
			h.fireComplete(r, canon.Model, "", provider.Usage{}, time.Since(startedAt),
				false, "channel_quota_exhausted")
			return
		}
		// Resolve 失败细分 —— 客户端按 error.code 给降级文案(见
		// apps/client chat_controller._classifyStreamError),不再让一个笼统
		// 的 credential_resolve_failed 吞掉「模型已停用 / 不存在 / plan 不够 /
		// 无可用 channel」的差异。HTTP 仍用 502(resolve 失败非可恢复),只细化
		// code;errMsg 原样带上供日志 + 客户端兜底。
		code := "credential_resolve_failed"
		switch {
		case errors.Is(err, router.ErrModelDisabled):
			code = "model_disabled"
		case errors.Is(err, router.ErrModelNotFound):
			code = "model_not_found"
		case errors.Is(err, router.ErrModelHidden):
			code = "model_hidden_for_plan"
		case errors.Is(err, router.ErrNoActiveChannel):
			code = "model_no_channel"
		case errors.Is(err, router.ErrCredentialUnavailable):
			code = "model_credential_unavailable"
		}
		writeJSONErr(w, http.StatusBadGateway, code, errMsg)
		// Resolver-level failure — no channel was selected, so
		// OnRequestComplete has nothing to record against. Surface the
		// metric anyway with the specific code.
		h.fireComplete(r, canon.Model, "", provider.Usage{}, time.Since(startedAt),
			false, code)
		return
	}
	if scoped != nil {
		// Resolver may stamp ctx (channel handle, request id) for downstream.
		r = scoped
	}

	// chat 路径 — Registry.GetChat 在拿到 adaptor 同时强制 type-assert 到
	// 老 chat Adaptor 接口. dashscope 等 modality-only adaptor 进不来 (它们
	// 不该路由到 /v1/messages, mode 校验由 caller 的 credsResolver 兜底).
	adaptor, ok := h.Registry.GetChat(providerName)
	if !ok {
		writeJSONErr(w, http.StatusBadRequest, "unknown_provider", providerName)
		h.fireComplete(r, canon.Model, providerName, provider.Usage{}, time.Since(startedAt),
			false, "unknown_provider")
		return
	}

	// ─── Billing 预处理 (BYOK / Hold) ─────────────────────
	// 在请求上游之前: BYOK 命中跳过 Hold; 否则 Hold 预扣最大可能成本.
	// state 通过 ctx 传给 fireComplete 让它写最终 usage; defer 在结束时
	// Settle / Release / TouchUsed.
	promptTok := estimatePromptTokensFromCanon(&canon)
	maxComp := int64(canon.MaxTokens)
	state, cont := h.preflightBilling(w, r, creds, canon.Model, providerName, promptTok, maxComp)
	if !cont {
		// preflightBilling 已写响应 (e.g. 402 insufficient_credits)
		h.fireComplete(r, canon.Model, providerName, provider.Usage{},
			time.Since(startedAt), false, "insufficient_credits")
		return
	}
	r = r.WithContext(ctxWithState(r.Context(), state))
	defer h.finalizeBilling(state)

	// Stuff the inbound user JWT into ctx so adaptors can resolve
	// source.type=file via brain (POST /v1/files/{id}/presign-get).
	// model-relay + brain share the JWT verifier, so the user's own token
	// authorizes the brain call without service-to-service plumbing.
	ctx := r.Context()
	if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
		ctx = files.WithBearerToken(ctx, strings.TrimPrefix(ah, "Bearer "))
	}

	// 发上游的 model 必须是 channel.upstream_model(resolver 解析出的真实
	// 供应商模型名),不是客户端提交的 model code。两者常相等
	// (claude-opus-4-8),但当 admin 给 channel 配了不同的 upstream_model
	// (如 code=deepseek-v4-pro → upstream=DeepSeek-V4-Pro)时,发 code 会被
	// 上游拒为 "Invalid model name"。各 modality handler(embeddings/images/
	// rerank/...)早已用 out.UpstreamModel,chat 此前漏了这一步 —— 因为历史上
	// 每个 active chat 模型的 code 恰好等于 upstream_model,直到出现大小写不同
	// 的 DeepSeek-V4-Pro 才暴露。
	//
	// 仅替换发上游的副本:canon.Model 保持 code,供计费(pricing 按 code 查)、
	// usage_log.model_code、流式回客户端 echo 继续使用。BYOK fast-path 不
	// stamp ResolveOutput → out 取不到 → upReq.Model 保持用户自带模型名(正确)。
	upReq := canon
	if out, ok := router.ResolveOutputFrom(ctx); ok && out.UpstreamModel != "" {
		upReq.Model = out.UpstreamModel
	}

	upstream, err := adaptor.TranslateRequest(ctx, &upReq, creds)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "translate_request_failed", err.Error())
		h.fireComplete(r, canon.Model, providerName, provider.Usage{}, time.Since(startedAt),
			false, "translate_request_failed")
		return
	}
	if h.Logger != nil {
		var bodyBytes int64
		if upstream.ContentLength > 0 {
			bodyBytes = upstream.ContentLength
		}
		h.Logger.DebugContext(r.Context(), "messages: upstream request",
			"model", canon.Model, "provider", providerName,
			"upstream_url", upstream.URL.String(),
			"body_bytes", bodyBytes, "stream", canon.Stream)
	}

	upstreamStart := time.Now()
	resp, err := h.HTTPClient.Do(upstream)
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		h.fireComplete(r, canon.Model, providerName, provider.Usage{}, time.Since(startedAt),
			false, "upstream_error")
		return
	}
	defer resp.Body.Close()
	if h.Logger != nil {
		h.Logger.DebugContext(r.Context(), "messages: upstream response",
			"model", canon.Model, "provider", providerName,
			"status", resp.StatusCode,
			"latency_ms", time.Since(upstreamStart).Milliseconds())
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		writeJSONErr(w, resp.StatusCode, "upstream_status", string(body))
		// R4-B：把上游 status + Retry-After plumb 给 OnRequestComplete 做失败
		// 分类（429/401/402/5xx → 不同 cooldown/disable）。经 ctx 传，避免改
		// fireComplete 签名 + 全部调用点。
		scoped := r.WithContext(router.WithUpstreamFailure(r.Context(), router.UpstreamFailure{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}))
		h.fireComplete(scoped, canon.Model, providerName, provider.Usage{}, time.Since(startedAt),
			false, "upstream_status")
		return
	}

	if !canon.Stream {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSONErr(w, http.StatusBadGateway, "upstream_read", err.Error())
			return
		}
		canonical, err := adaptor.ParseResponse(body)
		if err != nil {
			writeJSONErr(w, http.StatusBadGateway, "parse_failed", err.Error())
			return
		}
		if h.Logger != nil {
			h.Logger.DebugContext(r.Context(), "messages: non-stream done",
				"model", canon.Model, "provider", providerName,
				"prompt_tokens", canonical.Usage.PromptTokens,
				"completion_tokens", canonical.Usage.CompletionTokens,
				"bytes", len(body),
				"total_ms", time.Since(startedAt).Milliseconds())
		}
		h.reportUsage(r, canonical.Usage, canon.Model, providerName, startedAt, true)
		h.fireComplete(r, canon.Model, providerName, canonical.Usage,
			time.Since(startedAt), true, "")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(canonical)
		return
	}

	// Streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONErr(w, http.StatusInternalServerError, "no_streaming", "ResponseWriter doesn't support flush")
		return
	}

	frames, err := adaptor.StreamAdapter(r.Context(), resp.Body)
	if err != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSEErr(w, flusher, err)
		return
	}

	// 客户端可选 Anthropic 原生 SSE 输出格式（biumindkit NewRelayEngine 用）：
	//   X-Stream-Format: anthropic   或   ?stream_format=anthropic
	// 默认走 unified frame SSE（brain / hub.go RelayProvider 用）。
	wantAnthropic := r.Header.Get("X-Stream-Format") == "anthropic" ||
		r.URL.Query().Get("stream_format") == "anthropic"
	if wantAnthropic {
		usage, ok, errCode := streamAsAnthropic(w, flusher, frames, canon.Model)
		h.reportUsage(r, usage, canon.Model, providerName, startedAt, ok)
		h.fireComplete(r, canon.Model, providerName, usage,
			time.Since(startedAt), ok, errCode)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	streamStart := time.Now()
	var (
		lastUsage    provider.Usage
		firstChunkAt time.Time
		deltaFrames  int
		thinkFrames  int
		toolFrames   int
	)
	for f := range frames {
		if firstChunkAt.IsZero() && (f.Type == provider.FrameDelta ||
			f.Type == provider.FrameThinking ||
			f.Type == provider.FrameToolCallStart) {
			firstChunkAt = time.Now()
			if h.Logger != nil {
				h.Logger.DebugContext(r.Context(), "messages: first chunk",
					"model", canon.Model, "provider", providerName,
					"first_chunk_ms", firstChunkAt.Sub(streamStart).Milliseconds(),
					"frame_type", f.Type)
			}
		}
		switch f.Type {
		case provider.FrameDelta:
			deltaFrames++
			writeSSE(w, flusher, "delta", map[string]any{"text": f.Delta})
		case provider.FrameThinking:
			thinkFrames++
			// Extended thinking — forwarded as its own SSE event so
			// Brain can build a separate ThinkingBlock. Falls back
			// to a no-op for older brain instances that don't know
			// the event name (SSE consumers ignore unknown events).
			writeSSE(w, flusher, "thinking", map[string]any{"text": f.Delta})
		case provider.FrameToolCallStart:
			toolFrames++
			writeSSE(w, flusher, "tool_call_start", map[string]any{
				"id": f.ToolCall.ID, "name": f.ToolCall.Name,
			})
		case provider.FrameToolCallArgs:
			writeSSE(w, flusher, "tool_call_args", map[string]any{
				"id": f.ToolCall.ID, "delta": f.ToolCall.ArgsDelta,
			})
		case provider.FrameToolCallEnd:
			writeSSE(w, flusher, "tool_call_end", map[string]any{"id": f.ToolCall.ID})
		case provider.FrameUsage:
			if f.Usage != nil {
				lastUsage = *f.Usage
			}
		case provider.FrameStop:
			writeSSE(w, flusher, "stop", map[string]any{"reason": f.Stop})
		case provider.FrameError:
			if h.Logger != nil {
				h.Logger.DebugContext(r.Context(), "messages: stream error",
					"model", canon.Model, "provider", providerName,
					"deltas", deltaFrames, "thinking", thinkFrames, "tool_calls", toolFrames,
					"total_ms", time.Since(streamStart).Milliseconds(),
					"err", f.Err)
			}
			writeSSEErr(w, flusher, f.Err)
			h.reportUsage(r, lastUsage, canon.Model, providerName, startedAt, false)
			h.fireComplete(r, canon.Model, providerName, lastUsage,
				time.Since(startedAt), false, "stream_error")
			return
		}
	}
	if h.Logger != nil {
		h.Logger.DebugContext(r.Context(), "messages: stream done",
			"model", canon.Model, "provider", providerName,
			"deltas", deltaFrames, "thinking", thinkFrames, "tool_calls", toolFrames,
			"prompt_tokens", lastUsage.PromptTokens,
			"completion_tokens", lastUsage.CompletionTokens,
			"total_ms", time.Since(streamStart).Milliseconds())
	}
	h.reportUsage(r, lastUsage, canon.Model, providerName, startedAt, true)
	h.fireComplete(r, canon.Model, providerName, lastUsage,
		time.Since(startedAt), true, "")
	writeSSE(w, flusher, "end", map[string]any{})
}

// fireComplete invokes the OnRequestComplete callback if set, and also
// records (usage, success, errCode) into the per-request state so the
// deferred finalizeBilling can settle/release the right amount.
func (h *MessagesHandler) fireComplete(
	r *http.Request,
	model, providerName string,
	usage provider.Usage,
	latency time.Duration,
	success bool,
	errCode string,
) {
	var creditsCharged int64
	if st := stateFromCtx(r.Context()); st != nil {
		st.Usage = usage
		st.Success = success
		st.ErrCode = errCode
		// Compute the same amount finalizeBilling will Settle, so the
		// usage_log row carries the credits for this call (single formula
		// site — see requestState.settleCredits).
		creditsCharged = st.settleCredits()
	}
	if h.OnRequestComplete == nil {
		return
	}
	h.OnRequestComplete(r, model, providerName, usage, latency, success, errCode, creditsCharged)
}

// reportUsage credits the caller's hub.tpm bucket with the actual
// token cost of the request. Post-hoc accounting: the request that
// pushed the user over budget completes normally; the *next* request
// hits the rate-limit middleware's TPM peek and gets 429'd. nil
// limiter / zero-token usage / missing claims → no-op.
//
// 同时上报扩展业务 metric (RecordHubLLMRequest), 含 model/provider/plan +
// 计算成本 + duration. dashboard 用这套切片.
func (h *MessagesHandler) reportUsage(
	r *http.Request, u provider.Usage,
	model, providerName string, startedAt time.Time, success bool,
) {
	// 1. 旧版 per-kind 计数 (保留兼容已有面板)
	metrics.RecordRelayTokens(
		int64(u.PromptTokens),
		int64(u.CompletionTokens),
		int64(u.CacheReadTokens),
		int64(u.CacheWriteTokens),
	)

	// 2. 业务维度: model × provider × plan × kind + 累计成本 + 时长
	plan := planFromClaims(r)
	costMc := pricing.CostMillicents(model,
		int64(u.PromptTokens), int64(u.CompletionTokens),
		int64(u.CacheReadTokens), int64(u.CacheWriteTokens))
	dur := time.Since(startedAt).Seconds()
	metrics.RecordHubLLMRequest(
		model, providerName, plan,
		int64(u.PromptTokens), int64(u.CompletionTokens),
		int64(u.CacheReadTokens), int64(u.CacheWriteTokens),
		costMc, dur, success,
	)

	// 3. 限流 — 跟旧逻辑一致
	if h.Limiter == nil {
		return
	}
	total := int64(u.PromptTokens + u.CompletionTokens +
		u.CacheReadTokens + u.CacheWriteTokens)
	if total <= 0 {
		return
	}
	c, ok := bauth.ClaimsFrom(r.Context())
	if !ok || c.UserID == "" {
		return
	}
	d := h.Limiter.CheckAndReserve("hub.tpm", c.UserID, total)
	metrics.RecordQuota("hub.tpm", d.Allow, d.Remaining)
	if h.Logger != nil && !d.Allow {
		h.Logger.Warn("hub.tpm exceeded; future calls will be 429",
			"user", c.UserID, "tokens_charged", total,
			"remaining", d.Remaining, "reset", d.Reset)
	}
}

// planFromClaims 从 JWT claims 提取 plan, 用于 metrics 切片 + 后续
// 限额扩展.
//  1. 优先 claims.Plan (identity 签 token 时写入: free/pro/team)
//  2. 后台角色 (admin/ops 等) 没 plan → 'admin' (内部用户不计 free 配额)
//  3. 其它兜底 → 'free'
//
// 老 token 在 plan 字段加上线之前签发的不带 Plan, 自然降级走分支 2/3.
// access token TTL ≤15min, 用户下次 refresh 后 token 就带 plan 了.
func planFromClaims(r *http.Request) string {
	c, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		return "free"
	}
	if c.Plan != "" {
		return c.Plan
	}
	if len(c.Roles) > 0 {
		return "admin"
	}
	return "free"
}

func writeSSE(w io.Writer, f http.Flusher, event string, data map[string]any) {
	body, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body)
	f.Flush()
}

func writeSSEErr(w io.Writer, f http.Flusher, err error) {
	if errors.Is(err, io.EOF) {
		return
	}
	writeSSE(w, f, "error", map[string]any{"message": err.Error()})
}

func writeJSONErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
