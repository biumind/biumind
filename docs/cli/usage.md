# 使用指南

本文覆盖 `biu` 的日常使用：REPL、斜杠命令、MCP、权限询问、成本统计、会话管理，以及 IDE / 桌面端集成（`biu bridge` 与 `biu serve`）。安装与首次配置见 [getting-started.md](getting-started.md)，全部子命令与 flag 参考（含本文未展开的 `biu agent`、`biu pair`、`biu plan`、`biu skill` 等）见 [commands.md](commands.md)。

## REPL 基础

在项目目录里直接运行：

```sh
biu
```

启动时会在 stderr 打印一行当前状态（`[biu] mode=… provider=… model=…`），随后进入交互界面：

- 直接输入自然语言发送对话；**工作目录持久**（跨命令保留），但 shell 状态（环境变量、`cd`）不保留。
- 输入 `/` 弹出斜杠命令面板：继续输入按前缀过滤，`↑` / `↓` 移动，`Tab` 补全，`Enter` 选中执行。
- `Ctrl-C`：流式输出中按下 = 中断当前回合并保留已生成内容；空闲时按下 = 退出。
- `Ctrl-D`：退出。
- 状态栏（底部）实时显示：模型、权限模式徽标（非 default 模式时整个状态栏会按模式着色）、累计花费（`$x.xxxx`）、上下文占用（`ctx NN% [██░░…]`）、轮次 / 流式状态，以及你自定义的 status-line 脚本输出。

常用顶层 flag（对子命令同样生效）：

```sh
biu --model <id>                # 临时切换模型
biu --continue                  # 恢复当前项目最近一次会话
biu --resume <session-id>       # 恢复指定会话（内容回放进引擎，原文件保留）
biu --no-log                    # 本次不写会话日志
biu --add-dir ../docs           # 临时追加一个可读写的工作目录
```

## 斜杠命令

在 REPL 输入 `/help` 可随时查看命令清单。完整列表如下（`[]` 内为可选参数）：

### 记忆与初始化

| 命令 | 说明 |
|------|------|
| `/init [--force\|--dry-run]` | 扫描当前项目并生成入门 `BIUMIND.md`（预填构建 / 测试 / lint 命令） |
| `/memory [list\|reload]` | 查看已加载的记忆文件与自动记忆状态；`reload` 在编辑后重新加载并热更新系统提示 |
| `/remember [-t <type>] <text>` | 保存一条记忆到 `~/.biumind/memory`（默认 type=user） |

记忆文件格式与加载层级见 [biumind-md.md](biumind-md.md)。

### 会话与历史

| 命令 | 说明 |
|------|------|
| `/sessions` | 列出最近的会话 |
| `/resume [#n\|latest\|<id>]` | 恢复历史会话；不带参数弹出编号选择器 |
| `/rename [<title>\|clear]` | 给当前会话起名，方便在 `/resume`、`/sessions` 里辨认 |
| `/export <path> [--format md\|json\|anthropic-replay]` | 把当前会话导出到文件 |
| `/share [md\|json\|<path>]` | 导出会话到临时文件并复制路径到剪贴板 |
| `/rewind [<uuid> [--dry-run]]` | 列出已捕获的文件快照；把文件恢复到某条消息之前的状态 |
| `/clear` | 清空历史，重新开始 |
| `/compact` | 压缩总结旧对话，释放上下文窗口 |
| `/stats` | 当前会话统计：时长、消息数、token、文件 |
| `/summary` | 本次会话的结构化小结（不调用模型） |

### 权限与安全

| 命令 | 说明 |
|------|------|
| `/permissions` | 查看当前生效的权限规则与模式（只读） |
| `/mode <mode>` | 切换权限模式（`default` / `acceptEdits` / `plan` / `bypass`） |
| `/add-dir <path> [--remember]` | 注册额外工作目录；`--remember` 持久化到 `.biumind/settings.local.json` |
| `/remove-dir <path>` | 移除一个工作目录 |
| `/hooks [<event-substring>]` | 列出各事件上注册的 hooks，可按事件名过滤 |
| `/trust [here\|session\|add <p>\|remove <p>]` | 管理哪些目录被信任运行 shell hooks / status-line 脚本 |
| `/reload` | 强制重新加载 settings.json（权限立即生效；hooks 需重启） |

