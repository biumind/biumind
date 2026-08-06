// Package agentplane — channels 端的 brain Agent Plane 集成（S12-1）。
//
// 替代老路径："channels Inbound → JetStream channels.inbound.<channel> →
// runtime channelsbus.Subscriber → agent.Run + AG-UI publish 到 realtime"。
//
// 新路径：
//
//	channels Inbound
//	    ↓
//	Trigger.CreateTaskSession  POST /v1/agent/sessions { mode:"task", prompt }
//	    ↓                          (brain 选 runtime + EnqueueWork)
//	    返回 session_id
//	Listener.Subscribe(session_id, replyTo)
//	    ↓ 订阅 biu.session.<sid>.out
//	streamlined_text 帧聚合 → result(success) 帧时调 driver.Send 推回 Telegram/微信
//
// 跟原 Realtime AG-UI 路径区别：
//   - 协议统一到 SDK Protocol v1（schema/sdk/v1）—— 跟 chat / agent mode 共用
//   - 不依赖 Realtime 服务做 fanout —— 直接 NATS JetStream
//   - 多 driver 复用同一回放代码（每个 driver 实现 Send 即可）

package agentplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Client 是 channels → brain Agent Plane 的 HTTP 客户端。鉴权用长效 admin
// JWT —— 跟 runtime 一样自签（JWT_SECRET 共享）。
type Client struct {
	BrainURL string
	Token    string
	HTTP     *http.Client
}

// NewClient 构造 Client；http 可空（默认 30s timeout）。
func NewClient(brainURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{BrainURL: brainURL, Token: token, HTTP: hc}
}

// CreateTaskSessionReq 跟 brain agentplane.CreateSessionAPIReq 对齐 ——
// channels 只用 task mode，永远填 mode:"task"。
type CreateTaskSessionReq struct {
	Prompt       string `json:"prompt"`
	Model        string `json:"model,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	PoolTag      string `json:"pool_tag,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
}

// CreateTaskSessionResp 跟 brain writeSessionCreated 对齐。channels 只读
// session_id —— ingress / output 用它订阅 .out subject。
type CreateTaskSessionResp struct {
	SessionID    uuid.UUID `json:"session_id"`
	SessionToken string    `json:"session_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Mode         string    `json:"mode"`
}

// CreateTaskSession 调 brain POST /v1/agent/sessions 创 task mode session。
// 错误：
//   - 503 no_runtime_available → 没在线 runtime；上层 log warn 可丢弃
//   - 4xx 其它 → APIError 包 status + body
//   - 网络错 → 透传 error
func (c *Client) CreateTaskSession(ctx context.Context, req CreateTaskSessionReq) (*CreateTaskSessionResp, error) {
	body := map[string]any{
		"mode":          "task",
		"prompt":        req.Prompt,
		"model":         req.Model,
		"system_prompt": req.SystemPrompt,
		"pool_tag":      req.PoolTag,
		"thread_id":     req.ThreadID,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("agentplane: marshal req: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BrainURL+"/v1/agent/sessions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agentplane: POST /v1/agent/sessions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, parseAPIError(resp)
	}

	var out CreateTaskSessionResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("agentplane: decode resp: %w", err)
	}
	return &out, nil
}

// APIError 是 4xx/5xx HTTP 的封装。channels 上层用 IsNoRuntime() 判断
// 是否是"没在线 runtime"的可降级错误（log warn 但不报警）。
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("agentplane: HTTP %d: %s", e.Status, e.Body)
}

// IsNoRuntime 判断是不是"task pool 空"错误。brain 这种情况返 503 +
// `error.code = no_runtime_available`。
func (e *APIError) IsNoRuntime() bool {
	if e.Status != http.StatusServiceUnavailable {
		return false
	}
	// 简单 substring 匹配 —— 不解 JSON，避免 channel 端依赖 brain 错误结构
	return bytesContains(e.Body, "no_runtime_available")
}

func parseAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &APIError{Status: resp.StatusCode, Body: string(body)}
}

// bytesContains 简化 substring 检查。
func bytesContains(haystack, needle string) bool {
	if len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
