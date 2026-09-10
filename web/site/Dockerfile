# BiuMind site gateway —— 多阶段构建
#   stage 1: builder (node) 构建 Astro 静态站 → /app/dist (营销页 /)
#   stage 1.5: docs-builder (python + mkdocs) 构建文档站 → /build/site (/docs)
#   stage 2: runner (nginx) serving dist/ (/) + docs/ + /v1/* 反代到各后端 (单 origin 网关)
#
# docs/ + mkdocs.yml 经 .dockerignore 例外进 build context（见仓库根 .dockerignore）。
#
# build context = 仓库根（web/site 源在 web/site/，见 deploy/docker-compose/docker-compose.yml）。
# 镜像源前缀 docker.io 与项目其他 service Dockerfile 保持一致。
# 本机不在该 mirror 后用 build args 覆盖：
#   docker build --build-arg NODE_IMAGE=node:22-alpine --build-arg NGINX_IMAGE=nginx:alpine .

ARG NODE_IMAGE=docker.io/library/node:22-alpine
ARG PYTHON_IMAGE=docker.io/library/python:3.12-slim
ARG NGINX_IMAGE=docker.io/library/nginx:1.27-alpine

# ─────────── 1. builder (Astro) ───────────
# 钉在构建机原生架构：产物是纯静态文件，与目标架构无关。
# 不钉的话 arm64 腿要在 QEMU 模拟里跑 pnpm install/build，慢 5-10 倍。
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS builder
WORKDIR /app

# pnpm 全局：用 corepack 锁定版本，避免与本机不一致
RUN corepack enable && corepack prepare pnpm@8.15.8 --activate

# context = 仓库根，web/site 源在 web/site/。先拷 lockfile + manifest 最大化 layer 缓存。
COPY web/site/package.json web/site/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile=false --prefer-offline

# 拷源码并构建（repo .dockerignore 已排 node_modules/dist）
COPY web/site/ ./
RUN pnpm build

# ─────────── 2. docs-builder (mkdocs) ───────────
# docs/ 纯静态产物，同样钉构建机原生架构。
# mkdocs-material 自带 pymdown-extensions 等全部 markdown 依赖，钉大版本。
# 国内网络可覆盖 pip 源：--build-arg PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
FROM --platform=$BUILDPLATFORM ${PYTHON_IMAGE} AS docs-builder
ARG PIP_INDEX_URL=https://pypi.org/simple
WORKDIR /build
RUN pip install --no-cache-dir --index-url ${PIP_INDEX_URL} "mkdocs-material==9.*"
COPY mkdocs.yml .
COPY docs/ docs/
RUN mkdocs build

# ─────────── 3. runner (nginx) ───────────
FROM ${NGINX_IMAGE} AS runner

# 升级基础镜像内 openssl（libcrypto3/libssl3）—— nginx:1.27-alpine 自带 3.3.3-r0
# 有 CRITICAL CVE-2026-31789，CI Trivy 门禁会拦；fixed in 3.3.7-r0
RUN apk upgrade --no-cache libcrypto3 libssl3

# 删默认配置 + 静态站
RUN rm -f /etc/nginx/conf.d/default.conf && \
    rm -rf /usr/share/nginx/html/*

COPY web/site/nginx.conf /etc/nginx/conf.d/biumind-site.conf
# resolver 自适应脚本 (docker 127.0.0.11 / podman aardvark 网关 IP)。
# releases.json upstream 注入脚本 (dev MinIO / prod OSS)。
# nginx:alpine 官方 entrypoint 会在启动前执行 /docker-entrypoint.d/*.sh。
COPY web/site/docker-entrypoint.d/05-resolver.sh /docker-entrypoint.d/05-resolver.sh
COPY web/site/docker-entrypoint.d/06-releases-upstream.sh /docker-entrypoint.d/06-releases-upstream.sh
RUN chmod +x /docker-entrypoint.d/05-resolver.sh /docker-entrypoint.d/06-releases-upstream.sh

COPY --from=builder /app/dist /usr/share/nginx/html
# 文档站烤进镜像：nginx location ^~ /docs/ 的 root /usr/share/nginx + uri /docs/... 命中
COPY --from=docs-builder /build/site /usr/share/nginx/docs

# nginx:alpine 默认 user nginx 已存在；只读根 + tmpfs 由 compose 处理
EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost/ || exit 1
