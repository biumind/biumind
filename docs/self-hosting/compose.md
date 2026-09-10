# 本地开发栈（compose）

`deploy/docker-compose/` 这套 compose 栈的定位是**开发 / 测试环境的启动示例（dev/test only）**：让贡献者一条命令拉起完整环境，或者只拉基础设施、把业务服务留在宿主机上热重载。

> [!NOTE]
> 与[部署指南](index.md)的关系：部署指南面向自托管运维，讲用预构建镜像起完整栈并长期运行；本文面向本地开发——最常见的用法是 `make up-infra` 只起 Postgres / MinIO / NATS，Go 服务在 IDE 里 `go run`，前端按需本地构建。两边用的是同一份 `docker-compose.yml` 和 `.env`，变量说明见[环境变量参考](env-vars.md)。

> [!WARNING]
> 本目录的 compose 文件按开发 / 测试便利性取舍（默认端口全部对外暴露、`.env.example` 携带开发密钥）。把它直接当生产方案使用前，请先按[部署指南](index.md)完成密钥替换、HTTPS 终结与端口收敛——生产环境的部署、加固与运维由使用者自行负责。

---

## 快速开始

```bash
cd deploy/docker-compose
cp .env.example .env        # ⚠️ 把 *_change_me 占位符全部换成强随机值
make up-infra               # 只起基础设施（Postgres / MinIO / NATS）—— 本地开发最常用
# 或
make up                     # 完整栈（infra + 全部 Go 服务 + 前端 + workers）
```

`make up` / `make up-infra` 会自动执行 `make wait-healthy`（最多 90 秒等所有 healthcheck 通过）。裸 `docker compose up -d` 与 `make up` 等价。

---

## 全部 make target

`.DEFAULT_GOAL` 是 `help`，直接敲 `make` 可看速查表。完整清单：

### 启动

| target | 作用 |
|--------|------|
| `make up-infra` | 只拉基础设施（postgres / minio / nats / minio-bootstrap）——本地开发：服务在宿主跑 |
| `make up` | 拉起完整栈（infra + 全部 Go 服务 + 前端 + workers），随后自动 `wait-healthy` 并打印 site 入口地址 |
| `make up-workers` | 只拉 workers（worker-aigc / worker-wiki-parse；依赖的 infra 由 `depends_on` 自动带起） |
| `make up-all` | 别名：完整栈（= `up`） |

### 停止与清理

| target | 作用 |
|--------|------|
| `make down` | 停容器（保留数据 volume） |
| `make clean` | ⚠️ 停容器并删除**所有** volume（数据全清，交互式要求输入 `yes` 二次确认） |
| `make restart` | `down` + `up` |
| `make fresh` | 一键重建（`clean` + `up-infra`，数据全清后重起 infra） |

### 状态与日志

| target | 作用 |
|--------|------|
| `make ps` | 列容器及健康状态 |
| `make logs` | 跟所有服务日志（`--tail=100`，CTRL-C 退出；可加 `SVC=<name>`） |
| `make tail SVC=<name>` | 跟单个服务日志（如 `make tail SVC=model-relay`，`SVC` 必填） |
| `make health` | 逐个 `curl` 7001/7002/7003/7004/7007/7008/7009 端口的 `/healthz` |
| `make wait-healthy` | 等所有 healthcheck 通过（最多 90 秒，超时打印 `ps` 并退出非零） |

### 镜像

| target | 作用 |
|--------|------|
| `make build-images` | 本地构建所有 compose 用到的 Go 服务镜像（`docker.io/biumind/<name>:dev`），配合 `up-local` |
| `make up-local` | 用本地 `build-images` 产物起栈（等价 `BIUMIND_REGISTRY=docker.io BIUMIND_TAG=dev make up`） |
| `make pull-images` | 拉外部依赖镜像（`postgres` / `minio` / `nats`） |
| `make authz-eval` | 测一条 authz 决策：`make authz-eval PRINCIPAL=user:u1 ACTION=wiki:Page::read RESOURCE=page:p1`（依赖 `jq`） |

> [!NOTE]
> `make build-images` 逐个构建 9 个 Go 服务（aigc / app-center / authz / brain / channels / identity / model-relay / realtime / runtime），build context 一律是**仓库根** `../..`——所有服务 Dockerfile 都要 `COPY packages/go-sdk/biu`（brain / runtime 还 COPY `apps/cli/biu`），窄 context 会 COPY 失败。注意 `app_center` 目录名与 `app-center` 镜像名不一致，Makefile 已处理。

### 数据库

| target | 作用 |
|--------|------|
| `make psql` | 进 Postgres shell（用 `.env` 里的 `POSTGRES_USER` / `POSTGRES_DB`） |
| `make backup-pg` | `pg_dump`（自定义格式）备份到 `./backups/` |
| `make restore-pg FILE=backups/xxx.dump` | 从备份恢复（`pg_restore --clean --if-exists`，`FILE` 必填） |

### 实用工具与校验

