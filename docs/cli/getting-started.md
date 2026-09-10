# 快速上手

`biu` 是 BiuMind 的终端 AI 编码代理：一个约 15 MB 的静态 Go 二进制，无运行时依赖。在终端里与模型对话，让它读写代码、跑命令、管理 Git，全程受权限系统与沙箱约束。

本文带你从零走到第一个可用的代理会话：安装 → 初始化配置 → 登录 → 自检。日常用法见 [usage.md](usage.md)，全部子命令参考见 [commands.md](commands.md)。

## 安装

### Homebrew（推荐，macOS / Linux）

```sh
brew install biumind/tap/biu
biu version
```

### 预编译二进制（darwin / linux × amd64 / arm64）

从 [GitHub Releases](https://github.com/biumind/biumind/releases) 下载对应平台的压缩包（文件名形如 `biu_0.2.0_Darwin_arm64.tar.gz`），校验后安装：

```sh
tar -xzf biu_*_$(uname -s)_$(uname -m).tar.gz
install -m 0755 biu /usr/local/bin/biu
biu doctor
```

> [!TIP]
> 每个 Release 都附带 `checksums.txt`（所有产物的 SHA256）。下载后执行 `sha256sum -c checksums.txt` 即可校验完整性。

### 源码编译（Go 1.25+）

```sh
go install github.com/biumind/biumind/apps/cli/biu/cmd/biu@latest
```

或克隆仓库后本地构建：

```sh
git clone https://github.com/biumind/biumind.git
cd biumind/apps/cli/biu
go build -o biu ./cmd/biu
```

> [!NOTE]
> 已安装的用户也可以在 REPL 里用 `/upgrade run` 自更新（brew / go install 安装的会委托给对应包管理器），用 `/install` 查看当前安装方式与版本信息。

## 三种部署模式

`biu` 通过 `~/.biu/config.toml` 里的 `[default].mode` 决定模型调用走哪条链路：

| 模式 | 适用场景 | 需要什么 |
|------|----------|----------|
| `cloud`（默认） | 通过 BiuMind model-relay 网关调用，配额 / 计费在服务端完成 | model-relay 地址 + 登录令牌 |
| `direct` | 个人使用，自带 Anthropic API key 直连 | `[providers.anthropic].api_key` |
| `byo_endpoint` | 自建 model-relay / 代理 | 自己的 model-relay 地址 + 令牌 |

> [!WARNING]
> 完整代理循环（工具调用、权限、hooks）目前只在 `cloud` 与 `direct` 两种模式下可用。`byo_endpoint` 不支持引擎路径，配置后只能走纯对话。

## 首次配置：`biu init`

`biu init` 是交互式向导：选择部署模式 → 输入凭据 → 写入 `~/.biu/config.toml`。已存在配置时会先询问是否覆盖，不会静默破坏。

```sh
biu init
```

向导末尾会自动做一次连通性冒烟测试（`direct` 模式探测 `https://api.anthropic.com`，其余模式调 model-relay 的 `/healthz`），让你当场发现拼写错误而不是等到第一次对话。

所有交互项都可以用 flag 代替，方便脚本化 / CI 场景：

```sh
# direct 模式一条命令完成
biu init --yes --mode direct --api-key sk-ant-xxxx --model claude-sonnet-4-6

# cloud 模式：先登录（令牌进 OS 钥匙串），再写配置
biu auth login
biu init --yes --mode cloud --model-relay-url https://biumind.xxlab.tech --model <model-id>
```

`biu init` 的全部 flag：

| Flag | 说明 |
|------|------|
| `--mode` | `cloud` \| `byo_endpoint` \| `direct`，跳过模式选择 |
| `--api-key` | Anthropic API key（`--mode=direct` 时使用） |
| `--model-relay-url` | model-relay 端点（`cloud` / `byo_endpoint` 时使用） |
| `--model-relay-token` | model-relay 令牌（不走浏览器登录时使用） |
| `--model` | 默认模型，跳过模型询问 |
| `--with-memory` | 顺带在当前目录生成 `BIUMIND.md` 模板（见 [biumind-md.md](biumind-md.md)） |
| `--with-settings` | 顺带生成 `~/.biumind/settings.json` 初始权限配置（见 [permissions.md](permissions.md)） |
| `--yes` | 跳过所有交互提问，完全依赖 flag |

### `~/.biu/config.toml` 结构

配置文件查找顺序：`--config <path>` flag → `BIU_CONFIG` 环境变量 → `~/.biu/config.toml` → 内置默认值。文件权限建议 `0600`（`biu doctor` 会检查）。

一个完整示例（所有小节均可选，按需填写）：

```toml
[default]
mode     = "cloud"       # cloud（默认）| byo_endpoint | direct
provider = "anthropic"   # direct 模式下对应 [providers.<name>] 小节
model    = "claude-sonnet-4-6"

[model-relay]
endpoint    = "https://biumind.xxlab.tech"
virtual_key = "..."      # 浏览器登录的用户不需要此项（令牌在钥匙串里）

[providers.anthropic]    # direct 模式使用
api_key   = "sk-ant-..."
endpoint  = ""           # 留空则用 https://api.anthropic.com

[permissions]            # 旧版权限配置；新版推荐用 settings.json
mode = "ask"             # ask | auto_edit | full_access（旧词汇，等价于 default / acceptEdits / bypassPermissions）

[search]                 # websearch 工具的解析方式
mode        = "model-relay"  # model-relay（默认）| direct
searxng_url = ""             # mode=direct 时必填

[auth]                   # OAuth 覆盖项；通常不需要——端点会从 [model-relay].endpoint 推导
authorize_url   = ""
token_url       = ""
revoke_url      = ""
client_id       = ""
scopes          = []
callback_port   = 0      # 0 = 自动选空闲端口
manual_redirect = ""

[[mcp_servers]]          # MCP 服务器，见 usage.md
name    = "filesystem"
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
```

字段说明（与解析代码一一对应，见 `apps/cli/biu/internal/config/config.go`）：

- **`[default]`**：`mode` 部署模式；`provider` 直连时的提供方名（默认 `anthropic`）；`model` 默认模型——**默认值为空且没有内置兜底**，不配置会在启动时报错引导你设置。
- **`[model-relay]`**：`endpoint` 网关地址（内置默认 `https://biumind.xxlab.tech`）；`virtual_key` 静态令牌，浏览器登录的用户无需填写。
- **`[providers.<name>]`**：`api_key` 与可选 `endpoint`。非 `anthropic` 的提供方按 OpenAI 兼容协议接入。
- **`[permissions]`**：旧版权限小节（`mode` + `allowlist`），另有 `plan_drift_threshold`、`suggest_plan_for`、`suggest_plan_disabled` 控制计划模式提示。规则推荐写在 `settings.json`（见 [permissions.md](permissions.md)）。
- **`[search]`**：`websearch` 工具走 model-relay 还是自建 SearxNG。
- **`[auth]`**：OAuth 端点覆盖项。默认从 `[model-relay].endpoint` 的 `scheme://host` 推导出 `/oauth/authorize`、`/oauth/token`、`/oauth/revoke`，客户端 ID 固定为预注册的 public client `biu-cli`。
- **`[[mcp_servers]]`**：MCP 服务器数组，支持 stdio 与 HTTP 两种传输，详见 [usage.md](usage.md#mcp-服务器)。

> [!TIP]
> `biu config show` 打印当前生效的合并配置；`biu config validate` 加载所有配置层并报告问题；`biu config schema config` / `biu config schema settings` 输出对应文件的 JSON Schema，方便编辑器补全。

## 登录：`biu auth`

cloud / byo_endpoint 模式下用浏览器完成 OAuth 授权（PKCE，无 client secret）：

```sh
biu auth login
```

流程：`biu` 在本机 `127.0.0.1:<空闲端口>` 起一个一次性回调监听 → 打开（或打印）授权 URL → 你在浏览器里确认 → 回调带回授权码 → 换取并保存令牌。默认超时 5 分钟。

SSH / 无浏览器环境用手工粘贴模式：

```sh
biu auth login --manual
```

终端会打印授权 URL，你在任意机器的浏览器打开它，授权后把跳转回来的完整 URL 粘贴回终端（`biu` 从中提取 `code` 与 `state`，并校验 state 防 CSRF）。

### 令牌存在哪里

`biu` 优先使用操作系统钥匙串，不可用时回退到文件：

- **macOS**：Keychain（服务名 `com.biumind.biu`）
- **Linux**：需 `secret-tool`（`libsecret-tools` 包）可用，经 D-Bus 访问系统凭据库
- **其他平台**：暂无钥匙串后端，直接使用文件回退
- **回退**：`~/.biu/auth.json`（权限 `0600`）

`biu auth status` 显示当前后端、令牌脱敏摘要、scope、过期时间；`biu auth logout` 先向服务端吊销 refresh token（失败仅告警、本地一定登出）再删除本地令牌；老版本用户可用 `biu auth migrate` 把 `~/.biu/auth.json` 里的令牌一次性迁入钥匙串。

> [!NOTE]
> 请求链路的令牌解析优先级为：`--token` flag > `BIUMIND_TOKEN` 环境变量 > 配置里的 `[model-relay].virtual_key` > OAuth 存储（钥匙串 / 文件）。`biu doctor` 的 `auth token source` 检查项显示当前实际生效的来源。

## 自检：`biu doctor`

```sh
biu doctor
```

逐项检查并输出带颜色的清单：`✓` 正常、`!` 降级但可用、`✗` 失败（任一失败则以非零码退出）。检查项包括：

| 检查项 | 内容 |
|--------|------|
| `config` / `config perm` | 配置可解析；文件权限是否为 `0600`（里面有 API key） |
| `mode` / `model` / `perm mode` | 当前部署模式、默认模型、权限模式 |
| `provider` / `endpoint` / `api key` | direct 模式：key 存在性（脱敏显示）与端点 |
| `connectivity` | direct 模式探测 `<endpoint>/v1/messages`（<500 即视为在线） |
| `model-relay URL` / `model-relay healthz` | relay 模式：地址与 `/healthz` 探活 |
| `~/.biu` / `~/.biumind` | 目录存在性与权限（不应组 / 全局可写） |
| `git` / `rg` / `gopls` | 外部工具是否在 PATH（缺失只降级告警） |
| `sandbox-exec`（macOS）/ `bwrap`（Linux） | 沙箱工具是否可用，缺失则 Bash 不受沙箱约束 |
| `settings.*` / `sandbox` | 已加载的 settings 层；合并后的沙箱规则条数（防止 JSON 键名写错静默失效） |
| `auth backend` / `oauth token` | 令牌存储后端；access token 过期状态与 refresh 能力 |
| `auth token source` | 当前生效的令牌来源（见上文优先级） |
| `update check` | 启动更新检查的开关与状态（本地状态读取，不联网） |
| `agent-plane secrets` | 远程调度凭据（device token / 私钥）的存放后端 |

> [!TIP]
> 遇到"明明登录过却 401"时，先看 `oauth token` 一行：`expired — will refresh on next API call` 表示会在下次调用时自动刷新；`NO refresh_token` 则需要重新 `biu auth login`。

## 第一个会话

```sh
cd ~/code/your-project
biu
```

REPL 启动后会打印当前的 mode / provider / model。直接输入自然语言开始对话；输入 `/` 弹出斜杠命令面板。常用按键：`Ctrl-C` 中断流式输出（空闲状态下再按退出），`Ctrl-D` 退出。

```text
> 这个项目是做什么的？先读 README 和几个顶层源文件。
```

下一步：

- [usage.md](usage.md) —— REPL、斜杠命令、MCP、权限询问、成本统计、会话管理、IDE 集成（`biu bridge` / `biu serve`）
- [permissions.md](permissions.md) —— 权限规则语法、模式与 hooks
- [biumind-md.md](biumind-md.md) —— `BIUMIND.md` 记忆文件格式
- [sandbox.md](sandbox.md) —— Bash 沙箱策略
