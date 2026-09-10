# 环境变量参考

compose 栈的全部配置通过 `deploy/docker-compose/.env` 注入：`docker compose` 自动读取同目录 `.env`，Makefile 也会 `include .env` 并 `export`（所以单独跑 `make psql` 等命令同样生效）。首次使用先 `cp .env.example .env`。

本文按 `.env.example` 的注释分段**全量转录**每个变量，并逐条与 `docker-compose.yml` 交叉核对。列含义：

- **必填**：`必填` = compose 中无默认值（或 `make check-env` 强制检查），缺失无法启动；`建议必改` = compose 有开发默认值，但生产/自托管必须替换；`可选` = 不设也能正常跑。
- **默认**：compose 文件里 `${VAR:-默认}` 的兜底值；`—` 表示无兜底、直接取 `.env` 值。
- **引用服务**：compose 文件中实际引用该变量的 service（含公共锚点 `x-svc-env` / `x-worker-env` 展开后的全部服务）。

> [!NOTE]
> 一个变量在 `.env` 与 compose 默认值同时存在时，`.env` 优先。以下 "全部 Go 服务" 指 `x-svc-env` 锚点覆盖的 authz / realtime / identity / model-relay / brain / runtime / app-center / channels / aigc。

---

## 通用

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `COMPOSE_PROJECT_NAME` | 可选 | `biumind` | compose 项目名（决定容器 / volume 前缀）。compose 文件顶部已固定 `name: biumind`，通常无需设置 | 无（compose CLI 内置变量） |
| `BIUMIND_ENV` | 可选 | `test` | 运行环境标识，注入全部服务与 worker | 全部 Go 服务（x-svc-env）、全部 worker |
| `BIUMIND_LOG_LEVEL` | 可选 | `info`（example 给 `debug`） | 日志级别 | 全部 Go 服务、全部 worker |
| `TZ` | 可选 | `UTC`（example 给 `Asia/Shanghai`） | 容器时区 | 全部 Go 服务、postgres |

## 镜像源

业务镜像 = `${BIUMIND_REGISTRY}/biumind/<name>:${BIUMIND_TAG}`，namespace 固定 `biumind`，只换 host。

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `BIUMIND_REGISTRY` | 可选 | `registry.cn-beijing.aliyuncs.com` | 业务镜像仓库 host。可选：`ghcr.io`（GitHub GHCR）、`docker.io`（Docker Hub） | 全部业务镜像的 `image:` 字段（9 Go 服务 + site / web-client / admin-web / miniapp-h5 + 3 worker） |
| `BIUMIND_TAG` | 可选 | `main` | 镜像 tag。`main` 每次 main 推送更新；也可固定 `v*` 版本号或 `sha-<短哈希>` | 同上。本地构建走 `make build-images` + `make up-local`（tag `dev`），无需在此设置 |

## 端口

仅当本机端口冲突时才需要改。全部为 `${VAR:-默认}:容器端口` 形式的端口映射。

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `POSTGRES_PORT` | 可选 | `5432` | Postgres 对外端口（同时拼进各服务 DATABASE_URL） | postgres；全部连库服务的 DATABASE_URL |
| `MINIO_API_PORT` | 可选 | `9000` | MinIO API 端口 | minio |
| `MINIO_CONSOLE_PORT` | 可选 | `9001` | MinIO 控制台端口（`make minio-console` 也读它） | minio |
| `NATS_PORT` | 可选 | `4222` | NATS client 端口 | nats |
| `NATS_MONITOR_PORT` | 可选 | `8222` | NATS monitor 端口 | nats |
| `MODEL_RELAY_PORT` | 可选 | `7001` | model-relay 端口 | model-relay |
| `RUNTIME_PORT` | 可选 | `7002` | runtime 端口 | runtime |
| `BRAIN_PORT` | 可选 | `7003` | brain 端口 | brain |
| `IDENTITY_PORT` | 可选 | `7004` | identity 端口 | identity |
| `CHANNELS_PORT` | 可选 | `7007` | channels 端口 | channels |
| `REALTIME_PORT` | 可选 | `7008` | realtime 端口 | realtime |
| `AUTHZ_PORT` | 可选 | `7009` | authz 端口（`make authz-eval` 也读它） | authz |
| `AIGC_PORT` | 可选 | `7012` | aigc 端口 | aigc |