权限规则语法与 hooks 配置见 [permissions.md](permissions.md)。

### 模型与输出

| 命令 | 说明 |
|------|------|
| `/model <id>` | 切换本次会话的模型 |
| `/effort [high\|medium\|low\|<model-id>]` | 切换推理深度档位；不带参数显示当前档位 |
| `/fast` | `/effort low` 的快捷方式，切到最快最便宜的模型 |
| `/output-style <name>` | 切换输出风格（concise / explanatory / …） |
| `/theme [dark\|light\|system]` | 切换配色 |
| `/break-cache` | 强制下一次请求跳过 prompt cache（仅调试用） |

### 成本统计

| 命令 | 说明 |
|------|------|
| `/cost [--by-tool]` | 本次会话累计 token 与美元开销；`--by-tool` 列每个工具的调用数 / 耗时 / 输出字节 / 错误数 |
| `/usage [today\|week\|month\|all] [<model-prefix>]` | 从历史用量台账读取累计 token 与美元开销，可按模型前缀过滤 |

命令行下等价命令是 `biu usage`（支持 `--since 7d|30d|all`、`--bucket day|week|month`、`--model <id>`、`--json`）。

### Git 与 GitHub

| 命令 | 说明 |
|------|------|
| `/commit [--dry-run\|--no-stage\|-m "msg"]` | 暂存 + 由模型起草 Conventional Commits 消息 + 提交 |
| `/pr [--dry-run\|--no-push\|--draft\|--base <br>\|--title <t>]` | 推送分支 + 模型起草 PR 标题 / 正文 + 经 `gh` 创建 |
| `/issue [<n>\|comment <n> "x"\|close <n>]` | 列出 / 查看 / 评论 / 关闭 GitHub issue |
| `/pr-comments [<n>]` | 查看 PR 审查评论（省略编号时取当前分支） |
| `/branch` | 当前分支、上游、脏状态、最近提交 |
| `/diff [staged\|<ref>]` | 紧凑的 `git diff --stat` 输出 |
| `/tag [<name> [-m "msg"\|--auto [--from <prev>]]]` | 列出 / 创建 tag；`--auto` 由模型起草 changelog |

### 子代理与计划

| 命令 | 说明 |
|------|------|
| `/agents [create <name> …]` | 列出已注册的子代理类型，或脚手架新建一个 |
| `/todo` | 打印会话内的任务清单 |
| `/plan [list\|show <id>\|diff\|approvals]` | 列出 / 打印计划、对比计划与实际工具调用、审计批量批准 |
| `/ultraplan <task>` | 派出 Plan 子代理设计实现方案 |
| `/review [scope]` | 派出 CodeReview 子代理审查（默认审查当前分支 diff） |
| `/verify [scope]` | 派出 Verification 子代理实际运行改动，结尾给出 VERDICT: PASS/FAIL/PARTIAL |
| `/workflow [<name> [args]\|show <name>]` | 列出 / 预览 / 触发自定义多步工作流 |

### 工具与运行环境

| 命令 | 说明 |
|------|------|
| `/mcp [<server>]` | 列出已连接的 MCP 服务器及其工具；指定名字则下钻到单个服务器 |
| `/tasks [list\|output <id> [n]\|kill <id>\|killall]` | 管理后台 Bash 任务 |
| `/plugin [<name>\|enable <n>\|disable <n>\|reload]` | 插件管理；启用 / 禁用持久化到 settings.json |
| `/doctor` | REPL 内健康自检（运行时 / git / shell / 引擎 / MCP） |
| `/env [<filter>]` | 显示 biu 相关的环境变量（KEY/TOKEN/SECRET 自动脱敏） |
| `/ide` | 显示 IDE bridge 端点与接入提示 |

