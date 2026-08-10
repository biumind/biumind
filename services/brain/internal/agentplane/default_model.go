// DefaultModelResolver — 从 model-relay 解析 "BiuMind 默认" chat model。
//
// client 没传 thread.model(显示 "BiuMind 默认")时,chat runner 需要知道
// 实际打哪个 model。真相源在 relay:admin 在 models 表上标
// is_default_chat,relay 经 internal 端点暴露(internalapi.Server,
// requireToken 校验 relay 的 IDENTITY_INTERNAL_TOKEN,见
// services/model-relay/cmd/model-relay/main.go):
//
//	GET /v1/internal/models/default-chat
//	Authorization: Bearer <IDENTITY_INTERNAL_TOKEN>
//	200 → {"code":"<model code>"}    404 → 未配默认模型
//
// 进程内缓存:命中缓存 60s TTL;失败(404 / 5xx / 网络)负缓存 10s ——
// relay 短暂不可用时每个 turn 都重试,但不打爆 relay。启动时 main.go
// 异步 Warm 预热,不阻塞 boot。并发安全(多 chat session 同时 resolve)。

package agentplane

import (
	"context"
	"encoding/json"
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

// DefaultModelResolver 按上面文档的契约查 relay 默认 chat model 并缓存。
// relayURL / token 任一空 → 禁用(DefaultChatModel 恒返 "")。
type DefaultModelResolver struct {
	relayURL string
	token    string
	client   *http.Client
	logger   *slog.Logger

	// TTL 是字段而非 const,方便单测缩短。
	cacheTTL    time.Duration
	negativeTTL time.Duration

	mu       sync.Mutex
	cached   string
	cacheExp time.Time
	negExp   time.Time
	now      func() time.Time // 单测注入;生产 time.Now
}

// NewDefaultModelResolver 构造 resolver。logger 可空。
func NewDefaultModelResolver(relayURL, internalToken string, logger *slog.Logger) *DefaultModelResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &DefaultModelResolver{
		relayURL:    relayURL,
		token:       internalToken,
		client:      &http.Client{Timeout: 5 * time.Second},
		logger:      logger,
		cacheTTL:    defaultModelCacheTTL,
		negativeTTL: defaultModelNegativeTTL,
		now:         time.Now,
	}
}

// Warm 启动时异步预热缓存(main.go `go resolver.Warm(ctx)`),让第一个
// chat turn 不付 relay 往返。失败静默 —— 下个 turn 会重试。
func (r *DefaultModelResolver) Warm(ctx context.Context) {
	_ = r.DefaultChatModel(ctx)
}

// DefaultChatModel 返回 relay 配的默认 chat model code;未配 / relay
// 不可达 / resolver 未启用时返 ""。
func (r *DefaultModelResolver) DefaultChatModel(ctx context.Context) string {
	if r.relayURL == "" || r.token == "" {
		return ""
	}
	r.mu.Lock()
	if r.cached != "" && r.now().Before(r.cacheExp) {
		m := r.cached
		r.mu.Unlock()
		return m
	}
	if r.now().Before(r.negExp) {
		r.mu.Unlock()
		return ""
	}
	r.mu.Unlock()

	// 并发下允许多个 goroutine 同时 fetch( last write wins ) —— 比
	// singleflight 简单,负缓存兜底保证不会持续打爆 relay。
	m, err := r.fetch(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil && m != "" {
		r.cached = m
		r.cacheExp = r.now().Add(r.cacheTTL)
		r.negExp = time.Time{}
		return m
	}
	// 负缓存:404(未配默认模型) 与 5xx / 网络错误同待遇。
	r.negExp = r.now().Add(r.negativeTTL)
	return ""
}

// fetch 打一次 relay internal 端点。404 返 ("", nil) —— admin 未配
// 默认模型是合法状态,由调用方负缓存。
func (r *DefaultModelResolver) fetch(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.relayURL+"/v1/internal/models/default-chat", nil)
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
			"status", resp.StatusCode)
		return "", fmt.Errorf("relay default-chat status %d", resp.StatusCode)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Code, nil
}
