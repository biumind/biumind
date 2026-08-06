// Package billing 是 services/aigc 调 services/identity /v1/internal/credits/*
// 的 thin HTTP client.
//
// 鉴权: 共享 bearer (IDENTITY_INTERNAL_TOKEN). NetworkPolicy 限制集群内可达,
// token 是 defence-in-depth.
//
// 错误码翻译:
//
//	402 → ErrInsufficientCredits     (积分不足, 提交时拒绝, 不要重试)
//	404 → ErrLogNotFound             (退款指向的原 log 不存在)
//	409 → ErrConflict                (log 不是消费 / package 全过期等)
//	400 → ErrBadRequest              (参数错)
//	5xx → 直接透传 (调用方决定是否重试)
//
// 重试策略: 仅对 5xx / 网络错重试 3 次, 指数退避 50/100/200ms. 写接口必须传
// idempotencyKey, 重试时同 key 不会重复扣减 (DB UNIQUE 索引兜底).
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ─── Sentinel errors ──────────────────────────────────

var (
	ErrInsufficientCredits = errors.New("billing: insufficient credits")
	ErrLogNotFound         = errors.New("billing: original log not found")
	ErrConflict            = errors.New("billing: conflict (log not consumption / packages expired)")
	ErrBadRequest          = errors.New("billing: bad request")
)

// ─── Client ───────────────────────────────────────────

type Client struct {
	BaseURL string       // http://identity:7004
	Token   string       // IDENTITY_INTERNAL_TOKEN
	HTTP    *http.Client // nil → 默认 30s timeout
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL, Token: token,
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
}

// ─── DTOs (与 identity/internalapi 字段对齐) ──────────

type ConsumeArgs struct {
	UserID         uuid.UUID `json:"-"`
	UserIDStr      string    `json:"user_id"`
	Amount         int64     `json:"amount"`
	RefType        string    `json:"ref_type"`
	RefID          string    `json:"ref_id,omitempty"`
	Remark         string    `json:"remark,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`

	// W3-7: 透传到 identity NATS 事件 (dashboard 模型分布 / 毛利率统计用).
	// 不进 credit_logs DB.
	ModelCode    string  `json:"model_code,omitempty"`
	ProviderCode string  `json:"provider_code,omitempty"`
	UpstreamUSD  float64 `json:"upstream_usd,omitempty"`
	UpstreamCNY  float64 `json:"upstream_cny,omitempty"`
}

type RefundArgs struct {
	OriginalLogID  uuid.UUID `json:"-"`
	OriginalLogStr string    `json:"original_log_id"`
	Amount         int64     `json:"amount"`
	Remark         string    `json:"remark,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
}

// ConsumeRefundResult 是 identity 返回的 {log: {...}, balance: {...}}.
// 两个字段都是 raw json (调用方按需 unmarshal); aigc 只关心 log_id 用于将来退款.
type ConsumeRefundResult struct {
	LogID        uuid.UUID `json:"-"` // 从 raw["log"]["id"] 解出
	BalanceTotal int64     `json:"-"` // raw["balance"]["permanent_balance"] + ["time_limited_balance"]
	Raw          json.RawMessage
}

// ─── Public RPCs ──────────────────────────────────────

// Consume 扣 amount 积分. ref 推荐填 task id, idempotencyKey 推荐用同 task id
// (重试不重复扣).
func (c *Client) Consume(ctx context.Context, a ConsumeArgs) (*ConsumeRefundResult, error) {
	a.UserIDStr = a.UserID.String()
	body, _ := json.Marshal(a)
	return c.doConsumeOrRefund(ctx, "/v1/internal/credits/consume", body)
}

// Refund 按原扣减 log 反向回填. amount 必须 ≤ 原 |delta| (含已退累计).
func (c *Client) Refund(ctx context.Context, a RefundArgs) (*ConsumeRefundResult, error) {
	a.OriginalLogStr = a.OriginalLogID.String()
	body, _ := json.Marshal(a)
	return c.doConsumeOrRefund(ctx, "/v1/internal/credits/refund", body)
}

// GetBalanceTotal 返回 user 的总余额 (permanent + time_limited).
// 仅做 UI / dashboard 简化用; aigc 内部决策不依赖此 (Consume 自己会校验).
func (c *Client) GetBalanceTotal(ctx context.Context, userID uuid.UUID) (int64, error) {
	url := c.BaseURL + "/v1/internal/credits/" + userID.String() + "/balance"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, errFromStatus(resp.StatusCode, mustReadAll(resp.Body))
	}
	var raw struct {
		Balance struct {
			Permanent   int64 `json:"permanent_balance"`
			TimeLimited int64 `json:"time_limited_balance"`
		} `json:"balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	return raw.Balance.Permanent + raw.Balance.TimeLimited, nil
}

// ─── Internal ─────────────────────────────────────────

func (c *Client) doConsumeOrRefund(ctx context.Context, path string, body []byte) (*ConsumeRefundResult, error) {
	url := c.BaseURL + path

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		// 指数退避 (仅 5xx/网络错触发重试; 4xx 直接返回不重试)
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(50<<(attempt-1)) * time.Millisecond):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http: %w", err)
			continue
		}
		raw := mustReadAll(resp.Body)
		_ = resp.Body.Close()

		switch {
		case resp.StatusCode == 200:
			return parseConsumeResult(raw)
		case resp.StatusCode >= 500:
			lastErr = errFromStatus(resp.StatusCode, raw)
			continue // retry
		default:
			// 4xx — 不重试
			return nil, errFromStatus(resp.StatusCode, raw)
		}
	}
	return nil, lastErr
}

func parseConsumeResult(raw []byte) (*ConsumeRefundResult, error) {
	var doc struct {
		Log struct {
			ID uuid.UUID `json:"id"`
		} `json:"log"`
		Balance struct {
			Permanent   int64 `json:"permanent_balance"`
			TimeLimited int64 `json:"time_limited_balance"`
		} `json:"balance"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &ConsumeRefundResult{
		LogID:        doc.Log.ID,
		BalanceTotal: doc.Balance.Permanent + doc.Balance.TimeLimited,
		Raw:          raw,
	}, nil
}

func errFromStatus(code int, body []byte) error {
	msg := string(body)
	switch code {
	case http.StatusPaymentRequired:
		return fmt.Errorf("%w: %s", ErrInsufficientCredits, msg)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrLogNotFound, msg)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", ErrConflict, msg)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrBadRequest, msg)
	default:
		return fmt.Errorf("billing http %d: %s", code, msg)
	}
}

func mustReadAll(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}
