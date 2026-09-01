// usage_billing.go — brain → model-relay /v1/internal/usage/charge 的
// 计费客户端（client-docproc W4，云端 wiki 解析按页扣费）。
//
// 设计要点：
//   - 价格查询在 model-relay 本地（pricing 单一 SoT），brain 不直读
//     pricing 表、不直连 identity credit-holds —— 统一经 relay 代理。
//   - 后付费语义：worker 解析完成并回报真实页数后才扣费。余额不足
//     （402）时 v1 只记日志，不回滚已落库的解析结果（文本层解析单价
//     极低；OCR 等高价档位上线时再评估 Hold 预扣）。
//   - 幂等：idempotency_key = parse:<source_id>（identity 对
//     (user_id, idempotency_key) 唯一兜底，重试/重复回写不重复扣费）。
package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// UsageCharger 调用 model-relay 的 /v1/internal/usage/charge。
type UsageCharger struct {
	BaseURL string // model-relay URL
	Token   string // MODEL_RELAY_INTERNAL_TOKEN
	Model   string // pricing 挂载的 pseudo-model code（如 wiki-parse-text）
	Logger  *slog.Logger
	client  *http.Client
}

func NewUsageCharger(baseURL, token, model string, logger *slog.Logger) *UsageCharger {
	if baseURL == "" || token == "" || model == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &UsageCharger{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Model:   model,
		Logger:  logger,
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

// ChargeParse 按页扣费。所有失败路径都只记日志不返回错误 —— 计费故障
// 不得阻断解析回写主路径（与 embed/rerank 的缺价零扣兜底同一哲学）。
func (c *UsageCharger) ChargeParse(ctx context.Context, userID, sourceID string, pages int64) {
	if c == nil || pages <= 0 || userID == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"user_id":         userID,
		"model":           c.Model,
		"ref_type":        "parse_page",
		"quantity":        pages,
		"idempotency_key": "parse:" + sourceID,
		"remark":          "wiki cloud parse",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/internal/usage/charge", bytes.NewReader(body))
	if err != nil {
		c.Logger.Warn("usage charge: build request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Biumind-Internal-Token", c.Token)
	resp, err := c.client.Do(req)
	if err != nil {
		c.Logger.Warn("usage charge: request failed", "source_id", sourceID, "err", err)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			ChargedAmount  int64 `json:"charged_amount"`
			PricingMissing bool  `json:"pricing_missing"`
		}
		if err := json.Unmarshal(respBody, &out); err == nil && out.PricingMissing {
			c.Logger.Warn("usage charge: pricing row missing, zero charge",
				"model", c.Model, "source_id", sourceID)
			return
		}
		c.Logger.Info("usage charge: parse charged",
			"source_id", sourceID, "pages", pages, "amount", out.ChargedAmount)
	case http.StatusPaymentRequired:
		c.Logger.Warn("usage charge: insufficient credits (post-paid v1 keeps result)",
			"source_id", sourceID, "user_id", userID, "pages", pages)
	default:
		c.Logger.Warn("usage charge: unexpected status",
			"source_id", sourceID, "status", resp.StatusCode,
			"body", fmt.Sprintf("%s", respBody))
	}
}
