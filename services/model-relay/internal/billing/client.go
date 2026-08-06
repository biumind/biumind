// Package billing — model-relay 调 identity 的 service-to-service 客户端.
//
// 封装 identity 的 internalapi 4 个 endpoint:
//
//	POST /v1/internal/credits/hold                      Hold
//	POST /v1/internal/credits/holds/{id}/settle         Settle
//	POST /v1/internal/credits/holds/{id}/release        Release
//	GET  /v1/internal/pricing/{ref_type}/{pricing_key}  价格查询
//
// 鉴权: 共享 bearer (IDENTITY_INTERNAL_TOKEN); HTTPS 走集群内 service URL.
//
// 设计: docs/BiuMind-Billing-Redesign.md §5.2 + §3.
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Sentinel — 调用方按 Is/As 处理的常见错误.
var (
	ErrInsufficient    = errors.New("billing: insufficient credits")
	ErrPricingNotFound = errors.New("billing: pricing entry not found")
	ErrHoldNotFound    = errors.New("billing: hold not found")
)

// Hold — identity.credit_holds 的精简视图.
type Hold struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	MaxAmount  int64  `json:"max_amount"`
	Status     string `json:"status"`
	ExpiresAt  string `json:"expires_at"`
}

// PricingEntry — billing.pricing_book 的精简视图. markup_ratio 客户端用 float
// 即可 (model-relay 自己估价不要求 4 位精度; identity 内部仍用 big.Rat).
type PricingEntry struct {
	RefType             string  `json:"ref_type"`
	PricingKey          string  `json:"pricing_key"`
	CostBasis           string  `json:"cost_basis"`
	CostInputPerUnit    int64   `json:"cost_input_per_unit"`
	CostOutputPerUnit   int64   `json:"cost_output_per_unit"`
	CostCacheRead       int64   `json:"cost_cache_read"`
	CostCacheWrite      int64   `json:"cost_cache_write"`
	MarkupRatio         float64 `json:"markup_ratio"`
	MinCharge           int64   `json:"min_charge"`
	MaxChargePerRequest *int64  `json:"max_charge_per_request,omitempty"`
}

// CalculateChat — 估 chat 一次请求的标价 (millicents); 与 identity pricing.go 同算法.
//
//	cost = ceil(cost_in*tokens_in/1M + cost_out*tokens_out/1M + cache)
//	list = floor(cost * markup); 钳制 [min, max]
func (e *PricingEntry) CalculateChat(promptTok, completionTok, cacheReadTok, cacheWriteTok int64) int64 {
	cost := mulDiv64(e.CostInputPerUnit, promptTok, 1_000_000)
	cost += mulDiv64(e.CostOutputPerUnit, completionTok, 1_000_000)
	cost += mulDiv64(e.CostCacheRead, cacheReadTok, 1_000_000)
	cost += mulDiv64(e.CostCacheWrite, cacheWriteTok, 1_000_000)
	return e.finalize(cost)
}

// EstimateChatRange — 给定 (prompt, max_completion) 算 (min, max) 标价.
// min: 假设 completion=0; max: 假设全部完成 max_completion.
func (e *PricingEntry) EstimateChatRange(promptTok, maxCompletionTok int64) (minList, maxList int64) {
	minList = e.CalculateChat(promptTok, 0, 0, 0)
	maxList = e.CalculateChat(promptTok, maxCompletionTok, 0, 0)
	return
}

// ─── v0.3 多模态计费 helpers (M5) ──────────────────────────────────
//
// embedding / rerank / speech 的算法跟 chat 同骨架, 只是 cost_basis 单位
// 不同. 共享 markup + min/max clamping 逻辑用 finalize() helper 浓缩.

