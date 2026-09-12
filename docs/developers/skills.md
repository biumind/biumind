# Skills 开发与使用

技能（Skill）是一个可复用的 Agent 能力包：一份 `SKILL.md`（YAML frontmatter + Markdown 正文），可选附带脚本、参考文档和素材文件。Agent 在运行时按需加载技能正文，从而"学会"某类任务的固定做法。技能与 [BiuApp](biuapp.md) 平行：技能是 Markdown 指令包，BiuApp 是带 UI 的完整小程序。

本文覆盖三部分：**SKILL.md 格式**（面向技能作者）、**云端生命周期**（安装 / 提审 / 审批 / 共享 / 按 Agent 启停）、**CLI 工具链**（打包 / 签名 / 同步）。

## SKILL.md 格式

一个技能就是一个目录，目录名即技能标识符（kebab-case），根下必须有 `SKILL.md`：

```text
my-skill/
├── SKILL.md          # 必需：frontmatter + 正文
├── scripts/          # 可选：可执行脚本（bash / python 等）
├── references/       # 可选：支撑文档（详细手册、大段参考资料）
└── assets/           # 可选：模板、图标等素材
```

`SKILL.md` 示例：

```markdown
---
name: my-skill
display_name: 我的技能
description: 一句话说清这个技能做什么、什么时候用。这段话会进入 Agent 的可用技能列表，直接决定 Agent 能否选对技能。
icon: 🛠
permissions: ["wiki.read"]
paths: ["docs/**"]
---

# 正文

写给 Agent 看的操作指令。保持精炼——正文会被注入提示词，
大段内容应拆到 references/ 里按需读取。

参数：$ARGS
```

### frontmatter 字段

frontmatter 是扁平的 `key: value` 块（仅支持顶层标量与逗号分隔列表，不支持嵌套结构）。字段按消费方分两组：

**云端安装与内置技能加载时解析**（安装 URL / `.biuskill` 归档、内置技能目录）：

| 字段 | 必填 | 说明 |
|---|---|---|
| `name` | 是 | 技能标识符，kebab-case，组织内唯一；缺失时安装被拒绝 |
| `description` | 是 | 单行描述，进入 Agent 的可用技能列表，驱动技能选择匹配；缺失时安装被拒绝 |
| `display_name` | 否 | 展示名（默认回退到 `name`） |
| `icon` | 否 | 图标：单个 emoji，或 `https://` 图片 URL |
| `paths` | 否 | 自动挂载 glob 列表（见下文"自动挂载"） |
| `permissions` | 否 | 声明的权限（见下文"权限声明"） |
| `version` | 否 | semver 版本号 |
| `license` | 否 | SPDX 标识 |
| `repository` | 否 | 上游 git 仓库地址 |
| `source_url` | 否 | 来源 URL（导入溯源） |
| `author` | 否 | 作者名 |

**CLI 本地加载时解析**（`~/.biumind/skills/`、`<项目>/.biumind/skills/` 等本地目录）：

| 字段 | 必填 | 说明 |
|---|---|---|
| `name` | 否 | 技能名（缺失时回退到目录名） |
| `description` | 否 | 单行描述 |
| `when-to-use` | 否 | 触发时机描述，供 Agent 判断何时使用该技能（别名 `whentouse`） |
| `user-invocable` | 否 | 布尔，是否允许用户以 `/技能名` 直接调用（别名 `userinvocable`，接受 `true/yes/1`） |
| `paths` | 否 | 自动挂载 glob 列表，规则同上 |

> [!NOTE]
> 两套解析器目前各自独立：本地加载不消费 `display_name` / `icon` / `permissions`，云端安装不消费 `when-to-use` / `user-invocable`。一份 SKILL.md 同时写全两组字段是完全合法的，这也是内置标准库的做法。

### 正文与 `$ARGS`

正文是 post-frontmatter 的 Markdown，会被整体注入提示词。惯例：正文控制在约 50KB 以内，更长的内容拆进 `references/`，Agent 通过 `skill.read_reference` 工具按需读取。

CLI 调用侧支持参数展开：正文中出现的 `$ARGS`（及其别名 `$1`）会在调用时替换为用户传入的参数串。例如 `biu skill run my-skill 每周五` 或会话内的 `/my-skill 每周五`，都会把 `每周五` 代入正文。未知占位符原样保留。

### 自动挂载（`paths`）

