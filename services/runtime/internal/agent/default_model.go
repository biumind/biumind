// DefaultModelResolver —— task-mode worker 的默认 chat model 兜底链。
//
// 语义与 brain 的 agentplane.DefaultModelResolver 一致(服务 go.mod 互不
// 依赖,无法直接 import,这里按同一契约实现):
//
//	GET /v1/internal/models/default-chat     → admin 指定的默认
//	GET /v1/internal/models/preferred-chat   → relay 自动优选可用模型
//	Authorization: Bearer <IDENTITY_INTERNAL_TOKEN>
//	200 → {"code":"<model code>"}    404 → 无(合法状态,负缓存)
//
// 兜底链: default-chat > env 覆盖 (RUNTIME_DEFAULT_CHAT_MODEL) >
// preferred-chat > 明确 error。绝不用硬编码模型名兜底。
//
// 进程内缓存:命中 60s TTL;失败(404 / 5xx / 网络)负缓存 10s。两个
// 端点各自独立缓存槽。并发安全。

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	defaultModelCacheTTL    = 60 * time.Second
	defaultModelNegativeTTL = 10 * time.Second
)

// ErrNoDefaultChatModel 是兜底链全部落空时的明确失败 —— biumindkit
// 会把它包装成 session 启动错误传给调用方。
var ErrNoDefaultChatModel = errors.New(
	"no chat model available: platform default chat model not configured " +
		"(set RUNTIME_DEFAULT_CHAT_MODEL or configure models in the relay admin)")

// DefaultModelResolver 查 relay internal 端点解析默认 chat model 并缓存。
// relayURL / token 任一空 → HTTP 两级禁用,只剩 env 覆盖一级。
type DefaultModelResolver struct {
	relayURL    string
	token       string
	envOverride string
	client      *http.Client
	logger      *slog.Logger

	cacheTTL    time.Duration
	negativeTTL time.Duration

	mu           sync.Mutex
	defCached    string
	defCacheExp  time.Time
	defNegExp    time.Time
	prefCached   string
	prefCacheExp time.Time
	prefNegExp   time.Time
}

// NewDefaultModelResolver 构造 resolver。logger 可空。
func NewDefaultModelResolver(relayURL, internalToken, envOverride string, logger *slog.Logger) *DefaultModelResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &DefaultModelResolver{
		relayURL:    relayURL,
		token:       internalToken,
		envOverride: envOverride,
		client:      &http.Client{Timeout: 5 * time.Second},
		logger:      logger,
		cacheTTL:    defaultModelCacheTTL,
		negativeTTL: defaultModelNegativeTTL,
	}
}

// Resolve 按兜底链解析默认 chat model code;全部落空返
// ErrNoDefaultChatModel。作为 biumindkit.Options.ResolveDefaultModel
// 钩子注入。
func (r *DefaultModelResolver) Resolve(ctx context.Context) (string, error) {
	if m := r.resolve(ctx, "/v1/internal/models/default-chat",
		&r.defCached, &r.defCacheExp, &r.defNegExp); m != "" {
		return m, nil
	}
	if r.envOverride != "" {
		return r.envOverride, nil
	}
	if m := r.resolve(ctx, "/v1/internal/models/preferred-chat",
		&r.prefCached, &r.prefCacheExp, &r.prefNegExp); m != "" {
		return m, nil
	}
	return "", ErrNoDefaultChatModel
}

// resolve 打一端点并走 TTL 正缓存 + 负缓存退避,缓存槽由调用方指定。
// 未启用 / 未配 / 不可达时返 ""。
func (r *DefaultModelResolver) resolve(ctx context.Context, path string,
	cached *string, cacheExp, negExp *time.Time) string {
	if r.relayURL == "" || r.token == "" {
		return ""
	}
	now := time.Now()
	r.mu.Lock()
	if *cached != "" && now.Before(*cacheExp) {
		m := *cached
		r.mu.Unlock()
		return m
	}
	if now.Before(*negExp) {
		r.mu.Unlock()
		return ""
	}
	r.mu.Unlock()

	m, err := r.fetch(ctx, path)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil && m != "" {
		*cached = m
		*cacheExp = time.Now().Add(r.cacheTTL)
		*negExp = time.Time{}
		return m
	}
	*negExp = time.Now().Add(r.negativeTTL)
	return ""
}

// fetch 打一次 relay internal 端点。404 返 ("", nil)。
func (r *DefaultModelResolver) fetch(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.relayURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		r.logger.Warn("default model resolver: relay unreachable", "err", err)
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		r.logger.Warn("default model resolver: unexpected status",
			"path", path, "status", resp.StatusCode)
		return "", fmt.Errorf("relay %s status %d", path, resp.StatusCode)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Code, nil
}
