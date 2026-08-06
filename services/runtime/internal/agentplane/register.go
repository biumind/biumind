// Package agentplane —— runtime 服务接 brain Agent Plane 的客户端。
//
// S11-1 落地：runtime 启动时调 brain `POST /v1/agent/environments`
// 注册成 worker_kind=runtime 的 environment，定时心跳，关停时 deregister。
//
// 跟 biu daemon (S3-8) 区别：
//
//	biu daemon         → 用户级，user JWT；DB user_id = 用户 uuid
//	runtime worker     → 系统/admin 级，runtime 自己签 JWT；DB user_id =
//	                     admin uuid。Pool 选择不按 user_id 过滤所以
//	                     task mode 跨用户调度仍能找到这个 runtime。
//
// 鉴权：runtime 用 `JWT_SECRET` 自签一个长效 admin JWT 当 PAT 等价物。
// brain 端 mustUserID 拿到的 uid 就是 admin uuid，所有 runtime worker
// 都登记在 admin 名下。这避免引入 PAT 表 / runtime service-account 体系。
//
// X25519 keypair：v1 不做。S3-4 envelope encryption 在 brain 路由层
// 已就位，但当前 work payload 走明文（NATS 内网信任）。runtime 暴露
// public_key=空字符串，brain 端不强制要求。

package agentplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

// Defaults —— 调用方覆盖即可。
const (
	defaultHeartbeatPeriod = 30 * time.Second
	defaultRegisterTimeout = 10 * time.Second
)

// Config 是 NewRegistrar 的输入。BrainURL + Token 必填。
type Config struct {
	// BrainURL 例如 "http://brain:7003"。无尾 `/`。
	BrainURL string
	// Token 是 runtime 自签的长效 JWT —— brain 端 requireAuth 解析；userID
	// claim 决定 DB user_id（推荐 admin uuid）。
	Token string
	// MachineName 默认 hostname；空时 NewRegistrar 自动取。
	MachineName string
	// PoolTag 路由用 —— Task 模式按 pool_tag 选 runtime 实例。空 = 默认池。
	PoolTag string
	// Capabilities 自陈支持的能力（"sandbox" / "skills" / "apps"）—— brain
	// pool 选择 v2 可能用到，v1 仅记录。
	Capabilities []string
	// HeartbeatPeriod 默认 30s。
	HeartbeatPeriod time.Duration
	// HTTPClient 可空，默认 30s timeout。
	HTTPClient *http.Client
}

// Registrar 维护 runtime 在 brain 的 environment 生命周期。
//
// 用法：
//
//	reg, err := agentplane.NewRegistrar(ctx, cfg, logger)
//	if err != nil { ... }                      // 注册 + 心跳已启
//	defer reg.Stop(context.Background())       // 关停 + deregister
type Registrar struct {
	cfg    Config
	envID  uuid.UUID
	logger interface {
		Debug(msg string, args ...any)
		Info(msg string, args ...any)
		Warn(msg string, args ...any)
		Error(msg string, args ...any)
	}

	cancelHeartbeat context.CancelFunc
	doneHeartbeat   chan struct{}
}

// NewRegistrar 注册环境 + 启心跳。返回前确保已成功注册一次。
//
// 心跳跑在内部 goroutine；ctx 取消会停心跳但不 deregister —— 调用方
// 仍要调 Stop 才会清掉 brain 的环境记录。
func NewRegistrar(ctx context.Context, cfg Config, logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}) (*Registrar, error) {
	if cfg.BrainURL == "" {
		return nil, fmt.Errorf("agentplane: BrainURL required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("agentplane: Token required")
	}
	if cfg.MachineName == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = "runtime-anon"
		}
		cfg.MachineName = h
	}
	if cfg.HeartbeatPeriod <= 0 {
		cfg.HeartbeatPeriod = defaultHeartbeatPeriod
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}

	r := &Registrar{cfg: cfg, logger: logger}

	// 注册一次。失败直接 return —— runtime 起不来，由编排层重试。
	regCtx, cancel := context.WithTimeout(ctx, defaultRegisterTimeout)
	defer cancel()
	if err := r.register(regCtx); err != nil {
		return nil, fmt.Errorf("agentplane: register: %w", err)
	}
	logger.Info("agentplane: runtime registered",
		"environment_id", r.envID, "machine_name", cfg.MachineName,
		"pool_tag", cfg.PoolTag, "brain_url", cfg.BrainURL)

	// 心跳 goroutine
	hbCtx, hbCancel := context.WithCancel(context.Background())
	r.cancelHeartbeat = hbCancel
	r.doneHeartbeat = make(chan struct{})
	go r.heartbeatLoop(hbCtx)

	return r, nil
}

// EnvironmentID 返回 brain 颁的 env_id。worker poller 用它做 work fetch。
func (r *Registrar) EnvironmentID() uuid.UUID { return r.envID }

