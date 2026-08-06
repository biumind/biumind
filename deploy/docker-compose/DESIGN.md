# BiuMind Docker Compose 部署设计

> compose 文件里的「为什么」都收在这。compose 本身只留结构 + 一行陷阱指针回指本文。
> 「怎么用」（命令 / 端口 / FAQ）见 `README.md`。

---

## 1. 文件分层与 include 机制

入口 `docker-compose.yml`（base + infra）末尾 `include:` 拉起 services / web / workers 三个子文件。observability **不在默认 include**——按需 `-f docker-compose.observability.yml`（见 §12）。

`include` ≠ `-f`：

- `include` 把子文件合并进同一 project，单条 `docker compose` 命令自动看到全部 service。profile 控制启停范围。
- `-f` 是命令行显式叠加，用于 override（prod / test）和可选栈（observability）。

base 是服务定义的**单一事实源**（image / 端口 / depends_on / healthcheck 都在 base）。override 只叠加环境差异（env_file / init / logging / 端口收放），不重定义服务。

---

## 2. Profile 三轴

每个 service 标 `profiles:`：

| Profile | 内容 | 典型用法 |
|---------|------|----------|
| `infra` | postgres / redis / minio(+bootstrap) / nats / searxng | 本地开发：服务在宿主 IDE 跑，依赖在容器 |
| `services` | 9 Go 服务 + envoy + 前端 4 个（site/web-client/admin-web/miniapp-h5） | CI / demo 全栈 |
| `workers` | 6 Python worker（ingest/embed/vision/extract/aigc/wiki-parse） | 文档摄入流水线 |
| `observability` | otel-collector → tempo/loki/prom + grafana + exporter | 调试 / 排障 |
| `all` | 上面全部 | 完整测试栈 |

Makefile 包了 4 档：`up-infra` / `up` / `up-workers` / `up-all`。

---

## 3. 服务依赖链（boot order）

`depends_on: condition: service_healthy` 串成链：

```
postgres / redis / nats  (healthcheck 过)
    └─→ minio-bootstrap  (one-shot 建 8 bucket, service_completed_successfully)
          └─→ authz / identity / realtime / model-relay / brain / runtime / aigc
                ├─→ identity-init  (chown volume → identity, 见 §9)
                ├─→ brain depends_on model-relay + searxng
                │     └─→ runtime depends_on brain
                │           └─→ app-center depends_on identity+realtime+authz
                │                 └─→ channels depends_on runtime
                └─→ model-relay depends_on identity + authz
```

`minio-bootstrap` 是关键 one-shot job（`restart: "no"`），用 `minio/mc` 建全 8 个 bucket + 配 ILM 过期规则，跑完即退。worker / aigc / brain-files 都 `depends_on minio-bootstrap: service_completed_successfully`。

---

## 4. env 三层注入优先级

按 Compose Specification：**inline `environment:` > `env_file:` > shell export**。

实际链：

1. **base 内联**（`x-svc-env` / `x-worker-env` anchor 复用）——服务间通信用 docker hostname（`postgres:5432` / `redis:6379` / `nats:4222` / `minio:9000` / `identity:7004` …）。`${VAR:-default}` 形式从 `.env` 插值。
2. **`.env` 文件**（compose 同目录自动读）——密钥 / 端口 / 可调参。
3. **shell export**（Jenkins `deploy-local.sh`）——`BIUMIND_REGISTRY` / `BIUMIND_TAG` 覆盖镜像源，优先级最高。

`check-env` Makefile target 守门：`.env` 缺失或没 `BIUMIND_MASTER_KEY=` 直接 exit 1。

**为什么不用 `env_file` 替代内联？** Compose `env_file` **不支持 `${VAR}` 插值**。大半 env（JWT_SECRET / 密码 / registry）靠 `.env` 插值，被迫留 inline。迁移收益 < 风险，故保留 inline + anchor DRY。

---

## 5. 三套 service bearer secret（命名易混，必读）

| 变量 | 谁发 | 谁验 | 保护什么 |
|------|------|------|----------|
| `IDENTITY_INTERNAL_TOKEN` | identity `/v1/internal/*` | model-relay / aigc / brain / worker-aigc | 调 identity 内部端点（BYOK 查询 / 计费查找 / internalapi） |
| `MODEL_RELAY_INTERNAL_TOKEN` | model-relay `/v1/internal/embeddings` | brain embedder | embedding egress 平台池（bge-m3，I6 不直连 openai） |
| `BIUMIND_INTERNAL_TOKEN` | brain `/v1/internal/wiki/*` | wiki-parse worker | wiki-parse worker 拉 blob-presign / 回写 parse-result |

dev 全是 `biumind-dev-*-change-me`，prod 必换。同名变量在多个服务出现时**必须同值**（如 `IDENTITY_INTERNAL_TOKEN` 在 identity / model-relay / aigc 都读）。

---

## 6. 镜像源两变量

