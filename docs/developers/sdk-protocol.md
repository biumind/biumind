# SDK Protocol（WebSocket 双向流）

客户端 / CLI 与 Agent 运行时之间的双向流协议：一条 WebSocket 上传 JSON 文本帧，承载**数据平面**（对话消息）、**控制平面**（权限询问 / 中断 / 配置）、**生命周期**（心跳 / 断线恢复）三组帧。协议的权威定义是仓库内的 JSON Schema（[`schema/sdk/v1/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/README.md)）与 Go 实现（[`packages/go-sdk/biu/sdkproto/v1/`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/unmarshal.go)），本文全部内容以这两处代码为准。

协议有两个接入面：

| 接入面 | 服务端 | 适用场景 |
|---|---|---|
| **云端 Agent Plane** | Brain 服务（`/v1/agent/sessions/...`） | Flutter 客户端、任意第三方 UI 远程驱动 BiuMind 托管的会话 |
| **本地 biu daemon bridge** | `biu serve` / `biu bridge` 进程（loopback HTTP + WS） | IDE / 本地工具驱动用户自己机器上的 biu 引擎 |

两个接入面使用**完全相同的帧格式**（同一个 `sdkproto.Frame` 集合），差别只在会话建立方式与断线重连机制（见[传输层](#2-传输层)）。

## 1. 总览：四组帧平面

每条 WS 消息就是一个 JSON 对象，一级判别字段是 `type`：

| 平面 | `type` 取值 | 方向 | 定义 |
|---|---|---|---|
| 数据平面 | `user` / `assistant` / `stream_event` / `result` / `system` / `auth_status` / `rate_limit_event` / `prompt_suggestion` / `tool_progress` / `tool_use_summary` / `streamlined_text` / `streamlined_tool_use_summary` | 双向（多数为服务端 → 客户端） | [`schema/sdk/v1/data/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/data/index.json) |
| 控制平面 | `control_request` / `control_response` / `control_cancel_request` | 双向 | [`schema/sdk/v1/control/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/control/initialize.json) |
| 生命周期 | `keep_alive` / `update_environment_variables` / `biumind.*`（共 8 种） | 双向（有方向限制） | [`schema/sdk/v1/lifecycle.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/lifecycle.json) |
| 编码模块 | `code_request` / `code_response` / `code_pty_*` / `code_session_event`（共 7 种） | 双向 | [`packages/go-sdk/biu/sdkproto/v1/code.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/code.go)，仅本地 bridge 的 `/v1/code/ws` 通道使用 |

通用解析入口是 [`UnmarshalFrame`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/service.go)：先按 `type` 分到平面，再走该平面的二级判别（见下）。协议是**封闭集合**——`Frame` 接口的 `isFrame()` 方法未导出，外部包不能新增 wire 类型（`service.go` 注释明确此约束）。

## 2. 传输层

### 2.1 云端 Agent Plane

#### 端点寻址

客户端只配一个「服务器地址」。官方部署里所有路径经站点 nginx 反代（[`web/site/nginx.conf`](https://github.com/biumind/biumind/blob/main/web/site/nginx.conf) 的 `location /v1/agent/`，上游 `http://brain:7003`）。该 location 针对 WS 做了必要配置：`proxy_http_version 1.1` + `Upgrade` 头透传、`proxy_read_timeout 600s`、**`proxy_buffering off`**（不关 buffering 的话服务端帧会被 nginx 攒着不吐）。

会话建立是「先 HTTP、后 WS」两步：

**第一步：创建会话**（`POST /v1/agent/sessions`，鉴权 `Authorization: Bearer <JWT 或 PAT>`）

请求体核心字段（完整定义见 [`services/brain/internal/agentplane/router.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/router.go) 的 `CreateSessionAPIReq`）：

```json
{
  "mode": "chat",
  "thread_id": "可选，关联对话线程",
  "model": "可选，留空由服务端按默认链解析",
  "prompt": "本轮用户输入（chat / agent / task 模式的首条消息）",
  "environment_id": "agent 模式必填；chat / task 留空",
  "workdir": "agent / task 模式的工作目录",
  "images": [{ "mime_type": "image/png", "data": "<base64，不带 data: 前缀>" }]
}
```

`mode` 三选一（`biumind_ext.json` 的 `Mode` enum）：

- `chat`：Brain 进程内直接驱动模型，不绑执行环境；
- `agent`：投给你自己的设备（`biu_daemon` / `biu_cli` 注册的 environment），`environment_id` 必填；
- `task`：从在线 runtime 池自动挑一个执行环境。

成功响应 `201`（`writeSessionCreated`，router.go）：

```json
{
  "session_id": "b0b6a3e2-…",
  "session_token": "<短效 JWT，30 分钟>",
  "expires_at": 1735689600000,
  "mode": "chat",
  "state": "active",
  "jetstream_subject_in": "biu.session.<sid>.in",
  "jetstream_subject_out": "biu.session.<sid>.out",
  "created_at": 1735687800000
}
```

> [!NOTE]
> `state` 为 `pending` 表示目标设备当前离线、任务已排队（设备上线后自动派发）——此时不要连 WS，等设备上线后再恢复。绑定了执行环境的会话（`agent` / `task`）额外返回 `environment_id`；创建时传了 `thread_id` 的会话额外返回 `thread_id`。

**第二步：连接 WS 流**

```text
GET /v1/agent/sessions/{session_id}/stream?session_token=<30分钟JWT>[&since_seq=<n>]
```

- 鉴权用 `session_token`：优先 query 参数（浏览器 WS 客户端不方便设 header），也接受 `Authorization: Bearer` 头。token 的 scope 必须匹配 URL 里的 session_id（拿 A 会话的 token 连 B 会话会被拒）。
- token 过期前调 `POST /v1/agent/sessions/{id}/refresh-token`（带长效凭证）换新 token。token 在连接建立时校验，新 token 于下次（重）连生效——Flutter 客户端在到期前 5 分钟定时刷新（`_scheduleTokenRefresh`）。
- **不协商 WS 子协议**（服务端 gorilla `Upgrader` 未配置 `Subprotocols`，客户端无需发送 `Sec-WebSocket-Protocol`）；帧一律是 **WS text frame**，一条消息一个 JSON 对象，无外层信封。
- 会话已进入终态（`completed` / `failed` / `cancelled`）时连接返回 `409 session_finalized`，响应里会指路 `GET /v1/agent/sessions/{id}/result` 取最终结果。

实现：[`services/brain/internal/agentplane/ingress.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/ingress.go)。

#### 心跳与帧大小限制

- **WS 协议层**：服务端每 30 秒发一个 ping，60 秒内收不到 pong 即断开（`ingressPingPeriod` / `ingressPongWait`）。客户端只要响应标准 pong 即可，不需要自己实现应用层心跳。
- **帧大小**：单条入站帧上限 32 KB（`ingressMaxFrameSize`）；LLM 流式单块通常远小于此。
- 云端路径**不会**周期性发送 `keep_alive` 应用帧（brain 侧无任何 KeepAlive 发布代码）；`keep_alive` 帧目前只在本地 bridge 出现（见 2.2）。

#### 断线重连：`?since_seq=N`

服务端把每个会话的下行帧逐条写入 NATS JetStream（stream `BIU_SESSIONS`，subject `biu.session.<sid>.out`，**保留期 1 小时**）。重连时带 `?since_seq=<已见最大序号>`：

- `since_seq` 缺省或为 0：实时拉，只看新帧；
- `since_seq > 0`：服务端用 OrderedConsumer 从 `since_seq + 1` 开始**重放历史帧**，重放完无缝接实时流；
- 若你要的历史已被流保留策略清理（`since_seq + 1 < 流当前最早序号`），服务端推一帧 `biumind.session_desynced` 后正常关闭连接——客户端按帧内 `final_result_url` 去取会话最终结果兜底。

```json
{
  "type": "biumind.session_desynced",
  "session_id": "b0b6a3e2-…",
  "final_result_url": "/v1/agent/sessions/b0b6a3e2-…/result",
  "since_seq": 42,
  "reason": "requested seq 42 before stream first seq 100"
}
```

> [!WARNING]
> `since_seq` 是「客户端**已经看过**的最大序号」，不是「想从哪条开始看」。客户端要自己持久化已见序号（Flutter 端 `BiuSessionConnection` 每处理 10 帧落一次库）。会话流只在 JetStream 保留 1 小时，更早的帧无法重放。另注意：v1 的下行帧**不携带服务端序号字段**，Flutter 客户端用本地收帧计数近似（`biu_client.dart` 注释明确把「帧内嵌 seq」列为后续版本工作）——重放起点可能因流内混入其他消息而略有偏移，集成方如需精确重放应自行评估（本地 bridge 的 `last_event_id` 无此问题，缓冲只存下行帧）。

客户端重连的参考实现：[`apps/client/lib/data/api/biu_client.dart`](https://github.com/biumind/biumind/blob/main/apps/client/lib/data/api/biu_client.dart)——指数退避 1 秒到 30 秒、最多 8 次；连续失败超过 2 次触发 token 刷新回调。

### 2.2 本地 biu daemon bridge

`biu serve`（桌面端嵌入守护进程）或 `biu bridge` 在本机起一个 HTTP + WS 服务（[`apps/cli/biu/internal/bridge/`](https://github.com/biumind/biumind/blob/main/apps/cli/biu/internal/bridge/server.go)）。鉴权可选：`Options.AuthToken` 非空时所有请求要求 `Authorization: Bearer <token>`，空则不鉴权（仅限 loopback / 开发环境）。

路由（server.go 顶部注释）：

| 路由 | 作用 |
|---|---|
| `POST /v1/code/sessions` | 创建会话，返回 `{ "id": "…" }` |
| `POST /v1/code/sessions/{id}/messages` | 提交一轮任务，body `{"prompt":"…"}`；语义是「中止当前轮 + 重开一轮」 |
| `GET /v1/code/sessions/{id}/ws` | WS 流（帧格式与云端完全一致） |
| `GET /v1/code/ws` | 编码模块专用通道（`code_*` 帧，PTY / Git / 文件系统） |
| `GET /v1/code/sessions/{id}/cost` | 费用快照 |
| `POST /v1/code/sessions/{id}/compact` | 手动压缩上下文 |
| `POST /v1/code/sessions/{id}/attachments` | 上传附件 |
| `DELETE /v1/code/sessions/{id}` | 关闭会话 |

#### 断线重连：`?last_event_id=N`（环形缓冲）

与云端的 JetStream 方案不同，本地 bridge 在每个会话内维护一个 **256 帧的内存环形缓冲**（`eventBufferCap`，server.go）：每帧分配单调递增 id，重连时带 `?last_event_id=N` 重放 id 大于 N 的帧，然后接实时推流。

- 没有在跑的任务、又没带 resume 游标时，连接返回 `409 no turn in progress`；
- 任务已跑完（缓冲还在）时，重放完后推一帧 `keep_alive` 作为「流正常结束」哨兵，然后以 `CloseNormalClosure("done")` 关闭连接。

心跳同样是 WS 协议层 ping（30 秒一次，60 秒无 pong 断开），与云端参数一致（`wsPingPeriod` / `wsPongWait`，[`ws.go`](https://github.com/biumind/biumind/blob/main/apps/cli/biu/internal/bridge/ws.go)）。

> [!NOTE]
> 两种接入面的恢复游标**不通用**：云端用 `since_seq`（JetStream 消息序号），本地用 `last_event_id`（进程内环形缓冲序号）。两者都只在各自进程 / 流的生命周期内有意义。

### 2.3 帧在服务端的流转（云端）

理解一条帧的路径有助于排障。云端一帧的旅程：

```text
客户端 ──WS──▶ brain ingress ──publish──▶ biu.session.<sid>.in（JetStream）
brain ingress ◀─subscribe── biu.session.<sid>.out ◀─publish── 执行方（chat: brain 进程内；agent/task: worker）
```

入站帧里有两类会**旁路** `.in` 主题、直接走控制队列投给执行方（ingress.go）：

- `control_cancel_request` —— 触发会话中断；
- `control_response` —— 权限答复 / 提问表单作答（按 `request_id` 分流）。

其余入站帧原样发布到 `.in` 主题存档。**当前执行方不消费 `.in` 主题上的数据帧**（多轮 user 消息直发 WS 尚未接通），因此云端一轮回来的标准做法是：prompt 走创建会话的 HTTP 请求，WS 只收帧 + 发控制帧。

## 3. 消息模型

### 3.1 判别规则（union discriminator）

帧的判别是「先 `type`、后二级字段」两步（[`unmarshal.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/unmarshal.go) 与 [`wrappers.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/wrappers.go)）：

1. `type` 字段决定平面（数据 / 控制 / 生命周期 / 编码）；
2. 数据平面内部：
   - `type: "system"` 的帧再用 `subtype` 区分 16 个子类型；
   - `type: "result"` 用 **`is_error`** 区分成功 / 失败（不是 `subtype`——两种 result 共享大部分字段，`subtype` 分别为 `"success"` / `"error_*"`）；
   - 其余 `type` 一对一对应一个结构；
3. 控制平面内部：`control_request` 的 `request` 对象用 `subtype` 区分 21 种请求。

各平面的多数结构带这些公共字段（具体见各结构定义）：

- `uuid`：本帧唯一标识（帧级别，非会话级别）；
- `session_id`：所属会话；
- `parent_tool_use_id`：子代理嵌套场景下，本消息归属的上层工具调用 id（`user` / `assistant` / `stream_event` / `tool_progress` 上可出现）；
- `type: "system"` 系帧额外带 `subtype`。

Schema 层面所有对象 **open by default**（`additionalProperties: true`，见 [`_common.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/_common.json) 描述）：服务端可能附加未来字段，客户端解析必须容忍未知字段。

> [!NOTE]
> 个别代码注释里仍写着「24 个 / 29 个 variant」的历史数字，与实现不符。**以 [`unmarshal.go` 的 `isSDKMessage()` 清单为准：数据平面共 28 个 variant**（下表逐个列出，`data/index.json` 的 union 也是 28 个引用）。

### 3.2 数据平面：28 个 variant

| `type` | （`subtype`） | Go 结构 | 含义 |
|---|---|---|---|
| `user` | — | `SDKUserMessage` | 用户消息（含工具结果回传：`tool_use_result` 字段）。`isReplay=true` 的重放形态在 schema 里单列为 `SDKUserMessageReplay`，Go 侧同一结构 |
| `assistant` | — | `SDKAssistantMessage` | 一轮完整助手消息，`message.content` 是 content block 数组（`text` / `tool_use` 等） |
| `stream_event` | — | `SDKPartialAssistantMessage` | 上游模型流的原始增量事件（`event` 为透传 JSON） |
| `result` | `success` | `SDKResultSuccess` | 一轮结束（成功）。含 `duration_ms` / `num_turns` / `total_cost_usd` / `usage` / `modelUsage` / `stop_reason` |
| `result` | `error_*`（`is_error=true`） | `SDKResultError` | 一轮结束（失败）。同上 + `errors` 数组 |
| `system` | `init` | `SDKSystemInit` | 会话初始化快照（工具清单 / 模型 / 权限模式等） |
| `system` | `status` | `SDKSystemStatus` | 引擎状态 |
| `system` | `compact_boundary` | `SDKCompactBoundary` | 上下文压缩完成边界 |
| `system` | `api_retry` | `SDKAPIRetry` | 上游模型调用重试通知 |
| `system` | `local_command_output` | `SDKLocalCommandOutput` | 本地命令输出 |
| `system` | `hook_started` | `SDKHookStarted` | hook 开始执行 |
| `system` | `hook_progress` | `SDKHookProgress` | hook 执行中间输出 |
| `system` | `hook_response` | `SDKHookResponse` | hook 执行结果 |
| `system` | `files_persisted` | `SDKFilesPersisted` | 文件持久化完成 |
| `system` | `task_notification` | `SDKTaskNotification` | 后台任务通知 |
| `system` | `task_started` | `SDKTaskStarted` | 后台任务开始 |
| `system` | `task_progress` | `SDKTaskProgress` | 后台任务进度 |
| `system` | `session_state_changed` | `SDKSessionStateChanged` | 会话状态切换（`idle` / `running` / `requires_action`） |
| `system` | `elicitation_complete` | `SDKElicitationComplete` | MCP 服务器发起的提问流程结束 |
| `system` | `form_answer` | `SDKFormAnswer` | 提问表单的终态（`action`: `accept` / `decline` / `cancel` / `timeout`） |
| `system` | `post_turn_summary` | `SDKPostTurnSummary` | 一轮结束后的结构化摘要 |
| `auth_status` | — | `SDKAuthStatus` | 认证状态变化 |
| `rate_limit_event` | — | `SDKRateLimitEvent` | 上游速率限制事件 |
| `prompt_suggestion` | — | `SDKPromptSuggestion` | 输入建议 |
| `tool_progress` | — | `SDKToolProgress` | 工具调用进行中（含 `tool_use_id` / `tool_name` / 耗时） |
| `tool_use_summary` | — | `SDKToolUseSummary` | 工具调用结果摘要（`preceding_tool_use_ids` 关联到 `tool_use_id`） |
| `streamlined_text` | — | `SDKStreamlinedText` | **推荐的流式文本增量**——轻量帧，客户端直接拼接渲染 |
| `streamlined_tool_use_summary` | — | `SDKStreamlinedToolUseSummary` | 轻量工具结果摘要 |

（表中「（`subtype`）」列的 `success` / `error_*` 是 `result` 帧的两种形态；`system` 的 16 个子类型各计一个 variant。）

字段级定义逐类见 [`schema/sdk/v1/data/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/data/index.json)（`user.json` / `assistant.json` / `result.json` / `system.json` / `tool.json` / `post_turn.json` / `streamlined.json`）与 [`data.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/data.go)。

> [!NOTE]
> 同一份流式文本可能以两种形式到达：`streamlined_text` 增量帧，以及 `assistant` 帧 `message.content` 里的 `text` 块（权威完整副本）。Flutter 客户端的策略是：已有流式文本块时跳过 `assistant` 帧里的 `text` 项，避免重复渲染（`biu_session_connection.dart` 的 `_onAssistantMessage`）。

### 3.3 控制平面

三种顶层帧：

**`control_request`**（`{type, request_id, request:{subtype, …}}`），`request.subtype` 共 21 种（[`control.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/control.go)）：

| `subtype` | 用途 |
|---|---|
| `initialize` | 会话初始化配置（hooks / MCP 服务器 / 系统提示词 / agents 等） |
| `interrupt` | 中断当前轮（本地 bridge 路径） |
| `can_use_tool` | **服务端 → 客户端**的权限询问（`tool_name` / `input` / `tool_use_id` 等） |
| `set_model` | 切换模型 |
| `set_permission_mode` | 切换权限模式（`default` / `acceptEdits` / `bypassPermissions` / `plan` / `dontAsk`） |
| `set_max_thinking_tokens` | 调整思考预算 |
| `mcp_status` / `mcp_message` / `mcp_set_servers` / `mcp_reconnect` / `mcp_toggle` | MCP 服务器管理 |
| `get_context_usage` | 查询上下文占用 |
| `hook_callback` | 回应服务端挂起的 hook 回调 |
| `rewind_files` | 回滚文件到某条用户消息时点 |
| `cancel_async_message` | 取消某条异步消息 |
| `seed_read_state` | 预置文件读取状态 |
| `reload_plugins` | 重载插件 |
| `stop_task` | 停止后台任务 |
| `apply_flag_settings` / `get_settings` | 设置读写 |
| `elicitation` | **服务端 → 客户端**的提问表单（`message` / `mode: form\|url` / `requested_schema`） |

**`control_response`**（`{type, kind?, response:{subtype, request_id, response?, error?}}`）——对某个 `request_id` 的应答，`subtype` 为 `success` / `error`。`kind` 字段（`elicitation_response` / `permission_response`）显式声明回包种类，服务端兼容不带的旧客户端（按回包体形状判别）。两种常见回包体：

- 权限答复：`{"behavior": "allow" | "deny", "updatedInput": …, "updatedPermissions": …}`（完整定义见 [`permissions.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/permissions.json) 的 `PermissionResult`）；
- 表单作答：`{"action": "accept" | "decline" | "cancel", "content": {…}}`。

**`control_cancel_request`**（`{type, request_id}`）——取消整个会话的当前执行，是独立顶层 type，与 `cancel_async_message`（subtype）不同。

### 3.4 生命周期：8 种 BiuMind 自有帧

[`lifecycle.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/lifecycle.json) / [`lifecycle.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/lifecycle.go)：

| `type` | 方向 | 含义 |
|---|---|---|
| `keep_alive` | 双向 | 心跳 / 流结束哨兵（`ts` 毫秒时间戳） |
| `update_environment_variables` | 客户端 → 服务端 | 更新会话环境变量 |
| `biumind.session_desynced` | 服务端 → 客户端 | 请求的历史序号已被清理，按 `final_result_url` 取最终结果 |
| `biumind.session_paused` | 服务端 → 客户端 | 会话暂停（如执行方在等表单作答时掉线） |
| `biumind.session_resumed` | 服务端 → 客户端 | 暂停会话已恢复，流继续 |
| `biumind.session_primary_promoted` | 服务端 → 客户端 | 多副本场景下的主副本切换通知 |
| `biumind.compact_started` | 服务端 → 客户端 | 上下文压缩开始（`reason` / `tokens_before`） |
| `biumind.compact_finished` | 服务端 → 客户端 | 压缩完成（`tokens_before` / `tokens_after` / `tokens_saved`） |

### 3.5 编码模块：7 种 `code_*` 帧

仅本地 bridge 的 `GET /v1/code/ws` 通道（`code.go`）：`code_request` / `code_response` 是通用 RPC 信封（`method` 分发 `git.status` / `fs.read` / `pty.open` 等）；`code_pty_chunk` / `code_pty_input` / `code_pty_resize` / `code_pty_exit` 是 PTY 字节流（字节经 JSON 自动 base64，无损传输）；`code_session_event` 是结构化会话事件（外部编码代理会话的 JSONL 事件解析结果）。

### 3.6 方向规则

同一套帧双向共用，[`service.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/service.json) 的两个 union 定义了名义方向：

- **StdinMessage（客户端 → 服务端）**：全部 `SDKMessage` + `control_request` + `control_cancel_request` + `update_environment_variables`；
- **StdoutMessage（服务端 → 客户端）**：全部 `SDKMessage` + `control_request`（反向询问）+ `control_response` + 除 `update_environment_variables` 外的 7 种生命周期帧。

> [!NOTE]
> `control_response` 实际上**双向都会出现**：服务端用它应答客户端的 `control_request`（本地 bridge 的中断请求），客户端用它应答服务端反向发起的权限 / 表单询问（两个接入面的服务端都显式处理这一方向，见 [`ws.go`](https://github.com/biumind/biumind/blob/main/apps/cli/biu/internal/bridge/ws.go) 的 read pump 与 [`ingress.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/ingress.go) 的 `maybeRoutePermissionResponse`）。Go 侧 `IsStdinMessage` 助手把 `control_response` 判为非上行方向，与实际收发路径不一致，仅用于单测断言——集成时以上述实际路径为准。

### 3.7 JSON Schema 与校验

- Schema 根入口：[`schema/sdk/v1/index.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/index.json)（`StdinMessage` / `StdoutMessage` union）、`data/index.json`（`SDKMessage` union）；
- 实例样本（每种帧一份可校验样例）：[`schema/sdk/v1/fixtures/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/fixtures/system_init.json)；
- 校验工具：`make schema-validate`（仓库根；工具实现 `tools/schema-validate/main.go`，schema draft 2020-12）。

> [!WARNING]
> fixture 的 `$schema` 字段必须指向具体 `$defs/<Type>`（schema 文件顶层只有 `$defs`、无约束，指向文件根会让校验空转通过）。

## 4. 会话生命周期与事件流

### 4.1 一轮的标准帧序列（云端 chat 模式）

首条用户消息随创建会话的 HTTP 请求传入（`prompt` 字段），随后 WS 上按序到达（帧生成逻辑：brain 进程内 [`FrameEmitter`](https://github.com/biumind/biumind/blob/main/services/brain/internal/chat/frame_emitter.go)，daemon / runtime worker 路径同构）：

```text
服务端 → 客户端                     说明
streamlined_text          ×N        流式文本增量（每帧一小段，客户端拼接）
tool_progress                       某工具开始执行
streamlined_tool_use_summary        （或 tool_use_summary）工具结果摘要
streamlined_text          ×N        工具结果后的继续输出
assistant                           本轮完整消息快照（content block 数组）
result (subtype=success)            本轮结束：耗时 / 轮数 / 用量 / 费用 / stop_reason
```

`result` 帧是**一轮的终止信号**：客户端收到后即可收尾渲染。`stop_reason` 为 `interrupted` 时表示用户取消走的干净停止路径，应按「已取消」而非「失败」渲染。

出错时以 `result`（`is_error: true`，`subtype: error_*`，带 `errors` 数组）终止。会话进入终态后，`result` 帧决定了会话状态（`completed` / `failed` / `cancelled`）。

### 4.2 权限询问（服务端 → 客户端 → 服务端）

执行方要跑一个需要授权的工具时，反向发一条 `control_request`：

```json
{
  "type": "control_request",
  "request_id": "9f2c…",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "Bash",
    "input": { "command": "rm -rf build" },
    "tool_use_id": "toolu_01…",
    "decision_reason": "destructive command"
  }
}
```

客户端弹卡，用户决策后回：

```json
{
  "type": "control_response",
  "kind": "permission_response",
  "response": {
    "subtype": "success",
    "request_id": "9f2c…",
    "response": { "behavior": "deny", "message": "user rejected" }
  }
}
```

> [!WARNING]
> 权限询问有 **30 秒超时**，超时或回包解析失败一律按 `deny` 处理（「默认拒绝」是安全侧，见 bridge `askPermission` 与 worker 同名实现）。客户端断线时正在挂起的询问也会落到 deny。

### 4.3 提问表单（elicitation）

执行方需要结构化用户输入时（选项 / 表单），发 `control_request`（`request.subtype: "elicitation"`，`mode: "form"`，`requested_schema` 是 JSON Schema 风格描述）。客户端渲染表单，作答回 `control_response`（`kind: "elicitation_response"`，`response.response` 为 `{"action":"accept","content":{"answer":"…"}}`）。服务端随后推 `system` + `subtype: form_answer` 终态帧沉淀问答结果。不应答时服务端约 5 分钟超时兜底，会话不死。

### 4.4 中断

- **云端**：客户端发 `control_cancel_request`（`{"type":"control_cancel_request","request_id":"cancel-…"}`）。执行方走干净停止路径：补发 `result`（`stop_reason: "interrupted"`）+ 为未完成的工具调用合成结果；
- **本地 bridge**：WS 上发 `control_request`（`request.subtype: "interrupt"`），服务端回 `control_response`（成功或 `"no turn in progress"` 错误）。

> [!NOTE]
> 发出取消后**不要立刻关连接**——干净停止需要时间投递最终 `result` 帧。Flutter 客户端设 3 秒兜底窗口，窗口内等到 `stop_reason: "interrupted"` 算干净取消，否则按已取消强制收尾（`_cancelGraceWindow`）。

### 4.5 暂停与恢复（durable resume）

执行方在等表单作答时掉线，brain 把会话置 `paused` 并推 `biumind.session_paused`。客户端仍可对挂起表单作答（迟到作答会落库），随后调 `POST /v1/agent/sessions/{id}/resume` 重跑，服务端推 `biumind.session_resumed` 后流式帧继续。挂起中的表单可用 `GET /v1/agent/sessions/{id}/elicitations` 拉回（路由见 [`resume.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/resume.go)）。

### 4.6 会话收尾

- **云端**：`result` 帧后连接**不会**被服务端自动关闭（可继续等下一轮 / 重新订阅），客户端确认收到后主动关；会话状态落终态后再连 `/stream` 会得到 `409 session_finalized`；
- **本地 bridge**：当前轮跑完，服务端推一帧 `keep_alive` 哨兵后以 `CloseNormalClosure("done")` 主动关闭连接。

## 5. 三端实现指针

| 语言 | 位置 | 说明 |
|---|---|---|
| Go | [`packages/go-sdk/biu/sdkproto/v1/`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/service.go) | 权威实现。`UnmarshalFrame`（任意帧）/ `UnmarshalSDKMessage` / `UnmarshalControlRequestInner` / `UnmarshalLifecycle`；105 个单测覆盖往返保真 |
| Dart | [`apps/client/lib/data/api/sdkproto/v1/`](https://github.com/biumind/biumind/blob/main/apps/client/lib/data/api/sdkproto/v1/service.dart) | sealed class + `ServiceFrame.fromJson` 工厂（`sdk_message.dart` 等数据类，`json_serializable` 生成 `.g.dart`）；上层封装 [`BiuClient`](https://github.com/biumind/biumind/blob/main/apps/client/lib/data/api/biu_client.dart)（连接 / 重连 / 离线队列）与 [`BiuSessionConnection`](https://github.com/biumind/biumind/blob/main/apps/client/lib/features/chat/data/biu_session_connection.dart)（帧 → 消息块渲染） |
| TypeScript | [`apps/miniapp/src/lib/sdkproto/v1/index.ts`](https://github.com/biumind/biumind/blob/main/apps/miniapp/src/lib/sdkproto/v1/index.ts) | 占位类型别名（计划 vendor Zod schema），当前仅 `Mode` 常量可用 |

自研客户端最省事的路径：直接引用 `schema/sdk/v1/` 的 JSON Schema 生成解析代码（Go 端即由此手写对齐），解析入口按 3.1 的两步判别实现。

## 6. 最小集成示例

以下序列来自代码与 schema fixture（`fixtures/system_init.json`、`fixtures/user__basic.json`、`fixtures/result_success.json`、`fixtures/streamlined_text.json` 及 `BiuClient.sendUserText` / bridge `askPermission` 的序列化字段），可直接当作集成对照样本。

**步骤 1 —— 创建会话：**

```bash
curl -X POST https://your-biumind.example.com/v1/agent/sessions \
  -H "Authorization: Bearer <你的PAT或JWT>" \
  -H "Content-Type: application/json" \
  -d '{"mode":"chat","prompt":"帮我看看这个仓库的结构"}'
```

**步骤 2 —— 连接 WS**（任一语言的标准 WebSocket 客户端）：

```text
wss://your-biumind.example.com/v1/agent/sessions/<session_id>/stream?session_token=<session_token>
```

**步骤 3 —— 收帧并处理。** 浏览器 JavaScript 骨架：

```javascript
const ws = new WebSocket(
  `wss://your-biumind.example.com/v1/agent/sessions/${sessionId}/stream` +
  `?session_token=${sessionToken}`
);

let text = "";
ws.onmessage = (e) => {
  const frame = JSON.parse(e.data);          // 一条消息 = 一个 JSON 帧
  switch (frame.type) {
    case "streamlined_text":                 // 流式文本：拼接渲染
      text += frame.text;
      break;
    case "assistant":                        // 本轮完整快照（含 tool_use 块）
      break;
    case "result":                           // 一轮终止信号
      if (frame.is_error) { /* 展示 frame.errors */ }
      else { /* 展示 frame.result / frame.usage / frame.total_cost_usd */ }
      ws.close();                            // 客户端主动收尾
      break;
    case "control_request":                  // 反向询问：权限 / 表单
      if (frame.request.subtype === "can_use_tool") {
        ws.send(JSON.stringify({
          type: "control_response",
          kind: "permission_response",
          response: {
            subtype: "success",
            request_id: frame.request_id,
            response: { behavior: "allow" }  // 或 { behavior: "deny", message: "…" }
          }
        }));
      }
      break;
    case "biumind.session_desynced":         // 重连游标过期：去取最终结果
      fetch(frame.final_result_url, { headers: { Authorization: "Bearer <长效凭证>" } });
      break;
    default:
      break;                                 // 未知 type / 字段：容忍，不报错
  }
};
```

**对照：一条 user 数据帧的 wire 形态**（多轮场景客户端上行，或回放历史时服务端下行）：

```json
{
  "type": "user",
  "message": { "role": "user", "content": [{ "type": "text", "text": "hi" }] },
  "uuid": "u1",
  "session_id": "s1"
}
```

**对照：一条成功的 result 帧**（fixture `result_success.json`）：

```json
{
  "type": "result",
  "subtype": "success",
  "duration_ms": 1000,
  "duration_api_ms": 800,
  "is_error": false,
  "num_turns": 3,
  "result": "done",
  "stop_reason": "end_turn",
  "total_cost_usd": 0.01,
  "usage": {},
  "modelUsage": {
    "claude-3-7": {
      "inputTokens": 10,
      "outputTokens": 20,
      "cacheReadInputTokens": 0,
      "cacheCreationInputTokens": 0,
      "webSearchRequests": 0,
      "costUSD": 0.01,
      "contextWindow": 200000,
      "maxOutputTokens": 4096
    }
  },
  "permission_denials": [],
  "uuid": "r1",
  "session_id": "s1"
}
```

**断线重连**：记录已处理的帧数作为游标，重连时带上（云端 `since_seq` / 本地 `last_event_id`），服务端会先补发错过的帧、再无缝接实时流。