// CalculateEmbed — embedding 一次请求标价.
//   cost_basis: per_mtok, 单位 = millicents/百万 token
//   公式: list = (cost_input_per_unit × prompt_tok / 1M) × markup
// 出 token (output) 不存在, completion 不计.
func (e *PricingEntry) CalculateEmbed(promptTok int64) int64 {
	cost := mulDiv64(e.CostInputPerUnit, promptTok, 1_000_000)
	return e.finalize(cost)
}

// CalculateRerank — rerank 一次请求标价.
//   cost_basis: per_search_unit (Cohere 标准 1 unit = 1 query × ≤100 docs;
//   dashscope 透传 total_tokens 当 search_unit 用)
//   公式: list = (cost_input_per_unit × search_units) × markup
func (e *PricingEntry) CalculateRerank(searchUnits int64) int64 {
	cost := e.CostInputPerUnit * searchUnits
	return e.finalize(cost)
}

// CalculateSpeech — TTS 一次请求标价.
//   cost_basis: per_kchar (千字符), cosyvoice / OpenAI tts-1 / elevenlabs 通用
//   公式: list = (cost_input_per_unit × chars / 1000) × markup
// chars 由 adaptor 从上游响应 usage.characters 提取.
func (e *PricingEntry) CalculateSpeech(chars int64) int64 {
	cost := mulDiv64(e.CostInputPerUnit, chars, 1000)
	return e.finalize(cost)
}

// CalculateImage — 图像生成一次请求标价.
//   cost_basis: per_call (按张), n>1 时按 n 计费
// 复用 aigc_image ref_type, n 默认 1.
func (e *PricingEntry) CalculateImage(n int64) int64 {
	if n <= 0 {
		n = 1
	}
	cost := e.CostInputPerUnit * n
	return e.finalize(cost)
}

// CalculateVideo — 视频生成一次请求标价.
//   cost_basis: per_second, 按实际产出秒数算 (上游可能比用户请求的短)
// 复用 aigc_video ref_type.
func (e *PricingEntry) CalculateVideo(durationSeconds int64) int64 {
	if durationSeconds <= 0 {
		durationSeconds = 1
	}
	cost := e.CostInputPerUnit * durationSeconds
	return e.finalize(cost)
}

// finalize — 共享的 markup + min/max clamping 逻辑.
func (e *PricingEntry) finalize(cost int64) int64 {
	list := int64(float64(cost) * e.MarkupRatio)
	if list < e.MinCharge {
		list = e.MinCharge
	}
	if e.MaxChargePerRequest != nil && list > *e.MaxChargePerRequest {
		list = *e.MaxChargePerRequest
	}
	return list
}

// PriceLookuper 是 LookupPrice 的可注入实现. main.go 启动时注入一个
// 走 model_relay.pricing 表的本地实现 (registry.Cache + PricingRepo
// 组装出 PricingEntry, 并把 numeric 原币种换算成 millicents).
//
// 历史: 这里曾走 HTTP 调 identity 的 /v1/internal/pricing,但 identity 端
// 的 billing.pricing_book 跟 admin 后台改的 model_relay.pricing 数据无同
// 步,Agent 模式 + glm-5.1 实测 admin 配了价但 Hold 链路查不到,扣不到
// 积分. W4 整合 SoT: identity 端点删除, model_relay.pricing 单一权威.
type PriceLookuper interface {
	Lookup(ctx context.Context, refType, modelCode string) (*PricingEntry, error)
}

