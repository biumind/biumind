<div align="center">

# BiuMind — 你的 AI 工作台

**写文档 · 跑 Agent · 写代码 · 做笔记 —— 把分散的 AI 工具收进同一个工作台，数据通在一起。**

**简体中文** · [English](./README.en.md)

云端注册即用 · 一行命令自托管 · 桌面 / 手机 / 浏览器 / 命令行一个账号随时切换

[快速开始](#快速开始) · [架构](#架构概览) · [官网](https://biumind.ai) · [贡献指南](./CONTRIBUTING.md)

</div>

---

## BiuMind 是什么？

BiuMind 是一个一体化 AI 工作平台：写文档、跑 Agent、写代码、做笔记这些原本分散在多个工具里的工作，被收进同一个工作台，并且**数据互通**——对话、剪藏、文档、创作产物都沉淀进同一个知识库，由 AI 连成知识图谱，供所有场景复用。

部署双轨：

- **云端（注册即用）**：几秒开账号，自带每月免费额度，适合个人和小团队。
- **自托管**：一行命令拉起全栈，数据全程留在自己的服务器，适合企业和数据敏感场景。

## 核心功能

六大模块，覆盖一天的全部 AI 工作：

| 模块 | 能力 |
|------|------|
| 🧠 **知识中枢** | 块编辑器 + 知识图谱 + AI 记忆；文档、对话、网页剪藏沉淀到同一个地方，语义搜索随时找回 |
| 💻 **编码工作台** | 多个 AI 工程师并行写代码、跑测试、提 PR；改文件 / 跑命令前先征求你的确认；桌面 / 手机 / CLI 切换，任务云端续跑 |
| 🎨 **创作** | 文生图、文生视频、爆款拆解；产物自动沉淀进知识库，可复用、二次创作 |
| ☁️ **云端工位** | 会话和记忆存云端，换设备上下文不丢 |
| 📮 **消息接入** | AI 接入飞书 / Telegram / Slack / Discord / 邮件，自动回复 + 知识库召回 |
| 📦 **应用中心** | RSS 订阅、邮件总结、股票动态、论文追踪等开箱即用的专业 AI 助手 |

## 多端覆盖

一个账号，四端随时切换，**CLI 与 GUI 共享同一内核、会话互通**：

- **桌面端（主力工作端）**：Flutter 单代码库，覆盖 macOS / Windows / Linux
- **移动端（伴随端）**：iOS / Android，随时随地审批 Agent 工作、看会话、查知识库；另有 uni-app 小程序（微信 / 支付宝 / 抖音 / 百度 / QQ / 快手 / 京东 / 飞书 / H5 共 9 端）
- **浏览器**：网页剪藏扩展（Chrome / Edge），看到的一切一键沉淀进知识库
- **命令行**：`biu` CLI，终端内对话、跑 Agent、操作知识库

## 架构概览

BiuMind 是一个 monorepo，前后端与共享组件全部在一个仓库内：

- **后端**：11 个 Go 微服务，HTTP + JWT 服务间通信，NATS JetStream 做异步消息，Postgres（pgvector + ltree）做统一数据层，每服务独立 schema + goose migration。
- **Agent 协议**：客户端 / CLI 与 Runtime 之间走 WebSocket 双向流的 **SDK Protocol**——JSON wire format + JSON Schema（`schema/sdk/v1/`），Go 手写 struct / Dart json_serializable / TS Zod 三端实现。服务端业务 API 另由 buf 管理的 proto3 定义（`packages/proto/`）。
- **模型网关**：`model-relay` 是所有模型调用的唯一出口，支持 BYOK（自带 Key）与平台池，计费口径统一。
- **异步任务**：Python workers 从 NATS 拉任务（文档解析、AIGC 生成、wiki 摄入、风控）。

### 后端服务（`services/`）

| 服务 | 职责 |
|------|------|
| `identity` | 账号与认证：注册 / 登录 / 多端 token、额度与 BYOK 管理 |
| `authz` | 鉴权策略引擎（policy + cache） |
| `realtime` | 实时推送：WebSocket hub + NATS fanout |
| `model-relay` | 模型网关：唯一出口、BYOK + 平台池、统一计费 |
| `runtime` | Agent 运行时引擎（与 `biu` CLI 共享内核） |
| `brain` | 知识层：Wiki / Graph / Memory / Search |
| `app_center` | 应用中心：BiuApp 注册与运行（内置 RSS / 翻译 / 任务等参考应用） |
| `aigc` | AIGC 接入层：文生图 / 视频 / 数字人（执行委托给 worker） |
| `channels` | 多渠道分发：Telegram 等 driver，按环境开关 |
| `sandbox` | 云沙箱：K8s Pod-per-sandbox，driver 可插拔 |
| `deploy` | 一键部署：JWT-gated deploy API + 静态托管 |

> `billing` 与 `presence` 服务在路线图中，尚未创建（见 `go.work` 注释）。

## 仓库结构

```
biumind/
├── apps/                 # 前端产品
│   ├── client/           #   Flutter 多端主端（桌面 / 移动 / Web）
│   ├── cli/biu/          #   biu Go CLI（与服务端共享内核）
│   ├── webclip/          #   浏览器剪藏扩展（Chrome / Edge MV3）
│   └── miniapp/          #   uni-app 跨端小程序（9 端）
├── web/site/             # 官网 biumind.ai（Astro + Tailwind，中英双语）
├── admin/                # 实例管理后台（Vue 3 + Element Plus）
├── services/             # 后端 Go 服务（见上表，11 个）
├── workers/              # Python workers：ingest / aigc / wiki-llm / wiki-parse / risk-control
├── packages/             # 跨产品共享：proto（buf）/ go-sdk / skills-stdlib
├── schema/               # SDK Protocol JSON Schema（sdk/v1）+ 发布 manifest（release/v1）
├── sdks/                 # 公共集成 SDK（go / python / node，Apache-2.0）
├── extensions/vscode/    # VS Code 扩展
├── deploy/docker-compose/# 本地全栈：PG / Redis / MinIO / NATS + 服务 + workers
└── tools/                # bootstrap / schema 校验 / 架构不变量检查等脚本
```

## 快速开始

需要：Go 1.26+、Flutter (stable)、buf、uv (Python)、Docker。

```bash
task bootstrap            # 一次性安装依赖（buf / goimports / dart / uv ...）

# 启动本地全栈（PG / Redis / MinIO / NATS + 各服务）
cd deploy/docker-compose
cp .env.example .env      # ⚠️ 把 *_change_me 占位符全部换成强随机值
make up-infra && make health   # 只起基础设施；make up / up-all 起完整栈

# 跑模型网关 + CLI
task model-relay:run
task cli:install          # biu → ~/.local/bin/biu
biu                       # 进入 REPL
```

说明：`deploy/docker-compose` 是 dev/test 本地栈示例，非生产部署方案；`sandbox` 与 `deploy` 服务依赖 K8s / S3，不在本地栈内。

## 开发

```bash
task proto:generate       # buf 生成 Go / Dart / TS
task test                 # 全量测试（Go + Dart + Python）
task lint                 # lint:go + lint:dart + lint:proto
task lint:invariants      # 架构不变量校验（CI 强制）
task --list               # 完整任务列表
```

贡献指引见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## 安全

发现漏洞请按 [SECURITY.md](./SECURITY.md) 私下上报，**勿开公开 issue**。
自托管前务必轮换 `deploy/docker-compose/.env.example` 里的占位密钥。

## License

BiuMind 采用**分层许可**：

- **平台 / 产品**（`apps/`、`admin/`、`services/`、`workers/`、`packages/`、`deploy/`、`schema/`）：**[BiuMind Community License](./LICENSE)** —— source-available，允许个人 / 研究 / 教育 / **企业内部**使用（含内部商用），但**禁止把 BiuMind 作为公开 SaaS / 托管服务提供给第三方**、禁止商业分发衍生品。商业使用联系 `geekidentity@163.com`。
- **SDK + 扩展**（`sdks/`、`extensions/`）：**Apache-2.0** —— 鼓励嵌入集成。

自带（vendored）的第三方代码归属见各自目录的 LICENSE 文件（miniflux、webclip readability·turndown、rss_icons）。
