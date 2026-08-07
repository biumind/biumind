# BiuMind Docker Compose（开发 / 测试示例）

> 单机本地 / CI / 内部测试环境用。**本目录的 compose 文件仅为开发/测试环境的启动示例（dev/test only）——生产环境的部署、加固与运维由使用者自行负责**，仓库不提交也不维护生产部署方案。
>
> 设计目标：`make up` 一条命令拉起完整测试栈；`make up-infra` 只拉基础设施（用于本地开发服务）。

---

## 快速开始

```bash
cd deploy/docker-compose
cp .env.example .env
make up-infra      # 只起基础设施（Postgres / MinIO / NATS）
# 或
make up            # 起基础设施 + 所有 BiuMind 服务（需要镜像已 build）
# 或
make up-all        # 上面 + Python workers（完整测试栈）
```

健康检查：

```bash
make ps            # 列所有容器状态
make health        # 调每个服务的 /healthz
make logs SVC=model-relay  # 跟某个服务的日志
```

清理：

```bash
make down          # 停容器，保留 volume（数据保留）
make clean         # 停容器 + 删 volume（⚠️ 数据全清）
```

---

## Profile 说明

通过 docker compose `--profile` 控制启停范围：

| Profile | 包含 | 用途 |
|---------|------|------|
| `infra` (默认) | postgres / minio(+bootstrap) / nats | 本地开发：服务在宿主机跑，依赖在容器 |
| `services` | model-relay / runtime / brain / identity / presence / billing / channels | 服务也在容器跑（CI / demo） |
| `workers` | python ingest / wiki-parse / aigc | 文档摄入 / AIGC 任务（向量化、视觉描述、图抽取已内建于 brain） |
| `all` | 上面全部 | 完整测试栈 |

例：

```bash
docker compose --profile infra --profile workers up -d
```

---

## 文件布局

```
deploy/docker-compose/
├── README.md                          ← 怎么用（命令 / 端口 / FAQ）
├── DESIGN.md                          ← 为什么这么搭（设计 rationale；compose 只留结构 + 1 行陷阱指针回指本文）
├── Makefile                           ← 便捷命令
├── .env.example                       ← 配置模板
├── docker-compose.yml                 ← 单文件全栈：infra + services + web + workers，profile 控制启停
├── postgres/
│   ├── Dockerfile                     ← pgvector + zhparser
│   └── init/                          ← 启动时执行的 SQL
│       ├── 00-extensions.sql
│       ├── 01-schemas.sql
│       └── 02-roles.sql
├── nats/
│   └── nats.conf                      ← JetStream 配置
```

---

## 端口映射（默认值，可在 `.env` 改）

| 服务 | 容器端口 | 暴露端口 | 备注 |
|------|---------|----------|------|
| site（统一入口 nginx） | 80 | **8088** | 客户端入口；`http://localhost:8088`（`/v1/*` 反代各后端） |
| Postgres | 5432 | 5432 | `psql -h localhost -U biumind` |
| MinIO | 9000 / 9001 | 9000 / 9001 | API / Console |
| NATS | 4222 / 8222 | 4222 / 8222 | client / monitor |
| model-relay | 7001 | 7001 | 仅 `services` profile |
| Runtime | 7002 | 7002 | 同上 |
| Brain | 7003 | 7003 | 同上 |
| Identity | 7004 | 7004 | 同上 |
| Presence | 7005 | 7005 | 同上 |
| Realtime | 7008 | 7008 | 同上；客户端唯一 SSE 入口 |
| Authz | 7009 | 7009 | 同上；服务间调用，admin UI 也用 |

---

## 本地开发模式（最常用）

只跑基础设施，业务服务在 IDE 里热重载：

```bash
make up-infra

# 在另一个 terminal 跑某个服务
cd services/model-relay
go run ./cmd/model-relay --config=../../deploy/docker-compose/configs/model-relay.dev.yaml
```

宿主机跑的服务自行 export 连接串（host 侧用 localhost 端口，如 `DATABASE_URL=postgres://biumind:<密码>@localhost:5432/biu_core?sslmode=disable`；compose 内的服务由 compose 自动注入，不用管）。

---

## 镜像构建（services profile 用）

```bash
# 构建所有服务镜像（dev tag）
make build-images

# 单独某个
docker build -t biumind/model-relay:dev -f services/model-relay/Dockerfile services/model-relay
```

CI（`.github/workflows/release-images.yml`）发布**全部业务镜像**——10 个 Go 服务
（含 channels/sandbox/deploy）+ 4 个前端（site/web-client/admin-web/miniapp-h5）
+ 4 个 worker（worker-ingest/worker-wiki-parse/aigc-worker/worker-wiki-llm）。
镜像名与本目录 compose 的 `image:` 字段一一对应；namespace 统一 `biumind`。
CI 推送三套仓库（namespace 均 `biumind`）：GHCR `ghcr.io/biumind/<name>`（必推）、
Docker Hub `docker.io/biumind/<name>`、Aliyun 北京 `registry.cn-beijing.aliyuncs.com/biumind/<name>`
（后两者需配 vars/secrets；本仓库已配）。tag：`:sha-<short7>` + `:main`（每次 main push），
release tag 时额外 `:v*` + `:latest`。

**纯拉取部署（不本地构建）**：compose 业务镜像 = `${BIUMIND_REGISTRY}/biumind/<name>:${BIUMIND_TAG}`，
namespace 固定 `biumind`，只换 host。默认 Aliyun 北京 + `:main`，三选一：

| 来源 | `.env` 设置 |
|------|------------|
| Aliyun 北京（默认，国内快） | `BIUMIND_TAG=main`（host 不设即可） |
| GitHub GHCR | `BIUMIND_REGISTRY=ghcr.io` + `BIUMIND_TAG=main` |
| Docker Hub | `BIUMIND_REGISTRY=docker.io` + `BIUMIND_TAG=main` |

设好后 `docker compose --profile all pull` 再 `make up`。tag 也可填 release 版本号
（如 `BIUMIND_TAG=v0.3.0`）或固定 commit（`sha-<short7>`）。GHCR package 需 public 才能匿名拉取。

---

## 数据持久化

所有 volume 命名为 `biumind_<service>_data`（见 `docker-compose.yml` 的 volumes 段）。

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
A: 部署后进管理后台「模型配置」填 key（envelope 加密落库 `model_relay.credentials`）；dev 兜底也可在 `.env` 设 `BIUMIND_ANTHROPIC_KEY`（brain/runtime 直连用）。

**Q: 想搞懂某段配置为什么这么写（端口冲突 / init container / 三套 bearer secret / 单 origin 路由）**
A: 看 `DESIGN.md`，compose 文件里只留 1 行陷阱指针回指它。

---

## 下一步

要把测试环境用满，建议读：

- `docs/BiuMind-Technical-Architecture.md` §2 运行时拓扑、§16 部署与发布
- `services/<name>/README.md`（每个服务的本地启动）
- `Makefile`（看有哪些 target）
