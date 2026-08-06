// Package exechost — Runtime v3 轴 B：工具执行环境抽象（none/local/cloud）。
//
// 与 mode（轴 A：agent loop 在哪转）正交。一个 agent 的工具往哪落地执行：
//   - none  : 无外设，拒绝任何命令执行（chat 模式 / 无工具会话）
//   - local : 本机进程执行（用户机器 daemon —— 今天的默认行为）
//   - cloud : services/sandbox 容器执行（R5 落地；当前 stub）
//
// 设计动机见 docs/BiuMind-Runtime-v3-Design.md §5。本包是**叶子包**（只依赖
// stdlib），故 biumindkit / engine / web / 各服务都能 import 而不成环——这是
// 它没放进 pkg/biumindkit 的原因（biumindkit→web→… 会与之成环）。
//
// 注意：富流式 BashTool 的 local 路径**不**走 Host.Exec（保留其流式 / 后台
// 任务 / 分层 FS 沙箱语义）；Host.Exec 是给 cloud/none 分流 + 未来一次性
// 执行用的最小原语。BashTool 仅在 Host.Mode()≠local 时 divert 到 Exec。
package exechost

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// Mode 是工具执行环境枚举，镜像 agent_sessions.runtime_env_mode。
type Mode string

const (
	ModeNone  Mode = "none"
	ModeLocal Mode = "local"
	ModeCloud Mode = "cloud"
)

// ErrNoneHost / ErrCloudNotReady 是分流到非执行 / 未就绪环境时的哨兵错误。
// 调用方（BashTool）应把它们转成给 LLM 看的 soft error，引导换 runtime_env_mode。
var (
	ErrNoneHost      = errors.New("exechost: runtime_env_mode=none —— 此会话无命令执行环境（仅知识类工具）")
	ErrCloudNotReady = errors.New("exechost: cloud 执行尚未就绪（Runtime v3 R5 提供）；请用 runtime_env_mode=local")
)

// ExecRequest 是一次命令执行请求（最小原语）。
type ExecRequest struct {
	Argv         []string // 例 ["/bin/sh", "-c", "<command>"]
	Workdir      string   // 空 → 进程当前目录
	Stdin        []byte
	TimeoutSec   int // 0 → 无超时
	AllowNetwork bool
}

// ExecResult 是命令执行结果。
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Host 是工具执行宿主。Mode() 报告环境类型；Exec 执行一条命令。
type Host interface {
	Mode() Mode
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
}

// NoneHost 拒绝任何执行。
type NoneHost struct{}

func (NoneHost) Mode() Mode { return ModeNone }
func (NoneHost) Exec(context.Context, ExecRequest) (ExecResult, error) {
	return ExecResult{}, ErrNoneHost
}

// LocalHost 在本机进程执行命令（os/exec）。这是“今天的行为”的一次性原语；
// 富流式 BashTool 的 local 路径不经过这里（保留其完整语义）。
type LocalHost struct{}

func (LocalHost) Mode() Mode { return ModeLocal }
func (LocalHost) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if len(req.Argv) == 0 {
		return ExecResult{}, errors.New("exechost: empty argv")
	}
	cctx := ctx
	if req.TimeoutSec > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(cctx, req.Argv[0], req.Argv[1:]...)
	if req.Workdir != "" {
		cmd.Dir = req.Workdir
	}
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	return res, err
}

// CloudHost 把执行派发到 services/sandbox 容器。R2 stub —— 返回
// ErrCloudNotReady；R5 接 services/runtime/internal/sandbox.Client。
type CloudHost struct {
	// R5: sandbox client + sessionID + image policy。
}

func (CloudHost) Mode() Mode { return ModeCloud }
func (CloudHost) Exec(context.Context, ExecRequest) (ExecResult, error) {
	return ExecResult{}, ErrCloudNotReady
}

// For 按 runtime_env_mode 字符串返回对应 Host。未知值 / 空 → LocalHost
// （保守默认本机，与历史行为一致）。
func For(mode string) Host {
	switch Mode(mode) {
	case ModeNone:
		return NoneHost{}
	case ModeCloud:
		return CloudHost{}
	default:
		return LocalHost{}
	}
}