| 变量 | 作用 | dev | prod / 内网 |
|------|------|-----|-------------|
| `INFRA_REGISTRY` | 基础设施镜像前缀（postgres/redis/minio/nats/…） | `docker.io` | `docker.io`（build-host 拉不动 docker.io） |
| `BIUMIND_REGISTRY` | 业务镜像前缀（9 Go 服务 + worker + 前端） | `biumind`（本地 build tag） | `registry.your-domain.com/biumind`（ACR） |

`BIUMIND_TAG` 控制版本（dev `:dev`，CI `:sha8`）。

---

## 7. site nginx 单 origin 路由

site（Astro 静态官网 + nginx 网关）是**客户端唯一入口**。client（桌面 + web）只配一个地址：

- 本地 `http://localhost:8088`
- prod `https://…`

site nginx 路径分发：

- `/` —— 静态官网
- `/v1/*` —— 反代各后端（identity 7004 / model-relay 7001 / brain 7003 / …）
- `/app` —— 反代 web-client（Flutter Web）
- `/admin` —— 反代 admin-web（Vue）
- `/m` —— 反代 miniapp-h5（uni-app）
- `/releases.json` —— 反代 releases（dev MinIO `/releases` bucket，prod CNAME OSS，避阿里云 ApkDownloadForbidden）

**新增后端接口必须同步在 `web/site/nginx.conf` 加 location，否则功能静默断。** 详见 memory `client_single_origin`。

web-client / admin-web / miniapp-h5 **不绑 host port**，只经 site nginx 暴露，全在 `biu-net` 内通信。

---

## 8. 部署模式

| 模式 | 入口 | compose 组合 | 镜像源 |
|------|------|--------------|--------|
| **本地开发** | `make up-infra` | base | docker.io + 本地 build |
| **Jenkins 测试机** | `deploy/jenkins/scripts/deploy-local.sh` | base + test.override | ACR + sha8 tag |

仓库不提交生产环境内容——生产部署不在本栈范围。

---

## 9. identity-init：为何要 chown volume

docker daemon 初始化新 named volume 默认 `root:root 0755`。identity 镜像按安全策略跑 **nonroot（UID 65532）**，首启写 RSA 签名密钥（`/var/lib/biumind/identity/jwt-signing-key.pem`）会 `permission denied` → crash-loop（Jenkins build-host runner 实测复现）。

解法：one-shot `identity-init`（alpine, `user: root`）先 `chown -R 65532:65532` + `chmod 0700`，跑完即退（`restart: "no"`）。identity `depends_on identity-init: service_completed_successfully`。后续 boot chown 是 no-op（已 65532），代价可忽略。

RSA 密钥首启自动生成（4096-bit，0600），后续 boot 复用——保证签发的 token 跨重启有效。

---

## 10. JWKS 验签 + HS256 fallback

所有服务读 `IDENTITY_JWKS_URL=http://identity:7004/.well-known/jwks.json` 拉 Identity 的 RS256 公钥验 token。

- Identity 设 `JWT_SIGNING_KEY_FILE` → 该端点服务真 RS256 公钥。
- 端点 404（未配）→ verifier 静默退 HS256 + `JWT_SECRET`（dev escape hatch）。

生产必须开 RS256。

---

## 11. AIGC egress 单一出口（不变量 I6）

`AIGC_GENERATE_VIA_RELAY=true` 让 aigc-worker 走 model-relay `/v1/internal/generations`，凭证由 relay vault 解密，**不直连 Python provider**（DashScope / VolcEngine）。

`DASHSCOPE_API_KEY` / `VOLCENGINE_ARK_API_KEY` 仅作 env fallback；P4.S1.3 起 envelope 解密的 NATS payload 凭证优先。

同理 brain embedding 走 model-relay `/v1/internal/embeddings`（§5 `MODEL_RELAY_INTERNAL_TOKEN`），绝不直连 `api.openai.com`。

---

## 12. observability 为何移出默认 include

base `include:` 原本含 observability，导致 `docker compose config` 输出带 8 个可观测 service，主栈结构噪声大。

移出后：

- 默认 `docker compose …` 只看 infra + services + web + workers。
- 用可观测栈时显式 `-f docker-compose.observability.yml`。Makefile 用 `COMPOSE_OBS` 变量封装（`up-obs` / `up-all` / `down` / `clean` / `restart` / `pull-images` 已路由）。

profile gate 仍生效（`--profile observability` 或 `--profile all` 控启停）。

---

## 13. override 文件机制

test override 用 `-f` 叠在 base 之后：

| 文件 | 场景 | 关键改动 |
|------|------|----------|
| `docker-compose.test.override.yml` | Jenkins build-host | 所有 host port `!reset []`（NPM 是唯一公网入口，走 docker 内网） |

`!reset []` 是 Compose Spec 2.20+ 语法——把 `ports` 字段**重置**为空（不是 merge）。单纯写 `ports: []` 在 override 里被解释成合并，无效。

---

## 14. 数据持久化与 postgres init

命名 volume（`biumind` project 前缀）：

- `postgres-data` / `redis-data` / `minio-data` / `nats-data` / `searxng-data` / `k3s-data`
- `identity-keys` —— RSA 签名密钥（§9）
- `app-center-data` —— 内置 tasks App 文件持久化（JSON fsync 单文件，刻意不进 DB）