// Stop 停心跳 + deregister。background ctx 让 deregister 在 graceful
// shutdown 时跑完（即使父 ctx 已 cancel）。
func (r *Registrar) Stop(ctx context.Context) {
	if r.cancelHeartbeat != nil {
		r.cancelHeartbeat()
		<-r.doneHeartbeat
	}
	if r.envID == uuid.Nil {
		return
	}
	delCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.deregister(delCtx); err != nil {
		r.logger.Warn("agentplane: deregister failed",
			"environment_id", r.envID, "err", err)
		return
	}
	r.logger.Info("agentplane: runtime deregistered",
		"environment_id", r.envID)
}

// ── HTTP plumbing ─────────────────────────────────────────────

type registerReq struct {
	WorkerKind   string   `json:"worker_kind"`
	MachineName  string   `json:"machine_name"`
	OsArch       string   `json:"os_arch,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	PublicKey    string   `json:"public_key,omitempty"`
	PoolTag      string   `json:"pool_tag,omitempty"`
}

type registerResp struct {
	EnvironmentID string `json:"environment_id"`
	WorkerKind    string `json:"worker_kind"`
	MachineName   string `json:"machine_name"`
	State         string `json:"state"`
}

func (r *Registrar) register(ctx context.Context) error {
	body, _ := json.Marshal(registerReq{
		WorkerKind:   "runtime",
		MachineName:  r.cfg.MachineName,
		Capabilities: r.cfg.Capabilities,
		PoolTag:      r.cfg.PoolTag,
	})
	req, err := r.newReq(ctx, http.MethodPost, "/v1/agent/environments", body)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /v1/agent/environments: %w", err)
	}
	defer resp.Body.Close()
	r.logger.Debug("agentplane: register http",
		"status", resp.StatusCode, "latency_ms", time.Since(start).Milliseconds(),
		"machine_name", r.cfg.MachineName, "pool_tag", r.cfg.PoolTag)
	if resp.StatusCode != http.StatusCreated {
		return errFromHTTPResp(resp)
	}
	var out registerResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode register resp: %w", err)
	}
	id, err := uuid.Parse(out.EnvironmentID)
	if err != nil {
		return fmt.Errorf("bad env_id %q: %w", out.EnvironmentID, err)
	}
	r.envID = id
	return nil
}

func (r *Registrar) heartbeat(ctx context.Context) error {
	req, err := r.newReq(ctx, http.MethodPost,
		"/v1/agent/environments/"+r.envID.String()+"/heartbeat", nil)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	defer resp.Body.Close()
	r.logger.Debug("agentplane: heartbeat http",
		"env_id", r.envID, "status", resp.StatusCode,
		"latency_ms", time.Since(start).Milliseconds())
	if resp.StatusCode/100 != 2 {
		return errFromHTTPResp(resp)
	}
	return nil
}

func (r *Registrar) deregister(ctx context.Context) error {
	req, err := r.newReq(ctx, http.MethodDelete,
		"/v1/agent/environments/"+r.envID.String(), nil)
	if err != nil {
		return err
	}
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("deregister: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode/100 != 2 {
		return errFromHTTPResp(resp)
	}
	return nil
}

func (r *Registrar) newReq(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	url := r.cfg.BrainURL + path
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	return req, nil
}

func (r *Registrar) heartbeatLoop(ctx context.Context) {
	defer close(r.doneHeartbeat)
	t := time.NewTicker(r.cfg.HeartbeatPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := r.heartbeat(hbCtx)
			cancel()
			if err == nil {
				continue
			}
			// 404 — environment 可能被 janitor 标 offline / 被外部删除，
			// 重新注册让 brain 重新承认我们。其他错误只 log + 下轮重试。
			if isNotFound(err) {
				r.logger.Warn("agentplane: heartbeat 404 -> re-register",
					"environment_id", r.envID)
				regCtx, regCancel := context.WithTimeout(ctx, defaultRegisterTimeout)
				if regErr := r.register(regCtx); regErr != nil {
					r.logger.Error("agentplane: re-register failed",
						"err", regErr)
				} else {
					r.logger.Info("agentplane: re-registered",
						"environment_id", r.envID)
				}
				regCancel()
			} else {
				r.logger.Warn("agentplane: heartbeat failed",
					"environment_id", r.envID, "err", err)
			}
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────

// httpError 把 4xx / 5xx HTTP 包成 error，附 status + body 让上层决策。
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("agentplane: HTTP %d: %s", e.Status, e.Body)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if he, ok := err.(*httpError); ok {
		return he.Status == http.StatusNotFound
	}
	return false
}

func errFromHTTPResp(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &httpError{Status: resp.StatusCode, Body: string(body)}
}
