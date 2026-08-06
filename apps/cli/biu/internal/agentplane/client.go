// HTTP client for the brain Agent Plane API（S3-8 biu 端）。
//
// biu daemon 用这个 client 跟 brain 对话：
//
//	Register      ← POST /v1/agent/environments
//	Heartbeat     ← POST /v1/agent/environments/{id}/heartbeat
//	Deregister    ← DELETE /v1/agent/environments/{id}
//	PollWork      ← POST /v1/agent/work/{env_id}/poll
//	AckWork       ← POST /v1/agent/work/{env_id}/ack/{token}
//	NakWork       ← POST /v1/agent/work/{env_id}/nak/{token}
//	PublishFrame  ← POST /v1/agent/sessions/{id}/publish
//
// 鉴权：长效 PAT（用户在 web 上创建），写到 ~/.biu/config.toml 或 env var
// `BIUMIND_PAT`，所有调用走 `Authorization: Bearer <pat>`。
//
// 错误处理：HTTP 4xx/5xx 都包成 *APIError 让调用方按 status 决定重试策略。
// network error / 5xx → 上层重试（指数退避）；4xx → 一般是 worker 配置错。

package agentplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Client 是 brain Agent Plane HTTP 入口的 stateless 包装。
type Client struct {
	baseURL string       // e.g. https://your-biumind.example.com
	token   string       // PAT (热更: SetToken 锁内写, newReq 锁内读)
	hc      *http.Client // 默认 30s timeout；poll endpoint 单独传 long-poll context
	mu      sync.Mutex   // 保护 token 热更 (heartbeat goroutine + poll loop 并发读)
}

// NewClient 构造 Client。baseURL 不带尾 `/`；token 是 PAT。hc 可空（默认值
// 适合大部分场景；测试可注 httptest server 的 *http.Client）。
func NewClient(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: baseURL, token: token, hc: hc}
}

// SetToken 热更新 brain PAT。BiuDaemonManager (Flutter) 监听 access_token
// refresh (生产 TTL 1h), 经 daemon bridge POST /internal/token 推新 token
// 到这里。worker 的 heartbeat goroutine + poll loop 并发读 token, 故锁保护。
// 不重启 daemon、不断当前 agent 会话 — 解决 token 过期后 worker 401 → daemon
// 退出 → brain GC environment → environment_id required 报错链。
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

// APIError 是 HTTP 4xx/5xx 的 wrapper。
type APIError struct {
	Status int
	Code   string
	Msg    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("agentplane: %d %s: %s", e.Status, e.Code, e.Msg)
}

// IsNotFound 让调用方简化判断（环境被删 / session finalize 等）。
func (e *APIError) IsNotFound() bool { return e.Status == http.StatusNotFound }

// IsBrainNotFound 只在 404 确实来自 brain（JSON 错误体带业务 code）时为 true。
// 反向代理 / 隧道（frp / nginx）在后端抖动时也会返 404，但 body 是 HTML 错误页，
// parseError 落在 Code="http" 分支 —— 那是基础设施噪声，调用方必须按 transient
// 退避处理，绝不能当 "environment 被删" 触发 re-register（实测：frp 抖一次 →
// worker 误判 env 被删 → re-register 再撞 404 → daemon 自杀到 client 重启）。
func (e *APIError) IsBrainNotFound() bool {
	return e.Status == http.StatusNotFound && e.Code != "" && e.Code != "http"
}

// ── Environment ────────────────────────────────────────────

type RegisterReq struct {
	WorkerKind   string          `json:"worker_kind"` // 'biu_daemon' / 'biu_cli' / 'runtime'
	MachineName  string          `json:"machine_name"`
	OsArch       string          `json:"os_arch,omitempty"`
	GitInfo      json.RawMessage `json:"git_info,omitempty"`
	Capabilities []string        `json:"capabilities,omitempty"`
	PublicKey    string          `json:"public_key,omitempty"` // X25519 hex（S3-4 启用时）
	PoolTag      string          `json:"pool_tag,omitempty"`
}

