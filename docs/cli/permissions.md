# 权限与 Hooks

`biu` 里每一次工具调用（文件读写、Bash 命令、MCP 工具）在执行前都要过一道权限闸门。三个组件共同决定结果：

1. **模式（mode）**——会话级的粗粒度默认行为；
2. **规则（rules）**——`Tool(content)` 形式的细粒度 allow / deny / ask 匹配；
3. **Hooks**——在生命周期节点上运行的用户脚本，可以审计、拦截、改写。

规则优先于模式的默认行为：一条 allow 规则永远压过模式的"询问"，一条 deny 规则永远压过模式的"放行"。

## settings.json 的三层结构

权限配置写在 settings.json（不是 `~/.biu/config.toml`），按三层加载、逐层合并：

| 层 | 路径 | 用途 |
|----|------|------|
| user | `~/.biumind/settings.json` | 全局个人偏好 |
| project | `<项目>/.biumind/settings.json` | 团队共享，提交进 git |
| local | `<项目>/.biumind/settings.local.json` | 单机覆盖，加入 `.gitignore` |

```json
{
  "permissions": {
    "allow": [
      "Bash(git status)",
      "Bash(go build:*)",
      "Bash(go test:*)",
      "Edit(/repo/**)"
    ],
    "deny": [
      "Bash(rm -rf /)"
    ],
    "ask": [
      "Bash(git push:*)"
    ],
    "defaultMode": "default",
    "additionalDirectories": ["../docs", "${PROJECT_ROOT}/bench"]
  }
}
```

- 三个规则数组合并后统一裁决（见下文"决策顺序"），不存在"哪一层的 allow 压过另一层的 deny"——deny 永远最强，不管写在哪层。
- `defaultMode` 按 local > project > user 取最具体的一层。
- `additionalDirectories` 追加可读写的工作目录（配合 REPL 的 `/add-dir <path> --remember` 与启动 flag `--add-dir`）。

REPL 里用 `/permissions` 查看当前生效的规则与模式，`/mode <name>` 切换模式，`/reload` 重新加载 settings.json。

## 模式

| 模式 | 行为 |
|------|------|
| `default` | 读取自动放行；破坏性操作首次使用时询问 |
| `acceptEdits` | 文件编辑 / 写入自动放行；Bash 等仍走正常判定 |
| `plan` | 只读思考模式——非只读工具一律拒绝（`EnterPlanMode` / `ExitPlanMode` 自身豁免） |
| `bypassPermissions` | 全部自动放行——危险，仅在可信环境使用 |
| `dontAsk` | 所有需要询问的调用直接拒绝（"panic"模式） |

兼容旧词汇：`ask` ≡ `default`，`auto_edit` ≡ `acceptEdits`，`full_access` ≡ `bypassPermissions`。

非 default 模式时，REPL 状态栏会整体着色并显示徽标作为外周视觉提示：plan 蓝色（`❙❙`）、acceptEdits 琥珀色（`⏵⏵ Accept`）、bypass 红色（`⏵⏵ Bypass`）、dontAsk 灰色（`⏵⏵ DontAsk`）。

### Plan 模式的生命周期

Plan 模式不只是权限翻转，引擎会记住进入前的模式并在退出时恢复：

1. 模型调用 `EnterPlanMode`，或你执行 `/mode plan`：保存当前模式，切换为 `plan`。
2. 模型做调研，所有非只读调用被拒绝。
3. 模型调用 `ExitPlanMode`（携带 markdown 计划与可选的 `allowedPrompts`），或你手动 `/mode <其他>`：恢复进入前保存的模式。

`allowedPrompts` 是批量批准机制——模型在计划里预先声明执行阶段需要的动作类别：

```json
{
  "plan": "## 步骤\n1. 跑测试\n2. 构建",
  "allowedPrompts": [
    { "tool": "Bash", "prompt": "go test ./..." },
    { "tool": "Bash", "prompt": "go build" }
  ]
}
```