### 账号、更新与杂项

| 命令 | 说明 |
|------|------|
| `/login` | 查看 OAuth 令牌状态（是否登录、过期时间） |
| `/logout` | 删除本地 OAuth 令牌（不吊销服务端） |
| `/upgrade [run\|check\|check skip]` | 更新 biu：手动安装走自更新，brew / go install / snap 委托给对应包管理器 |
| `/install` | 二进制诊断：版本、commit、安装方式、更新命令 |
| `/release-notes [full\|<substring>]` | 查看 biu 发行说明（默认最近 80 行） |
| `/telemetry [tail [N]\|export <path>\|enable <endpoint>\|disable]` | 遥测状态、日志尾巴 / 导出（默认关闭） |
| `/feedback ["summary"\|--print]` | 打开预填了版本与会话诊断的 GitHub issue |
| `/onboarding` | 新手引导 |
| `/copy [code\|<pattern>]` | 复制最近一条助手回复（或其中的代码块 / 匹配片段）到剪贴板 |
| `/help` | 显示命令清单 |
| `/quit` | 退出 |

> [!TIP]
> `~/.biumind/commands/` 目录下可以用 Markdown 自定义斜杠命令，它们会与内置命令一起出现在 `/` 面板里。

## 权限询问

当一次工具调用没有被规则放行、且当前模式需要确认时，REPL 会在输入框上方弹出内联确认框，显示工具名、输入摘要与原因，并给出快捷键：

- `a` —— 允许一次
- `shift+a`（或 `s`）—— 允许并记住（本会话内同类调用不再询问）
- `d` —— 拒绝
- `q` / `esc` —— 拒绝并中断当前回合

部分询问带有预计算的建议快捷键（例如 `w` = "允许并把该目录加入工作目录"），按下即应用对应的调整并放行。

规则、模式与决策顺序详见 [permissions.md](permissions.md)。

## 成本统计

三层视图：

1. **状态栏**：实时显示累计美元开销（低于 $0.0001 时隐藏）与上下文占用比例条。
2. **`/cost`**：本次会话的完整账单——输入 / cache 读 / cache 写 / 输出 token、缓存命中率、美元总额、上一轮的上下文占用；`/cost --by-tool` 追加每个工具的调用数、耗时、输出字节、错误数排行榜。
3. **`/usage` 与 `biu usage`**：跨会话的历史台账，数据来自 `~/.biu/usage.jsonl`（每次回合自动追加记录）。

## 会话管理

会话日志以 JSONL 形式存放在 `~/.biu/sessions/<项目目录>/<会话id>.jsonl`——按启动目录分桶，因此 `--continue` 只会找当前项目下最近的会话。`--no-log` 可以对单次运行禁用记录。

```sh
biu sessions list           # 当前项目的会话，最新在前
biu sessions list --all     # 所有项目
biu sessions show <id>      # 打印一个会话的全部事件（JSONL）
biu sessions export <id> --format markdown   # 导出为 markdown / json / anthropic-replay
```

> [!TIP]
> `biu sessions export` 在导出前会自动脱敏（api_key / token / refresh_token / virtual_key 字段，以及正文里的 Bearer、sk-ant-… 模式）；`--include-tool-output=false` 可整体丢弃工具输出，适合分享触及过敏感文件的会话。

恢复语义：

- `biu --resume <id>` / `biu --continue`：把历史会话的事件日志回放进引擎后继续对话。**恢复总是落到新的会话 id**，原始日志文件不会被改写（`--fork-session` flag 因此是显式的空操作，仅为兼容旧习惯保留）。
- 若原会话是在 worktree 里进行的，恢复时会一并回到该 worktree 目录与分支。
- `biu --rewind-files <uuid> --resume <id>`：把文件系统恢复到某条用户消息之前的状态后直接退出（配合 REPL 里的 `/rewind` 使用；消息 UUID 来自会话 JSONL 中的 `user_message` 事件）。

REPL 内对应 `/sessions`、`/resume`、`/rename`、`/export`。

