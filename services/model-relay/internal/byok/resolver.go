// Package byok — model-relay 调 identity 取用户自带 Key + 失败上报.
//
//	GET  /v1/internal/byok/{user_id}/{provider}              返明文 + config
//	POST /v1/internal/byok/{user_id}/{provider}/incr-failure 累计失败
//	POST /v1/internal/byok/{user_id}/{provider}/touch-used   命中打点
//
// resolver 命中时:
//   1. messages.go 跳过 Hold/Settle (BYOK 不扣平台积分)
//   2. 用上游 API 直接走用户的 Key
//   3. 成功 → TouchUsed; 失败 (401/403) → IncrementFailure; 5 次 → 自动 invalid

package byok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrKeyNotFound = errors.New("byok: user has no valid key for this provider")
)

// Key — model-relay 拿到的用户上游凭据.
type Key struct {
	APIKey   string          `json:"api_key"`
	Config   json.RawMessage `json:"config,omitempty"`
	BaseURL  string          `json:"base_url,omitempty"`  // 00033: custom/代理 endpoint
	Protocol string          `json:"protocol,omitempty"`  // 00033: 选 adaptor 用 (P1)
}

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTPClient: &http.Client{Timeout: 3 * time.Second},
	}
}

// Get — 取明文 Key + config. status != valid 时上游返 404 → ErrKeyNotFound.
func (c *Client) Get(ctx context.Context, userID, provider string) (*Key, error) {
	var k Key
	url := fmt.Sprintf("/v1/internal/byok/%s/%s", userID, provider)
	if err := c.do(ctx, http.MethodGet, url, nil, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// IncrementFailure — 上游 401/403 时调; 返 autoInvalid=true 表示该 Key 已转 invalid.
func (c *Client) IncrementFailure(ctx context.Context, userID, provider string) (bool, error) {
	var resp struct {
		AutoInvalid bool `json:"auto_invalid"`
	}
	url := fmt.Sprintf("/v1/internal/byok/%s/%s/incr-failure", userID, provider)
	if err := c.do(ctx, http.MethodPost, url, nil, &resp); err != nil {
		return false, err
	}
	return resp.AutoInvalid, nil
}

// TouchUsed — 异步打点 last_used_at; 失败不报错.
func (c *Client) TouchUsed(ctx context.Context, userID, provider string) error {
	url := fmt.Sprintf("/v1/internal/byok/%s/%s/touch-used", userID, provider)
	return c.do(ctx, http.MethodPost, url, nil, nil)
}

// Match — 按 (userID, model) 匹配 custom BYOK 记录 (identity 00034).
// model-relay messages.go CredsResolver 在 catalog 失败时调: 用 identity 返回的
// protocol 选 adaptor, 用 api_key + base_url 打用户上游. 404 → ErrKeyNotFound.
func (c *Client) Match(ctx context.Context, userID, model string) (*Key, error) {
	var k Key
	u := fmt.Sprintf("/v1/internal/byok/%s/match?model=%s", userID, url.QueryEscape(model))
	if err := c.do(ctx, http.MethodGet, u, nil, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
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
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrKeyNotFound
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("byok http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