// Client — 一个进程内复用一个 Client. 跨请求共享 *http.Client. Hold/Settle/
// Release/Refund 仍走 identity (积分账本是 identity 的事); pricing 查询走
// 本地 model_relay.pricing (Pricing 字段).
type Client struct {
	BaseURL    string // identity URL, e.g. "http://identity:7004"
	Token      string // IDENTITY_INTERNAL_TOKEN
	HTTPClient *http.Client
	Pricing    PriceLookuper // 单一价格 SoT; nil → LookupPrice 报错
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// WithPricing 注入价格查询实现 (本地 model_relay.pricing). 必须在 LookupPrice
// 被调用前 setup. main.go 在 cache + PricingRepo 就绪后调.
//
// 之所以是 setter 而不是构造函数参数: NewClient 在 main.go 早期 (identity URL
// 配置阶段) 创建,Cache + PricingRepo 在 DB pool 就绪后才能造,顺序依赖
// 用 setter 解耦比 reorder 整个 main.go 简洁.
func (c *Client) WithPricing(p PriceLookuper) *Client {
	c.Pricing = p
	return c
}

// HoldArgs — 调 POST /v1/internal/credits/hold.
type HoldArgs struct {
	UserID         string `json:"user_id"`
	MaxAmount      int64  `json:"max_amount"`
	RefType        string `json:"ref_type"`
	RefID          string `json:"ref_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	TTLSeconds     int    `json:"ttl_seconds,omitempty"`

	// W3-7: 透传到 identity NATS HoldEvent (dashboard 模型分布用).
	ModelCode    string `json:"model_code,omitempty"`
	ProviderCode string `json:"provider_code,omitempty"`
}

// Hold — 预扣. 余额不足返 ErrInsufficient.
func (c *Client) Hold(ctx context.Context, args HoldArgs) (*Hold, error) {
	var resp struct {
		Hold *Hold `json:"hold"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/internal/credit-holds", args, &resp); err != nil {
		return nil, err
	}
	return resp.Hold, nil
}

// Settle — 结算 actualAmount; 必须 ≤ Hold.MaxAmount.
func (c *Client) Settle(ctx context.Context, holdID string, actualAmount int64, remark string) error {
	body := map[string]any{
		"actual_amount": actualAmount,
		"remark":        remark,
	}
	return c.do(ctx, http.MethodPost,
		fmt.Sprintf("/v1/internal/credit-holds/%s/settle", holdID), body, nil)
}

// Release — 全额释放 (失败 / 取消时调).
func (c *Client) Release(ctx context.Context, holdID string) error {
	return c.do(ctx, http.MethodPost,
		fmt.Sprintf("/v1/internal/credit-holds/%s/release", holdID), nil, nil)
}

// LookupPrice — 查 model_relay.pricing 经 PriceLookuper 转 PricingEntry.
//
// pricingKey 是 model code (e.g. "glm-5.1"). refType 决定取哪些字段
// (chat → in/out/cache; embedding → input only; rerank → search_unit;
// aigc_* → cost_per_image / video_second; audio_speech → per_kchar).
//
// 之前是 HTTP 调 identity, W4 整合后本地查询. 没配 PriceLookuper 时报错
// (调用方 messages_billing/modality_billing 都已处理这个错: pricing 缺
// 失走"不计费"路径).
func (c *Client) LookupPrice(ctx context.Context, refType, pricingKey string) (*PricingEntry, error) {
	if c.Pricing == nil {
		return nil, errors.New("billing: no pricing source configured (Client.Pricing is nil)")
	}
	return c.Pricing.Lookup(ctx, refType, pricingKey)
}

// ─── Internal HTTP plumbing ──────────────────────────

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPaymentRequired {
		return ErrInsufficient
	}
	if resp.StatusCode == http.StatusNotFound {
		// Distinguish hold 404 from pricing 404 by path
		if strings.Contains(path, "/credit-holds/") {
			return ErrHoldNotFound
		}
		if strings.Contains(path, "/pricing/") {
			return ErrPricingNotFound
		}
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("billing http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// mulDiv64 — 算 a*b/c, 用 int128 等价的公式防溢出 (a*b/c 中 a*b 可能 > int64 上限).
//
// 我们的取值都很小 (cost ≤ 1e6, tokens ≤ 1e7), a*b ≤ 1e13 不会溢出 int64,
// 所以这里用普通乘除即可; 留这层包装为以后大数字预留.
func mulDiv64(a, b, c int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	return (a * b) / c
}