## MCP 服务器

在 `~/.biu/config.toml` 里声明 MCP 服务器，`biu` 启动时拉起并把它们的工具并入本地工具目录，命名为 `mcp__<服务器名>__<工具名>`。

### stdio 传输（本地子进程）

```toml
[[mcp_servers]]
name    = "filesystem"
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
env     = { GITHUB_TOKEN = "ghp_…" }   # 可选
cwd     = ""                            # 可选
```

### HTTP 传输（Streamable HTTP）

```toml
[[mcp_servers]]
name      = "github"
transport = "http"
url       = "https://example.com/mcp/"
headers   = { Authorization = "Bearer …" }
```

HTTP 服务器若返回 401 + Bearer 质询，可以配置自动 PKCE 登录：

```toml
[[mcp_servers]]
name      = "github"
transport = "http"
url       = "https://mcp.example.com/mcp/"

  [mcp_servers.oauth]
  client_id     = "…"
  authorize_url = "https://github.com/login/oauth/authorize"
  token_url     = "https://github.com/login/oauth/access_token"
  scopes        = ["read:user", "repo"]
  callback_port = 0   # 0 = 随机空闲端口；服务端要求固定端口时填写
```

其他字段：

- `disabled = true`：保留配置但跳过启动。
- `defer_tools = true`：把该服务器的工具放进延迟目录——系统提示里只出现工具名，完整 JSON Schema 在模型需要时按需加载。适合工具数庞大的服务器（Slack / GitHub / Notion 类）。

诊断命令：

```sh
biu mcp list          # 逐个拉起并列出工具清单
biu mcp probe <name>  # 单独重启一个服务器并 dump tools/list
```

REPL 内用 `/mcp` 查看运行中的服务器与工具，`/mcp <name>` 下钻。

