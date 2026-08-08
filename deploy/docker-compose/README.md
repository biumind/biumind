# BiuMind Docker Compose（开发 / 测试示例）

> 单机本地 / CI / 内部测试环境用。**本目录的 compose 文件仅为开发/测试环境的启动示例（dev/test only）——生产环境的部署、加固与运维由使用者自行负责**，仓库不提交也不维护生产部署方案。
>
> 设计目标：`make up` 一条命令拉起完整测试栈；`make up-infra` 只拉基础设施（用于本地开发服务）。

---

## 快速开始

```bash
cd deploy/docker-compose
cp .env.example .env
docker compose up -d   # 裸命令即起完整栈（infra + 服务 + 前端 + workers）
# 或走 Makefile：
make up-infra      # 只起基础设施（Postgres / MinIO / NATS）—— 本地开发
make up            # 完整栈（= 裸 docker compose up -d）
```

健康检查：

```bash
make ps            # 列所有容器状态
make health        # 调每个服务的 /healthz
make tail SVC=model-relay  # 跟某个服务的日志
```

清理：

```bash
make down          # 停容器，保留 volume（数据保留）
make clean         # 停容器 + 删 volume（⚠️ 数据全清，二次确认）
```

---

## 启动范围

无 `profiles`——裸 `docker compose up -d` 默认起**完整栈**（infra + 9 Go 服务 + 4 前端 + 3 worker）。要子集就点名服务：

| 想起什么 | 命令 |
|---------|------|
| 完整栈 | `docker compose up -d`（= `make up`） |
| 只基础设施（本地开发，服务在宿主跑） | `docker compose up -d postgres minio nats minio-bootstrap`（= `make up-infra`） |
| 只 workers（依赖 infra 自动带起） | `docker compose up -d worker-ingest worker-aigc worker-wiki-parse`（= `make up-workers`） |

> `sandbox` / `deploy` / `presence` / `billing` 服务**不在 compose**（依赖 K8s / 外部 SaaS）。`sandbox` / `deploy` 有 Dockerfile 且 CI 会发布镜像（见下「镜像构建」），但无单机编排。

---

## 文件布局

```
deploy/docker-compose/
├── README.md                          ← 怎么用（命令 / 端口 / FAQ，本文件）
├── DESIGN.md                          ← 为什么这么搭（设计 rationale；compose 只留结构 + 1 行陷阱指针回指本文）
├── Makefile                           ← 便捷命令
├── .env.example                       ← 配置模板
├── docker-compose.yml                 ← 单文件全栈：infra + services + web + workers，裸 up 默认全起
├── authz/policies/                    ← Cedar 授权策略（挂载进 authz 容器）
├── postgres/
│   ├── Dockerfile                     ← pgvector + zhparser（当前 Debian Trixie 编译坏，待修；QUICKSTART 用 stock 镜像）
│   └── init/                          ← 启动时执行的 SQL（仅空卷时跑）
│       ├── 00-extensions.sql
│       ├── 01-schemas.sql
│       └── 02-roles.sql
└── nats/
    └── nats.conf                      ← JetStream 配置
```

---

## 端口映射（默认值，可在 `.env` 改）

| 服务 | 容器端口 | 暴露端口 | 备注 |
|------|---------|----------|------|
| **site（统一入口 nginx）** | 80 | **8088** | 客户端唯一入口；`http://localhost:8088`（`/v1/*` 反代各后端，`/app` `/admin` `/m` 反代 SPA） |
| Postgres | 5432 | 5432 | `psql -h localhost -U biumind` |
| MinIO | 9000 / 9001 | 9000 / 9001 | API / Console |
| NATS | 4222 / 8222 | 4222 / 8222 | client / monitor |
| model-relay | 7001 | 7001 | LLM 网关 |
| Runtime | 7002 | 7002 | Agent 引擎 |
| Brain | 7003 | 7003 | 知识库 |
| Identity | 7004 | 7004 | 认证 / 计费 |
| Channels | 7007 | 7007 | 多渠道 IM 网关（需 bot token） |
| Realtime | 7008 | 7008 | SSE 实时推送（客户端唯一 SSE 入口） |
| Authz | 7009 | 7009 | Cedar 统一授权 |
| App Center | 7011 | 7011 | 应用中心 |
| AIGC | 7012 | 7012 | 文生图 / 视频 / 数字人 |

> `web-client` / `admin-web` / `miniapp-h5` **不绑 host 端口**，只经 site nginx 暴露。

---

## 本地开发模式（最常用）

只跑基础设施，业务服务在 IDE 里热重载：

```bash
make up-infra
```

服务用 12-factor 环境变量加载配置（`packages/go-sdk/biu/config`，纯 env，无 config 文件）。在宿主机跑某服务时，把 compose 里 `x-svc-env` 那组 env 用 `localhost` 端口导出：

```bash
cd services/model-relay
export BIUMIND_ENV=test
export DATABASE_URL='postgres://biumind:<密码>@localhost:5432/biu_core?sslmode=disable'
export NATS_URL='nats://localhost:4222'
export S3_ENDPOINT='http://localhost:9000'
export S3_ACCESS_KEY=biumind S3_SECRET_KEY='<minio密码>' S3_BUCKET=biumind
export JWT_SECRET='<同 .env>' JWT_ISSUER='https://identity.biumind.local' JWT_AUDIENCE='biumind-api'
export IDENTITY_JWKS_URL='http://localhost:7004/.well-known/jwks.json'
export LISTEN_ADDR=':7001' SERVICE_NAME=model-relay
go run ./cmd/model-relay
```

