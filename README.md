<div align="center">

# BiuMind Agentics

**An all-in-one, highly-extensible AI work platform — Knowledge Base + Agent Runtime + Coding Collaboration + App Center + Cloud Sandbox.**

一体化 AI 工作平台 SaaS：知识库 + Agent 运行时 + 编码协作 + 应用中心 + 云沙箱。

Self-hostable + cloud SaaS · Flutter multi-platform · Go-first backend

<!-- 截图 / Screenshots —— TODO: 补产品截图或 demo GIF -->
<!-- <img src="docs/assets/screenshot.png" width="800" alt="BiuMind screenshot"> -->

[License](#license) · [Quick Start](#快速开始-quick-start) · [Docs](#文档地图-docs-map) · [Contributing](./CONTRIBUTING.md)

</div>

---

## 这是什么 / What it is

BiuMind 把"知识库 / Agent 运行时 / 编码协作 / 应用中心 / 云沙箱"做成一个一体化平台，可**自托管**也可跑**云端 SaaS**。核心能力：

- **🧠 Brain（知识层）**：Wiki 文档 + Graph 图谱 + Memory 记忆 + Search 检索（Postgres + pgvector + ltree 邻接表，不用 Neo4j）
- **🤖 Runtime（Agent 引擎）**：WebSocket **SDK Protocol**（proto3 over WS，28 message variant）双向流；本地 / 云端 CLI backend（Claude Code / Codex）
- **🔌 model-relay（模型网关）**：单一 egress，BYOK + 平台池，计费单一 SoT（`model_relay.pricing`）
- **💻 编码协作**：`biu` CLI（与服务端共享内核 `biumindkit`），VS Code 扩展
- **📦 App Center**：应用中心（Skills + Apps 一等公民并行）
- **🎨 AIGC / Channels**：文生图/视频/语音产物 + 多渠道分发（IM / 邮件 / Webhook）
- **🏝️ Cloud Sandbox**：K8s Pod-per-sandbox（gVisor 可选）
- **🌐 多端**：Flutter（macOS/Windows/Linux/iOS/Android/Web）+ uni-app 跨平台小程序（9 端）+ 浏览器扩展

## Tech stack

| 层 | 技术 |
|----|------|
| 客户端（多端单代码） | Flutter + Riverpod |
| CLI（与服务端共享内核） | Go（`apps/cli/biu`） |
| 浏览器扩展 | TypeScript |
| 跨端小程序（9 端） | uni-app + Vue 3 + TS |
| 后端服务（11 个） | Go（HTTP + JWT 通信，非 gRPC） |
| 异步 worker | Python（ingest / embed / vision / AST） |
| Proto schema | buf 管理（`packages/proto`） |
| 数据层 | Postgres + pgvector + ltree（每服务独立 schema + goose migration） |
| 消息 | NATS JetStream |

完整技术架构见 [`docs/BiuMind-Technical-Architecture.md`](./docs/BiuMind-Technical-Architecture.md)。

## 仓库结构 / Repo layout

```
biumind/
├── apps/                # 前端产品
│   ├── client/          #   Flutter 多端主端
│   ├── cli/biu/         #   biu Go CLI
│   ├── webclip/         #   浏览器扩展
│   └── miniapp/         #   uni-app 跨平台小程序（9 端）
├── admin/               # 实例管理后台（Vue 3 + Element Plus：用户/RBAC/模型/系统配置/用量/审计）
├── services/            # 后端 Go 服务（identity/authz/realtime/model-relay/runtime/brain/sandbox/deploy/channels/billing/presence）
├── workers/             # Python 工作进程（ingest/embed/vision/extract，NATS 拉任务）
├── packages/            # 跨产品共享（proto / go-sdk / dart sdk）
├── sdks/                # 公共集成 SDK（go / python / node）
├── extensions/vscode/   # VS Code 扩展
├── deploy/docker-compose/  # 自托管 / 本地全栈（postgres/redis/minio/nats + 11 服务）
├── tools/               # bootstrap / migration / lint 脚本
└── docs/                # 全部权威设计文档（70+ 篇）
```

## 快速开始 / Quick start

需要：Go 1.26+、Flutter (stable)、buf、uv (Python)、Docker。

```bash
task bootstrap            # 一次性装依赖（buf / goimports / dart / uv ...）

# 起本地全栈（PG / Redis / MinIO / NATS + 11 服务）
cd deploy/docker-compose
cp .env.example .env      # ⚠️ 把 *_change_me 占位符全换成强随机值
make up-infra && make health

# 跑模型网关 + CLI
task model-relay:run
task cli:install          # biu → ~/.local/bin/biu
biu                       # REPL
```

自托管部署详见 [`docs/BiuMind-Self-Hosted-Deployment.md`](./docs/BiuMind-Self-Hosted-Deployment.md)。

## 开发 / Development

```bash
task proto:generate       # buf 生成 Go / Dart / TS
task test                 # 全量（Go + Dart + Python）
task lint                 # lint:go + lint:dart + lint:proto
task lint:invariants      # 架构不变量校验（CI 强制）
task --list               # 完整任务列表
```

贡献指引见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## 文档地图 / Docs map

| 文档 | 作用 | 何时读 |
|------|------|--------|
| [`docs/BiuMind-Product-Plan.md`](./docs/BiuMind-Product-Plan.md) | 做什么 / 模块边界 / 路线图 | 新人第一份 |
| [`docs/BiuMind-Technical-Architecture.md`](./docs/BiuMind-Technical-Architecture.md) | 后端怎么做 / 协议 / DB schema | 后端工程师 |
| [`docs/BiuMind-Client-Architecture.md`](./docs/BiuMind-Client-Architecture.md) | 端怎么做 / 跨平台 / 离线 | 前端 / 移动工程师 |
| [`docs/BiuMind-Development-Plan.md`](./docs/BiuMind-Development-Plan.md) §11 | 实时执行进度 | 想知道"现在该做什么" |
| [`docs/research/`](./docs/research/) | 参考开源项目调研（We Take / We Skip） | 决策时回查 |

## 安全 / Security

发现漏洞请按 [SECURITY.md](./SECURITY.md) 私下上报，**勿开公开 issue**。
自托管前务必轮换 `deploy/docker-compose/.env.example` 里的占位密钥。

## License

BiuMind 采用**分层许可**：

- **平台 / 产品**（`apps/`、`admin/`、`services/`、`workers/`、`packages/`、`deploy/`、`schema/`）：**[BiuMind Community License](./LICENSE)** —— source-available，允许个人 / 研究 / 教育 / **企业内部**使用（含内部商用），但**禁止把 BiuMind 作为公开 SaaS / 托管服务提供给第三方**、禁止商业分发衍生品。商业使用联系 `geekidentity@163.com`。
- **SDK + 扩展**（`sdks/`、`extensions/`）：**Apache-2.0** —— 鼓励嵌入集成。

自带（vendored）的第三方代码归属见各自目录的 LICENSE 文件（miniflux、webclip readability·turndown、rss_icons）。
