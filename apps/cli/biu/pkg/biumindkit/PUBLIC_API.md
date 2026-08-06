# biumindkit — Public API 盘点

> 版本：v0.2（2026-06-01）
> S0-2 阶段产出 + S4/S11 集成完成 + Agent Plane follow-up 全部归档。
> 配套 [`docs/BiuMind-Agent-Plane-Dev-Plan.md`](../../../../../docs/BiuMind-Agent-Plane-Dev-Plan.md)。
>
> 用途：brain / runtime / Flutter daemon 三方都已经在用 biumindkit 跑同一份
> agent 内核 — 本文档列出公开 API + 使用模式 + 历史 follow-up 归档。
>
> **变更记录**：
> - v0.2（2026-06-01）：F1–F13 全部完成（详见 §6 归档）。新增公开类型
>   `BaseEvent` / `AssistantBlock` / `StreamingText` / `MCPRegistry` /
>   `ToolCost` / `ErrInterrupted` 等。`Options` 加 `PriorMessages` /
>   `SessionID` / `ParentToolUseID` / `MCPRegistry`。`Agent` 加
>   `Interrupt` / `CostByTool` 方法。
> - v0.1（2026-05-29）：S0-2 初版盘点。

---

## 1. 包定位

- **路径**：`github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit`
- **总规模**：~1100 LOC（`sdk.go` ~960 + `tool.go` 120 + `mcp.go` + 共享 helper）
- **API 稳定性**：[`sdk.go:5-12`](sdk.go) 自述 "everything exported is part of the supported surface"，遵循 semver
- **依赖边界**：`internal/*` 不暴露；公开接口是 `Options` / `Agent` / `Event` / `BaseEvent` / `Tool` / `MCPRegistry` 几组类型
- **当前消费者**：biu CLI（`cmd/biu/wiring/`）+ brain (`services/brain/internal/chat/agent_v2.go` + `agentplane/chat_runner.go`) + runtime (`services/runtime/internal/agent/agent.go`) + 4 个 example 程序

---

## 2. 公开类型清单

### 2.1 入口

```go
type Options struct { ... }                             // 18 字段，详见 §3
func New(opt Options) (*Agent, error)                   // 构造器
type Agent struct { /* 私有 */ }                        // 主句柄
```

### 2.2 Agent 方法

```go
func (a *Agent) Submit(ctx context.Context, prompt string) <-chan Event
func (a *Agent) Run(ctx context.Context, prompt string) (text, stopReason string, err error)
func (a *Agent) Compact(ctx context.Context) error
func (a *Agent) Cost() Cost                              // session 聚合
func (a *Agent) CostByTool() map[string]ToolCost          // F4: 按工具拆分
func (a *Agent) Interrupt() error                         // F5: 干净 stop,emit Done{interrupted}
func (a *Agent) Close() error
```

### 2.3 事件类型（Submit 流出来的）

每个 Event 都通过嵌入 `BaseEvent` 暴露 `SessionID()` / `ParentToolUseID()`
两个路由元数据访问器（F10/F13）。embedder（brain / sdkbridge）拿到事件
后无需 type assert,统一调接口方法即可填到 SDK Protocol frame 上。

```go
type Event interface {
    sdkEvent()                                // sealed marker
    SessionID() string                        // F10
    ParentToolUseID() string                  // F10/F13
}

type BaseEvent struct {                       // 嵌入到下面所有 event
    EventSessionID       string
    EventParentToolUseID string
}

type StreamingText   struct { BaseEvent; Text string }                                       // 增量 chunk
type AssistantText   struct { BaseEvent; Text, StopReason string }                           // 整段 assembled
type AssistantBlock  struct { BaseEvent; Block ContentBlock; Index int; StopReason string }  // F2: per-block 视图
type ToolStart       struct { BaseEvent; ID, Name string; Input map[string]any }
type ToolResult      struct { BaseEvent; ID, Name, Output string; IsError bool; Elapsed time.Duration }
type CompactStarted  struct { BaseEvent; Reason string; TokensBefore int }
type CompactFinished struct { BaseEvent; TokensBefore, TokensAfter, TokensSaved int }
type Error           struct { BaseEvent; Err error; Recoverable bool }
type Done            struct { BaseEvent; StopReason string; InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int; Elapsed time.Duration }

type Cost            struct { Model string; InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int; USD float64 }
type ToolCost        struct { Calls int; ElapsedMs, OutputBytes int64; Errors int }          // F4: 按工具
```