type RegisterResp struct {
	EnvironmentID string   `json:"environment_id"`
	WorkerKind    string   `json:"worker_kind"`
	MachineName   string   `json:"machine_name"`
	State         string   `json:"state"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

func (c *Client) Register(ctx context.Context, req RegisterReq) (*RegisterResp, error) {
	var resp RegisterResp
	if err := c.doJSON(ctx, "POST", "/v1/agent/environments", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Heartbeat(ctx context.Context, envID uuid.UUID) error {
	return c.doJSON(ctx, "POST", "/v1/agent/environments/"+envID.String()+"/heartbeat", nil, nil)
}

func (c *Client) Deregister(ctx context.Context, envID uuid.UUID) error {
	return c.doJSON(ctx, "DELETE", "/v1/agent/environments/"+envID.String(), nil, nil)
}

// ── Work poll ─────────────────────────────────────────────

// WorkItem 是 PollWork 一次返回的任务包。Body 是 JSON-marshaled WorkPayload
// （schema 跟 brain 端 router.go 的 WorkPayload 对齐 —— 客户端反序列化）。
// AckToken 给 ack/nak 端点用。
type WorkItem struct {
	AckToken string          `json:"ack_token"`
	Body     json.RawMessage `json:"body"`
}

// PollWork 长轮询 work。wait 决定 server 端最大等待（默认 30s）；ctx 也能
// 提前 cancel。返回 (nil, nil) 表示超时无 work，调用方继续下一轮。
func (c *Client) PollWork(ctx context.Context, envID uuid.UUID, wait time.Duration) (*WorkItem, error) {
	if wait <= 0 || wait > 30*time.Second {
		wait = 30 * time.Second
	}
	url := fmt.Sprintf("/v1/agent/work/%s/poll?wait=%s", envID, wait)
	// poll 用单独 client 让 ctx 控超时（baseClient.hc 有 30s timeout 跟 wait
	// 撞）—— 临时构造无 timeout 的 client，依赖 ctx 截断。
	pollClient := &http.Client{}
	req, err := c.newReq(ctx, "POST", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := pollClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll work: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}
	var item WorkItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("decode work: %w", err)
	}
	return &item, nil
}

func (c *Client) AckWork(ctx context.Context, envID uuid.UUID, token string) error {
	return c.doJSON(ctx, "POST",
		fmt.Sprintf("/v1/agent/work/%s/ack/%s", envID, token), nil, nil)
}

func (c *Client) NakWork(ctx context.Context, envID uuid.UUID, token string) error {
	return c.doJSON(ctx, "POST",
		fmt.Sprintf("/v1/agent/work/%s/nak/%s", envID, token), nil, nil)
}

// ── Control poll (反向打断 / 后续 reload) ──────────────────

// ControlItem 跟 WorkItem 同 shape (ack_token + body)。Body 是
// brain 端 ingress.maybeRouteCancel 投递的 JSON,worker 反序列化后
// 路由到对应 sessionID 的本地 *Agent。schema(当前):
//
//	{ "type": "cancel_session", "session_id": "<uuid>", "request_id": "<id>" }
//
// 未来可加 reload / set_model 等 type;不识别的 type worker 应该 ack
// 后忽略 —— 老 daemon 不重启也能 silently 跳过新指令。
type ControlItem struct {
	AckToken string          `json:"ack_token"`
	Body     json.RawMessage `json:"body"`
}

// PollControl 长轮询 control 队列。形状同 PollWork,但走独立的 endpoint
// + 独立的 JetStream 流(BIU_AGENT_CONTROL),所以 worker 应该用独立
// goroutine 调它,不被 work poll 占满。返回 (nil, nil) = 超时无消息。
func (c *Client) PollControl(ctx context.Context, envID uuid.UUID, wait time.Duration) (*ControlItem, error) {
	if wait <= 0 || wait > 30*time.Second {
		wait = 30 * time.Second
	}
	url := fmt.Sprintf("/v1/agent/control/%s/poll?wait=%s", envID, wait)
	pollClient := &http.Client{}
	req, err := c.newReq(ctx, "POST", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := pollClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll control: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}
	var item ControlItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("decode control: %w", err)
	}
	return &item, nil
}

// AckControl 跟 AckWork 一样 —— 标 broker 「这条已处理」让消息删除。
// Control 不走 nak (cancel 重投也是同一个 cancel,没意义);失败的话
// 5s AckWait 后 broker 自动重投给下一个 fetch。
func (c *Client) AckControl(ctx context.Context, envID uuid.UUID, token string) error {
	return c.doJSON(ctx, "POST",
		fmt.Sprintf("/v1/agent/control/%s/ack/%s", envID, token), nil, nil)
}

// ── Frame publish ─────────────────────────────────────────

// PublishFrame 把一帧 SDK Protocol JSON 推到 brain；brain 转发到 session
// `.out` subject，ingress 推给 client。frame 必须已经是 wire-ready bytes
// （`json.Marshal(sdkproto.Frame)` 输出）。
func (c *Client) PublishFrame(ctx context.Context, sessionID uuid.UUID, frame []byte) error {
	url := "/v1/agent/sessions/" + sessionID.String() + "/publish"
	req, err := c.newReq(ctx, "POST", url, bytes.NewReader(frame))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("publish frame: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return c.parseError(resp)
	}
	return nil
}

// ── HTTP plumbing ─────────────────────────────────────────

func (c *Client) newReq(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

// doJSON 是普通 JSON 入参 + JSON 出参的简化封装。out=nil 表示忽略 body。
func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := c.newReq(ctx, method, path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return c.parseError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// parseError 把 4xx/5xx body 解出来包 APIError。无法解就拿 status 兜底。
func (c *Client) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Code != "" {
		return &APIError{Status: resp.StatusCode, Code: e.Error.Code, Msg: e.Error.Message}
	}
	return &APIError{Status: resp.StatusCode, Code: "http", Msg: string(body)}
}

// 让 errors 不空 import 警告
var _ = errors.New
