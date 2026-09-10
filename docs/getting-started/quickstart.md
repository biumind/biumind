# 快速开始

两条路径任选其一：

- **路径 A · 云端版**：注册即用，几分钟上手，适合个人和小团队。
- **路径 B · 自托管试用**：用 Docker Compose 在本机拉起完整服务栈，适合想先看看全貌、或要把数据留在自己服务器的人。

---

## 路径 A：云端版（注册即用）

1. 打开官网下载页 [biumind.ai/download](https://biumind.ai/download)，下载对应平台的客户端（macOS 安装包、Android APK等；下载页会自动检测你的系统并推荐）。

   不想装客户端？直接在浏览器打开 [Web 版](https://biumind.ai/app) 也能用。

2. 首次启动，在登录页**注册账号**（邮箱 + 密码）。

3. 登录后即可开始：先和 AI 对个话，或新建一个 Wiki 项目写第一篇文档。

> [!TIP]
> 桌面端首次打开如果提示"未签名包"，macOS 上右键点击应用选「打开」，或在「系统设置 → 隐私与安全性」里点「仍要打开」。详见[下载与安装](download.md)。

各平台的下载渠道、安装注意事项见[下载与安装](download.md)。

## 路径 B：自托管试用

用仓库自带的 Docker Compose 栈在本机拉起完整服务（Postgres / MinIO / NATS + 全部后端服务 + Web 前端 + 异步 worker），统一入口为本地 nginx 网关。

### 前置要求

- Docker 与 Docker Compose 插件（v2）
- 约 4 GB 以上可用内存
- 能访问镜像仓库（默认走阿里云镜像仓库拉取 CI 预构建镜像，无需本地编译）

### 步骤

```bash
# 1. 克隆仓库
git clone https://github.com/biumind/biumind.git
cd biumind/deploy/docker-compose

# 2. 生成环境配置
cp .env.example .env

# 3. 拉起完整栈（infra + 全部服务 + 前端 + workers）
make up

# 4. 等所有服务就绪后，检查健康状态
make health
```

`make up` 会自动等待所有容器健康检查通过。然后浏览器打开：

```text
http://localhost:8088
```

这是本地的统一入口（静态官网 + `/v1/*` API 网关）。Web 版客户端在 `/app` 路径下，首次访问同样先注册账号。桌面客户端若要连这套自托管环境，把设置里的"服务器地址"填 `http://localhost:8088` 即可——所有服务入口都由这一个地址提供。

> [!WARNING]
> `.env.example` 里的密码、JWT 密钥、`BIUMIND_MASTER_KEY` 都是占位值，本机试用可以先用，**对外部署前必须全部换成强随机值**。另外，这套 compose 栈是开发 / 测试环境的启动示例，生产部署的加固与运维需要自行负责，参见[自托管部署指南](../self-hosting/index.md)。

### 常用命令

```bash
make ps                 # 列出所有容器状态
make tail SVC=brain     # 跟某个服务的日志（如 model-relay / identity / brain）
make psql               # 进 Postgres shell
make down               # 停容器（保留数据卷）
make clean              # 停容器并删除全部数据卷（需二次确认，数据全清）
```

> [!TIP]
> `sandbox`（云沙箱，依赖 K8s）和 `deploy`（一键部署）服务不在这套本地栈内。想改后端代码再跑，用 `make build-images` 本地构建镜像后 `make up-local` 启动。

## 30 秒上手 biu CLI

`biu` 是 BiuMind 的终端 AI 编码代理，与图形客户端共享同一内核、会话互通。macOS / Linux 一行命令安装：

```bash
brew install biumind/tap/biu
```

初始化配置（交互式向导，会引导你选择云端 / 自托管 / 直连 API Key 三种模式，云端模式走浏览器授权登录）：

```bash
biu init
```

然后直接进入 REPL 开始干活：

```bash
biu doctor    # 检查环境与配置是否健康
biu           # 进入 REPL，输入 /help 查看命令列表
```

更多内容（配置、权限、记忆文件、命令参考）见 [CLI 上手指南](../cli/getting-started.md)。

## 下一步

- [下载与安装](download.md) —— 各端安装渠道
- [什么是 BiuMind](index.md) —— 六大模块总览
- [自托管部署指南](../self-hosting/index.md) —— 生产部署与环境变量参考