（容器内的服务由 compose 自动注入这些 env，宿主侧跑才需手动 export。）

---

## 镜像构建（services 用）

```bash
# 构建所有 compose 用到的 Go 服务镜像（dev tag）
make build-images

# 单独某个 —— ⚠️ context 必须是仓库根 ../..，否则 COPY packages/go-sdk/biu 失败
docker build -t biumind/model-relay:dev -f ../../services/model-relay/Dockerfile ../..
```

CI（`.github/workflows/release-images.yml`）发布**全部 19 个业务镜像**，与 compose 的 `image:` 字段一一对应；namespace 统一 `biumind`：

- **11 个 Go 服务**：identity / model-relay / brain / runtime / realtime / app-center / aigc / authz / channels / sandbox / deploy
- **4 个前端**：site / web-client / admin-web / miniapp-h5
- **4 个 worker**：worker-ingest / worker-wiki-parse / aigc-worker / worker-wiki-llm

> compose 实际只编排其中 **16 个**（9 Go 服务 + 4 前端 + 3 worker）：`sandbox` / `deploy` / `worker-wiki-llm` 有镜像但不在 compose，按需自行取用。

CI 推送三套仓库（namespace 均 `biumind`）：GHCR `ghcr.io/biumind/<name>`（必推）、Docker Hub `docker.io/biumind/<name>`、Aliyun 北京 `registry.cn-beijing.aliyuncs.com/biumind/<name>`（后两者需配 vars/secrets）。tag：`:sha-<short7>` + `:main`（每次 main push），release tag 时额外 `:v*` + `:latest`。

**纯拉取部署（不本地构建）**：compose 业务镜像 = `${BIUMIND_REGISTRY}/biumind/<name>:${BIUMIND_TAG}`，namespace 固定 `biumind`，只换 host。默认 Aliyun 北京 + `:main`，三选一：

| 来源 | `.env` 设置 |
|------|------------|
| Aliyun 北京（默认，国内快） | 不设 `BIUMIND_REGISTRY`，`BIUMIND_TAG=main` |
| GitHub GHCR | `BIUMIND_REGISTRY=ghcr.io`，`BIUMIND_TAG=main` |
| Docker Hub | `BIUMIND_REGISTRY=docker.io`，`BIUMIND_TAG=main` |

设好后 `docker compose pull` 再 `make up`。tag 也可填 release 版本号（如 `BIUMIND_TAG=v0.3.0`）或固定 commit（`sha-<short7>`）。GHCR package 需 public 才能匿名拉取。

---

## 数据持久化

命名 volume（project 名 `biumind`，docker 自动加前缀）：

| volume | 内容 |
|--------|------|
| `biumind_postgres-data` | Postgres 数据 |
| `biumind_minio-data` | MinIO 对象 |
| `biumind_nats-data` | NATS JetStream |
| `biumind_identity-keys` | Identity RSA 签名密钥（首启生成 0600，复用保 token 跨重启有效） |
| `biumind_app-center-data` | App Center 内置 tasks App 文件（JSON fsync 单文件，刻意不进 DB） |

```bash
docker volume ls | grep biumind   # 查看
make backup-pg                     # pg_dump → ./backups/
make restore-pg FILE=...           # 从备份恢复
```

---

## 常见问题

**Q: `make up` 报 unhealthy**
A: 先 `make logs SVC=postgres`；多数情况下是初始化 SQL 报错（看 `postgres/init/*.sql`）。

**Q: 改了 `init/*.sql` 没生效**
A: Postgres 只在数据卷为空时跑 init 脚本。`make clean && make up-infra` 重建。

**Q: websearch 工具不可用 / brain 的 `/v1/search` 没有 web scope**
A: compose 已不带 SearxNG。自行部署一个实例后，在 `.env` 设 `SEARXNG_URL=http://<host>:8080` 并重建 brain 容器即启用（不设则 brain 正常运行，只是 catalog 不含 websearch）。

**Q: macOS 上 file_max 报错**
A: `ulimit -n 65536` 或 Docker Desktop 设置里调高。

**Q: 想接入真实 LLM provider 测试**
A: 部署后进管理后台「模型配置」填 key（信封加密落库 `model_relay.credentials`）；dev 兜底也可在 `.env` 设 `BIUMIND_ANTHROPIC_KEY`（brain/runtime 直连用）。

**Q: `identity` 容器崩溃循环，日志 `permission denied: jwt-signing-key.pem`**
A: identity 跑 nonroot（UID 65532），镜像构建时已预建 `/var/lib/biumind/identity` 并 chown。若 `identity-keys` volume 是用旧镜像创建的（root 属主），删卷重建：`docker compose down identity && docker volume rm biumind_identity-keys && make up`。

**Q: 想搞懂某段配置为什么这么写（端口冲突 / 三套 bearer secret / 单 origin 路由 / volume 属主）**
A: 看 `DESIGN.md`，compose 文件里只留 1 行陷阱指针回指它。

---

## 下一步

要把测试环境用满，建议读：

- [`docs/BiuMind-Self-Hosted-Deployment.md`](../../docs/BiuMind-Self-Hosted-Deployment.md) — 自托管部署完整指南
- [`docs/BiuMind-Technical-Architecture.md`](../../docs/BiuMind-Technical-Architecture.md) §2 运行时拓扑、§16 部署与发布
- `services/<name>/README.md`（每个服务的本地启动）
- `Makefile`（看有哪些 target）