声明了 `paths` 的技能，当会话工作目录或最近触碰的文件命中任一 glob 时，正文会被自动注入系统提示词，无需显式调用。匹配规则：

- 纯字面路径（不含通配符）：按"包含"匹配，如 `apps/cli` 命中其子树下的任何路径；
- 含 `**` 的模式：翻译为正则（`*` → `[^/]*`，`**` → `.*`）匹配路径段；
- 其他 glob：对完整路径及文件名做 `filepath.Match`；
- 末尾 `/**` 会被剥离；只剩裸 `*` / `**` 视为"到处都匹配"，等价于不自动挂载。

未声明 `paths`（或视为空）的技能始终可通过 `skill.activate` 工具或 `/技能名` 调用，只是不自动注入。

### 权限声明（`permissions`）

`permissions` 是逗号分隔的权限串列表，常见值如 `sandbox.exec`、`network.fetch`、`wiki.read`、`memory.recall`。声明会随技能行存储，并作为属性传入鉴权策略引擎参与决策；调用方应只声明技能实际需要的最小权限集。

## 云端生命周期

技能在云端（Runtime 服务，REST 接口见 [API 参考](api.md)，统一挂在单 origin 的 `/v1/skills` 下）按**来源**与**状态**两个维度管理。

### 来源（source）

| 值 | 含义 |
|---|---|
| `bundled` | 平台内置（skills-stdlib 随 Runtime 启动自动装载），对所有组织可见，不可删除 |
| `org` | 组织共享，管理员维护 |
| `user` | 用户自创（propose 流程或本地 `.biuskill` 安装） |
| `marketplace` | 从技能市场拉取（签名包） |
| `imported` | 从外部 URL 导入 |

### 状态机

```text
                 propose                    approve
   (不存在) ───────────────▶ staged ──────────────────▶ active
                                │                        │    │
                            reject│                 reject(admin)│ │
                                ▼                        ▼    │ share-org
                             disabled ◀────────────── staged_org
                                │                     (管理员审批)
                                ▼                          │
                            [终态]                    approve(admin)
                                                              │
                                                              ▼
                                         (回到 active，成为组织共享)
```

服务端校验的完整转移表：

| 当前状态 | 允许到达 | 触发动作 |
|---|---|---|
| `staged` | `active` / `disabled` | 审批通过 / 驳回 |
| `staged_org` | `active` / `disabled` | 管理员审批 / 驳回 |
| `active` | `disabled` / `staged_org` | 所有者停用 / 申请组织共享 |
| `disabled` | （无） | 终态；要复活请以 `update_of` 指向原技能重新提审 |
| `suspended` | （无） | 平台级封禁，用户操作不可触达 |

> [!WARNING]
> 内置技能（`source=bundled`）不可删除。标识符在组织内唯一，重名创建会返回冲突错误。

### 自助创作审批流（propose / approve / reject）

用户（或 Agent 在会话中通过 `skill.propose` 工具）提交草稿后技能进入 `staged`，等待审批：

```bash
# 提审（identifier / name / description / body 均必填；update_of 可选，
# 指向既有技能 ID 时审批卡片会渲染与旧版的 diff）
curl -X POST https://<服务器地址>/v1/skills/propose \
  -H "Authorization: Bearer <PAT>" \
  -H "Content-Type: application/json" \
  -d '{
    "identifier": "weekly-report",
    "name": "周报生成",
    "description": "汇总本周 git 提交与文档变更，生成周报",
    "body": "# 步骤\n…"
  }'

# 审批通过；enable_on_default_agent=true 时同时启用并固定到默认 Agent
curl -X POST https://<服务器地址>/v1/skills/<skill_id>/approve \
  -H "Authorization: Bearer <PAT>" \
  -d '{"enable_on_default_agent": true}'

# 驳回，reason 会出现在提案人的审计记录里
curl -X POST https://<服务器地址>/v1/skills/<skill_id>/reject \
  -H "Authorization: Bearer <PAT>" \
  -d '{"reason": "缺少权限声明"}'
```

每次状态变更都会落审计事件（`skill.status_changed` 等），可回放。

### 组织共享（share-org）

`active` 状态的个人技能可申请转为组织共享：`POST /v1/skills/{id}/share-org` 将其置为 `staged_org`，组织管理员审批通过后成为 `org` 共享技能，对全体成员可见。组织共享技能默认只出现在可用列表里，不会自动固定或自动挂载，直到用户显式启用。

### 按 Agent 启停与固定

