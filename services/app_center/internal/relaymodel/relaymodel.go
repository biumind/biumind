// Package relaymodel — app_center 启动期按模态从 model-relay 解析
// 平台优选模型 code。
//
// 链(与 brain / runtime / wiki-llm 同契约):
//
//	功能级 env 显式覆盖(调用方先查, 见 EnvOr)
//	  > GET {relay}/v1/internal/models/preferred?mode=<m>
//	    (Authorization: Bearer <IDENTITY_INTERNAL_TOKEN>;
//	     200 → {"code": "..."}  404 → 该模态无 active 建档)
//	  > "" — 调用方功能级降级 (worker 不起 / 任务明确失败),
//	    不允许硬编码模型名兜底。
//
// 进程内缓存: 每 mode 60s 正缓存; 失败(404/5xx/网络)不缓存, 下次
// 调用重试(RSS worker 调用频率低, 不需要负缓存退避)。
// relayURL / token 任一空 → 禁用 (Mode 恒返 "")。
package relaymodel

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
	cacheTTL = 60 * time.Second
)

type slot struct {
	code string
	exp  time.Time
}

// Resolver 按 mode 查询 relay preferred 端点并缓存。并发安全。
type Resolver struct {
	baseURL string
	token   string
	client  *http.Client
	logger  *slog.Logger

	mu    sync.Mutex
	slots map[string]slot
	now   func() time.Time // 单测注入
}

// New 构造 resolver。logger 可空。
func New(baseURL, internalToken string, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Resolver{
		baseURL: baseURL,
		token:   internalToken,
		client:  &http.Client{Timeout: 5 * time.Second},
		logger:  logger,
		slots:   map[string]slot{},
		now:     time.Now,
	}
}

// Mode 返回该模态 (chat / embedding / audio_transcription /
// audio_speech …) 的优选模型 code; 未启用 / 未建档 / relay 不可达返 ""。
func (r *Resolver) Mode(ctx context.Context, mode string) string {
	if r == nil || r.baseURL == "" || r.token == "" {
		return ""
	}
	r.mu.Lock()
	if s, ok := r.slots[mode]; ok && s.code != "" && r.now().Before(s.exp) {
		code := s.code
		r.mu.Unlock()
		return code
	}
	r.mu.Unlock()

	code, err := r.fetch(ctx, mode)
	if err != nil || code == "" {
		return ""
	}
	r.mu.Lock()
	r.slots[mode] = slot{code: code, exp: r.now().Add(cacheTTL)}
	r.mu.Unlock()
	return code
}

// EnvOr 是完整的两级链: env 显式覆盖优先, 空时落 relay preferred。
func (r *Resolver) EnvOr(ctx context.Context, envVal, mode string) string {
	if envVal != "" {
		return envVal
	}
	return r.Mode(ctx, mode)
}

// fetch 打一次 preferred 端点。404 (该模态无 active 建档) 是合法状态,
// 返 ("", nil); 网络 / 5xx 返 error。
func (r *Resolver) fetch(ctx context.Context, mode string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.baseURL+"/v1/internal/models/preferred?mode="+mode, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		r.logger.Warn("relaymodel: relay unreachable", "mode", mode, "err", err)
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		r.logger.Warn("relaymodel: unexpected status",
			"mode", mode, "status", resp.StatusCode)
		return "", fmt.Errorf("relay preferred status %d", resp.StatusCode)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Code, nil
}
