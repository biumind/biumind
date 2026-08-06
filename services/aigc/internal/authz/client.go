// Package authz 是 services/aigc 调中央 services/authz 的 Cedar 决策客户端.
//
// 设计参考: services/runtime/internal/authz/client.go (同一份 wire-shape).
// 通过 Decider 接口隔离 HTTP 实现, 测试用 AlwaysAllow / Stub 替换.
//
// 调用约定:
//
//   - 任何状态变更前先 Decide(ctx, principal, action, resource).
//   - HTTP 错误 / Authz 服务不可达 → fail-closed (deny). 不允许 transient
//     outage 放过权限检查.
//   - 3 秒超时 (与 runtime / realtime 一致, 不引入新尾延迟).
//
// 常见 action / resource 见 doc.go 同包内枚举.
package authz

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

// Decision 复刻 services/authz 返回. 字符串便于 log 直观.
type Decision string

const (
	Allow Decision = "ALLOW"
	Deny  Decision = "DENY"
)

// Decider 是 aigc 真实需要的 authz 接口切片.
type Decider interface {
	Check(ctx context.Context, req Request) (*Result, error)
}

// Request / Entity / Result 形态与 runtime 同包对齐 (跨服务 entity 通用).
type Request struct {
	Principal Entity
	Action    string
	Resource  Entity
}

type Entity struct {
	Type       string // Cedar 类型, 如 "User" / "aigc.Task" / "aigc.Character"
	ID         string
	Attributes map[string]any
}

type Result struct {
	Decision Decision
	Reason   string
}

// Allowed 是常用快捷方式: result.Decision == Allow.
func (r *Result) Allowed() bool { return r != nil && r.Decision == Allow }

// HTTP 是 wire-bound Decider, POST JSON 到 /v1/authz/check.
type HTTP struct {
	URL string
	HC  *http.Client
}

func NewHTTP(url string) *HTTP {
	return &HTTP{
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
	Reason   string `json:"reason"`
}

func (c *HTTP) Check(ctx context.Context, req Request) (*Result, error) {
	if c.URL == "" {
		return nil, errors.New("authz: no URL configured")
	}
	body, _ := json.Marshal(checkReq{
		Principal: entityJSON(req.Principal),
		Action:    req.Action,
		Resource:  entityJSON(req.Resource),
	})
	httpReq, err := http.NewRequestWithContext(ctx,
		http.MethodPost, c.URL+"/v1/authz/check", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HC.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("authz: http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("authz: status %d body=%s", resp.StatusCode, string(raw))
	}
	var out checkResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &Result{Decision: Decision(out.Decision), Reason: out.Reason}, nil
}

func entityJSON(e Entity) map[string]any {
	out := map[string]any{
		"type":       e.Type,
		"id":         e.ID,
		"attributes": e.Attributes,
	}
	return out
}

// AlwaysAllow / AlwaysDeny — 测试 / dev 模式用 (AUTHZ_URL 空时 main.go 退化到 AlwaysAllow,
// 同时 log 警告).
type AlwaysAllow struct{}

func (AlwaysAllow) Check(_ context.Context, _ Request) (*Result, error) {
	return &Result{Decision: Allow, Reason: "always-allow stub"}, nil
}

type AlwaysDeny struct{}

func (AlwaysDeny) Check(_ context.Context, _ Request) (*Result, error) {
	return &Result{Decision: Deny, Reason: "always-deny stub"}, nil
}