每个 Agent 与技能之间是一条独立的启停关系（`agent_id` + `skill_id` + `is_enabled` + `pinned`）：

```bash
# 在 Agent 上启用（is_enabled=true）
curl -X POST https://<服务器地址>/v1/skills/<skill_id>/toggle \
  -H "Authorization: Bearer <PAT>" \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "<agent_uuid>", "is_enabled": true, "pinned": false}'
```

`pinned=true` 表示固定：正文在每个会话直接注入系统提示词，跳过 `skill.activate` 往返。提示词预算有限，只对高频核心技能使用固定。

运行时技能按三层注入：

| 层 | 触发 | 注入方式 |
|---|---|---|
| 固定（pinned） | `agent_skills.pinned=true` | 正文直接进系统提示词 |
| 自动挂载（auto_attach） | `paths` 命中工作目录 | 正文直接进系统提示词 |
| 可用（available） | 已启用但不满足上面两条 | 仅名称 + 描述进列表，Agent 调 `skill.activate` 按需加载正文 |

Agent 侧共有六个技能工具：`skill.list`（枚举）、`skill.activate`（加载正文）、`skill.read_reference`（读捆绑资源）、`skill.exec_script`（在沙箱执行脚本）、`skill.export_file`（导出文件）、`skill.propose`（在会话中创作并提审新技能）。

## 内置标准库

平台随 Runtime 内置 8 个技能（`source=bundled`，全部组织可见），核心定位是"教 Agent 使用 BiuMind 自身"：

| 标识符 | 展示名 | 用途 | 声明权限 |
|---|---|---|---|
| `biumind` | BiuMind | 平台总指南与任务路由：不了解平台或不知该用哪个模块/工具/技能时，从这里开始 | — |
| `wiki` | 知识库 | 知识库（文档 + 块编辑器）工具集：查/读/写/搜知识库页面、从自有知识库做检索 | `wiki.read` |
| `memory` | 记忆 | 记忆系统（recall / preference / habit）：持久记住事实、跨会话回忆用户上下文 | `memory.recall` |
| `graph` | 知识图谱 | 梳理实体关系、追踪依赖、从积累的笔记分析主题全景 | `wiki.read` |
| `sandbox` | 云沙盒 | 在隔离环境（gVisor / Firecracker）中执行代码、运行命令、管理文件 | `sandbox.exec` |
| `app-center` | 应用中心 | 调用应用中心的一等公民应用（RSS / 邮件 / 股票 / PPT 等） | — |
| `artifacts` | Artifacts | 生成并预览交互式 UI 组件、SVG 图形、图表与可视化内容 | — |
| `skill-creator` | 技能创建 | 把刚跑通的工作流打包成可复用技能（用自然语言描述步骤即生成 SKILL.md） | — |

其中 `wiki` 还声明了 `paths: ["wiki/**", "docs/**"]`——在文档目录下工作时会自动挂载。

## CLI 工具链

`biu skill` 命令族覆盖本地管理与云端同步。除 `run` / `pack` / `unpack` / `keygen` / `sign` / `verify` 为纯本地操作外，其余命令需要 Runtime 地址：`--runtime-url` 参数或 `BIUMIND_RUNTIME_URL` 环境变量；Bearer token 取 `--token` 或配置文件中 `model-relay.virtual_key`。

```bash
# 安装：URL（服务器抓取 SKILL.md，source=imported）或本地 .biuskill（source=user）
biu skill install https://example.com/my-skill/SKILL.md
biu skill install ./my-skill.biuskill --agent <agent_uuid> --pin
biu skill install ./my-skill.biuskill --dry-run   # 只解析归档、列出将写入的文件，不联网

# 列出云端技能（组织范围）
biu skill list
biu skill list --status staged        # active / disabled / staged / staged_org / suspended
biu skill list --source bundled       # bundled / org / user / marketplace / imported

# 云端 → 本地（写入 ~/.biumind/skills/<identifier>/SKILL.md）
biu skill pull

# 本地 → 云端（不存在则创建，存在且内容有变则更新，相同则 no-op）
biu skill push weekly-report

# 比较本地与云端的 SKILL.md 哈希
biu skill diff weekly-report          # in-sync / diverged / local-only / cloud-only

# 离线展开：把 $ARGS 代入正文输出，不调用模型，适合脚本与 CI
biu skill run weekly-report 本周五截止

# 在指定 Agent 上启停（enable 可附带 --pin）
biu skill enable skill_xxx --agent <agent_uuid> --pin
biu skill disable skill_xxx --agent <agent_uuid>
```