| target | 作用 |
|--------|------|
| `make nats-streams` | 列出 NATS JetStream |
| `make minio-console` | 打开 MinIO 控制台（默认 `http://localhost:9001`） |
| `make lint` | `docker compose config -q` 校验 compose 文件语法 |
| `make check-env` | 守门员（被 `up*` 依赖）：检查 `.env` 存在且 `BIUMIND_MASTER_KEY` 已设置 |

Makefile 会自动 `include .env` 并 `export`，所以单独跑 `make psql` 之类的命令也能拿到 `.env` 里的变量。

---

## 启动范围与 profile

默认（裸 `docker compose up -d` / `make up`）启动：4 个基础设施容器 + 9 个 Go 服务 + 4 个前端 + 2 个 Python worker。两个服务挂在可选 profile 下，默认**不**启动：

| profile | 服务 | 启动命令 | 说明 |
|---------|------|----------|------|
| `ocr` | `mineru` | `docker compose --profile ocr up -d mineru` | 自部署 OCR（PDF 解析）。官方无预构建镜像，需自行构建后以 `MINERU_IMAGE` 指定；启用还需把 `.env` 的 `WIKI_PARSE_OCR_ENABLED` 置 `true` |
| `llm` | `worker-wiki-llm` | `docker compose --profile llm up -d worker-wiki-llm` | wiki 摄入链路的 LLM 生成段，栈内唯一主动消耗 API 费用的 worker，故默认关闭 |

其余子集直接点名服务即可，例如 `docker compose up -d postgres minio nats`。`sandbox` / `deploy` 服务不在 compose 内（依赖 K8s / 独立环境）。

---

## 本地开发模式（最常用）

只跑基础设施，业务服务在宿主机上跑（改代码即生效）：

```bash
make up-infra
```

服务全部走 12-factor 环境变量加载配置（无 config 文件）。在宿主机跑某个服务时，把 compose 里 `x-svc-env` 那组 env 换成 `localhost` 地址导出，例如跑 model-relay：

```bash
cd services/model-relay
export BIUMIND_ENV=test
export DATABASE_URL='postgres://biumind:<密码>@localhost:5432/biumind?sslmode=disable'
export NATS_URL='nats://localhost:4222'
export S3_ENDPOINT='http://localhost:9000'
export S3_ACCESS_KEY=biumind S3_SECRET_KEY='<MinIO密码>' S3_BUCKET=biumind
export JWT_SECRET='<同 .env>' JWT_ISSUER='https://identity.biumind.local' JWT_AUDIENCE='biumind-api'
export IDENTITY_JWKS_URL='http://localhost:7004/.well-known/jwks.json'
export LISTEN_ADDR=':7001' SERVICE_NAME=model-relay
go run ./cmd/model-relay
```

> [!NOTE]
> 上例数据库名 `biumind` 与 `.env.example` 的 `POSTGRES_DB=biumind` 一致；compose 文件与 Makefile 里该变量未设置时的兜底值是 `biu_core`。只要 `.env` 在，以 `.env` 为准，详见[环境变量参考](env-vars.md)。

前端三个 SPA 与 site 的本地构建 / 联调属于仓库开发流程（`task` 任务见仓库根 `Taskfile.yml`），compose 侧对应的是 `make build-images` + `make up-local` 用本地代码构建的镜像起完整栈验证。

---

## 常见问题

**Q：`make up` 报 unhealthy / `wait-healthy` 超时**
A：先 `make logs SVC=postgres`；多数情况是初始化 SQL 报错（看 `postgres/init/*.sql`），或某服务等不到依赖的健康状态——顺着 `make ps` 里 unhealthy 的容器查日志。

**Q：改了 `postgres/init/*.sql` 没生效**
A：Postgres 只在数据卷为空时执行 init 脚本。`make clean && make up-infra`（或 `make fresh`）重建。

**Q：`identity` 容器崩溃循环，日志 `permission denied: jwt-signing-key.pem`**
A：identity 以非 root 用户运行，`identity-keys` volume 若是用旧镜像创建的（root 属主）会写不进密钥。删卷重建：

```bash
docker compose down identity
docker volume rm biumind_identity-keys
make up
```

**Q：想接真实 LLM provider 测试**
A：部署后进管理后台（`http://localhost:8088/admin/`）「模型配置」填上游凭证（信封加密落库）。`.env` 里的 `BIUMIND_ANTHROPIC_KEY` 只是 runtime 的兜底直连通道。

**Q：websearch 工具不可用**
A：compose 不带搜索实例。自行部署一个 SearxNG 后，在 `.env` 设 `SEARXNG_URL=http://<host>:8080` 并重建 brain 容器即启用。

**Q：macOS 上文件句柄报错**
A：`ulimit -n 65536`，或在 Docker Desktop 设置里调高。

---

## 下一步

- 用预构建镜像做自托管部署：[部署指南](index.md)
- 全部 `.env` 变量说明：[环境变量参考](env-vars.md)
- compose 栈的设计取舍：仓库 `deploy/docker-compose/DESIGN.md`
