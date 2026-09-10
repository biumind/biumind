# `biu` 命令参考

`biu` 是 BiuMind 的命令行工具：在终端里与 AI 对话、让 AI 读写代码与文件、把本机注册成可被远端调度的执行环境，以及管理插件、技能和应用。

本文覆盖 `biu` 的全部子命令与 flag。REPL 内的斜杠命令（`/help`、`/model` 等）见[顶层用法](#顶层用法biu--repl)一节。

> [!TIP]
> 顶层 flag（如 `--model`、`--token`、`--config`）在所有子命令上同样可用，无需在 `biu` 与子命令之间区分位置。

## 目录

- [快速上手](#快速上手)
- [配置与凭据存放位置](#配置与凭据存放位置)
- [顶层用法（biu / REPL）](#顶层用法biu--repl)
- [命令总览](#命令总览)
- [账号与初始化](#账号与初始化)
  - [`biu init`](#biu-init) · [`biu auth`](#biu-auth) · [`biu pair`](#biu-pair)
- [诊断](#诊断)
  - [`biu doctor`](#biu-doctor) · [`biu version`](#biu-version)
- [会话、计划与用量](#会话计划与用量)
  - [`biu sessions`](#biu-sessions) · [`biu plan`](#biu-plan) · [`biu usage`](#biu-usage)
- [配置管理](#配置管理)
  - [`biu config`](#biu-config)
- [MCP 服务器](#mcp-服务器)
  - [`biu mcp`](#biu-mcp)
- [知识库摄取](#知识库摄取)
  - [`biu ingest`](#biu-ingest)
- [守护进程与远程调度](#守护进程与远程调度)
  - [`biu serve`](#biu-serve) · [`biu bridge`](#biu-bridge) · [`biu agent worker`](#biu-agent-worker)
- [插件与技能](#插件与技能)
  - [`biu plugin`](#biu-plugin) · [`biu skill`](#biu-skill)
- [应用开发与本地运行](#应用开发与本地运行)
  - [`biu app`](#biu-app) · [`biu repo-app`](#biu-repo-app)
- [环境变量](#环境变量)

## 快速上手

```bash
# 1. 交互式初始化（写入 ~/.biu/config.toml，并引导浏览器登录）
biu init

# 2. 自检
biu doctor

# 3. 进入交互式 REPL
biu

# 4. 单次提问（非交互，stdout 输出 JSONL 事件流）
biu --headless --json --prompt "解释这段代码的作用"
```

首次运行 `biu` 需要一个默认模型：在 `~/.biu/config.toml` 中设置 `[default].model`（运行 `biu init` 会引导配置），或每次运行时传 `--model <id>`。

## 配置与凭据存放位置

| 路径 | 内容 |
|------|------|
| `~/.biu/config.toml` | 主配置（部署模式、模型、端点、token）。建议权限 0600 |
| `~/.biu/auth.json` | OAuth token 的文件回退存储（仅当系统没有钥匙串时使用，0600） |
| 系统钥匙串 | OAuth token 与设备 token 的首选存储：macOS Keychain / Linux Secret Service（服务名 `com.biumind.biu`） |
| `~/.biu/sessions/` | 会话 JSONL 日志，按项目目录分桶 |
| `~/.biu/plans/` | 计划文件（`ExitPlanMode` 的输出） |
| `~/.biu/usage.jsonl` | token 用量记录（`biu usage` 的数据来源） |
| `~/.biu/telemetry.json` / `~/.biu/telemetry.jsonl` | 遥测开关文件 / 事件日志（默认关闭） |
| `~/.biu/update-check.json` | 启动更新检查的状态文件 |
| `~/.biu/logs/daemon.log` | `biu serve` 守护进程日志（超过 10MB 自动截断） |
| `~/.biu/device_token` | 设备 token 的文件回退存储（0600；有钥匙串时存钥匙串） |
| `~/.biumind/settings.json` | 分层设置（权限、hooks、状态栏、沙箱、插件禁用清单等） |
| `~/.biumind/skills/` | 本地技能目录（`SKILL.md`） |
| `~/.biumind/plugins/` | 已安装插件目录 |
| `~/.biumind/repo-apps/` | repo-app 实例目录 |
| `~/.biumind/keys/` | 应用发布者签名密钥 |

> [!NOTE]
> OAuth 登录得到的 token 优先写入系统钥匙串；在没有钥匙串的环境（如部分 Linux 服务器）会回退到 `~/.biu/auth.json`（0600）。可以用 `biu auth status` 查看实际生效的存储后端，用 `biu auth migrate` 把旧文件里的 token 迁入钥匙串。

## 顶层用法（biu / REPL）

不带子命令直接运行 `biu` 进入交互式 REPL；带 `--headless` / `--json` 则执行单次提问后退出。

```bash
biu                        # 交互式 REPL
biu --headless --prompt "…"   # 单次提问，纯文本输出
biu --json --prompt "…"       # 单次提问，stdout 输出 JSONL 事件流
biu --resume <session-id>     # 恢复指定会话（重放事件日志）
biu --continue                # 恢复当前项目最近一次会话
```

### 顶层 flag

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--config` | string | `""` | 指定 config.toml 路径（默认 `~/.biu/config.toml`，或 `BIU_CONFIG`） |
| `--headless` | bool | `false` | 非交互单次提问模式 |
| `--json` | bool | `false` | 在 stdout 输出 JSONL 事件流（隐含 `--headless`） |
| `--prompt` | string | `""` | 提示词文本（headless 模式）；留空则从 stdin 读取 |
| `--model` | string | `""` | 覆盖默认模型（优先于 `[default].model`） |
| `--system` | string | `""` | 系统提示词前缀 |
| `--model-relay-url` | string | `""` | 覆盖 model-relay 端点 |
| `--token` | string | `""` | 覆盖 bearer token |
| `--mode` | string | `""` | 部署模式：`cloud` \| `byo_endpoint` \| `direct`（覆盖配置文件） |
| `--no-log` | bool | `false` | 禁用 JSONL 会话日志 |
| `--resume` | string | `""` | 要恢复的会话 id（把该会话的事件日志重放进引擎） |
| `--continue` | bool | `false` | 恢复当前项目目录下最近一次会话（设置 `--resume` 时被忽略） |
| `--fork-session` | bool | `false` | 显式从 `--resume` 分叉；实际是空操作——biu 总是重放进新的会话 id，原会话文件不会被覆盖 |
| `--rewind-files` | string | `""` | 把文件恢复到指定用户消息（UUID）之前的状态然后退出；需配合 `--resume` 或 `--continue` |
| `--permission-policy` | string | `deny` | headless / SDK 权限策略：`deny`（默认，拒绝并报错）\| `allow` \| `stdin`（终端交互询问）\| `stdin-json`（面向 GUI：stdout 输出 PERMISSION_ASK 事件，stdin 读 JSON 决策） |
| `--permission-mode` | string | `""` | 引擎权限模式：`default` \| `acceptEdits` \| `bypassPermissions`；留空回退 settings.json 的 `defaultMode` |
| `--add-dir` | string[] | — | 追加模型可读写的额外工作目录；可重复传，也支持逗号分隔；仅本次运行有效（不落盘） |

> [!WARNING]
> headless 模式的默认权限策略是 `deny`：无人值守时工具调用会被直接拒绝并报错，而不是挂起等待。自动化场景请显式选择 `--permission-policy=allow`（放开）或 `stdin` / `stdin-json`（转交决策）。

### REPL 简介

REPL 内输入 `/` 会弹出斜杠命令列表，`/help` 查看全部。常用命令：

| 命令 | 说明 |
|------|------|
| `/help` | 显示全部斜杠命令 |
| `/model <id>` | 切换本次会话的模型 |
| `/mode <mode>` | 切换权限模式（`default` / `acceptEdits` / `plan` / `bypass`） |
| `/compact` | 压缩历史对话以节省上下文 |
| `/clear` | 清空历史，重新开始 |
| `/resume [#n\|latest\|<id>]` | 重放一个已保存会话 |
| `/sessions` | 列出最近的会话 |
| `/export <path>` | 把当前会话导出为 md / json / anthropic-replay |
| `/cost [--by-tool]` | 查看本次会话的 token 与费用 |
| `/usage [today\|week\|month\|all]` | 查看历史用量汇总 |
| `/permissions` | 查看当前生效的权限规则与模式 |
| `/mcp [<server>]` | 查看已连接的 MCP 服务器及其工具 |
| `/plugin` | 查看 / 启停插件 |
| `/memory [list\|reload]` | 查看 BIUMIND.md 与自动记忆状态 |
| `/remember <text>` | 保存一条记忆到 `~/.biumind/memory` |
| `/agents [create <name>]` | 列出 / 创建子代理 |
| `/todo` | 打印会话内待办清单 |
| `/commit` / `/pr` | 暂存并生成 Conventional Commits 提交 / 创建 PR（依赖 `gh`） |
| `/doctor` | REPL 内健康自检 |
| `/upgrade [run\|check]` | 升级 biu 自身 |
| `/quit` | 退出 |

完整清单以 REPL 内 `/help` 输出为准。

## 命令总览

`biu` 共有 18 个顶层子命令：

| 命令 | 用途 |
|------|------|
| [`biu init`](#biu-init) | 交互式初始化配置 |
| [`biu auth`](#biu-auth) | OAuth 登录 / 登出 / 状态 / 迁移凭据 |
| [`biu pair`](#biu-pair) | 把本机配对到 BiuMind 账号（设备 token） |
| [`biu doctor`](#biu-doctor) | 全面自检 |
| [`biu version`](#biu-version) | 打印版本信息 |
| [`biu sessions`](#biu-sessions) | 查看与导出会话日志 |
| [`biu plan`](#biu-plan) | 查看与管理计划文件 |
| [`biu usage`](#biu-usage) | 汇总 token 用量 |
| [`biu config`](#biu-config) | 检查 / 校验配置，输出 JSON Schema，管理遥测与更新检查开关 |
| [`biu mcp`](#biu-mcp) | 检查 MCP 服务器与工具目录 |
| [`biu ingest`](#biu-ingest) | 解析本地文件并可选提交到 Wiki |
| [`biu serve`](#biu-serve) | 以守护进程方式运行（bridge HTTP + 可选远程调度注册） |
| [`biu bridge`](#biu-bridge) | 通过 HTTP/SSE 暴露代理供 IDE / 本地 UI 驱动 |
| [`biu agent worker`](#biu-agent-worker) | 把本机注册为 Agent Plane 执行节点 |
| [`biu plugin`](#biu-plugin) | 插件管理（含插件市场） |
| [`biu skill`](#biu-skill) | 技能管理（本地与云端同步、打包、签名） |
| [`biu app`](#biu-app) | 应用中心 BiuApp 的开发、打包与本地调试 |
| [`biu repo-app`](#biu-repo-app) | 把 GitHub 开源项目作为本地 Web 服务运行 |

## 账号与初始化

### `biu init`

交互式初始化向导：选择部署模式（`cloud` 走 BiuMind 云端 / `byo_endpoint` 自带 model-relay / `direct` 直连 Anthropic API），采集认证材料，写入 `~/.biu/config.toml`（已存在时会先确认再覆盖）。向导全程纯文本，可在 SSH / CI 中使用；所有问答都可以用 flag 代替，便于脚本化。

写入配置后会做一次连通性冒烟测试；`--with-memory` / `--with-settings` 可顺带生成项目记忆文件和权限设置初稿。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--mode` | string | `""` | 部署模式：`cloud` \| `byo_endpoint` \| `direct`（跳过模式选择提问） |
| `--api-key` | string | `""` | Anthropic API key（`--mode=direct` 时使用） |
| `--model-relay-url` | string | `""` | model-relay 端点 URL（`cloud` / `byo_endpoint` 模式） |
| `--model-relay-token` | string | `""` | model-relay 认证 token（`cloud` / `byo_endpoint` 模式） |
| `--model` | string | `""` | 默认模型（跳过提问） |
| `--with-memory` | bool | `false` | 同时在当前目录生成 BIUMIND.md 模板 |
| `--with-settings` | bool | `false` | 同时生成入门版 `~/.biumind/settings.json` |
| `--yes` | bool | `false` | 跳过全部交互提问，完全依赖 flag（覆盖已有配置也不再确认） |

```bash
biu init                          # 全交互
biu init --mode=direct --api-key sk-ant-… --yes   # 脚本化直连模式
```

> [!TIP]
> cloud 模式下向导会优先引导浏览器 OAuth 登录，token 进系统钥匙串而不落配置文件；浏览器不可用（SSH 等）时可回退粘贴 token，或之后运行 `biu auth login --manual`。

### `biu auth`

管理 OAuth 凭据。OAuth 端点默认从 `[model-relay].endpoint` 推导（单 origin 架构），自部署环境可用配置文件 `[auth]` 段或 `BIU_OAUTH_*` 环境变量覆盖。

#### `biu auth login`

走浏览器 OAuth（PKCE）登录并持久化 token。默认拉起本地回调端口自动完成；`--manual` 适合 SSH / 沙箱环境：打印授权 URL，你把批准后跳转回来的完整 URL 粘贴回终端完成交换。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--manual` | bool | `false` | 手动粘贴流程（SSH / 沙箱环境） |

#### `biu auth logout`

吊销远端 refresh token（网络失败仅告警）并删除本地缓存的 OAuth token。无 flag。

#### `biu auth status`

显示登录状态：存储后端、token 摘要、scope、过期时间、是否已过期。无 flag。

#### `biu auth migrate`

把旧版 `~/.biu/auth.json` 中的 token 一次性迁入系统钥匙串。幂等：迁移成功后文件即被删除，重复运行是无操作。没有钥匙串的主机上会明确提示并保持文件不动。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--dry-run` | bool | `false` | 只打印将执行的迁移动作，不写入 |

### `biu pair`

把这台机器配对到 BiuMind 账号，换取一个**受限且可随时吊销的设备 token**，替代在守护进程机器上放置完整账号 PAT。流程：`biu pair` 拿到配对码 → 在任一已登录设备（手机 / 网页）输入批准 → 本机轮询拿到 device token 并存入钥匙串（回退 `~/.biu/device_token`，0600）。之后 `biu agent worker` / `biu serve` 自动优先使用它。配对码 5 分钟内有效。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--brain-url` | string | `""` | brain 服务地址（默认：`BIUMIND_BRAIN_URL` 或 `[model-relay].endpoint`） |
| `--name` | string | `""` | 上报给服务端的设备名（默认：主机名） |

```bash
biu pair --brain-url https://your-biumind.example.com
```

## 诊断

### `biu doctor`

出问题时第一个该跑的命令。逐项检查并打印彩色清单（✓ 正常 / ! 降级警告 / ✗ 失败），任一项失败则以非零退出码结束。检查项包括：

- 配置文件加载与权限（应 0600）
- 部署模式、模型、权限模式
- 直连模式的 provider / endpoint / 连通性，或云端模式的 model-relay `/healthz`
- `~/.biu` 与 `~/.biumind` 目录布局与权限
- 外部工具（`git` / `rg` / `gopls`）与沙箱工具（macOS `sandbox-exec`、Linux `bwrap`）
- 分层 settings.json 与沙箱规则合并结果
- 认证存储后端、OAuth token 过期与刷新能力、token 来源（`--token` > `BIUMIND_TOKEN` > `[model-relay].virtual_key` > OAuth 存储）
- 更新检查状态（仅读本地状态，不发网络请求）

无自身 flag；顶层 flag（如 `--config`、`--mode`、`--token`）会影响检查结果。

### `biu version`

打印版本、commit、构建时间、Go 版本与 OS/架构。`biu --version` 打印单行版本号。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--short` | bool | `false` | 只打印版本号 |

## 会话、计划与用量

### `biu sessions`

查看与导出会话日志。日志存于 `~/.biu/sessions/<项目目录>/<会话id>.jsonl`，按启动目录分桶。

#### `biu sessions list`

按项目列出已保存会话（新→旧）。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--all` | bool | `false` | 列出所有项目的会话，而不只是当前目录 |

#### `biu sessions show <id>`

把一个会话的全部事件按 JSONL 原样打印。参数：会话 id（必填）。

#### `biu sessions export <id>`

把会话导出为人读或工具友好的格式。导出前会自动脱敏（api_key / token / refresh_token / virtual_key 字段值及自由文本中的 Bearer、sk-ant-… 模式）。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-f, --format` | string | `markdown` | 输出格式：`markdown`（对话转录，含工具调用框）\| `json`（按轮合并的结构化导出）\| `anthropic-replay`（可直接 POST 到 `/v1/messages` 的消息体） |
| `--include-tool-output` | bool | `true` | 是否渲染工具输出；分享触碰过敏感文件的会话时建议设为 `false` |
| `--exclude-system` | bool | `false` | 丢弃 system_* 事件（权限拒绝、hook 拦截等） |
| `--max-tool-output-bytes` | int | `4096` | 每条工具输出截断到 N 字节（0 = 不设上限） |
| `-o, --output` | string | `""` | 写入文件而非 stdout |

### `biu plan`

查看与管理计划文件（`ExitPlanMode` 工具的输出），存于 `~/.biu/plans/<会话id>.md`（可用 `BIU_PLANS_DIR` 覆盖）。`<ref>` 接受完整会话 id、无歧义前缀或字面量 `latest`。

| 子命令 | 说明 |
|--------|------|
| `biu plan list` | 列出计划（新→旧），带大小与首行预览 |
| `biu plan show [<ref>\|latest]` | 打印一个计划（默认 latest） |
| `biu plan rm [<ref>]` | 删除一个计划，或用 `--older-than` 批量清理 |

`biu plan rm` 的 flag：

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--older-than` | string | `""` | 批量删除早于该时长的计划（如 `30d`、`2w`、`4h`、`15m`，或任意 Go duration 字符串） |

### `biu usage`

汇总 `~/.biu/usage.jsonl` 中的 token 用量记录。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--since` | string | `7d` | 时间窗口：`7d` / `30d` / `90d` / `all`，或 RFC3339 时间戳 |
| `--bucket` | string | `day` | 分组粒度：`day` \| `week` \| `month` |
| `--model` | string | `""` | 只统计指定模型 id |
| `--json` | bool | `false` | 输出 JSON 而非表格 |

```bash
biu usage --since 30d --bucket month
biu usage --model claude-opus-4-7 --json
```

## 配置管理

### `biu config`

检查、校验配置文件，输出 JSON Schema，以及管理遥测与更新检查两个开关。

| 子命令 | 说明 |
|--------|------|
| `biu config show` | 打印解析后的 config.toml（密钥字段自动脱敏） |
| `biu config validate` | 加载全部配置层并逐项报告问题，任一层失败则非零退出 |
| `biu config schema [config\|settings]` | 输出 JSON Schema 文档 |
| `biu config telemetry …` | 管理可选遥测（默认关闭） |
| `biu config update-check …` | 管理启动更新检查（默认开启） |

#### `biu config show`

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--settings` | bool | `false` | 显示合并后的 settings.json 分层（user / project / local）而非 config.toml |

#### `biu config validate`

加载 `~/.biu/config.toml` 与 settings.json 各层，逐层输出 ok / warn / fail 及具体原因。无 flag。

#### `biu config schema [config|settings]`

参数二选一（必填）。输出 JSON Schema 到 stdout，可管道到文件后通过 `$schema` 引用获得编辑器自动补全与校验：

```bash
biu config schema settings > ~/.biumind/settings.schema.json
```

#### `biu config telemetry`

遥测默认关闭。开启后，匿名事件（子命令名、结果、耗时、版本、os/arch）追加到 `~/.biu/telemetry.jsonl`——绝不包含提示词内容、文件路径或 API key。事件文件可直接查看审计。

| 子命令 | 说明 |
|--------|------|
| `biu config telemetry status` | 打印当前状态、install_id、端点与文件路径 |
| `biu config telemetry on` | 开启（轮换 install_id） |
| `biu config telemetry off` | 关闭（保留磁盘上的 jsonl） |

`on` 的 flag：

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--endpoint` | string | `""` | 可选的 HTTPS 上报地址（不设则只写本地文件） |

环境变量：`BIU_TELEMETRY_DISABLED=1`（硬关）、`BIU_TELEMETRY_ENABLED=1`（单次开启）、`BIU_TELEMETRY_ENDPOINT`（覆盖上报地址）。

#### `biu config update-check`

交互式 REPL 启动时检查新版本（最多 24 小时一次，后台进行，从不阻塞启动）。headless、serve 及一次性子命令不会为此发网络请求；开发构建与桌面客户端托管的构建始终跳过。环境变量 `BIU_UPDATE_CHECK=0` 可硬关。

| 子命令 | 说明 |
|--------|------|
| `biu config update-check status` | 打印开关状态、上次检查时间、已知最新版本 |
| `biu config update-check on` / `off` | 开启 / 关闭启动更新检查 |

## MCP 服务器

### `biu mcp`

诊断配置在 `~/.biu/config.toml` 中 `[[mcp_servers]]` 段的 MCP 服务器及其暴露的工具。

| 子命令 | 说明 |
|--------|------|
| `biu mcp list` | 启动全部已配置服务器并列出各自工具（disabled 的标记跳过；启动失败会给出具体错误与缺失的环境变量） |
| `biu mcp probe <server-name>` | 单独拉起一个服务器，打印握手信息（名称、协议版本、instructions）与完整工具目录 |

`probe` 用于区分问题出在启动、握手还是某个工具的 schema。两个子命令均无自身 flag，超时均为 30 秒。

## 知识库摄取

### `biu ingest <file>`

单次流水线：解析本地文件（markdown / html / 纯文本），跑两步思维链生成页面草稿（PageDraft），打印结果；配 `--commit` 可把草稿通过 Wiki API 推送成 Wiki 页面（创建页面与块）。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--url` | string | `""` | 来源 URL（默认 `file://…`） |
| `--title` | string | `""` | 覆盖页面标题 |
| `--json` | bool | `false` | 以 JSON 输出 PageDraft |
| `--commit` | bool | `false` | 把 PageDraft POST 到 Wiki API（需配 `--project`） |
| `--project` | string | `""` | 项目 id 或名称（`--commit` 时必填） |
| `--wiki-url` | string | `""` | 覆盖 Wiki API 端点（默认取 model-relay endpoint） |
| `--wiki-token` | string | `""` | 覆盖 Wiki API bearer token |

```bash
biu ingest README.md
biu ingest --json notes.md > page.json
biu ingest --commit --project Notes README.md
```

## 守护进程与远程调度

三个相关命令的关系：

- `biu bridge`：本机 IDE / 本地 UI 驱动 biu（biu 开端口等人来连）。
- `biu agent worker`：biu 主动连接服务端，远端（手机 / 网页）触发的任务被投递到本机执行。
- `biu serve`：两者的超集（bridge HTTP + 可选 worker 注册 + PID 文件 + 健康检查），主要供桌面客户端作为子进程拉起；前两个命令保留为单一职能入口。

### `biu serve`

长驻守护进程。典型用法是桌面客户端以 `biu serve --port 0 --pid-file ~/.biumind/biu.pid` 拉起，从 stdout 解析 `BIU_BRIDGE_URL=http://127.0.0.1:<port>` 获得实际端口；`--register` 时还会在注册成功后输出 `BIU_DAEMON_ENV_ID=<env_id>`。暴露 `GET /healthz`（探活）、`GET /metrics`（Prometheus）与 `POST /internal/token`（仅 loopback，供客户端热更新 access token，避免重启）。

带 `--pid-file` 时会监视父进程：父进程退出（如桌面应用关闭）后守护进程自动优雅退出，避免孤儿进程；PID 文件指向的旧 biu serve 进程会被安全接管。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--port` | int | `0` | 监听端口（0 = 系统分配；实际端口以 stdout 的 `BIU_BRIDGE_URL=…` 为准） |
| `--listen` | string | `""` | 显式监听地址（覆盖 `--port`，如 `0.0.0.0:8088`） |
| `--auth-token` | string | `""` | 每个请求要求的 bearer token（空 = 无认证，仅限开发） |
| `--pid-file` | string | `""` | PID 文件路径；已有存活进程会阻止启动，过期文件自动清理 |
| `--register` | bool | `false` | 同时注册为 Agent Plane 环境（biu_daemon worker），远端客户端可调度本机 |
| `--brain-url` | string | `""` | brain 服务地址（默认：`BIUMIND_BRAIN_URL` 或 `[relay].endpoint`） |
| `--identity-url` | string | `""` | identity 服务地址，用于 client-side BYOK 密钥获取（默认：`BIUMIND_IDENTITY_URL` 或 brain-url） |
| `--allowed-roots` | string[] | — | 本守护进程可触达的文件系统根；可重复；留空 = 仅守护进程当前目录 |
| `--tool-policy` | string | `workspace-write` | 能力地板：`readonly` \| `workspace-write` \| `full` |

```bash
# 手动带注册运行
BIUMIND_PAT=<pat> biu serve --register --brain-url https://your-biumind.example.com
```

> [!TIP]
> `--tool-policy` 是本地独立硬地板，守护进程不盲信服务端：实际生效的策略取「本地 flag 与服务端按设备下发策略的交集」（本地 flag 是上限，服务端只能收窄）。`readonly` 禁用一切危险工具；`workspace-write` 允许文件读写（仍受 `--allowed-roots` 路径约束）但禁 shell / 子代理；`full` 不设能力地板。越界路径一律直接拒绝，不会弹询问。

### `biu bridge`

把代理通过 HTTP/SSE 暴露给 IDE 或远端 UI。每个请求构建全新代理实例，多客户端不共享状态。启动时会先做一次配置冒烟测试，配置错误立刻失败而不是等到第一次请求。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--listen` | string | `:8088` | 监听地址（传 `:0` 时实际端口打印到 stderr） |
| `--auth-token` | string | `""` | 每个请求要求的 bearer token（空 = 无认证，仅限开发） |

### `biu agent worker`

把本机注册为 Agent Plane 的执行环境（`biu_daemon` 类型），长轮询领取任务，在本地跑代理，把流式帧推回服务端供远端客户端查看。凭据解析优先级：设备 token（`biu pair` 所得）> `BIUMIND_PAT` > `--token` > `BIUMIND_TOKEN` > 配置中的 `virtual_key`。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--brain-url` | string | `""` | brain 服务地址（默认：`BIUMIND_BRAIN_URL` 或 `[relay].endpoint`） |
| `--machine-name` | string | `""` | 上报给服务端的机器名（默认：主机名） |
| `--pool-tag` | string | `""` | 可选的池标签，用于运行时风格的路由 |
| `--allowed-roots` | string[] | — | 本 worker 可触达的文件系统根；可重复；留空 = 仅启动目录 |
| `--tool-policy` | string | `workspace-write` | 能力地板：`readonly` \| `workspace-write` \| `full`（与服务端策略取交集） |

```bash
biu pair                 # 先配对拿设备 token
biu agent worker --brain-url https://your-biumind.example.com
```

## 插件与技能

### `biu plugin`

本地插件管理。一个插件是一个目录，可同时打包命令、子代理、技能、输出样式、hooks 与 MCP 服务器。所有写操作都落在用户层（`~/.biumind/plugins/`、`~/.biumind/settings.json`），项目层 / local 层文件不会被改动。

| 子命令 | 说明 |
|--------|------|
| `biu plugin list` | 列出全部已发现插件（用户 + 项目 + 兼容目录），含启用状态与组件摘要 |
| `biu plugin show <name>` | 打印一个插件的完整详情 |
| `biu plugin install <path\|plugin@marketplace>` | 从本地目录或已注册市场安装 |
| `biu plugin uninstall <name>` | 从 `~/.biumind/plugins/` 移除 |
| `biu plugin enable <name>` / `disable <name>` | 启用 / 禁用（改写 `~/.biumind/settings.json` 的禁用清单，重启生效） |
| `biu plugin validate <path>` | 校验插件目录的 manifest 与组件布局（面向插件作者） |
| `biu plugin marketplace …` | 插件市场管理（别名 `market`） |

#### `biu plugin install`

三种安装形式：

```bash
biu plugin install ./my-plugin            # 本地目录（拷入 ~/.biumind/plugins/<name>/）
biu plugin install /abs/path/to/plugin    # 绝对路径
biu plugin install code-review@biumind    # 从已注册市场安装
```

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--force` | bool | `false` | 覆盖同名已有安装 |

#### `biu plugin marketplace`

| 子命令 | 说明 |
|--------|------|
| `biu plugin marketplace list` | 列出已注册的市场（名称、是否签名、来源） |
| `biu plugin marketplace add <name> <source>` | 注册市场。来源支持本地路径 / `https://…` JSON 地址 / `git+https://…` |
| `biu plugin marketplace remove <name>` | 注销市场（别名 `rm`） |
| `biu plugin marketplace show <name>` | 拉取并列出市场中的插件，附安装命令提示 |

`add` 的 flag：

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--pinned-key` | string | `""` | 固定的 ed25519 公钥，用于校验 marketplace.json 签名（格式 `ed25519:<base64-spki>`；密钥对可用 `biu skill keygen` 生成）。不设则不做完整性校验 |

```bash
biu plugin marketplace add biumind-official git+https://github.com/biumind/marketplace
biu plugin marketplace show biumind-official
biu plugin install code-review@biumind-official
```

### `biu skill`

技能（SKILL.md）管理：本地与云端同步、安装、打包与签名。`list / pull / push / diff / enable / disable / install` 需要运行时地址——传 `--runtime-url` 或设 `BIUMIND_RUNTIME_URL`；`run / pack / unpack / keygen / sign / verify` 完全离线可用。

| Persistent flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--runtime-url` | string | `""` | Runtime 端点（覆盖 `BIUMIND_RUNTIME_URL`） |

| 子命令 | 说明 |
|--------|------|
| `biu skill list` | 列出云端技能 |
| `biu skill pull` | 云端技能同步到 `~/.biumind/skills/` |
| `biu skill push <identifier>` | 上传本地 SKILL.md 到云端（创建或更新） |
| `biu skill diff <identifier>` | 比较本地与云端的哈希 |
| `biu skill run <identifier> [args…]` | 离线展开本地 SKILL.md（`$ARGS` 替换）输出到 stdout，不调用模型 |
| `biu skill install <url\|path>` | 从 HTTPS SKILL.md 地址或本地 `.biuskill` 包安装 |
| `biu skill pack <dir>` / `unpack <file.biuskill>` | 打包 / 解包 `.biuskill` 归档 |
| `biu skill keygen` / `sign` / `verify` | ed25519 密钥对生成与 `.biuskill` 签名 / 验签 |
| `biu skill enable <skill-id>` / `disable <skill-id>` | 在指定代理上开 / 关技能 |

各子命令的 flag：

**`biu skill list`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--status` | string | `""` | 按状态过滤：`active` / `disabled` / `staged` / `staged_org` / `suspended` |
| `--source` | string | `""` | 按来源过滤：`bundled` / `org` / `user` / `marketplace` / `imported` |

**`biu skill install <url|path-to-biuskill>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--agent` | string | `""` | 安装后同时在指定代理（UUID）上启用 |
| `--pin` | bool | `false` | 配合 `--agent`：把技能固定，正文始终内联进系统提示词 |
| `--dry-run` | bool | `false` | 只解析来源（URL 重写或本地解包）并打印将要安装的内容，不联系服务器 |

> [!NOTE]
> `pull` 遇到本地与云端内容冲突（diverged）时以非零退出码结束，提示先用 `biu skill diff <name>` 检查，再 `biu skill push <name>` 上传本地或删除本地文件接受云端。

**`biu skill pack <dir>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-o, --output` | string | `""` | 输出路径（默认 `<dir>.biuskill`） |

打包是确定性的（mtime / 顺序 / mode 固定），同一份源字节两次打包结果一致——这是 ed25519 签名的前提。只收录 SKILL.md 与 `scripts/` / `references/` / `assets/` 目录，其余文件跳过并提示。

**`biu skill unpack <file.biuskill>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-o, --output` | string | `""` | 输出目录（默认：去掉 `.biuskill` 扩展名的路径） |

**`biu skill keygen`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--prefix` | string | `""` | 输出文件名前缀（默认 `biuskill`，生成 `biuskill.key` 私钥 0600 与 `biuskill.key.pub` 公钥）。已有私钥时拒绝覆盖 |

**`biu skill sign <pack.biuskill>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--key` | string | `""` | PEM 编码的 ed25519 私钥路径（必填）。签名写到 `<pack>.sig` |

**`biu skill verify <pack.biuskill>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--pubkey` | string | `""` | 发布者 PEM 公钥路径（必填） |
| `--sig` | string | `""` | 签名路径（默认 `<pack>.sig`） |

**`biu skill enable / disable <skill-id>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--agent` | string | `""` | 目标代理 UUID（必填） |
| `--pin` | bool | `false` | 仅 enable：固定技能正文内联进系统提示词 |

## 应用开发与本地运行

### `biu app`

应用中心 BiuApp 的开发、打包与检查工具链。

| 子命令 | 说明 |
|--------|------|
| `biu app new <slug>` | 从内置模板在 `<slug>/` 脚手架一个新 App 项目 |
| `biu app validate` | 校验 manifest.yaml（一次性列出全部问题而非只报第一个） |
| `biu app inspect` | 打印解析后的 manifest（含权限、数据范围、动作、视图、触发器、侧边栏配置） |
| `biu app pack` | 打包 `.biuapp` 分发包（打包前强制校验 manifest） |
| `biu app verify <file.biuapp>` | 校验分发包的哈希与签名 |
| `biu app keygen` | 生成 ed25519 发布者密钥对 |
| `biu app run` | 本地开发服务器 |

各子命令的 flag：

**`biu app new <slug>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--from` | string | `hybrid_full` | 模板名：`minimal` \| `view_only` \| `hybrid_full` |

**`biu app validate` / `biu app inspect`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--manifest` | string | `""` | manifest.yaml 路径（默认 `./manifest.yaml`） |
| `--json`（仅 inspect） | bool | `false` | 以 JSON 输出解析结果 |

**`biu app pack`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--source` | string | `""` | 项目根目录（默认：当前目录） |
| `--out` | string | `""` | 输出路径（默认 `dist/<slug>-<version>.biuapp`） |
| `--key` | string | `""` | 签名私钥路径（默认 `~/.biumind/keys/publisher.ed25519`） |
| `--unsigned` | bool | `false` | 跳过签名（仅限本地安装使用，市场提交会被拒绝） |

打包收录范围由项目根的 `.biuapp.yaml`（include 列表）决定；没有该文件时回退为 `manifest.yaml + README.md + LICENSE`。

**`biu app verify <file.biuapp>`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--trust-key` | string[] | — | 受信发布者密钥对（私钥路径）；可重复 |

**`biu app keygen`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--name` | string | `publisher` | 密钥名（文件为 `<name>.ed25519`）；输出含应写入 manifest.yaml 的 publisher id |

**`biu app run`**

本地开发服务器。启动后校验 manifest、绑定开发端口、按需拉起 `go run` 子进程，并监听文件变化（manifest 与 Go 源码改动自动重载 / 重启子进程）。桌面客户端会在"开发中"面板发现该 App。运行中按 `r` 手动重启子进程，`q` 退出。

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--dev` | bool | `false` | 开发模式（当前版本必传） |
| `--source` | string | `""` | App 源码目录（默认：当前目录） |
| `--addr` | string | `127.0.0.1:7099` | 开发服务器监听地址 |
| `--mock` | string | `""` | fixtures 目录：调用按 `<action>.json` 返回模拟数据而非子进程（设了该 flag 自动跳过子进程） |
| `--no-subproc` | bool | `false` | 跳过 `go run` 子进程（纯视图 App） |

### `biu repo-app`

把 GitHub 开源项目克隆到本地、自动识别技术栈、装好依赖并作为本地 Web 服务（仅 127.0.0.1）运行。实例存于 `~/.biumind/repo-apps/`（可用 `BIU_REPOAPP_ROOT` 或配置 `[repo-app].cache_dir` 覆盖）。当前仅支持 macOS / Linux。

`ensure` / `run` 在健康检查通过后向 stdout 输出 `BIU_REPOAPP_URL=http://127.0.0.1:<port>`（与 `biu serve` 的 `BIU_BRIDGE_URL` 同一约定），供桌面客户端解析。

| 子命令 | 说明 |
|--------|------|
| `biu repo-app install <github-url\|owner/repo>` | 克隆、识别技术栈、安装依赖 |
| `biu repo-app ensure <name\|github-url\|owner/repo>` | 幂等拉起：未安装则安装，未运行则启动，运行中则复用 |
| `biu repo-app list` | 列出已安装实例与运行状态 |
| `biu repo-app run <name>` | 作为分离的本地服务启动（仅 127.0.0.1） |
| `biu repo-app stop <name>` | 停止（SIGTERM，3 秒后 SIGKILL） |
| `biu repo-app logs <name>` | 查看运行日志 |
| `biu repo-app update <name>` | 拉取新 ref、重装依赖、重启 |
| `biu repo-app remove <name>` | 停止并删除实例目录 |
| `biu repo-app doctor` | 探测本机运行时（git / python3 / uv / node / mise / docker）并报告 |

各子命令的 flag：

**`biu repo-app install`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--ref` | string | `""` | 要安装的分支 / 标签（默认：仓库默认分支） |

**`biu repo-app ensure` / `biu repo-app run`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--env` | string[] | — | `KEY=VALUE` 合并进实例 `.env`（0600）；可重复，flag 值覆盖已有值 |
| `--port`（仅 run） | int | `0` | 绑定到 127.0.0.1 的端口（0 = 系统分配） |

**`biu repo-app logs`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-f, --follow` | bool | `false` | 持续跟随日志输出 |

**`biu repo-app update`**

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--ref` | string | `""` | 要切换到的分支 / 标签（默认：已安装的 ref） |
| `--install-id` | string | `""` | 应用中心安装 id；与 `--build-id` 一起把更新结果回报服务端 |
| `--build-id` | string | `""` | 应用中心构建 id（来自重新部署调用）；与 `--install-id` 成对出现 |
| `--report-url` | string | `""` | 构建上报的基址（默认 `[model-relay].endpoint`） |

```bash
biu repo-app install owner/repo
biu repo-app run owner-repo --port 8123
biu repo-app logs owner-repo -f
```

> [!NOTE]
> `biu repo-app doctor` 探测到的缺失运行时会由 `install` / `update` 自动引导安装（如 uv / mise）；但项目自带 Dockerfile 而 Docker 缺失时会直接报错要求先安装 Docker。

## 环境变量

| 变量 | 作用 |
|------|------|
| `BIU_CONFIG` | 覆盖 config.toml 路径 |
| `BIUMIND_MODEL_RELAY_URL` | 覆盖 model-relay 端点（低于 `--model-relay-url`） |
| `BIUMIND_TOKEN` | 覆盖 bearer token（低于 `--token`） |
| `BIUMIND_PAT` | Agent Plane worker 的个人访问令牌 |
| `BIUMIND_DEVICE_TOKEN` | 设备 token（`biu pair` 产物，优先于 PAT） |
| `BIUMIND_BRAIN_URL` | brain 服务地址 |
| `BIUMIND_IDENTITY_URL` | identity 服务地址 |
| `BIUMIND_RUNTIME_URL` | Runtime 端点（`biu skill` 云端操作） |
| `BIUMIND_LOG_LEVEL` | `biu serve` 日志级别（`debug` 提升为调试级别） |
| `BIU_TELEMETRY_DISABLED` / `BIU_TELEMETRY_ENABLED` / `BIU_TELEMETRY_ENDPOINT` | 遥测硬关 / 单次开启 / 上报地址覆盖 |
| `BIU_UPDATE_CHECK` | `0` = 硬关启动更新检查 |
| `BIU_PLANS_DIR` | 覆盖计划文件目录 |
| `BIU_REPOAPP_ROOT` | 覆盖 repo-app 实例根目录 |

云端模式下 token 的解析优先级为：`--token` > `BIUMIND_TOKEN` > 配置 `[model-relay].virtual_key` > OAuth 存储（`biu auth login` 得到的 token，可自动刷新）。`biu doctor` 会显示当前实际生效的来源。