> [!TIP]
> `biu skill install` 对公共技能目录站的页面链接做了适配：会自动把目录页 URL 改写成可直连抓取的 SKILL.md 地址（终端会提示经由哪个适配器）。GitHub raw 及任意直链 SKILL.md 无需适配直接可用。

> [!WARNING]
> `pull` 遇到"本地已改且云端也变了"的技能不会自动合并或覆盖，会标记 conflict 并以非零退出码结束——先 `biu skill diff <name>` 查看，再决定 `push` 覆盖云端还是删除本地接受云端版本。

## `.biuskill` 归档与签名

`.biuskill` 是技能的分发格式，本质是一个**确定性 ZIP**：对同一份源目录重复打包，字节完全一致。这是签名链的前提——签名覆盖的是归档字面字节，不是其规范化形式。

### 归档布局与限制

```text
my-skill.biuskill
├── SKILL.md          # 必需，归档根下
├── scripts/…         # 可选
├── references/…      # 可选
└── assets/…          # 可选
```

- 三个目录之外、根下 `SKILL.md` 之外的文件在打包与安装时都会被忽略（带警告）；
- 归档上限 8MB；`SKILL.md` 上限 256KB；单个捆绑资源上限 64KB（更大文件需走对象存储路径，当前版本直接拒绝）；
- 打包确定性规则：条目按路径字典序、mtime 固定为 1980-01-01、文件权限位固定 `0644`；
- 含 `..` 或绝对路径的条目在打包与安装两侧都被拒绝（防路径穿越）。

### 打包与签名流程

```bash
# 1. 生成 ed25519 密钥对（私钥 PKCS#8，公钥 SPKI，均 PEM）
biu skill keygen                        # 产出 biuskill.key + biuskill.key.pub
biu skill keygen --prefix acme          # 产出 acme.key + acme.key.pub

# 2. 打包（默认输出 <dir>.biuskill）
biu skill pack ./my-skill               # 或 -o /tmp/my-skill.biuskill

# 3. 签名：对归档字节做 ed25519 签名，base64 单行写入 <pack>.sig
biu skill sign my-skill.biuskill --key acme.key

# 4.（接收方）验证
biu skill verify my-skill.biuskill --pubkey acme.key.pub
biu skill verify my-skill.biuskill --pubkey acme.key.pub --sig other.sig

# 解包检视 / 手工编辑
biu skill unpack my-skill.biuskill      # 解到 ./my-skill/
```

私钥务必保密（`keygen` 会拒绝覆盖已存在的私钥文件）；公钥随发布渠道公开。密钥文件可用 `openssl pkey -in <file> -text -noout` 检视。

### 服务端信任模型

Runtime 侧通过信任库决定校验强度：

- **未配置信任库**（默认，兼容模式）：签名可选，任意归档可安装；
- **配置了信任库**（严格模式）：`.biuskill` 安装请求必须携带能通过任一受信公钥验证的 `signature_b64`，否则拒绝（403）。

信任库通过环境变量配置，目录优先：`BIUMIND_SKILL_TRUSTED_PUBKEY_DIR`（目录内每个 `*.pub` 文件是一个受信发布者，文件名即审计日志中的发布者 ID）或 `BIUMIND_SKILL_TRUSTED_PUBKEY_PEM`（单把内联公钥）。URL 与 inline 安装路径不经过签名校验。

## 客户端使用

桌面 / 移动客户端的「技能管理」页提供与 CLI 等价的可视化操作：

- **列表与筛选**：按 全部 / 内置 / 组织 / 我的 / 市场 / 待审 六类过滤，点击进入详情页（内容、权限、启用状态、调用次数与最近调用时间）；
- **安装**：从 URL 或本地 `.biuskill` 安装，可同时绑定到某个 Agent；
- **审批**：待审草稿（`staged` / `staged_org`）的通过 / 驳回操作在详情页完成；
- **多端同步**：页面订阅组织级技能事件流，别人远程审批了你的草稿时列表会自动刷新并弹出提示。

## 下一步

- 接口细节（认证、错误码）：[API 参考](api.md)
- 在会话流中驱动 Agent（含技能工具）：[SDK Protocol](sdk-protocol.md)
- 做一个带 UI 的完整应用而非指令包：[BiuApp 开发](biuapp.md)