批准计划后这些条目即为会话级授权，执行阶段不再逐条询问（显式 deny 规则仍可否决）。已批准的计划持久化到 `~/.biu/plans/<会话id>.md`，供事后复盘与 `--resume` 衔接；`/compact` 压缩历史后计划会作为系统附件重新注入，模型不会"忘了"自己承诺过的方案。`/plan list` / `/plan show <id>` / `/plan diff` / `/plan approvals` 在 REPL 里管理，命令行对应 `biu plan` 子命令。

## 规则语法

一条规则是一个字符串，写进 `allow` / `deny` / `ask` 数组：

```text
Tool                  # 覆盖该工具的所有调用
Tool(content)         # 工具 + 内容限定（命令、路径、模式……）
Tool(prefix:*)        # 前缀匹配：prefix 本身或 "prefix 任意参数"
Tool(pattern*)        # 通配符：* 匹配任意字符
Tool(\(literal\))     # 内容里包含字面括号时用 \ 转义
```

- **不带括号的裸 `Tool`** 匹配该工具的每一次调用。
- **工具名不区分大小写**：`bash` 与 `Bash` 等价。
- 旧版 `tool:detail` 冒号语法仍被接受，语义等价于 `tool(detail:*)` 前缀匹配。

### 各工具族的匹配语义

| 工具 | content 匹配对象 | 示例 |
|------|------------------|------|
| `Bash` | 完整命令字符串 | `Bash(go test)` 只匹配这条命令；`Bash(go:*)` 匹配 `go` 及任何 `go <子命令>`；`Bash(git push*)` 通配匹配 |
| `Edit` / `Write` / `Read` / `Glob` | 文件路径 / 模式，glob 风格 | `Edit(./src/**)` 匹配 src 下任意深度文件；`*` 单层、`**` 跨层、`?` 单字符 |
| `Grep` | 搜索 pattern（其次 path） | `Grep(TODO)` |
| 其他工具（含 MCP） | 依次尝试 `path` / `file_path` / `pattern` / `command` / `url` / `query` 字段做 glob 匹配 | `WebFetch(https://api.example.com/*)` |

### MCP 工具规则

MCP 工具的规则名与工具的限定名一致：

- `mcp__github__create_issue` —— 精确匹配单个工具；
- `mcp__github` —— 服务器级规则，匹配 `mcp__github__*` 全部工具（`mcp__github__*` 写法等价）。

### 决策顺序

每次工具调用按以下顺序裁决，命中即返回：

1. `bypassPermissions` 模式 → 放行；
2. `plan` 模式且非只读（且非计划切换工具）→ 拒绝；
3. 本会话"总是允许"的授权缓存 → 放行；
4. 计划批准的 `allowedPrompts` 命中（且无 deny 规则命中）→ 放行；
5. **deny 规则命中 → 拒绝**；
6. **ask 规则命中 → 询问**；
7. **allow 规则命中 → 放行**；
8. 只读且非破坏性 → 放行（路径类工具须在允许的工作目录内，否则落入询问）；
9. `acceptEdits` 模式下的编辑类工具 → 放行；
10. `dontAsk` 模式 → 拒绝；
11. 其余 → 询问。

其中"只读自动放行"与"文件写入"两步前置了一道**工作目录闸门**：`Read` / `Glob` / `Grep` / `Edit` / `Write` / `MultiEdit` / `NotebookEdit` 等路径类工具，如果目标路径不在任何已注册工作目录（启动目录 + `additionalDirectories` + `/add-dir`）之内，会直接进入询问，即使规则或模式本会放行。

### REPL 询问框

被询问时，输入框上方弹出确认框，快捷键：`a` 允许一次；`shift+a`（或 `s`）允许并记住（写入会话授权缓存）；`d` 拒绝；`q` / `esc` 拒绝并中断本回合。带建议快捷键（如 `w` = "允许并加入工作目录"）的询问，按下即应用建议并放行。

### 旧版 config.toml 的 [permissions]