> [!TIP]
> MCP 工具同样受权限规则约束：`mcp__github` 这样的服务器级规则匹配该服务器的全部工具，`mcp__github__create_issue` 精确到单个工具。详见 [permissions.md](permissions.md#mcp-工具规则)。

## Headless 模式

```sh
biu --headless --prompt "总结这个仓库的架构"          # 纯文本输出
biu --headless --json --prompt "…"                    # stdout 输出 JSONL 事件流
echo "…" | biu --headless                             # prompt 也可从 stdin 读
```

`--json` 输出 AG-UI 兼容的 JSONL 事件（`RUN_STARTED`、`TEXT_MESSAGE_*`、工具调用与权限询问等），供 GUI / CI / SDK 消费。

无人值守运行时的权限策略用 `--permission-policy` 控制：

| 值 | 行为 |
|----|------|
| `deny`（默认） | 所有询问一律拒绝——宁可失败也不挂起 |
| `allow` | 全部放行 |
| `stdin` | 每次询问在终端提示一行，`a` 允许一次、`s` 总是允许、其他拒绝 |
| `stdin-json` | 面向 GUI：stdout 发出 `PERMISSION_ASK` JSON 事件（含建议快捷项），stdin 读一行 JSON 决策（`allow` / `deny` / `always`） |

另可用 `--permission-mode default|acceptEdits|bypassPermissions` 直接指定引擎权限模式（不填则回落到 settings.json 的 `defaultMode`）。

## IDE 集成：`biu bridge`

`biu bridge` 把代理能力通过 HTTP + WebSocket 暴露给 IDE 或远程 UI：

```sh
biu bridge --listen :8088 --auth-token <随机串>
```

- `--listen`：监听地址，默认 `:8088`；传 `:0` 自动分配端口，实际地址会打印到 stderr。
- `--auth-token`：非空时每个请求都必须带 `Authorization: Bearer <token>`；留空 = 无鉴权（仅限本机开发）。

每个会话（session）对应一个独立构建的 agent，多客户端之间不共享状态。

### HTTP 路由

| 路由 | 说明 |
|------|------|
| `POST /v1/code/sessions` | 创建会话，返回 `{"id": …}` |
| `POST /v1/code/sessions/:id/messages` | 提交一轮对话，body 为 `{"prompt": "…"}`；提交会中止该会话上一个进行中的回合 |
| `GET /v1/code/sessions/:id/ws` | WebSocket 流式接收事件（SDK Protocol v1 帧） |
| `GET /v1/code/sessions/:id/cost` | 该会话的成本快照（JSON） |
| `POST /v1/code/sessions/:id/compact` | 手动压缩历史 |
| `POST /v1/code/sessions/:id/attachments` | 上传附件 |
| `DELETE /v1/code/sessions/:id` | 关闭会话 |
| `GET /v1/code/ws` | 编码模块（终端 PTY / Git / 文件）共享 WebSocket |

### 断线续传

每个会话维护一个最近 256 条事件的环形缓冲。客户端重连 `GET /v1/code/sessions/:id/ws?last_event_id=N` 即可从断点补发错过的帧，不丢事件。

### 权限询问走 WebSocket

引擎需要授权时，bridge 通过 WebSocket 给客户端发一条 `can_use_tool` 控制请求；客户端回 `{"behavior": "allow"}` 或 `{"behavior": "deny"}`。**30 秒未回复自动拒绝**（安全默认），连接断开同样立即拒绝。

### VS Code 扩展

官方 VS Code 扩展会自动 spawn `biu bridge`（随机端口 + 每会话一次性令牌），提供会话面板、选中代码发送、取消回合等命令。扩展设置里可覆盖二进制路径（`biu.binaryPath`）、端口（`biu.bridgePort`）与初始权限模式（`biu.permissionMode`）。

## 守护进程：`biu serve`

`biu serve` 是 `biu bridge` 的超集，为桌面端 / 常驻场景设计——bridge HTTP 服务 + 健康检查 + 可选的远端调度注册，合为一个长驻进程：

```sh
biu serve --port 0 --pid-file ~/.biumind/biu.pid
```

- stdout 第一行打印 `BIU_BRIDGE_URL=http://127.0.0.1:<端口>`，父进程解析该行拿到实际端口。
- `GET /healthz`：探活（返回 `{"ok":true,…}`）；`GET /metrics`：Prometheus 指标。
- `--pid-file`：PID 文件保护——已有存活的 `biu serve` 会被新实例接管（热重启），无关进程占用则拒绝启动；带 PID 文件启动时还会监视父进程，父进程退出后自动收尾，避免遗留孤儿进程。
- 日志同时写 stderr 与 `~/.biu/logs/daemon.log`（超过 10 MB 自动截断），排查崩溃时有据可查。

### 注册为远端可调度的执行环境

加 `--register` 后，本机还会注册为服务端的 agent 运行环境（`biu_daemon`），远端客户端可以把你这台机器当作执行端调度：

```sh
BIUMIND_PAT=<pat> biu serve --register --brain-url https://your-biumind.example.com
```

| Flag | 说明 |
|------|------|
| `--register` | 启用 agent worker（注册 + 长轮询接活） |
| `--brain-url` | 服务端地址（默认取 `BIUMIND_BRAIN_URL` 或 `[model-relay].endpoint`） |
| `--identity-url` | identity 服务地址（默认与 brain 同源） |
| `--allowed-roots` | 本 daemon 允许触碰的文件系统根（可重复；缺省仅启动目录） |
| `--tool-policy` | 能力地板：`readonly` \| `workspace-write`（默认）\| `full` |

注册成功后 stdout 打印 `BIU_DAEMON_ENV_ID=<id>`。桌面端在运行中可通过 `POST /internal/token` 热推送新的访问令牌，无需重启 daemon。该端点不走 `--auth-token` 鉴权，设计上只供同机的父进程调用——因此 `biu serve` 对外监听时应绑定回环地址。

> [!NOTE]
> 单一职能入口仍然保留：只要本地 HTTP bridge 时用 `biu bridge`，只要 worker 时用 `biu agent worker`。`biu serve` 内部复用同一套实现，没有新协议。