> [!NOTE]
> 还有两个端口映射变量 `.env.example` 没有列出：`SITE_PORT`（默认 `8088`，site 统一入口）与 `APP_CENTER_PORT`（默认 `7011`）。需要改这两个端口时同样在 `.env` 里设置即可，详见下文[未列入 .env.example 的变量](#compose-引用但-envexample-未列出的变量)。

## Postgres

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `POSTGRES_USER` | 可选 | `biumind` | 数据库用户名 | postgres；全部服务的 DATABASE_URL；`make psql` / `backup-pg` / `restore-pg` |
| `POSTGRES_PASSWORD` | 建议必改 | `biumind_dev_password_change_me` | 数据库密码 | 同上 |
| `POSTGRES_DB` | 可选 | example 给 `biumind`（compose / Makefile 兜底为 `biu_core`） | 数据库名 | 同上 |
| `POSTGRES_HOST` | 可选 | `postgres`（注释状态） | 改用外部 Postgres（RDS / 自建）时取消注释填地址 | 全部服务的 DATABASE_URL |
| `POSTGRES_SSLMODE` | 可选 | `disable`（注释状态） | 外部托管数据库常需 `require` / `verify-full` | 全部服务的 DATABASE_URL |

> [!NOTE]
> `POSTGRES_DB` 的 example 值与 compose / Makefile 兜底值不一致（`biumind` vs `biu_core`）。`.env` 存在时以 `.env` 为准，功能不受影响；只删掉该行才会落到 `biu_core`，两者不要混用。

## MinIO / 对象存储

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `MINIO_ROOT_USER` | 可选 | `biumind` | MinIO root 用户；同时作为各服务 S3 access key 的取值来源 | minio、minio-bootstrap；全部 Go 服务与 worker（`S3_ACCESS_KEY` / `MINIO_ACCESS_KEY` / `AIGC_S3_ACCESS_KEY` 均由它派生） |
| `MINIO_ROOT_PASSWORD` | 建议必改 | `biumind_minio_dev` | MinIO root 密码；同上作为各服务 S3 secret key 的取值来源 | 同上 |
| `MINIO_BUCKET` | 可选 | `biumind` | 主桶名（minio-bootstrap 自动创建，另有 `<bucket>-snapshots` / `<bucket>-deploy` 等衍生桶） | minio-bootstrap；全部 Go 服务与 worker（`S3_BUCKET`） |

`.env.example` 注释里还提到换外部 S3 / OSS 时可设 `S3_ENDPOINT` / `S3_ACCESS_KEY` / `S3_SECRET_KEY` / `S3_BUCKET`、`MINIO_ENDPOINT` / `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY` / `MINIO_USE_SSL`、`AIGC_S3_ENDPOINT` / `AIGC_S3_REGION` / `AIGC_S3_USE_SSL`——注意其中**只有 `S3_ENDPOINT`、`MINIO_ENDPOINT`、`MINIO_USE_SSL`、`AIGC_S3_*` 是 compose 直接读取的变量名**，凭证类（`S3_ACCESS_KEY` 等）compose 一律从 `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` 派生，直接设置不生效。接外部对象存储需要改 compose 文件，详见下文[核对发现的不一致](#核对发现的不一致)。

## 加密与签名

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `BIUMIND_MASTER_KEY` | **必填** | example 给开发值 | 服务级加密主密钥，base64 编码 32 字节（`openssl rand -base64 32`）。`make up*` 前置的 `make check-env` 强制检查，缺失直接拒绝启动 | model-relay |
| `JWT_SECRET` | **必填** | example 给开发值 | JWT HS256 签名密钥（compose 无兜底） | 全部 Go 服务（x-svc-env） |
| `JWT_ISSUER` | 可选 | `https://identity.biumind.local` | JWT issuer 声明 | 全部 Go 服务 |
| `JWT_AUDIENCE` | 可选 | `biumind-api` | JWT audience 声明 | 全部 Go 服务 |
| `BRAIN_SHARE_SIGNING_KEY` | 建议设置 | 空 | 笔记分享访问 JWT 的 HS256 签名密钥（base64 32 字节）。留空 = brain 启动时随机生成（单实例可用，重启后已签发的访客 token 全部失效）；**多实例部署必须显式配同一值** | brain |

## 会话生命周期

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `IDENTITY_ACCESS_TTL` | 可选 | `24h` | Access JWT 有效期（example 注释：dev 用 24h 避免频繁 refresh，生产建议 1h） | identity |
| `IDENTITY_REFRESH_TTL` | 可选 | `2160h`（90 天） | Refresh token 滑动有效期（每次 refresh 续到 now + sliding） | identity |
| `IDENTITY_REFRESH_ABSOLUTE_TTL` | 可选 | `8760h`（1 年） | Refresh token 绝对有效期（首次签发定死，rotation 不重置，防永久泄漏） | identity |
| `IDENTITY_REFRESH_REUSE_GRACE` | 可选 | `10s` | Refresh rotation 宽限窗口：rotate 后窗口内重放旧 token 可沿链找回，超窗判定复用于以整族撤销 | identity |

## 平台 LLM 池

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `BIUMIND_ANTHROPIC_KEY` | 可选 | 空 | runtime Agent Plane 直连 Anthropic 的兜底 key（compose 映射为 `AGENT_PLANE_ANTHROPIC_API_KEY`）。常规模型调用不走的通道，主用方式是管理后台「模型配置」落库凭证 | runtime |

## 搜索增强（可选）

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `SEARXNG_URL` | 可选 | 空（注释状态） | 自部署 SearxNG 实例地址，设置后 brain 启用 websearch 工具；不设则正常跑，仅 catalog 无 websearch | brain |
| `RERANK_PROVIDER` | 可选 | 空（注释状态） | 搜索 rerank provider。启用前提：管理后台在模型目录注册 `mode='rerank'` 的模型并配价格行，否则流量静默零扣费；不设保持 RRF 原序 | brain |
| `RERANK_MODEL` | 可选 | 空（注释状态） | rerank 模型名（如 `BAAI/bge-reranker-v2-m3`）；留空可经 model-relay 自动优选 | brain |

## Wiki OCR（MinerU 自部署，可选）

启用步骤：① 自行构建 MinerU 镜像（官方无预构建）② `docker compose --profile ocr up -d mineru` ③ `WIKI_PARSE_OCR_ENABLED=true`。启用后所有 PDF 全量走 MinerU，失败降级纯文本层。

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `WIKI_PARSE_OCR_ENABLED` | 可选 | `false`（注释状态） | OCR 总开关（compose 映射为 worker 的 `BIUMIND_WIKI_PARSE_OCR_ENABLED`） | worker-wiki-parse |
| `MINERU_API_BASE` | 可选 | `http://mineru:8000`（注释状态） | MinerU 服务地址（compose 映射为 `BIUMIND_MINERU_API_BASE`） | worker-wiki-parse |
| `MINERU_IMAGE` | 可选 | `mineru:latest`（注释状态） | MinerU 镜像名（自行构建后打 tag 或指向完整镜像引用） | mineru |
| `OCR_BILLING_MODEL` | 可选 | 空（注释状态） | OCR 计费 pseudo-model 名（按页计价，在管理后台注册模型 + 价格行）；空 = OCR 免费 | brain |

## Channels（可选）

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `TELEGRAM_BOT_TOKEN` | 可选 | 空 | Telegram bot token | channels |
| `DISCORD_BOT_TOKEN` | 可选 | 空 | Discord bot token | channels |
| `SLACK_BOT_TOKEN` | 可选 | 空 | Slack bot token | channels |
| `FEISHU_APP_ID` | 可选 | 空 | 飞书 App ID | channels |
| `FEISHU_APP_SECRET` | 可选 | 空 | 飞书 App Secret | channels |
| `BIUMIND_ANTHROPIC_BASE_URL` | 可选 | 空 | runtime Agent Plane 的 Anthropic 兼容 endpoint 覆盖（compose 映射为 `AGENT_PLANE_ANTHROPIC_ENDPOINT`） | runtime |

## AIGC（文生图 / 视频 / 数字人）

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `AIGC_PORT` | 可选 | `7012` | aigc 服务端口 | aigc |
| `DASHSCOPE_API_KEY` | 可选 | 空 | 阿里云百炼（DashScope）上游 key；不填 worker 拒收对应任务 | aigc、worker-aigc |
| `VOLCENGINE_ARK_API_KEY` | 可选 | 空 | 火山方舟（豆包 Seedream / Seedance）上游 key | aigc、worker-aigc |

## 计费与 BYOK

| 变量 | 必填 | 默认 / 示例 | 作用 | 引用服务 |
|------|------|-------------|------|----------|
| `IDENTITY_INTERNAL_TOKEN` | 建议必改 | `biumind-dev-internal-token-change-me` | 服务间内部调用（`/v1/internal/*`）共享 bearer。identity / model-relay / brain / runtime / app-center / aigc / worker 必须同值 | identity、model-relay、brain、runtime、app-center、aigc、worker-aigc、worker-wiki-llm |
| `BYOK_MASTER_KEY` | 建议设置 | 空 | BYOK（用户自带模型 key）加密主密钥，base64 32 字节。**留空 = BYOK 功能禁用**；严禁进 git；轮换后旧密文无法解密 | identity |

---

## compose 引用但 .env.example 未列出的变量

以下变量在 `docker-compose.yml` 中被引用，但 `.env.example` 没有列出（默认值基本都可用，需要覆盖时在 `.env` 里补写即可）。

### 镜像与入口

| 变量 | 默认 | 作用 | 引用服务 |
|------|------|------|----------|
| `INFRA_REGISTRY` | `docker.io` | 基础设施镜像（pgvector / minio / nats）的仓库 host | postgres、minio、minio-bootstrap、nats |
| `SITE_PORT` | `8088` | site 统一入口对外端口 | site |
| `RELEASES_UPSTREAM` | `http://minio:9000/releases` | 客户端 `releases.json` 反代上游（本地 MinIO / 云端 OSS） | site |

### 内部调用 token

| 变量 | 默认 | 作用 | 引用服务 |
|------|------|------|----------|
| `MODEL_RELAY_INTERNAL_TOKEN` | `biumind-dev-relay-internal-token-change-me` | model-relay 内部车道（embedding / rerank）共享密钥，brain 与 model-relay 同值 | model-relay、brain |
| `BIUMIND_INTERNAL_TOKEN` | `biumind-dev-internal-token-change-me` | brain 内部 Wiki 接口（`/v1/internal/wiki/*`）共享密钥，brain 与解析 worker 同值 | brain、worker-wiki-parse、worker-wiki-llm |
| `IDENTITY_JWKS_URL` | `http://identity:7004/.well-known/jwks.json` | JWKS 公钥拉取地址（RS256 验签） | 全部 Go 服务（x-svc-env） |

### 管理后台与超级管理员

| 变量 | 默认 | 作用 | 引用服务 |
|------|------|------|----------|
| `BIUMIND_BOOTSTRAP_SUPERADMINS` | 空 | 逗号分隔邮箱列表，identity 启动时自动提升为超级管理员（邮箱须已注册并验证，幂等；留空不提升） | identity |
| `MODEL_RELAY_SYNC_UPSTREAM_URL` | 空 | 管理后台「模型同步」的上游模型目录地址 | model-relay |
| `MODEL_RELAY_FX_SYNC_URL` | 空 | 每日汇率同步（USD↔CNY）数据源地址 | model-relay |
| `MODEL_RELAY_FX_SYNC_DISABLED` | `false` | 置 `1` 关闭汇率同步（离线环境） | model-relay |

### 模型偏好

| 变量 | 默认 | 作用 | 引用服务 |
|------|------|------|----------|
| `EMBED_PROVIDER` | `openai` | embedding provider | brain |
| `EMBED_MODEL` | 空（自动优选） | embedding 模型；空 = 启动时经 model-relay 自动优选 | brain |
| `EMBED_DIMS` | `1024` | embedding 向量维度 | brain |
| `PARSE_BILLING_MODEL` | 空 | 云端文档解析计费 pseudo-model（空 = 免费） | brain |
| `RUNTIME_DEFAULT_CHAT_MODEL` | 空（按链解析） | task 模式默认模型兜底（relay 默认 → 此处 → relay preferred → 报错） | runtime |
| `RADAR_LLM_MODEL` | 空（自动优选） | App Center radar 顾问模型（空 = relay 优选，再空则不接顾问） | app-center |
| `GITHUB_TOKEN` | 空 | App Center Repo Apps 的 GitHub 只读 token；空 = 匿名限流 60 req/h，启动时 WARN 降级 | app-center |
| `WIKI_LLM_HUB_URL` | `http://model-relay:7001` | wiki-llm worker 走的模型网关地址（profile `llm`） | worker-wiki-llm |
| `WIKI_LLM_MODEL` | 空（按链解析） | wiki 摄入生成模型显式覆盖（owner 偏好 → relay default → relay preferred；不要设默认值否则链不生效） | worker-wiki-llm |
| `WIKI_LLM_IDENTITY_URL` | `http://identity:7004` | wiki-llm worker 取用户 ingest 模型偏好的 identity 地址 | worker-wiki-llm |

### Runtime / Channels 运维参数

| 变量 | 默认 | 作用 | 引用服务 |
|------|------|------|----------|
| `RUNTIME_CHANNELS_DEFAULT_USER_ID` | 空 | 渠道消息默认归属用户（agent 环境为空时的兜底） | runtime |
| `RUNTIME_CHANNELS_DEFAULT_PROJECT_ID` | 空 | 渠道消息默认归属项目 | runtime |
| `AGENT_PLANE_ADMIN_USER_ID` | 空 | Agent Plane 自注册 environment 的管理员用户；缺则 agent 环境列表为空 | runtime |
| `MINERU_MODEL_SOURCE` | `local` | MinerU 模型来源 | mineru |

### 小程序 / OAuth 凭证（identity）

以下变量均默认空、按需配置，全部只被 **identity** 引用：

| 变量 | 用途 |
|------|------|
| `WECHAT_MP_APPID` / `WECHAT_MP_APPSECRET` | 微信小程序 |
| `WECHAT_WEB_APPID` / `WECHAT_WEB_APPSECRET` | 微信 Web（H5 OAuth） |
| `H5_FRONTEND_BASE_URL` | H5 OAuth 回跳前端地址 |
| `ALIPAY_MP_APPID` / `ALIPAY_MP_APPSECRET` / `ALIPAY_MP_PRIVATE_KEY` / `ALIPAY_MP_PUBLIC_KEY` | 支付宝小程序 |
| `TOUTIAO_MP_APPID` / `TOUTIAO_MP_APPSECRET` | 抖音小程序 |
| `BAIDU_MP_APPID` / `BAIDU_MP_APPSECRET` | 百度智能小程序 |
| `QQ_MP_APPID` / `QQ_MP_APPSECRET` | QQ 小程序 |
| `KUAISHOU_MP_APPID` / `KUAISHOU_MP_APPSECRET` | 快手小程序 |
| `JD_MP_APPID` / `JD_MP_APPSECRET` | 京东小程序 |
| `LARK_MP_APPID` / `LARK_MP_APPSECRET` | 飞书小程序 |

### 其他

| 变量 | 默认 | 作用 | 引用服务 |
|------|------|------|----------|
| `IDENTITY_URL` | `http://identity:7004` | brain 拉用户 BYOK key 的 identity 地址 | brain |
| `MINIO_ENDPOINT` | `minio:9000` | brain 文件通道的 MinIO 地址 | brain |
| `MINIO_USE_SSL` | `false` | brain 文件通道是否走 SSL | brain |
| `AIGC_S3_ENDPOINT` | `http://minio:9000` | AIGC 对象存储地址 | aigc、worker-aigc |
| `AIGC_S3_REGION` | `us-east-1` | AIGC 对象存储 region | aigc、worker-aigc |
| `AIGC_S3_USE_SSL` | `false` | AIGC 对象存储是否走 SSL | aigc、worker-aigc |
| `AIGC_GENERATE_VIA_RELAY` | `true` | AIGC 生成是否统一经 model-relay 出口 | worker-aigc |

---

## 核对发现的不一致

转录时逐条与 `docker-compose.yml` 比对，发现以下差异，使用时注意：

1. **`POSTGRES_DB` 双默认值**：`.env.example` 给 `biumind`，compose / Makefile 的兜底是 `biu_core`。`.env` 在场时以 `.env` 为准，功能无影响；只删掉该行才会落到 `biu_core`。
2. **外部对象存储的凭证变量不生效**：`.env.example` 注释建议设 `S3_ACCESS_KEY` / `S3_SECRET_KEY` / `S3_BUCKET` / `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY`，但 compose 实际把这些 env 从 `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` / `MINIO_BUCKET` 派生（`S3_ACCESS_KEY: ${MINIO_ROOT_USER:-biumind}` 等），直接在 `.env` 设置无效。要接外部 S3 / OSS，除地址类变量（`S3_ENDPOINT` 等，直接生效）外，凭证需改 compose 文件。
3. **`NATS_URL` 不可经 `.env` 配置**：compose 中硬编码 `nats://nats:4222`，换外部 NATS 需改 compose。
4. **brain 文件桶名硬编码**：brain 的 `MINIO_BUCKET` 是硬编码 `biumind-files`（与主桶 `MINIO_BUCKET` 变量无关），改主桶名不影响它。
5. **`COMPOSE_PROJECT_NAME`**：compose 文件顶部已有 `name: biumind`，`.env.example` 再列此项属于冗余（两者一致，无实际影响）。
6. **`BIUMIND_LOG_LEVEL` 示例值**：example 给 `debug`，compose 兜底 `info`——按 `.env.example` 部署的栈默认就是 debug 日志，生产建议改 `info`。

其余 `.env.example` 变量均与 compose 引用一一对应，无「写了但没人读」的项（`COMPOSE_PROJECT_NAME` 为 compose CLI 内置变量，属正常例外）。