`~/.biu/config.toml` 里的 `[permissions]` 小节（`mode` + `allowlist`）是旧接口，仍然生效，但规则建议统一写到 settings.json。该小节另有三个计划提示相关字段：`plan_drift_threshold`（计划偏移提示阈值，0 = 首次偏移即提示，负数 = 只观察不提示）、`suggest_plan_for`（触发"建议进入计划模式"的关键词列表）、`suggest_plan_disabled`（关闭该提示）。

## Hooks

Hooks 是你在生命周期节点上注册的 shell 命令。写在 settings.json 的 `hooks` 段，三层文件里的同名事件**全部执行**（合并为并集）：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "scripts/audit.sh", "timeout": 30 }
        ]
      },
      {
        "matcher": "Edit|Write",
        "hooks": [{ "type": "command", "command": "scripts/protect.sh" }]
      }
    ],
    "UserPromptSubmit": [
      { "hooks": [{ "type": "command", "command": "jq -r .prompt >> /tmp/biu-prompts.log" }] }
    ]
  }
}
```

### 配置字段

| 字段 | 说明 |
|------|------|
| `matcher` | 正则（Go regexp 语法，支持 `\|` 或写法），匹配事件标识符：`PreToolUse` / `PostToolUse` 匹配工具名，`Notification` 匹配通知类型，`SessionStart` 匹配来源（`startup` / `resume` / `compact`）；省略 = 匹配全部 |
| `type` | `command`（fork 子进程）或 `internal`（内置插件用）；`prompt` / `agent` / `http` 为保留值，当前不执行 |
| `command` | 要执行的命令 |
| `shell` | `bash` / `sh` / `pwsh`，默认 `sh` |
| `timeout` | 秒；默认 60 |
| `if` | 可选的前置条件规则 |

### 命令契约

- **stdin**：一行 JSON 对象（事件相关字段：`tool_name`、`tool_input`、`prompt` 等），以换行结尾。
- **stdout**：可选 JSON；能解析成决策对象时引擎会采纳（见下）。
- **退出码 0**：成功；stdout 若是 JSON 则被消费。
- **退出码 2**：软阻断——本次工具调用 / 提交被中止，stderr 回灌给模型让它自行调整。
- **其他退出码**：非致命告警，stderr 展示给用户。

决策对象（stdout JSON）支持的字段：

```json
{
  "block": true,
  "reason": "不允许删除 main 分支",
  "additionalContext": "追加到系统上下文的提示（SessionStart / UserPromptSubmit）",
  "replacePrompt": "改写后的用户提示（UserPromptSubmit）"
}
```

`block: true` 与退出码 2 等效；空字段表示"无意见"。

### 事件清单

工具与权限：`PreToolUse`、`PostToolUse`、`PostToolUseFailure`、`PermissionRequest`、`PermissionDenied`

对话回合：`UserPromptSubmit`、`Stop`、`StopFailure`、`Notification`

子代理：`SubagentStart`、`SubagentStop`、`TeammateIdle`

任务与文件：`TaskCreated`、`TaskCompleted`、`FileChanged`、`CwdChanged`

会话与压缩：`SessionStart`、`SessionEnd`、`PreCompact`、`PostCompact`

REPL 里 `/hooks [<事件名片段>]` 列出当前注册的全部 hooks（含来源层）。

### 信任闸门

shell hooks 与 status-line 脚本等于任意命令执行。为了防止恶意仓库的 `.biumind/settings.json` 携带恶意 hook，biu 只在**受信任的目录**下执行 hooks：首次在新目录使用时用 `/trust here` 授予（持久化到 `~/.biumind/trust.json`）、`/trust session` 仅本会话信任、`/trust remove <path>` 撤销。CI 等脚本环境可用环境变量 `BIU_TRUST=1` 一次性信任全部目录。

## Headless / SDK 场景的权限

无人值守运行时没有交互终端，用 `--permission-policy` 指定策略：`deny`（默认，全部拒绝、宁可失败）、`allow`、`stdin`（终端逐条询问）、`stdin-json`（stdout 发 `PERMISSION_ASK` JSON 事件、stdin 收 JSON 决策，供 GUI 消费，支持 `allow` / `deny` / `always` 三种决定与建议快捷项）。详见 [usage.md](usage.md#headless-模式)。