### 2.4 工具自定义

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    IsReadOnly() bool
    IsDestructive() bool
    IsConcurrencySafe() bool
    Run(ctx context.Context, args map[string]any) (string, error)
}

type ToolDef struct {                                    // 6 字段，写起来比手撸接口短
    Name, Description string
    InputSchema       map[string]any
    IsReadOnly        bool
    IsDestructive     bool
    IsConcurrencySafe bool
    Run               func(ctx, args) (string, error)
}

func NewTool(def ToolDef) Tool
```

### 2.5 权限策略

```go
type PermissionDecision int
const ( PermDeny PermissionDecision = iota; PermAllow; PermAlways )

type PermissionRequest struct {
    ToolUseID string
    ToolName  string
    Input     map[string]any
    Reason    string
}

type PermissionPolicyFn func(ctx, req PermissionRequest) PermissionDecision

func PermissionAllow() PermissionPolicyFn               // 全 allow
func PermissionDeny() PermissionPolicyFn                // 全 deny（默认）
func PermissionAlways() PermissionPolicyFn              // allow + remember
```

### 2.6 Mode 开关

```go
type MemoryMode int                                     // AutoMemory / NoMemory
const ( AutoMemory MemoryMode = iota; NoMemory )

type SettingsMode int                                   // AutoSettings / NoSettings
const ( AutoSettings SettingsMode = iota; NoSettings )
```

### 2.7 Sentinels

```go
var ErrConcurrent  = errors.New("biumindkit: another Submit is in progress")
var ErrInterrupted = engine.ErrInterrupted   // F5: cancel cause sentinel
```

`ErrInterrupted` 用于不持有 `*Agent` 引用时通过 ctx 触发干净打断:

```go
subCtx, cancel := context.WithCancelCause(ctx)
go agent.Submit(subCtx, prompt)
// 别处:
cancel(biumindkit.ErrInterrupted)   // 等价于 agent.Interrupt()
```

---

## 3. Options 字段表

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `APIKey` | `string` | （必填） | Anthropic key 或 model-relay bearer（依 `UseRelayAuth`） |
| `AnthropicEndpoint` | `string` | `api.anthropic.com` | 直连模式或 model-relay URL |
| `UseRelayAuth` | `bool` | `false` | true = 走 BiuMind model-relay Bearer 鉴权 |
| `Model` | `string` | `claude-sonnet-4-6` | LLM 选型 |
| `Cwd` | `string` | `os.Getwd()` | 工作目录（文件工具用） |
| `System` | `string` | `""` | 自定义 system prompt（会跟 BIUMIND.md / skills 拼接） |
| `MaxToolTurns` | `int` | 25 | 工具循环上限 |
| `MaxTokens` | `int` | 4096 | 单次请求 max_tokens |
| `LoadProjectMemory` | `MemoryMode` | `AutoMemory` | BIUMIND.md 自动加载 |
| `LoadProjectSettings` | `SettingsMode` | `AutoSettings` | settings.json 加载（permissions / hooks） |
| `PermissionMode` | `string` | `""`（settings 里取 / `default`） | 显式锁定权限模式 |
| `BypassPermissions` | `bool` | `false` | 等价 `PermissionMode="bypassPermissions"` |
| `ExtraTools` | `[]Tool` | `nil` | 用户自定义工具（最后注册，可覆盖内置） |
| `PermissionPolicy` | `PermissionPolicyFn` | `PermissionDeny()` | 权限决策回调 |
| `MCPRegistry` | `*MCPRegistry` | `nil` | F1: 公开 wrapper,nil-safe,不再暴露 internal/mcp |
| `PriorMessages` | `[]Message` | `nil` | F3: 注入 thread 历史让 Submit 看到上下文 |
| `SessionID` | `string` | `""` | F10: 打到所有 Event 上的 SessionID() |
| `ParentToolUseID` | `string` | `""` | F10/F13: 打到所有 Event 上的 ParentToolUseID()（sub-agent 用） |

> **隐含装载**（无开关）：每个 `New` 都会自动加载 BIUMIND.md / 自动 memory / skills auto-attach prompt / settings 里的 sandbox 配置 / 4 个工具 family（files / orchestration / web / interactive）/ background-task store / cost tracker / usage logger / plan verifier / plan hint。

---

## 4. 使用模式

### 4.1 同步一次性运行

```go
ag, err := biumindkit.New(biumindkit.Options{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
    Model:  "claude-sonnet-4-6",
    Cwd:    "/repo",
})
if err != nil { return err }
defer ag.Close()

