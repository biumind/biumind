// Package agent —— 工具 / 权限 / RunInput 类型定义层。
//
// **历史**：原 agent.Run / consumeStream / invokeToolGated / publisher
// 路径在 S11-4 全删；新路径走 BuildBiumindkitAgent（biumindkit_builder.go）+
// brain Agent Plane work poller（services/runtime/internal/agentplane）。
//
// 当前 agent 包仅保留：
//   - Tool / Registry / Risk —— 工具元数据
//   - PermissionMode + AllowsRisk —— 权限策略
//   - RunInput / SkillToolDeps / AppToolDeps —— 旧 RunInput 字段集仍由
//     BuildBiumindkitAgentInput 复用（保留兼容签名）
//   - SkillEventSink + adapters —— Skills 工具事件通道
//   - utility helpers (newID / IDs / Backoff / errString)

package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/biumind/biumind/services/runtime/internal/store"
	"github.com/google/uuid"
)

// Agent 是为了向后兼容 RunInput 等接口签名而保留的 placeholder。
// **不再有 Run 方法** —— 调用方走 BuildBiumindkitAgent + biumindkit.Agent.Submit。
//
// 字段保留是因为：api.Server 引用 *Agent 字段，移除会触发更大改动；
// 且测试 helpers 也用。fields 都是可选 / 默认值即可。
type Agent struct {
	Store *store.Store
	Tools *Registry

	// DefaultPermissionMode 给 BuildBiumindkitAgentInput 默认值用。
	DefaultPermissionMode PermissionMode
}

// PermissionMode is the three-level gate that runs before every tool
// invocation. It maps to a (Risk → outcome) policy.
//
//   - PermAuto:    allow every Risk level (developer-machine mode).
//   - PermSafe:    allow Low + Medium; deny High with a message back to
//                  the model so it can ask the user via chat to retry
//                  with `auto`. Default for unconfigured runs.
//   - PermReadOnly: allow Low only; deny Medium + High the same way.
//   - PermAsk:     reserved for the bidirectional approval flow shipped
//                  in P2.7; currently behaves like PermSafe + a TOOL_CALL_PERMISSION
//                  event so the client can surface a confirmation UI.
//                  Until P2.7 lands, treat it as a synonym for PermSafe.
type PermissionMode string

const (
	PermAuto     PermissionMode = "auto"
	PermSafe     PermissionMode = "safe"
	PermReadOnly PermissionMode = "read_only"
	PermAsk      PermissionMode = "ask"
)

// AllowsRisk returns whether the mode permits a tool of the given risk.
// Unknown modes fall back to PermSafe.
func (m PermissionMode) AllowsRisk(r Risk) bool {
	switch m {
	case PermAuto:
		return true
	case PermReadOnly:
		return r == RiskLow
	case PermAsk, PermSafe, "":
		return r == RiskLow || r == RiskMedium
	default:
		return r == RiskLow || r == RiskMedium
	}
}

type RunInput struct {
	TaskID   uuid.UUID
	UserID   uuid.UUID
	RunID    string
	ThreadID string
	Model    string
	System   string
	Prompt   string

	// PermissionMode gates tool invocations by Risk. When empty, falls
	// back to Agent.DefaultPermissionMode (which itself defaults to
	// PermSafe inside Run).
	PermissionMode PermissionMode

	// ProjectID scopes memory tools (memory.recall / memory.store) to
	// one Brain project. When empty, memory tools are not registered
	// for this run even if the Agent has a MemoryClient configured.
	ProjectID string

	// Memory is an optional client; when non-nil and ProjectID is set,
	// memory.recall + memory.store tools are registered for this run.
	Memory MemoryClient

	// Skills wires the six skill.* builtin tools per-run. When nil,
	// the tools are not registered. Same per-run isolation rationale
	// as Memory: the (orgID, agentID, ownerID, sessionID) tuple
	// must match the caller, not leak across concurrent runs.
	Skills *SkillToolDeps

	// Apps wires installed App actions as Runtime tools per-run
	// (M3.1+). When nil, no apps are loaded. Per-run isolation: the
	// (UserID, AgentID) tuple must match the caller. Loaded actions
	// become tools named `<identifier>.<action>`; the system prompt
	// gets an "Available Apps" block summarising what's available.
	Apps *AppToolDeps
}

// AppToolDeps captures the per-run inputs apptools.LoadForAgent
// needs. Defined in agent.go (not apptools/) so RunInput can reference
// it without an import cycle (apptools depends on agent).
type AppToolDeps struct {
	// AgentID resolves grants in app_center.agent_apps. Required.
	AgentID uuid.UUID

	// LoadFn is the apptools.Loader.LoadForAgent method, captured
	// here as a function so agent.go doesn't import apptools (which
	// would create a cycle: apptools.RegisterTools needs agent.Tool).
	LoadFn func(ctx context.Context, userID, agentID uuid.UUID, orgID string) (loaded AppsLoaded, err error)

	// RegisterFn binds the Loaded apps into the per-run registry. Same
	// indirection rationale as LoadFn.
	RegisterFn func(reg *Registry, loaded AppsLoaded) (count int)

	// PromptFn produces the system-prompt block. Same indirection.
	PromptFn func(loaded AppsLoaded) string

	// OrgID is forwarded to LoadFn so org-scope installs are visible.
	OrgID string
}

// AppsLoaded is the opaque result of LoadFn — the agent doesn't need
// to know the shape, it just hands it to RegisterFn / PromptFn. The
// concrete type is *apptools.Loaded.
type AppsLoaded any

// ─── helpers ────────────────────────────────────────────

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func IDs() (threadID, runID string) {
	return fmt.Sprintf("th_%s", newID()), fmt.Sprintf("rn_%s", newID())
}

func Backoff(attempt int) time.Duration {
	base := 200 * time.Millisecond
	d := base << attempt
	if d > 5*time.Second {
		return 5 * time.Second
	}
	return d
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
