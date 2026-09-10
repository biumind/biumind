# 自托管部署

BiuMind 支持私有化部署：全部后端服务、前端与异步 worker 均以容器镜像交付，官方 CI 持续发布预构建镜像。自托管的基本路径是：**克隆仓库 → 配置 `.env` → 拉取镜像 → `docker compose` 拉起整套栈**。

> [!NOTE]
> 三篇文档的分工：
>
> - **本文**：面向自托管运维，讲如何用预构建镜像把整套 BiuMind 跑起来，以及网关路由、验证、备份与升级。
> - [本地开发栈 (compose)](compose.md)：面向贡献者 / 二次开发，讲只起基础设施、服务跑宿主机、本地构建镜像等用法。
> - [环境变量参考](env-vars.md)：`.env` 全量变量说明，含与 compose 文件的交叉核对结果。

---

## 前置要求

- **Docker Engine + Docker Compose v2**（`docker compose` 子命令形式；仓库不提供裸 `docker-compose` v1 适配）。
- **git 克隆本仓库**。compose 栈除了镜像，还从仓库 bind-mount 了几个运行必需的文件：Postgres 初始化 SQL（`deploy/docker-compose/postgres/init/`）、NATS 配置（`deploy/docker-compose/nats/nats.conf`）、Authz 授权策略（`deploy/docker-compose/authz/policies/`）、Runtime 内置 Skills（`packages/skills-stdlib/`）。因此即使纯拉镜像、不本地构建，也需要一份仓库检出。
- `make`、`curl`（Makefile 便捷命令依赖）；`jq`（仅 `make authz-eval` 需要）。
- 机器可访问所选镜像仓库（见下文[镜像源](#4-选择镜像源)）。
- 下表端口未被占用（均可在 `.env` 修改）：

| 服务 | 默认端口 | 说明 |
|------|----------|------|
| **site（统一入口）** | **8088** | 客户端唯一入口，`http://localhost:8088` |
| Postgres | 5432 | 数据库（pgvector 镜像） |
| MinIO | 9000 / 9001 | 对象存储 API / Console |
| NATS | 4222 / 8222 | 消息队列 client / monitor |
| model-relay | 7001 | 模型网关（所有模型调用的唯一出口） |
| runtime | 7002 | Agent 运行时引擎 |
| brain | 7003 | 知识层（Wiki / Graph / Memory / Search） |
| identity | 7004 | 账号认证、额度、BYOK |
| channels | 7007 | 多渠道 IM 网关（可选，需 bot token） |
| realtime | 7008 | 实时推送（SSE） |
| authz | 7009 | 统一授权策略引擎 |
| app-center | 7011 | 应用中心 |
| aigc | 7012 | 文生图 / 视频 / 数字人 |

> [!NOTE]
> `web-client` / `admin-web` / `miniapp-h5` 三个前端容器**不绑定宿主端口**，只经 site 网关按路径（`/app`、`/admin`、`/m`）暴露。`sandbox` 与 `deploy` 两个服务有 CI 镜像但**不在 compose 栈内**（分别依赖 K8s 与独立运行环境），按需自行取用。

---

## 架构一览

整套栈由四层组成（全部在同一个 `biu-net` docker 网络内，通过容器名互访）：

```text
客户端（桌面 / Web / 小程序 / CLI）
        │  单 origin：只配一个"服务器地址" = site 地址
        ▼
site (nginx, :8088)  ──  /v1/* 反代各后端；/app /admin /m 反代 SPA；静态官网 + /docs 文档
        │
        ├── identity (:7004)      账号认证 / 计费 / BYOK
        ├── authz (:7009)         Cedar 授权策略
        ├── model-relay (:7001)   模型网关（BYOK + 平台池 + 计费）
        ├── brain (:7003)         知识层
        ├── runtime (:7002)       Agent 运行时
        ├── realtime (:7008)      SSE 实时推送
        ├── app-center (:7011)    应用中心
        ├── channels (:7007)      多渠道 IM（可选）
        ├── aigc (:7012)          AIGC 接入层
        └── workers (Python)      异步任务：worker-aigc / worker-wiki-parse（+ 可选 worker-wiki-llm）

基础设施：Postgres 16 (pgvector) / MinIO / NATS JetStream 2.10
```

**单 origin 寻址**是 BiuMind 客户端的核心约束：客户端只配置一个服务器地址（本地默认 `http://localhost:8088`），所有 API 调用都打到 site，由其 nginx 按路径反代到对应后端。这也意味着部署完成后**只需对外暴露 site 一个端口**，其余服务端口都可以只绑内网或干脆不发布。

---

## 部署步骤

### 1. 克隆仓库并进入 compose 目录

```bash
git clone https://github.com/biumind/biumind.git
cd biumind/deploy/docker-compose
```

### 2. 生成配置文件

```bash
cp .env.example .env
```

### 3. 修改必改密钥

`.env` 里所有带 `change_me` / `dev` 字样的值都必须换成强随机值。至少包括：

| 变量 | 说明 | 生成方式 |
|------|------|----------|
| `BIUMIND_MASTER_KEY` | 服务加密主密钥（`make` 会强制检查，缺失直接拒绝启动） | `openssl rand -base64 32` |
| `JWT_SECRET` | JWT 签名密钥（compose 中无默认值，必须设置） | 32 字符以上随机串 |
| `POSTGRES_PASSWORD` | Postgres 密码（compose 有开发默认值，务必替换） | 强随机值 |
| `MINIO_ROOT_PASSWORD` | MinIO 密码（compose 有开发默认值，务必替换） | 强随机值 |
| `IDENTITY_INTERNAL_TOKEN` | 服务间内部调用共享 bearer（多服务必须同值，务必替换） | 强随机值 |
| `BYOK_MASTER_KEY` | BYOK 用户密钥加密主密钥；留空则 BYOK 功能禁用 | `openssl rand -base64 32` |

> [!WARNING]
> 密钥一旦投入使用不要随意轮换：`BIUMIND_MASTER_KEY` / `BYOK_MASTER_KEY` 轮换后旧密文无法解密；`identity-keys` 卷中还有首启自动生成的 RSA 签名密钥，删除该卷会使所有已签发 token 失效。

其余变量（模型偏好、渠道 token、OCR 等）按需配置，逐项说明见[环境变量参考](env-vars.md)。

### 4. 选择镜像源

业务镜像统一为 `${BIUMIND_REGISTRY}/biumind/<name>:${BIUMIND_TAG}`，namespace 固定 `biumind`，只换 host。三选一，写进 `.env`：

| 来源 | `.env` 设置 |
|------|-------------|
| Aliyun 北京（默认，国内快） | 不设 `BIUMIND_REGISTRY`，`BIUMIND_TAG=main` |
| GitHub GHCR | `BIUMIND_REGISTRY=ghcr.io`，`BIUMIND_TAG=main` |
| Docker Hub | `BIUMIND_REGISTRY=docker.io`，`BIUMIND_TAG=main` |

`BIUMIND_TAG` 可选值：`main`（每次 main 分支推送更新）、`v*`（release 版本号）、`sha-<短哈希>`（固定某次提交）。基础设施镜像（Postgres / MinIO / NATS）由 `INFRA_REGISTRY` 控制，默认 `docker.io`。

> [!NOTE]
> GHCR 上的 package 需为 public 才能匿名拉取。若拉取超时，可为基础设施镜像单独设置 `INFRA_REGISTRY` 指向可达的镜像源。

### 5. 拉镜像并启动

```bash
docker compose pull   # 先拉取，避免 compose 转入本地构建
make up               # = docker compose up -d + 等待 healthcheck 通过
```

`make up` 完成后会输出统一入口地址（默认 `http://localhost:8088`）。

### 6. 验证

见下文[健康验证](#健康验证)。

---

## 启动范围与 profile

compose 文件里绝大多数服务**不挂 profile**，裸 `docker compose up -d`（即 `make up`）默认起完整栈。只有两个服务挂在可选 profile 下：

| 目标 | 命令 |
|------|------|
| 完整栈（默认） | `make up` / `docker compose up -d` |
| 只起基础设施 | `make up-infra`（Postgres / MinIO / NATS + bucket 初始化） |
| 只起 workers | `make up-workers`（依赖的 infra 由 `depends_on` 自动带起） |
| 附加 MinerU OCR 服务（profile `ocr`） | `docker compose --profile ocr up -d mineru` |
| 附加 wiki-llm worker（profile `llm`） | `docker compose --profile llm up -d worker-wiki-llm` |
| 任意子集 | 点名服务，如 `docker compose up -d postgres minio nats` |

> [!WARNING]
> `llm` profile 的 `worker-wiki-llm` 是栈内唯一会主动消耗 LLM API 费用的 worker（把外部来源文本生成为 Wiki 页面），所以默认不启动，确认需要且已配置好模型后再显式启用。`ocr` profile 的 `mineru` 无官方预构建镜像，需自行构建后以 `MINERU_IMAGE` 指定（详见[环境变量参考](env-vars.md)）。

启动顺序由 `depends_on` + healthcheck 自动保证：所有 Go 服务等 Postgres / NATS 健康；model-relay 等 identity / authz 健康；brain 等 model-relay 健康；runtime 等 brain 健康；site 等三个 SPA 前端起来。`make up` 内部会调用 `make wait-healthy`（最多等待 90 秒）。

---

## 健康验证

每个服务容器都配置了 healthcheck（Go 服务为各自端口上的 `/healthz`，前端容器为 `http://localhost/healthz`）。常用验证手段：

```bash
make ps                 # 列出所有容器与健康状态
make health             # 逐个 curl 后端 /healthz（7001/7002/7003/7004/7007/7008/7009）
make tail SVC=model-relay   # 跟踪单个服务日志（make logs 跟全部）
curl http://localhost:8088/healthz   # site 网关健康检查
```

> [!NOTE]
> `make health` 目前只探测 7001–7009 这 7 个端口，**不含 app-center (7011)、aigc (7012) 和 site (8088)**。这三个用 `make ps` 看容器健康状态，或手动 `curl http://localhost:7011/healthz`、`curl http://localhost:7012/healthz` 验证。

浏览器访问验证：

| 地址 | 内容 |
|------|------|
| `http://localhost:8088/` | 官网静态站 |
| `http://localhost:8088/app/` | Web 版客户端（Flutter Web） |
| `http://localhost:8088/admin/` | 管理后台（Vue） |
| `http://localhost:8088/m/` | 小程序 H5 版 |
| `http://localhost:8088/docs/` | 在线文档 |

首次使用需在 Web 客户端注册账号；模型调用需在管理后台「模型配置」中填入上游 provider 凭证（信封加密落库，不走环境变量）。

---

## site 网关与 `/v1/*` 路由表

site 容器内的 nginx（`web/site/nginx.conf`）承担双重角色：静态官网 + API 网关。客户端单 origin 的全部 API 调用都由它按路径分发：

| 上游服务 | 路由前缀 |
|----------|----------|
| identity (:7004) | `/v1/auth/`、`/v1/identity/`、`/oauth/`、`/.well-known/oauth-authorization-server`、`/v1/admin/`（兜底）、`/v1/billing/webhook`、`/v1/plans`、`/v1/subscriptions`、`/v1/coupons`、`/v1/referrals`、`/v1/credits`、`/v1/announcements` |
| model-relay (:7001) | `/v1/messages`、`/v1/admin/providers`、`/v1/admin/credentials`、`/v1/admin/models`、`/v1/admin/channels`、`/v1/admin/pricing`、`/v1/admin/fx-rates`、`/v1/admin/model-groups`、`/v1/chat/estimate`、`/v1/me/usage`、`/v1/me/models`、`/v1/audio/` |
| brain (:7003) | `/v1/agent/`、`/v1/memory`、`/v1/threads`、`/v1/providers`、`/v1/mcp`、`/v1/wiki/`、`/v1/notes`、`/v1/notebooks`、`/v1/note-tags`、`/v1/shares`（公开分享端，有限流）、`/v1/graph/`、`/v1/search`、`/v1/chat/stats`、`/v1/chat/search`、`/v1/chat/tombstones`、`/v1/tools`、`/v1/files`、`/v1/brain/` |
| runtime (:7002) | `/v1/agents`、`/v1/skills` |
| realtime (:7008) | `/v1/realtime/`（SSE 长连接） |
| authz (:7009) | `/v1/authz/` |
| aigc (:7012) | `/v1/models`、`/v1/gallery`、`/v1/generations`、`/v1/characters`、`/v1/voices`、`/v1/aigc/` |
| app-center (:7011) | `/v1/apps`、`/v1/sidebar` |
| SPA 前端 | `/app/` → web-client、`/admin/` → admin-web、`/m/` → miniapp-h5 |
| 静态站 | `/`、`/_astro/*`、`/s/`（分享落地页）、`/docs/`（在线文档）、`/downloads/*`（客户端安装包） |

几个值得注意的网关行为：

- **`/v1/internal/*` 一律 `deny all`（403）**——服务间内部端点不暴露给公网，只能在 docker 网络内调用。
- **`/v1/admin/*` 按最长前缀分流**：7 个 model-relay 精确前缀优先，其余落到 identity。
- **WebSocket 与流式端点**（`/v1/wiki/` 同步 WS、`/v1/agent/` 会话 WS、`/v1/messages` 流式响应、`/v1/realtime/` SSE）关闭了代理缓冲并配置了 upgrade 头与长超时。
- **上传上限**：`/v1/files` 为 110 MB（对齐 brain 服务端 100 MB 上限 + multipart 开销），`/v1/messages` 与 `/v1/agent/` 为 20 MB（base64 内联图片）。
- **分享公开端限流**：`/v1/shares` 30 req/min（burst 20），unlock 端点 10 req/min（burst 5），超限返回 429。

> [!WARNING]
> 如果你在二次开发中给后端新增了 `/v1/...` 接口，必须同步在 `web/site/nginx.conf` 增加对应 `location` 反代，否则该请求会落到静态站返回 404——功能静默失效。自托管用户如遇某个客户端功能 404，优先排查网关路由。

### HTTPS

site 容器只监听明文 HTTP 80（对外映射为 8088）。**TLS 由前置层负责终结**：测试环境可用 frp 等隧道，生产环境建议前置公网负载均衡 / Nginx Proxy Manager / CDN，将 443 转发到 site 的 8088。生产环境还应收敛各后端服务端口的对外暴露（单 origin 架构下只需暴露 site）。

---

## 数据持久化与备份

compose 使用命名 volume（project 名 `biumind`，docker 自动加前缀）：

| volume | 内容 |
|--------|------|
| `biumind_postgres-data` | Postgres 全部业务数据（各服务独立 schema） |
| `biumind_minio-data` | MinIO 对象（原始文档 / 媒体 / 构建产物） |
| `biumind_nats-data` | NATS JetStream 持久化 |
| `biumind_identity-keys` | Identity RSA 签名密钥（首启生成，保 token 跨重启有效） |
| `biumind_app-center-data` | App Center 内置 tasks App 文件 |
| `biumind_mineru-models` | MinerU 模型缓存（仅 `ocr` profile） |

常用操作：

```bash
make backup-pg                    # pg_dump 备份到 ./backups/（自定义格式）
make restore-pg FILE=backups/xxx.dump   # 从备份恢复
make down                         # 停止全部容器（volume 保留，数据不动）
make clean                        # ⚠️ 停止并删除所有 volume，数据全清，有二次确认
```

> [!WARNING]
> `make clean` 会删除包括 Postgres 数据在内的全部 volume，且不可恢复。生产环境请先用 `make backup-pg` 留档。Postgres 的 `postgres/init/` 初始化 SQL 只在数据卷为空时执行，改了 init 脚本不会影响已有数据。

MinIO 的 bucket（主桶、snapshots、deploy、aigc 五个桶、releases 等）由一次性任务 `minio-bootstrap` 在首次启动时自动创建并配置匿名下载策略与生命周期规则，无需手工干预。

---

## 升级

```bash
cd deploy/docker-compose
# 1. 改 .env 里的 BIUMIND_TAG（如从 main 切到固定版本 v0.x.x，或保持 main 滚动跟进）
# 2. 拉新镜像并重建变化的容器
docker compose pull
docker compose up -d
```

数据库 schema 由各服务启动时自动执行 goose migration，无需手工操作。升级前建议 `make backup-pg`。回滚时把 `BIUMIND_TAG` 设回旧版本重新 `pull + up -d` 即可（schema 迁移通常只前进，跨大版本回滚前请先确认备份可恢复）。

---

## 下一步

- 逐项配置说明见[环境变量参考](env-vars.md)。
- 要在本地跑开发环境（服务跑宿主机热重载、本地构建镜像），见[本地开发栈 (compose)](compose.md)。
- 各服务内部细节见仓库 `services/<name>/`。