text, stop, err := ag.Run(ctx, "list every TODO in this repo")
```

### 4.2 流式 + 工具可见性（IDE 集成）

```go
for ev := range ag.Submit(ctx, "refactor foo.go") {
    switch e := ev.(type) {
    case biumindkit.AssistantText: ui.AppendText(e.Text)
    case biumindkit.ToolStart:     ui.ShowToolCall(e.Name, e.Input)
    case biumindkit.ToolResult:    ui.ShowToolResult(e.Name, e.Output, e.IsError)
    case biumindkit.Done:          ui.ShowFinal(e)
    case biumindkit.Error:         ui.ShowError(e.Err)
    }
}
```

### 4.3 自定义工具

```go
echo := biumindkit.NewTool(biumindkit.ToolDef{
    Name:        "Echo",
    Description: "Echoes input.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{"msg": map[string]any{"type": "string"}},
    },
    IsReadOnly:        true,
    IsConcurrencySafe: true,
    Run: func(ctx context.Context, args map[string]any) (string, error) {
        return args["msg"].(string), nil
    },
})

ag, _ := biumindkit.New(biumindkit.Options{
    APIKey: ..., ExtraTools: []biumindkit.Tool{echo},
})
```

### 4.4 自定义权限策略

```go
ag, _ := biumindkit.New(biumindkit.Options{
    APIKey: ...,
    PermissionPolicy: func(ctx context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
        log.Printf("perm ask: %s %v", req.ToolName, req.Input)
        if isWhitelisted(req.ToolName) {
            return biumindkit.PermAlways
        }
        return biumindkit.PermDeny
    },
})
```

---

## 5. Agent Plane 三个使用方现状

### 5.1 brain（Chat 模式底子）

**已落地**：`services/brain/internal/chat/agent_v2.go::AgentLoop.RunV2` 直接
调 `biumindkit.Submit`,替代了原来 88 行的 for-loop。`agentplane/chat_runner.go`
进一步把 `RunV2` 包成 in-process session runner,并在 PR-2 + cancel 路由
PR(d186112) 之后支持 `InterruptSession()`。

**API 是否够用**：✅ **完全够用**。原来列出的 5 个 brain 缺口（F1/F2/F3/F4/F5）
全部已闭环 —— 详见 §6 归档。

**预期使用代码**：

```go
// services/brain/internal/chat/biumindkit_runner.go（新文件，S4 阶段写）
func (h *HTTPSender) runWithBiumindkit(ctx context.Context, in AgentRunInput) error {
    ag, err := biumindkit.New(biumindkit.Options{
        APIKey:              in.Bearer,                  // 透传用户 JWT
        UseRelayAuth:          true,                       // 走 model-relay
        AnthropicEndpoint:   h.RelayURL,
        Model:               in.Model,
        System:              in.System,
        Cwd:                 "/dev/null",                // brain 没有 cwd 概念
        LoadProjectMemory:   biumindkit.NoMemory,        // 不加载用户机器 BIUMIND.md
        LoadProjectSettings: biumindkit.NoSettings,
        PermissionPolicy:    biumindkit.PermissionAllow(), // brain 4 个工具不需要 permission
        ExtraTools:          chatToolsAsBiumindkit(),    // wiki/memory/web/time → biumindkit.Tool
    })
    if err != nil { return err }
    defer ag.Close()

    for ev := range ag.Submit(ctx, in.UserPrompt) {
        switch e := ev.(type) {
        case biumindkit.AssistantText:
            in.Emitter.TextDelta(e.Text)
            // ... 现有 BlockEmitter 集成路径
        }
    }
    return nil
}
```

### 5.2 runtime（Task 模式）

**已落地**：`services/runtime/internal/agent/agent.go` 已切到 biumindkit
(S11-3 完成)。runtime 工具(skills / apps registry)走 `ExtraTools` 路径
注册;Hook 暴露通过 `Hooks() *hooks.Registry` 让上层 publisher 直接订
SDK Protocol HookCallback frame。

**API 是否够用**：✅ **完全够用**。原来列出的 3 个 runtime 缺口（F6/F7/F8）
全部已闭环 —— 详见 §6 归档。

**注意**：runtime 自带的 sandbox 容器跟 biumindkit 内置 BashTool
是两层独立的隔离 — biumindkit 不主动去包容器 SDK,runtime 在工具注册
时把 BashTool 替换成容器内执行版即可。

### 5.3 Flutter 桌面 daemon（Agent 模式）

**目标用法**：daemon 进程自身就是 biu CLI（`biu serve`），biumindkit 已经在用 —— 不需要额外集成。

**API 是否够用**：✅ **完全够用**（biu CLI 自己一直在用）。

---

## 6. Follow-up 归档（已全部完成）

> 本节是 S0-2 时识别的 13 个 follow-up 缺口的最终落地记录。所有 biumindkit
> 范畴内的工作（F1–F10、F13）已经合并;F11/F12 明确归调用方处理。后来人
> 看历史时能从这里反查 commit。

### 6.1 brain 集成相关 — 全部完成

| # | 缺口 | 落地 commit | 实际方案 |
|---|---|---|---|
| **F1** | `Options.MCPRegistry` 暴露 `internal/mcp` | S11-0 | 包了 `pkg/biumindkit/mcp.go` 的 `*MCPRegistry` wrapper,nil-safe;老字段已切 |
| **F2** | brain 需要 streamed parts JSON | S11-0 | 加 `AssistantBlock` event 在 `AssistantText` 之前 fire;暴露 `ContentBlock` alias |
| **F3** | brain 想注入用户级历史 | S11-0 | `Options.PriorMessages []Message`,`agent_v2.go` 拆 history 末位作 prompt,前面进 PriorMessages |
| **F4** | `Cost()` 没按工具拆分 | `9177b91` | `Agent.CostByTool() map[string]ToolCost`(Calls/ElapsedMs/OutputBytes/Errors),不摊分 token;runner.go 5 个退出路径都记录 |
| **F5** | 没有 cancellation / interrupt API | `e065fe5` | `Agent.Interrupt()` + cancel cause `engine.ErrInterrupted`;engine 走 clean-stop 合成未应答的 tool_result + emit Done{interrupted};ingress 反向路由在 `d186112` 完成 |

### 6.2 runtime 集成相关 — 全部完成

| # | 缺口 | 落地 commit | 实际方案 |
|---|---|---|---|
| **F6** | runtime 工具 per-run 注册 | S11-0 | `Options.ExtraTools []Tool` 接 closure 捕获 run-scoped state;runtime 在每次 Submit 之前重建 Agent |
| **F7** | runtime Hook → HookCallback frame | S11-0 | 暴露 `(a *Agent).Hooks() *hooks.Registry`,runtime publisher 直接订 EventPostToolUse 等 |
| **F8** | publisher emit 包成 Submit 流 | S11-3 | runtime 写了 frame adapter,把 biumindkit Event 翻译成 SDK Protocol `StdoutMessage` |

### 6.3 Flutter daemon / 共享相关

| # | 缺口 | 落地 commit | 实际方案 |
|---|---|---|---|
| **F9** | bridge.go 转发 ControlRequest | (existing) | `PermissionPolicy` 已经拦截 permission_request;其他 ControlRequest(set_model 等)按需扩,**当前没硬需求**,方案保持开放 |
| **F10** | session_id / parent_uuid 没暴露 | `b7539f6` | `Event` 接口加 `SessionID()` / `ParentToolUseID()`;每个 event 嵌入 `BaseEvent`;engine `Submit` 内置 stamping forwarder 自动填字段;`Options.SessionID` / `ParentToolUseID` 给 embedder 配置 |
| **F11** | 多设备订阅同一 session | n/a | **不属于 biumindkit 责任**:brain WS ingress 层做 fanout(JetStream `.out` subject 同 session 多 consumer);biumindkit 只保证 Submit 流稳定有序,这个已经是默认行为 |

### 6.4 协议兼容相关

| # | 缺口 | 落地 commit | 实际方案 |
|---|---|---|---|
| **F12** | Event ↔ SDKMessage variant 适配 | n/a | **不属于 biumindkit 责任**:`pkg/sdkbridge/mapping.go`(biu / brain 共享)做 7 SDK Event → 24+ SDKMessage variant 映射;Flutter / 小程序客户端在 sdkproto 里直接消费 |
| **F13** | `parent_tool_use_id` 链式追踪 | `b7539f6` | 跟 F10 同机制(`Event.ParentToolUseID()`);AgentTool / AgentBackground 自动把 `env.ToolUseID` 透传给 `AgentSpawnRequest.ParentToolUseID`,子 engine 所有事件 carry 该 id;sdkbridge 写进 `SDKToolProgress.parent_tool_use_id` *string omitempty |

### 6.5 顺手做的相关工作

虽然不在原 13 条 follow-up 列表里,但同期落地:

| 工作 | commit | 说明 |
|---|---|---|
| **Brain ingress 反向 cancel** | `d186112` | client SDKControlCancelRequest → brain ingress → JetStream control 队列 / 进程内 ChatRunner.InterruptSession → daemon worker 长轮询 → Agent.Interrupt。完整闭环 F5 |
| **Flutter cancel UX** | `b0b59a0` + `936eb3c` | Composer "Stopping..." 中间态 + 等 Done{interrupted} 落地 + 3s timeout 兜底;message 落 cancelled 不是 error |

---

## 7. 集成路径总结（历史记录）

> 这张表是 S0-2 时画的集成蓝图,当时还有大量 forward-looking todos。
> 现在所有阶段都已落地,留作历史记录。

| 阶段 | 落地动作 | follow-up 处理结果 |
|---|---|---|
| **S1**（协议层） | sdkproto v1 完成 | n/a |
| **S2**（biu bridge P1） | bridge 内调 `agent.Submit` 翻译成 SDK Protocol | F9 暂留,F10/F13 在 PR-1 落地 |
| **S3**（brain environments） | environments CRUD + agent_sessions 表 | n/a |
| **S4**（brain.AgentLoop 替换内核） | `agent_v2.go::RunV2` 直接调 `biumindkit.Submit` | F1/F2/F3 → S11-0;F4 → `9177b91`;F5 → `e065fe5` |
| **S5**（Flutter BiuClient） | Dart sdkproto + WS connection layer | F12 由 sdkbridge + Dart 客户端各自处理 |
| **S11**（runtime worker） | `services/runtime/internal/agent/agent.go` 切到 biumindkit | F6/F7 → S11-0;F8 → S11-3 |

实际工日影响（事后回顾）：S4 / S11 各 +1~1.5 工日,与原始预测吻合;F4/F5
是后期跟 cancel UX 一起做的,不在 S4/S11 工日内,单独预算。

---

## 8. Smoke Test 验证

```bash
cd /Users/didi/workspaces/biumind/apps/cli/biu
go build ./pkg/biumindkit/...    # ✅ 通过
go test ./pkg/biumindkit/        # ✅ ok（cached）
```

外部消费者（brain / runtime）通过添加 module replace 即可：

```go
// services/brain/go.mod
require github.com/biumind/biumind/apps/cli/biu v0.0.0
replace github.com/biumind/biumind/apps/cli/biu => ../../apps/cli/biu
```

S4 第一个 PR 做这件事时确认 brain / runtime 都能 import 成功。

---

## 9. 验收标准（DoD）

S0-2 阶段定义、v0.2 全部完成:

- ✅ 公开 API 完整列出（18 字段 Options + 7 Agent 方法 + 10 Event 类型 + Tool 接口 + 3 PermissionPolicy + 2 Mode 开关 + 2 Sentinels）
- ✅ brain / runtime / Flutter daemon 三个集成方都已经在生产路径上跑 biumindkit
- ✅ smoke 编译 + race 测试通过（`go test -race ./pkg/biumindkit/...`）
- ✅ 13 个 follow-up 缺口全部归档（详见 §6）
- ✅ 配套 cancel 反向通道:client → brain ingress → JetStream control 队列 → daemon → Agent.Interrupt 完整闭环

---

## 10. 参考

- 源码：[`sdk.go`](sdk.go) + [`tool.go`](tool.go)
- 现有 examples：`examples/{customtool,headless,policy,streaming}/`
- 现有 README：[`examples/README.md`](examples/README.md)
- Agent Plane 设计：[`../../../../../docs/BiuMind-Agent-Plane-Design.md`](../../../../../docs/BiuMind-Agent-Plane-Design.md)
- Schema 对照：[`../../../../../docs/BiuMind-Agent-Plane-Schema-Mapping.md`](../../../../../docs/BiuMind-Agent-Plane-Schema-Mapping.md)
- Dev Plan：[`../../../../../docs/BiuMind-Agent-Plane-Dev-Plan.md`](../../../../../docs/BiuMind-Agent-Plane-Dev-Plan.md) S4 / S11
