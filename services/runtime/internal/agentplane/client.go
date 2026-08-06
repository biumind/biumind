// Brain Agent Plane HTTP client（worker side endpoints）—— S11-2。
//
// register.go 的 Registrar 已经包了 register/heartbeat/deregister；这里加
// runtime worker poll loop 需要的 4 个端点：
//
//	PollWork      ← POST /v1/agent/work/{env_id}/poll
//	AckWork       ← POST /v1/agent/work/{env_id}/ack/{token}
//	NakWork       ← POST /v1/agent/work/{env_id}/nak/{token}
//	PublishFrame  ← POST /v1/agent/sessions/{id}/publish
//
// 跟 biu daemon 的 internal/agentplane/client.go 模式一致。沿用同一鉴权
// scheme（Bearer admin JWT）+ 同一错误模型（APIError with status + body）。

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

// WorkItem 是 PollWork 返回的任务包。Body 是 brain router.go WorkPayload
// 的 JSON marshal。AckToken 是 brain 内存映射到 JetStream Msg 的 key。
type WorkItem struct {
	AckToken string          `json:"ack_token"`
	Body     json.RawMessage `json:"body"`
}

// WorkPayload 跟 brain 端 router.go WorkPayload 对齐。runtime 反序列化用。
type WorkPayload struct {
	SessionID    uuid.UUID `json:"session_id"`
	UserID       uuid.UUID `json:"user_id"`
	Mode         string    `json:"mode"`
	Prompt       string    `json:"prompt"`
	Model        string    `json:"model,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	ThreadID     string    `json:"thread_id,omitempty"`
	PoolTag      string    `json:"pool_tag,omitempty"`
}

// APIError 是 4xx / 5xx HTTP 的封装。worker poll loop 用它判断是否是
// "404 environment 不存在 → 触发 re-register" 这种特殊路径。
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("agentplane: HTTP %d: %s", e.Status, e.Body)
}

func (e *APIError) IsNotFound() bool { return e.Status == http.StatusNotFound }

// PollWork 长轮询 work。wait 决定 server 端最大等待（≤30s）；返回 (nil, nil)
// 表示超时无 work，poll loop 继续下一轮。
//
// 用独立 http.Client（无 timeout）避免跟 wait 撞 —— ctx 控总超时。
func (r *Registrar) PollWork(ctx context.Context, wait time.Duration) (*WorkItem, error) {
	if wait <= 0 || wait > 30*time.Second {
		wait = 30 * time.Second
	}
	if r.envID == uuid.Nil {
		return nil, fmt.Errorf("agentplane: not registered yet")
	}
	url := r.cfg.BrainURL + fmt.Sprintf("/v1/agent/work/%s/poll?wait=%s", r.envID, wait)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)

	pollClient := &http.Client{} // 无 timeout — ctx 控
	resp, err := pollClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll work: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	var item WorkItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("decode work: %w", err)
	}
	return &item, nil
}

// AckWork 标记 work 处理成功 —— brain 把对应的 jetstream Msg ack 掉，
// JetStream WorkQueue retention 删除消息。
func (r *Registrar) AckWork(ctx context.Context, token string) error {
	url := r.cfg.BrainURL + fmt.Sprintf("/v1/agent/work/%s/ack/%s", r.envID, token)
	return r.doJSONRequest(ctx, http.MethodPost, url, nil)
}

// NakWork 标记处理失败 —— brain 把 jetstream Msg nak 掉触发 redelivery。
func (r *Registrar) NakWork(ctx context.Context, token string) error {
	url := r.cfg.BrainURL + fmt.Sprintf("/v1/agent/work/%s/nak/%s", r.envID, token)
	return r.doJSONRequest(ctx, http.MethodPost, url, nil)
}

// ControlItem 跟 WorkItem 同 shape (ack_token + body)。Body 是 brain
// ingress.maybeRouteCancel 投递的 JSON,runtime 反序列化后路由到对应
// sessionID 的本地 *Agent。schema 当前:
//
//	{ "type": "cancel_session", "session_id": "<uuid>", "request_id": "<id>" }
//
// 不识别的 type 应该 ack 后忽略 — 老 runtime 跑新协议时合法,不该 spam ERROR。
type ControlItem struct {
	AckToken string          `json:"ack_token"`
	Body     json.RawMessage `json:"body"`
}

// PollControl 长轮询 control 队列(反向打断 / 后续 reload)。形状同
// PollWork,但走独立 endpoint + 独立 JetStream 流(BIU_AGENT_CONTROL),
// 所以 worker 用独立 goroutine 调它,不被 work poll 占满。返回 (nil, nil)
// = 超时无消息。
func (r *Registrar) PollControl(ctx context.Context, wait time.Duration) (*ControlItem, error) {
	if wait <= 0 || wait > 30*time.Second {
		wait = 30 * time.Second
	}
	if r.envID == uuid.Nil {
		return nil, fmt.Errorf("agentplane: not registered yet")
	}
	url := r.cfg.BrainURL + fmt.Sprintf("/v1/agent/control/%s/poll?wait=%s", r.envID, wait)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)

	pollClient := &http.Client{}
	resp, err := pollClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll control: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	var item ControlItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("decode control: %w", err)
	}
	return &item, nil
}

// AckControl 跟 AckWork 一样 — 标 broker 「这条已处理」让消息删除。
// Control 不走 nak (cancel 重投也是同一个 cancel,没意义);失败的话 5s
// AckWait 后 broker 自动重投给下一个 fetch。
func (r *Registrar) AckControl(ctx context.Context, token string) error {
	url := r.cfg.BrainURL + fmt.Sprintf("/v1/agent/control/%s/ack/%s", r.envID, token)
	return r.doJSONRequest(ctx, http.MethodPost, url, nil)
}

// PublishFrame 把一帧 SDK Protocol v1 JSON 推到 brain；brain 转到 session
// .out subject，ingress 转给 client。frame 必须是 wire-ready bytes
// （`json.Marshal(sdkproto.Frame)` 输出）。
func (r *Registrar) PublishFrame(ctx context.Context, sessionID uuid.UUID, frame []byte) error {
	url := r.cfg.BrainURL + "/v1/agent/sessions/" + sessionID.String() + "/publish"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(frame))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("publish frame: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return parseAPIError(resp)
	}
	return nil
}

// doJSONRequest 是 ack/nak 这种无 body 的 POST 简化封装。返回 4xx/5xx
// 时包成 APIError 让调用方判断重试策略。
func (r *Registrar) doJSONRequest(ctx context.Context, method, url string, body []byte) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return parseAPIError(resp)
	}
	return nil
}

// parseAPIError 把 HTTP 错误响应包成 APIError；body 截断到 4KB 防爆 log。
func parseAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &APIError{Status: resp.StatusCode, Body: string(body)}
}
