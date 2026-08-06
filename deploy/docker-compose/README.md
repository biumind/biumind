# BiuMind Docker Compose（测试环境）

> 单机本地 / CI / 内部测试环境用。**不要用于生产**——仓库不提交生产环境内容。
>
> 设计目标：`make up` 一条命令拉起完整测试栈；`make up-infra` 只拉基础设施（用于本地开发服务）。

---

## 快速开始

```bash
cd deploy/docker-compose
cp .env.example .env
make up-infra      # 只起基础设施（Postgres / Redis / MinIO / NATS / SearxNG）
# 或
make up            # 起基础设施 + 所有 BiuMind 服务（需要镜像已 build）
# 或
make up-all        # 上面 + observability 栈（OTel / Tempo / Loki / Grafana）
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
| `infra` (默认) | postgres / redis / minio / nats / searxng / envoy | 本地开发：服务在宿主机跑，依赖在容器 |
| `services` | model-relay / runtime / brain / identity / presence / billing / channels | 服务也在容器跑（CI / demo） |
| `workers` | python ingest / embed / vision | 文档摄入流水线 |
| `observability` | otel-collector / tempo / loki / mimir / grafana | 可观测性 |
| `all` | 上面全部 | 完整测试栈 |

例：

```bash
docker compose --profile infra --profile observability up -d
```

---

## 文件布局

```
deploy/docker-compose/
├── README.md                          ← 怎么用（命令 / 端口 / FAQ）
├── DESIGN.md                          ← 为什么这么搭（设计 rationale；compose 只留结构 + 1 行陷阱指针回指本文）
├── Makefile                           ← 便捷命令
├── .env.example                       ← 配置模板
├── docker-compose.yml                 ← 基础设施（infra profile）+ include 拉起 services/web/workers
├── docker-compose.services.yml        ← BiuMind 服务（services profile）
├── docker-compose.workers.yml         ← Python workers（workers profile）
├── docker-compose.observability.yml   ← OTel + Grafana（observability profile；已移出 base include，`make up-obs` / `up-all` 显式 -f 加载）
├── docker-compose.test.override.yml   ← 测试 override（收 host 端口，NPM 唯一入口）
├── postgres/
│   ├── Dockerfile                     ← pgvector + zhparser
│   └── init/                          ← 启动时执行的 SQL
│       ├── 00-extensions.sql
│       ├── 01-schemas.sql
│       └── 02-roles.sql
├── envoy/
│   └── envoy.yaml                     ← API 网关配置（auth / ratelimit / SSE）
├── nats/
│   └── nats.conf                      ← JetStream 配置
├── searxng/
│   └── settings.yml                   ← Web 搜索（自托管）
├── otel/
│   ├── collector.yaml
│   ├── tempo.yaml
│   └── loki.yaml
└── grafana/
    └── provisioning/
        ├── datasources/datasources.yml
        └── dashboards/dashboards.yml
```

---

## 端口映射（默认值，可在 `.env` 改）

| 服务 | 容器端口 | 暴露端口 | 备注 |
|------|---------|----------|------|
| Envoy（API 网关） | 10000 | **8080** | 客户端入口；`http://localhost:8080` |
| Envoy admin | 9901 | 9901 | 调试 |
| Postgres | 5432 | 5432 | `psql -h localhost -U biumind` |
| Redis | 6379 | 6379 | `redis-cli -p 6379` |
| MinIO | 9000 / 9001 | 9000 / 9001 | API / Console |
| NATS | 4222 / 8222 | 4222 / 8222 | client / monitor |
| SearxNG | 8080 (内部) | 8888 | `http://localhost:8888` |
| model-relay | 7001 | 7001 | 仅 `services` profile |
| Runtime | 7002 | 7002 | 同上 |
| Brain | 7003 | 7003 | 同上 |
| Identity | 7004 | 7004 | 同上 |
| Presence | 7005 | 7005 | 同上 |
| Realtime | 7008 | 7008 | 同上；客户端唯一 SSE 入口 |
| Authz | 7009 | 7009 | 同上；服务间调用，admin UI 也用 |
| Grafana | 3000 | **3000** | observability profile |
| Tempo | 3200 | 3200 | trace 后端 |
| Loki | 3100 | 3100 | log 后端 |
| OTel Collector | 4317/4318 | 4317/4318 | gRPC / HTTP OTLP |

---

## 本地开发模式（最常用）

只跑基础设施，业务服务在 IDE 里热重载：

```bash
make up-infra

# 在另一个 terminal 跑某个服务
cd services/model-relay
go run ./cmd/model-relay --config=../../deploy/docker-compose/configs/model-relay.dev.yaml
```

`.env` 里的 `DATABASE_URL` / `REDIS_URL` 等本机直接可用。

---

## 镜像构建（services profile 用）

```bash
# 构建所有服务镜像（dev tag）
make build-images

# 单独某个
docker build -t biumind/model-relay:dev -f services/model-relay/Dockerfile services/model-relay
```

CI 自动推到 `ghcr.io/biumind/<svc>:<sha>`。

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

**Q: SearxNG 第一次启动 403**
A: 第一次启动会生成 `secret_key`，之后正常。或在 `.env` 里把 `SEARXNG_SECRET` 设为定值。

**Q: macOS 上 file_max 报错**
A: `ulimit -n 65536` 或 Docker Desktop 设置里调高。

**Q: 想接入真实 LLM provider 测试**
A: 在 `.env` 设 `BIUMIND_OPENAI_KEY` / `BIUMIND_ANTHROPIC_KEY`，model-relay 启动时进 platform pool。

**Q: observability 栈怎么起 / 为什么 `docker compose up` 不含它**
A: observability 已移出 base `include`（让主栈 `config` 更轻）。用 `make up-obs` 或 `make up-all`（内部已 `-f docker-compose.observability.yml`）。手动则 `docker compose -f docker-compose.yml -f docker-compose.observability.yml --profile observability up -d`。

**Q: 想搞懂某段配置为什么这么写（端口冲突 / init container / 三套 bearer secret / 单 origin 路由）**
A: 看 `DESIGN.md`，compose 文件里只留 1 行陷阱指针回指它。

---

## 下一步

要把测试环境用满，建议读：

- `docs/BiuMind-Technical-Architecture.md` §2 运行时拓扑、§16 部署与发布
- `services/<name>/README.md`（每个服务的本地启动）
- `Makefile`（看有哪些 target）