postgres `init/*.sql` **仅在数据卷为空时跑**（改了 SQL 不生效 → `make clean && make up-infra` 重建）：

- `00-extensions.sql` —— uuid-ossp / pgcrypto / **vector**(pgvector) / pg_trgm / ltree / citext / btree_gin/gist + zhparser（§15）
- `01-schemas.sql` —— 9 schema（identity / hub / brain / runtime / sandbox / presence / billing / audit / deploy）
- `02-roles.sql` —— default privilege 授权 superuser `biumind`

---

## 15. zhparser 中文分词 fallback

QUICKSTART 用 stock `pgvector/pgvector:pg16`（无 zhparser）。`00-extensions.sql` 检测 `zhparser` 是否可装：

- 可装 → 建 `biumind_zhcn` text search config（PARSER = zhparser）。
- 不可装 → `biumind_zhcn` alias 到内置 `simple`（按空格分词，中文混排不理想，但 SQL 不破坏）。

要真中文 FTS 用 `postgres/Dockerfile`（编译 zhparser）。**当前该 Dockerfile 在 Debian Trixie 上编译坏，待修**——base compose 暂用 stock 镜像。

---

## 16. minio-bootstrap：8 bucket + ILM

one-shot 建：

- `biumind`（主）/ `biumind-snapshots` / `biumind-deploy`（anonymous download）
- `biumind-aigc-uploads` / `-outputs` / `-derivatives` / `-public`（anonymous）/ `-temp`
- `releases`（anonymous download，客户端版本下发）

ILM 过期：`aigc-uploads` / `aigc-derivatives` 30 天，`aigc-temp` 1 天。

---

## 17. NATS JetStream durable consumer

brain / runtime / channels 设 `BUS_USE_JETSTREAM: "true"`。Stream `BIUMIND_BRAIN` / `BIUMIND_CHANNELS` boot 时 auto-ensure（幂等，无 boot-order 耦合）。

durable consumer 让**服务重启不丢消息**：

- runtime 绑 `brain-graph-extractor`（graph 自动抽取）
- runtime inbound channel envelope（Telegram / Slack 不再因重启丢）
- channels publish inbound envelopes via JetStream

无中央 subject 命名约定，自律用 `<service>.<entity>.<event>`（如 `biumind.test.brain.ingest.requested`）。

NATS 测试环境无认证（生产用 NKey + accounts）。`max_payload: 8MB`。

---

## 18. 前端容器最小权限

web.yml 4 个前端容器（site / web-client / admin-web / miniapp-h5）统一：

- `read_only: true` + `tmpfs`（nginx cache 64M / run 8M / tmp 16M）
- `cap_drop: ALL` + `cap_add` 仅 CHOWN/SETUID/SETGID/NET_BIND_SERVICE
- `security_opt: no-new-privileges:true`

envoy 容器跳过默认 ENTRYPOINT（`entrypoint: ["envoy"]`）——默认 `/docker-entrypoint.sh` 启动时 chown stdout/stderr，podman rootless 无权限会 crash-loop。envoy 写 stdout 不需要 own 它。

---

## 19. 端口冲突史

design 原锁部分端口，后因冲突调整（客户端 `_appCenterPort` / `aigcUri` 同步改）：

- **app-center**：design 锁 7008，realtime 已占 → 改 **7011**。
- **aigc**：app-center 占 7011 → 改 **7012**。
- **site**：**8088**（host）→ 容器 80（nginx）。

业务服务端口固定：identity 7004 / model-relay 7001 / brain 7003 / runtime 7002 / realtime 7008 / authz 7009 / channels 7007。

---

## 20. 易踩雷

1. **README 说 infra 含 envoy，实际 compose envoy 是 `profile: services`**——`up-infra` 不起 envoy。
2. **改 postgres init SQL 不生效**——只空卷时跑，`make clean && make up-infra` 重建。
3. **build context 必须 = 仓库根**——`docker build -f services/<x>/Dockerfile ../..`，窄 context COPY `packages/go-sdk/biu` 失败。
4. **app_center 目录 → app-center 镜像**——唯一名不一致，`build-images` 特判。
5. **presence / billing / channels / sandbox 无 Dockerfile**——deploy-local.sh 跳过，要起需先补。
6. **compose service 增删必须同步 `deploy-local.sh` 的 INFRA/SERVICES/WEB/WORKERS 数组**——否则 CI 部署漏起。
7. **podman rootless 坑**：docker.io 不可达 → 设 `INFRA_REGISTRY`；并发 blob copy 死锁 → `image_parallel_copies=1`；裸名 → `BIUMIND_REGISTRY=localhost/biumind`。
8. **3 套 bearer secret 别混**——见 §5。
9. **macOS file_max 报错**——`ulimit -n 65536` 或 Docker Desktop 调高。
10. **observability 已移出默认 include**——用 obs 栈走 `make up-obs` / `up-all`（内部已 `-f observability`）。
