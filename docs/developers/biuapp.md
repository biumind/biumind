# BiuApp 开发指南

BiuApp 是 BiuMind 应用中心的扩展单元：一个自包含的能力包，声明自己支持哪些动作（actions）、需要哪些权限（permissions）、渲染哪些界面（views），平台负责把它挂进 Agent 的工具列表和客户端的应用面板。同一个应用对 Agent 是一组可调用的工具，对用户是一组声明式渲染的页面——两套消费面共享同一份 manifest 和同一套后端实现。

本文面向想开发自己的 BiuApp 并上架应用中心的第三方开发者。所有字段、命令、接口均以仓库内实现为准。

> [!TIP]
> 如果你只是想让外部 AI Agent 读写你的知识库，不需要写 BiuApp——用 [MCP 工具服务](api.md)即可。BiuApp 适合"把一个新能力同时暴露给 Agent 和用户界面"的场景。

## 目录

- [应用形态](#应用形态)
- [快速开始](#快速开始)
- [本地开发与调试](#本地开发与调试)
- [manifest.yaml 参考](#manifestyaml-参考)
- [Go 实现：App 契约](#go-实现app-契约)
- [打包与签名](#打包与签名)
- [实例解析：RSS 应用](#实例解析rss-应用)
- [上架应用中心](#上架应用中心)
- [运行时行为](#运行时行为)
- [校验规则速查](#校验规则速查)

## 应用形态

manifest 的 `kind` 字段声明应用形态，取值：

| kind | 含义 |
|---|---|
| `backend` | 纯后端动作，无独立界面。Agent 通过工具调用使用 |
| `view` | 纯视图壳，不含自己的 Go 后端，视图数据来自平台已有接口或自己声明的 action |
| `hybrid` | 后端 + 声明式视图，最常见的完整形态 |
| `webview` | 内嵌外部网页 |
| `container` | 容器形态，暂未开放，安装时会被拒绝 |

脚手架提供了三个模板对应三种典型起点：

```bash
biu app new my-app --from minimal      # 纯后端：单 action，无 UI
biu app new my-app --from view_only    # 纯视图：无 Go 后端
biu app new my-app --from hybrid_full  # 完整形态（默认）：actions + views + triggers + sidebar
```

`<slug>` 必须是小写 kebab-case（首字符为字母，仅 `a-z`、`0-9`、`-`），目标目录必须为空，否则脚手架拒绝执行。模板中的 `{{slug}}` 占位符会被替换为你给的 slug。

## 快速开始

前置：安装 [biu CLI](../cli/getting-started.md)。

```bash
# 1. 脚手架（默认 hybrid_full 模板）
biu app new feed-hub
cd feed-hub   # 目录名 = 你传入的 slug

# 2. 校验 manifest
biu app validate

# 3. 查看解析结果（人读或 --json）
biu app inspect
biu app inspect --json

# 4. 本地起开发服务器（见下一节）
biu app run --dev --mock fixtures/

# 5. 打包
biu app pack
```

`biu app` 的全部子命令：

| 命令 | 作用 |
|---|---|
| `biu app new <slug> [--from <模板>]` | 从模板脚手架新项目 |
| `biu app validate [--manifest <路径>]` | 按平台规则校验 manifest，默认 `./manifest.yaml` |
| `biu app inspect [--manifest <路径>] [--json]` | 输出解析后的 manifest |
| `biu app run --dev [...]` | 本地开发服务器（详见下节） |
| `biu app pack [--source DIR] [--out FILE] [--key PATH] [--unsigned]` | 打包为 `.biuapp` 分发文件 |
| `biu app verify <file.biuapp> [--trust-key PATH]` | 校验包内哈希与签名 |
| `biu app keygen [--name publisher]` | 生成 ed25519 发布者密钥对 |

`validate` 会一次性列出**所有**问题（格式为 `路径 [错误码]: 说明`），不用每个 typo 修一轮跑一轮。

## 本地开发与调试

```bash
biu app run --dev \
  [--source DIR]          # 应用源码目录，默认当前目录
  [--addr 127.0.0.1:7099] # 开发服务器绑定地址，只监听回环
  [--mock fixtures/]      # mock 模式：用 fixture 文件应答 invoke
  [--no-subproc]          # 不启动 go run 子进程（纯 view 应用用）
```

开发服务器暴露以下端点，桌面客户端据此在"开发中"面板里发现并加载你的应用：

```text
GET  /v1/dev/health              存活探测
GET  /v1/dev/apps                开发中的应用清单 + manifest
GET  /v1/dev/apps/{slug}/manifest
POST /v1/dev/apps/{slug}/invoke  调用 action（mock 模式下读 fixture）
GET  /v1/dev/events              SSE 状态流（manifest 重载 / 子进程日志等）
```

行为细节：

- 启动时先解析并校验 manifest，失败直接中止。
- 监视 `manifest.yaml` 与 `*.go` 的变更：manifest 变了就重新加载校验；Go 源码变了就自动重启 `go run` 子进程。
- 运行中按键：`r` 手动重启子进程，`q` 退出，`h` 帮助。

### mock 模式

当前版本中，开发服务器**不会**把 invoke 代理进 Go 子进程——后端动作的本地调试用 mock fixture 驱动：

```text
fixtures/
  list_recent.json        # 优先匹配 <action>.json
  feed-hub.list_recent.json  # 也可以 <slug>.<action>.json
```

fixture 内容就是 action 的 JSON 返回值。前端视图联调（布局、模板插值、toolbar 交互）用 mock 完全够用；真实后端路径的验证走 `biu app pack` 后安装到服务端的路径。

## manifest.yaml 参考

manifest 是应用的单一事实来源。YAML 键与内部模型的映射关系（与直觉不同的地方只有两处）：

- `identifier:` → 应用 slug（路由 / 注册唯一键）；同时填充内部的 legacy Name 字段。
- `name:` → **显示名**（给人看的标题），不是 slug。

未知的 YAML 键会被忽略而不是报错，所以新版 SDK 写的 manifest 在旧版工具上仍能解析；严格性由 `biu app validate` 与安装路径负责。

### 基础字段

```yaml
identifier: feed-hub        # 必填。kebab-case，或 marketplace 的 <author>/<slug> 作用域形式
version: 0.1.0              # 必填。语义化版本（x.y.z[-pre]）
name: Feed Hub              # 显示名
description: 聚合你的所有信息源 # 必填，≤ 200 字符
author: 张三                 # 字符串或对象两种写法（见下）
icon: 📡                    # 空 / emoji（≤8 字节）/ https URL / cas:<sha256>
category: productivity      # productivity|content|data|comm|dev|utility
kind: hybrid                # backend|view|hybrid|webview|container（container 暂不开放）
```

`author` 的对象写法（上架签名时需要 `public_key`）：

```yaml
author:
  name: 张三
  url: https://example.com
  public_key: ed25519:<base64 公钥>   # biu app keygen 的输出
```

### permissions

应用声明的平台权限清单，安装时用户可见并逐项确认。每条形如 `前缀` 或 `前缀:参数`，合法前缀：

| 前缀 | 授权内容 |
|---|---|
| `net.outbound` | 出站网络请求；可带参数限定域名，如 `net.outbound:*.example.com` |
| `model-relay.invoke` | 调用平台模型网关（LLM / embedding / 计费统一出口） |
| `wiki.read` / `wiki.write` | 知识库文档读写 |
| `graph.read` / `graph.write` | 知识图谱读写 |
| `memory.read` / `memory.write` | 记忆读写 |
| `files.read` / `files.write` | 文件对象读写 |
| `cron.register` | 注册定时任务 |
| `webhook.register` | 注册 webhook 回调 |
| `notify.send` | 发送通知 |
| `sandbox.exec` | 在云沙箱执行命令 |
| `oauth:<provider>` | 完成 OAuth 授权流 |
| `secrets.read:<provider>` | 读取托管的凭据 |

> [!NOTE]
> `hub.invoke` 是 `model-relay.invoke` 的旧别名，旧 manifest 仍然兼容；新应用请直接写 `model-relay.invoke`。

用户安装时授予的权限必须是声明清单的**子集**，超出会被服务端以 `permissions_exceed` 拒绝（HTTP 400）。权限声明会进入平台的 Cedar 策略引擎做细粒度裁决，因此声明粒度越窄，用户安装时的信任成本越低。

### data_scopes

```yaml
data_scopes:
  - wiki:collection:feed-hub
```

自由格式的数据范围声明，用于向用户表达"本应用会触碰哪些数据域"，以及供平台的隔离策略使用。

### actions

每个 action 是一个可调用单元，Agent 侧表现为工具，视图侧可作为 `data_source` / 表单提交目标：

```yaml
actions:
  - name: add_item                 # 必填。^[a-z][a-z0-9_-]*$，应用内唯一
    description: 添加一条记录        # 建议必写，Agent 靠它理解何时调用
    risk: low                      # low|medium|high，决定运行时的审批策略
    input_schema:                  # JSON Schema 片段（object 类型）
      type: object
      required: [title]
      properties:
        title:
          type: string
          title: 标题
    output_schema: ...
    # 以下为可选增强字段：
    human_intervention: required   # never|optional|required；required 强制每次人工确认
    timeout_ms: 30000              # 0–600000；0 = 服务默认
    streamable: true               # 声明后走 Stream 接口而非 Invoke
    rate_limit:                    # 单安装维度的限流
      per_minute: 10
      per_hour: 100
      per_day: 1000
```

`risk` 驱动平台的权限模式（自动放行 / 询问 / 高危隔离），`human_intervention: required` 可以强制某个 action 无论用户偏好如何都要人工确认——适合删除、支付类操作。

### views

每个 view 声明一条客户端路由和它的渲染方式。路由必须以 `/apps/<identifier>` 开头：

```yaml
views:
  - id: home                       # 应用内唯一
    route: /apps/feed-hub
    title: 首页                     # 支持 i18n key
    layout: list_detail            # 见下表
    data_source:
      action: list_items           # 必须是已声明的 action
      input:
        limit: 20
    refresh_on:                    # 订阅 Realtime topic 失效缓存；<self> 运行时替换为安装 id
      - "app:install:<self>:item_added"
    item_template: ...
    toolbar: ...
```

layout 取值与各 layout 的必备字段：

| layout | 说明 | 必备字段 |
|---|---|---|
| `list` | 列表 | `item_template` |
| `list_detail` | 列表 + 详情页 | `item_template`、`detail_view`（子视图 id） |
| `form` | 表单 | `schema_ref` 和/或 `submit` |
| `webview` | 内嵌网页 | `url` |
| `grid` | 网格 | `item_template`、可选 `grid` |
| `dashboard` | 卡片面板 | `cards`（每张卡有 `id`、`kind: text\|number\|list\|chart`、`span` 1–12、可选 `data_source` / `field` / `format`） |
| `agent_chat` | 内嵌 Agent 对话 | `agent_id`；可选 `agent_chat.initial_prompt` / `tool_filter` / `system_prompt_override` |
| `custom` | 应用自定义渲染 | 由平台客户端侧约定 |

**模板插值**：列表项字段用 `${item.<字段>}` 引用 `data_source` 返回的 items 元素，可叠加过滤器，例如：

```text
${item.title}
${item.updated_at | relative_time}
${item.summary | truncate(120)}
${item.url | domain}
${item.last_status | default(等待首次抓取)}
```

**动作绑定**（toolbar 按钮、列表项操作）要么调 action，要么做路由跳转，二者必有其一：

```yaml
toolbar:
  - label: 新建
    icon: add
    route: /apps/feed-hub/add
  - label: 刷新
    icon: refresh
    action: refresh_all          # 必须在 actions[] 里声明过
    on_success:
      toast: 已刷新
      refresh: true              # 调用成功后刷新当前视图
  - label: 清空
    action: purge_all
    confirm: 确认清空全部数据？      # 二次确认文案
    risk_warning: 此操作不可恢复     # 高危操作的附加警示
    on_success:
      toast: 已清空
      navigate: /apps/feed-hub    # 成功后跳转
```

**表单视图**直接复用 action 的 schema，避免重复声明：

```yaml
  - id: add
    route: /apps/feed-hub/add
    layout: form
    schema_ref: actions.add_item.input_schema   # manifest 内的点路径
    submit:
      action: add_item
      on_success:
        toast: 已添加
        navigate: /apps/feed-hub
```

**路由参数**：route 可以带参数段（如 `/apps/feed-hub/boards/:board_id`），在 `data_source.input` 里用 `${route.board_id}` 插值取回。分页用 `pagination.page_param`（路由参数名）/ `total_field`（返回值里的总数路径，如 `data.total`）/ `page_size` 声明。

`grid` 可配响应式列数 `[窄, 中, 宽]`（各 1–6，对应客户端断点）、`spacing`、`aspect_ratio`。

### triggers

声明式注册的自主入口，触发时平台调用你指定的 action：

```yaml
triggers:
  - kind: cron               # 定时
    name: hourly_refresh     # 应用内唯一
    expr: "5 * * * *"        # 标准 5 字段 cron；最小间隔 1 分钟，"* * * * *" 被拒绝
    if_inactive_for: 30m     # 可选：用户闲置达到该时长则跳过本次
    action: refresh_all
  - kind: webhook
    name: inbound
    path: /callback          # 必须以 / 开头
    auth: hmac               # hmac|none
    accept_methods: [POST]
    action: ingest
  - kind: inbox              # 消息渠道路由
    name: chat_command
    pattern: "订阅 "
    action: subscribe_from_text
```

所有 trigger 的 `action` 必须在 `actions[]` 中声明。trigger 也可以带静态 `input`，触发时与动态负载合并后传给 action。

### skills

应用可捆绑 Skills（SKILL.md）随安装写入平台的技能库，卸载时级联清理：

```yaml
skills:
  - identifier: feed-hub-digest   # 技能标识，应用内唯一
    file: skills/digest.md        # 包内相对路径
```

Go 侧需实现 `SkillContent(identifier)` 返回文件内容（见下文契约），通常用 `//go:embed` 在编译期嵌入。

### requires / billing / sidebar / i18n

```yaml
requires:                        # 安装时校验的硬依赖；缺失则安装失败并提示先装依赖
  - kind: app                    # app | mcp_server
    identifier: rss
    min_version: 0.2.0

billing:                         # 应用市场计费声明
  tier: free                     # free | pro | usage
  # pro：
  price: { currency: USD, amount: 4.99, period: monthly }   # period: monthly|yearly|lifetime
  trial_days: 14
  # usage：
  meters:
    - name: api_calls
      unit: 次
      unit_price_micro: 1000     # 微美元（1e-6 USD）

sidebar:                         # 侧栏行为偏好
  preferred_position: middle     # top|middle|bottom
  badge_action: unread_count     # 返回 {count, severity} 的 action，用作侧栏角标
  badge_refresh: 120             # 角标刷新秒数，≥ 60
  mobile_bottom_eligible: true   # 移动端底部栏候选
  # default_pin 仅平台/组织安装可用，市场应用忽略此值

i18n:
  default: zh-CN
  locales: [zh-CN, en-US]
  files: locales/                # 包内目录，默认 locales/
```

## Go 实现：App 契约

后端形态（`backend` / `hybrid`）的应用实现以下接口（Go，基于 `packages/go-sdk/biu/biuapp`）：

```go
type App interface {
    Manifest() Manifest
    Init(ctx context.Context, deps Deps) error
    Invoke(ctx context.Context, action string, in json.RawMessage) (any, error)
}
```

- `Manifest()` 返回声明的动作 / 权限 / 视图 / 触发器。进程内注册时重复的 slug 会直接报错。
- `Init()` 在注册时调用一次。平台注入的 `Deps` 见下。
- `Invoke()` 承接每次调用。`in` 是不透明的 JSON，应用按自己声明的 `input_schema` 校验；返回值任意可 JSON 序列化。未知名建议返回包内预定义的 `biuapp.ErrUnknownAction`，运行时会把它映射成友好的"工具不存在"错误。

`Deps` 注入的平台能力：

```go
type Deps struct {
    HTTP    HTTPClient      // 出站 HTTP 客户端（测试可注入 fake）
    Logger  Logger          // 可选，零值为丢弃
    Now     func() any      // 可覆盖时钟，测试确定性用
    Events  EventPublisher  // 视图数据失效事件出口（见"运行时行为"）
}
```

### 可选接口

按需实现任意子集，注册中心用类型断言探测，未实现的方法静默跳过：

| 接口 | 方法 | 时机 |
|---|---|---|
| 生命周期钩子 | `OnInstall / OnUninstall / OnUpgrade / OnConfigUpdate` | 安装 / 卸载 / 升级 / 配置变更。钩子里适合做外部世界的簿记（注册远端 webhook、撤销 OAuth）；数据库清理平台已级联完成。`OnInstall` 失败会回滚安装；钩子在事务提交**之后**执行，慢操作不会拖住事务 |
| `TriggerHandler` | `OnTrigger(ctx, TriggerEvent)` | trigger 触发。若触发逻辑就是"调某个 action"，可以不实现——平台默认把 `TriggerEvent` 路由到 `Invoke(action, input)` |
| `ViewDataProvider` | `OnViewData(ctx, ViewDataRequest)` | 视图数据不走通用 action 路径时覆盖（如昂贵联查）。不实现则平台默认调用视图的 `data_source.action` |
| `StreamingApp` | `Stream(ctx, action, in, emit)` | 声明了 `streamable: true` 的 action 走这里；通过 `emit` 推 `log` / `progress` / `partial` / `final` 事件（推送间隔建议 ≥ 200ms，客户端会合并） |
| `BundledSkillProvider` | `SkillContent(identifier) ([]byte, error)` | 声明了 `skills[]` 的应用必须实现；未知的 identifier 返回 `biuapp.ErrSkillNotFound` |

生命周期钩子收到的 `Install` 携带安装 id、slug、版本、scope（`user`/`org`）、scope id、安装配置（不含密钥——密钥走平台凭据托管）。

## 打包与签名

### .biuapp.yaml

项目根目录的打包清单，控制 `biu app pack` 复制哪些文件进包：

```yaml
include:
  - manifest.yaml
  - README.md
  - LICENSE
  - skills/**
  - assets/**
exclude:
  - .git/**
  - "**/*_test.go"
```

glob 语义：`manifest.yaml` 精确匹配；`skills/**` 递归目录；`**/*_test.go` 任意深度匹配尾部模式。点开头的目录（`.git` / `.idea` 等）自动跳过。没有 `.biuapp.yaml` 时默认只打包 `manifest.yaml` + `README.md` + `LICENSE`。

### biu app pack

```bash
biu app pack \
  [--source DIR]   # 项目根，默认当前目录
  [--out FILE]     # 输出路径，默认 dist/<slug>-<version>.biuapp
  [--key PATH]     # 签名私钥，默认 ~/.biumind/keys/publisher.ed25519
  [--unsigned]     # 跳过签名（仅限本地安装，市场拒绝）
```

打包前会强制校验 manifest，坏 manifest 不允许出包。产物是 zip 格式的 `.biuapp`：

```text
manifest.yaml      # 必有，写在 zip 首位
manifest.sig       # 有签名时：ed25519 对 manifest.yaml 字节签名
<你 include 的文件>  # 按确定顺序写入
SHA256SUMS         # 每个文件一行 "<sha256 hex>  <相对路径>"
SHA256SUMS.sig     # ed25519 对 SHA256SUMS 字节签名 —— 信任根
```

双签名各防一种攻击：`manifest.sig` 防"在有效清单文件下偷换 manifest"，`SHA256SUMS.sig` 防"在有效 manifest 下偷换其它文件"。打包是确定性的——同源同字节同哈希，CI 可以直接 pin 输出的 zip sha256（pack 命令会打印它）。

### 密钥：biu app keygen

```bash
biu app keygen [--name publisher]
```

在 `~/.biumind/keys/` 下生成 `<name>.ed25519`（私钥，权限 0600）与 `<name>.ed25519.pub`（公钥）。**已存在的密钥拒绝覆盖**——发布密钥一旦轮换，历史签名全部失效。输出中的 publisher id 形如 `ed25519:<base64公钥>`，把它写进 manifest：

```yaml
author:
  name: 张三
  public_key: ed25519:MCowBQYDK2VwAyEA...
```

### biu app verify

```bash
biu app verify dist/feed-hub-0.1.0.biuapp \
  [--trust-key ~/.biumind/keys/publisher.ed25519]   # 可重复
```

校验三件事：manifest.yaml 存在；SHA256SUMS 列出的每个文件哈希匹配；有签名时两个签名都能被信任公钥验证。不传 `--trust-key` 时对已签名包"验哈希但跳过身份验证"（输出标注 signed/unsigned）。单个文件上限 50 MiB。

> [!WARNING]
> 当前版本 `--trust-key` 只接受含私钥的密钥对路径，直接传 `.pub` 公钥文件暂不支持。

## 实例解析：RSS 应用

仓库内置的 RSS 订阅应用（`packages/go-sdk/biu/biuapp/rss/`）是 hybrid 形态的完整范本。它的 manifest（Go 字面量声明，与 YAML 等价）展示了一整套声明：

```go
func (a *App) Manifest() biuapp.Manifest {
    return biuapp.Manifest{
        Name:        "rss",
        Version:     "0.2.0",
        Description: "Subscribe to RSS / Atom feeds; AI digest into wiki",
        Author:      "BiuMind",
        Permissions: []string{"net.outbound", "hub.invoke", "wiki.write", "cron.register"},
        Actions:     []biuapp.ActionSpec{ /* 见下 */ },
        ManifestExt: biuapp.ManifestExt{
            Identifier: "rss",
            Title:      "RSS 订阅",
            Category:   "content",
            Kind:       "hybrid",
            Views:      []biuapp.ViewSpec{ /* ... */ },
            Triggers:   []biuapp.TriggerSpec{ /* ... */ },
            Skills:     []biuapp.SkillRef{ /* ... */ },
            Sidebar:    &biuapp.SidebarHints{ /* ... */ },
        },
    }
}
```

几个值得照抄的做法：

**1. 动作分级声明风险。** `fetch` / `list_subscriptions` 是 `risk: low`（读外部源、读自己的数据），`digest` 要过模型网关做总结，标 `risk: medium`——用户在权限确认页看到的审批策略随之不同。

**2. 视图覆盖全部常用 layout。** `list_detail` 主列表（卡片模板 + 每项"取消订阅"动作带确认与 `on_success` toast/refresh）、`form` 添加页复用 `actions.subscribe.input_schema`、`grid` 榜单页、带路由参数的详情页：

```go
{
    ID: "board_detail",
    Route: "/apps/rss/boards/:board_id",   // 路由参数
    Layout: biuapp.LayoutListDetail,
    DataSource: &biuapp.ViewDataSource{
        Action: "boards_snapshot",
        Input: map[string]any{
            "board_id": "${route.board_id}",  // 参数插值回填 input
            "limit":    30,
        },
    },
    ...
}
```

**3. trigger 驱动无人值守刷新。** 两条 cron：`"5 * * * *"` 全量刷新、`"0 8 * * *"` 早八点摘要（带静态 input `{"window":"24h","max_items":10}`）。App 自身没实现 `TriggerHandler`，平台默认路由直接落到对应 action。

**4. 技能捆绑 + 编译期嵌入。**

```go
//go:embed skills/summarize.md
var summarizeSkill []byte

func (a *App) SkillContent(identifier string) ([]byte, error) {
    if identifier == "rss-summarize" {
        return summarizeSkill, nil
    }
    return nil, biuapp.ErrSkillNotFound
}
```

技能内容随二进制发布，不会在"运行的代码"与"装进技能库的内容"之间漂移。

**5. Invoke 用 switch 分发并埋点。** `Invoke` 包一层 `invokeInternal`，按返回值记录 `ok`/`error` 指标再返回；真实分发就是一个 action 名到处理函数的 switch。`Init` 为空实现——依赖全部通过 `WithXxx` 选项注入（`WithBoards` / `WithRadar` / `WithLLM`...），没注入的可选能力对应 action 返回明确的"未接线"错误，而不是 panic。

**6. 侧栏角标。** `Sidebar.BadgeAction: "unread_count"` + `BadgeRefreshSec: 120`——平台每两分钟调一次这个轻量 action 拿 `{count, severity}` 刷侧栏角标。

## 上架应用中心

当前有两种把应用交给用户的途径；应用市场（`.biuapp` 包的目录化分发）正在建设中，包格式与签名体系已就绪。

### 途径一：GitHub 仓库应用（repo-app）

把项目放在 GitHub 上，用户通过应用中心直接安装并**在其本机**运行（本地 CLI 拉起、只监听 127.0.0.1）。支持的技术栈自动探测：Node（`package.json` 的启动脚本）、Python（入口文件 / uv）、Dockerfile、纯静态站点（`index.html`）。macOS / Linux 可用（Windows 暂不支持）。

服务端接口（均需 Bearer JWT，经单 origin 网关 `/v1/apps/*` 路由到应用中心）：

| 接口 | 作用 |
|---|---|
| `POST /v1/apps/repo/analyze` | `{repo_url}` → 仓库分析草稿（语言、启动方式、配置项建议） |
| `POST /v1/apps/repo/installs` | `{repo_url, ref_type: release\|branch, config}` → 安装并登记目录。服务端会**重新执行**分析，客户端传来的草稿仅作展示 |
| `GET /v1/apps/installs/{id}/runtime` | 运行状态 `{mode:"local", status, url:null}`——URL 由本机 CLI 解析 |
| `GET /v1/apps/installs/{id}/builds` | 构建历史（最新在前，最多 20 条） |
| `POST /v1/apps/installs/{id}/redeploy` | 排队一次重部署，返回 `{build_id, ref, sha}` |
| `POST /v1/apps/installs/{id}/builds/{build_id}/complete` | CLI 上报构建结果 `{status: live\|failed, sha, log_ref}` |

用户本机的命令行界面：

```bash
biu repo-app install <github-url|owner/repo> [--ref v1.2.3]
biu repo-app ensure <name> [--env KEY=VALUE]...   # 幂等：缺则装、停则启
biu repo-app list
biu repo-app run <name> [--port 0] [--env KEY=VALUE]...
biu repo-app stop <name>
biu repo-app logs <name> [-f]
biu repo-app update <name> [--ref ...] [--install-id ID --build-id ID]
biu repo-app remove <name>
biu repo-app doctor                              # 探测 git/python3/uv/node/mise/docker
```

对开发者的关键约定：

- `run` / `ensure` 健康检查通过后在 stdout 打一行 `BIU_REPOAPP_URL=http://127.0.0.1:<port>`，桌面客户端靠这行拿到地址——不要污染 stdout。
- `--env KEY=VALUE` 合并写入实例的 `.env`（权限 0600），这是配置与密钥的交付通道。
- 桌面端点"重新部署"时，客户端拿到 redeploy 返回的 `build_id`，在本机执行 `biu repo-app update <name> --install-id <id> --build-id <id>`，CLI 完成后调 complete 接口回报结果。
- 仓库需要能被自动探测：可运行的 package.json 脚本 / Python 入口 / Dockerfile / index.html 四者有其一，否则要手工改 `runtime.json` 的 `start_cmd`。

### 途径二：编译进服务端（自托管）

自托管部署可以修改服务端代码，把 Go 实现的 App 直接 `Register` 进应用中心的注册表（内置的 rss / translate / tasks / email / webclip 就是这么做的）。适合私有化场景的内部应用；云上不能替你编译，云端分发走 repo-app 与（将来的）应用市场。

### 走向应用市场

`.biuapp` 包 + ed25519 签名就是为市场化分发准备的格式：上架要求带签名（`--unsigned` 仅限本地安装）、marketplace 形态要求 `<author>/<slug>` 作用域 identifier。目录、投稿与审核流程随市场上线开放，届时会提供 `biu app publish` 一类的命令；现在就可以按本文的签名流程提前把发布密钥和 `author.public_key` 准备好。

## 运行时行为

理解平台如何执行你的应用，有助于写出符合预期的动作与视图。

### 调用链路

用户或 Agent 调用 action 走 `POST /v1/apps/{name}/invoke`，请求体 `{"action": "...", "input": {...}}`：

1. Bearer JWT 校验；
2. 查调用者的安装记录——**未安装直接 403**（`not_installed`）；已禁用的安装同样 403；
3. 鉴权服务按安装时授予的权限做 Cedar 策略裁决，拒绝返回 403（`permission_denied`）；
4. 注册表检查 action 是否在 manifest 中声明，未声明的 400（`unknown_action`）；
5. 进入你的 `Invoke`（或 streamable action 的 `Stream`）；
6. 每次调用（含被拒的）写入调用审计表：调用方、动作、耗时、状态、错误码。

所以 manifest 是硬边界：没声明的 action 调不到，没授予的权限过不了鉴权。

### 事件与视图刷新

应用中心的所有状态变更（安装、卸载、升级、启停、配置更新、动作调用、触发器触发……）都伴随一条事件写入，经 Realtime 推送到客户端——这是平台侧的统一机制，保证任何已打开的界面不会停留在过期状态。对应用作者而言，它落在两处：

**被动侧**：视图可声明 `refresh_on` 订阅感兴趣的事件 topic（`<self>` 运行时替换为安装 id），命中即重新拉取 `data_source`。

**主动侧**：后端数据变化时，通过 `Init` 注入的 `Deps.Events` 主动宣布视图失效：

```go
deps.Events.PublishViewDataChanged(ctx, installID, "home", "boards")
// viewIDs 为空 = 该安装的所有视图全部失效
```

它产生一条 `app.view_data_changed` 事件，客户端收到后重新拉取对应视图的数据。

> [!WARNING]
> 应用代码不允许直接写平台的数据库。事件、技能内容、安装记录全部经由平台提供的出口（`Deps.Events`、生命周期钩子、安装接口）落库——统一出口是事件不丢、界面不 stale 的前提，绕过出口的写法在托管环境不可用。

事件推送是"尽力而为"的非关键路径：`PublishViewDataChanged` 的错误应记录但不作为动作的主错误返回；真正的数据事实永远在你的 action 返回值里。

## 校验规则速查

`biu app validate` / 安装路径 / 打包共用的规则，最常踩的条目：

| 字段 | 规则 |
|---|---|
| `identifier` | 非空；`^[a-z][a-z0-9._-]*(/[a-z][a-z0-9-]*)?$`（marketplace 需 `<author>/<slug>` 形式，由上架路径检查） |
| `version` | 语义化版本 `x.y.z`，可带预发布后缀 |
| `description` | 非空，≤ 200 字符 |
| `category` | 六选一：productivity / content / data / comm / dev / utility |
| `kind` | 五选一，`container` 当前会被安装路径拒绝 |
| `icon` | 空 / emoji（≤ 8 字节）/ `http(s)://` URL / `cas:<64 位小写 hex>` |
| `permissions[]` | 前缀必须在平台白名单内（见上文表格） |
| `actions[].name` | `^[a-z][a-z0-9_-]*$`，应用内唯一 |
| `actions[].timeout_ms` | 0–600000 |
| `views[].route` | 必须以 `/apps/<自己的 identifier>` 开头 |
| `views[]` 引用的 action | toolbar / item_template / data_source / submit / 卡片引用的 action 必须都在 `actions[]` 声明 |
| form 视图 | `schema_ref` 与 `submit` 至少有其一 |
| webview 视图 | 必须有 `url` |
| agent_chat 视图 | 必须有 `agent_id` |
| dashboard 视图 | 至少一张卡；卡 `span` 1–12；`kind` ∈ text/number/list/chart |
| grid 视图 | 必须有 `item_template`；列数 1–6 |
| cron 表达式 | 标准 5 字段；`* * * * *`（每分钟）被拒绝，最小间隔 1 分钟 |
| webhook 触发器 | `path` 以 `/` 开头；`auth` ∈ hmac / none |
| `sidebar.badge_action` | 必须是已声明的 action |
| `sidebar.badge_refresh` | ≥ 60 秒 |
| `skills[].identifier` / `file` | 非空；identifier 应用内唯一 |
| `requires[].kind` | app / mcp_server；`min_version` 需为 semver |

---

- 上一级：[开发者](index.md)
- 认证与 API 通用说明：[API 参考](api.md)
- 给 Agent 写可复用技能：[Skills 开发](skills.md)
- CLI 安装与配置：[CLI 指南](../cli/getting-started.md)
