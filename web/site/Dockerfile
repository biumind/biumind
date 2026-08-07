# BiuMind site gateway —— 多阶段构建
#   stage 1: builder (node) 构建 Astro 静态站 → /app/dist (营销页 /)
#   stage 2: runner (nginx) serving dist/ (/) + /v1/* 反代到各后端 (单 origin 网关)
#
# 注: 文档站 (/docs) 暂未随公开仓发布 —— docs/ 不进公开仓 (后续选择性提交)。
#     届时恢复 docs-builder stage (python + mkdocs) + nginx /docs location 即可。
#
# build context = 仓库根（web/site 源在 web/site/，见 deploy/docker-compose/docker-compose.yml）。
# 镜像源前缀 docker.io 与项目其他 service Dockerfile 保持一致。
# 本机不在该 mirror 后用 build args 覆盖：
#   docker build --build-arg NODE_IMAGE=node:22-alpine --build-arg NGINX_IMAGE=nginx:alpine .

ARG NODE_IMAGE=docker.io/library/node:22-alpine
ARG NGINX_IMAGE=docker.io/library/nginx:1.27-alpine

# ─────────── 1. builder (Astro) ───────────
FROM ${NODE_IMAGE} AS builder
WORKDIR /app

# pnpm 全局：用 corepack 锁定版本，避免与本机不一致
RUN corepack enable && corepack prepare pnpm@8.15.8 --activate

# context = 仓库根，web/site 源在 web/site/。先拷 lockfile + manifest 最大化 layer 缓存。
COPY web/site/package.json web/site/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile=false --prefer-offline

# 拷源码并构建（repo .dockerignore 已排 node_modules/dist）
COPY web/site/ ./
RUN pnpm build

# ─────────── 2. runner (nginx) ───────────
FROM ${NGINX_IMAGE} AS runner

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

# nginx:alpine 默认 user nginx 已存在；只读根 + tmpfs 由 compose 处理
EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost/ || exit 1
