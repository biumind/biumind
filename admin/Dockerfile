# BiuMind Admin (Vue 3 + Element Plus) — multi-stage build.
#
# Stage 1: pnpm install + vite build → /app/dist
# Stage 2: nginx:1.27-alpine 提供静态 + SPA fallback (跟 site/web-client 同套)
#
# Build context = admin/  (build 时 docker build 应该用 admin 作 cwd)
#
# Vite base = /admin/, 所以静态资源 URL 会带 /admin/ 前缀.
# 反代由 site nginx 在 location /admin/ 用 rewrite + proxy_pass 处理.

ARG INFRA_REGISTRY=docker.io
FROM ${INFRA_REGISTRY}/library/node:22-alpine AS builder
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@9.x --activate
COPY package.json pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile=false --prefer-offline
COPY . .
RUN pnpm build

FROM ${INFRA_REGISTRY}/library/nginx:1.27-alpine
# 升级 openssl（libcrypto3/libssl3 ≥3.3.7-r0）—— 基础镜像自带 3.3.3-r0 有 CRITICAL CVE-2026-31789
RUN apk upgrade --no-cache libcrypto3 libssl3
RUN rm -f /etc/nginx/conf.d/default.conf && \
    rm -rf /usr/share/nginx/html/*
COPY nginx.conf /etc/nginx/conf.d/biumind-admin.conf
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
  CMD wget -q --spider http://localhost/healthz || exit 1
