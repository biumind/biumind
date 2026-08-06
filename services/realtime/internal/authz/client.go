// Package authz is a thin HTTP client for the Authz service.
package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	URL string
	HC  *http.Client
}

func New(url string) *Client {
	return &Client{
		URL: strings.TrimRight(url, "/"),
		HC:  &http.Client{Timeout: 3 * time.Second},
	}
}

type checkReq struct {
	Principal map[string]any `json:"principal"`
	Action    string         `json:"action"`
	Resource  map[string]any `json:"resource"`
}

type checkResp struct {
	Decision string `json:"decision"`
}

// CanSubscribe asks Authz whether `principal` can subscribe to `topic`.
// Topic format: "<kind>:<scope>:<id>" e.g. "wiki:project:p1".
func (c *Client) CanSubscribe(ctx context.Context, principal, topic string) (bool, error) {
	parts := strings.SplitN(topic, ":", 3)
	tkind := topic
	tid := topic
	if len(parts) == 3 {
		tkind = parts[0] + ":" + parts[1]
		tid = parts[2]
	}
	body, _ := json.Marshal(checkReq{
		// principal.attributes.id 是 Cedar 策略里 `principal.id` 的来源；
		// 顶层 id 只是 entity UID 用来构造 User::"<uid>"，Cedar 不会从那
		// 取属性。两个都填上以兼容旧代码 + 满足 Cedar `.id` 求值。
		Principal: map[string]any{
			"id":   principal,
			"type": "User",
			"attributes": map[string]any{
				"id": principal,
			},
		},
		Action: "realtime:Topic::subscribe",
		Resource: map[string]any{
			"type": "Topic",
			"id":   topic,
			"attributes": map[string]any{
				"kind":  tkind,
				"id":    tid,
				"owner": principal, // 默认 self ownership for user-scoped topics
			},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/authz/check", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HC.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("authz: status %d body=%s", resp.StatusCode, string(raw))
	}
	var out checkResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Decision == "ALLOW", nil
}

// AlwaysAllow is a stub for tests / dev when authz service is offline.
type AlwaysAllow struct{}

func (AlwaysAllow) CanSubscribe(ctx context.Context, principal, topic string) (bool, error) {
	return true, nil
}
