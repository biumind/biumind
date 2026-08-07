# BiuMind 官网（web/site）

BiuMind Agentics 对外官网，Astro + Tailwind + TypeScript，纯静态输出。中英双语：默认中文 `/`，英文挂在 `/en`。

## 本地开发

```bash
cd web/site
pnpm install         # 首次
pnpm dev             # 开发模式 → http://localhost:4321
```

页面：

| 路径 | 文件 |
|---|---|
| `/` | `src/pages/index.astro`（中文首页） |
| `/download` | `src/pages/download.astro`（中文下载） |
| `/en` | `src/pages/en/index.astro`（英文首页） |
| `/en/download` | `src/pages/en/download.astro`（英文下载） |

## 改文案

所有文案集中在 `src/i18n/zh.ts` 和 `src/i18n/en.ts`，类型定义在 `src/i18n/types.ts`。改字串只动这两处即可。

## 改样式

设计 token（颜色、间距、圆角、字体）来自 `apps/client/lib/app/theme.dart` 的 `BiuTokens`，对应位置：

- Tailwind class（`bg-purple` `text-ink-secondary` `rounded-md` 等）→ `tailwind.config.mjs`
- CSS 变量（`var(--purple)` 等）→ `src/styles/global.css`
- 组件级样式 token → 各 `.astro` 内联 class

修改设计 token 时需同步两处（Flutter 端 + 官网），保证产品和官网视觉连续。

## 构建产物

```bash
pnpm build           # 输出到 dist/
pnpm preview         # 预览 build 产物
```

`dist/` 是纯静态 HTML/CSS/SVG，无运行时 JS（Hero 视差用了一段内联 `<script>`，约 1KB）。直接扔任意静态主机或 CDN 即可。

## 部署 —— Docker Compose（折叠进主栈，统一入口）

site 是**客户端统一入口**（静态官网 + `/v1/*` 反代各后端 + `/app` `/admin` `/m`
反代 SPA），已折叠进主 compose 栈：`deploy/docker-compose/docker-compose.yml`
（连同 web-client / admin-web / miniapp-h5）。**不再单独维护 `web/site/docker-compose.yml`**
（避免与主栈定义漂移）。

```bash
cd deploy/docker-compose
cp .env.example .env                      # 首次
make up                                   # infra + services (含 site 等前端)
open http://localhost:8088                # 官网 + 统一入口
make health                               # 全服务 + site healthz
```

只起前端层（site + 三个 SPA），backend 已在跑时：

```bash
cd deploy/docker-compose
docker compose --profile services up -d --build site web-client admin-web miniapp-h5
```

文件：

- `Dockerfile` — 多阶段：node 22 alpine 构建 → nginx 1.27 alpine serving `dist/`
- `nginx.conf` — 统一入口 nginx 配置（静态站 + `/v1/*` 反代 + `/app` `/admin` `/m` + `/healthz`）
- 服务定义 → `deploy/docker-compose/docker-compose.yml`（services profile；端口 `SITE_PORT`，默认 `8088`）
- `.dockerignore` — 排除 `node_modules` `dist` 等

镜像源：默认拉 `docker.io/library/{node,nginx}` 与项目其他 service Dockerfile 一致；
本机不在该 mirror 后用 build args 覆盖 `NODE_IMAGE` / `NGINX_IMAGE`。

## 部署 —— 直接 nginx（无 Docker）

参考 `nginx.example.conf`。核心：

```nginx
server {
  listen 80;
  server_name biumind.ai www.biumind.ai;
  root /var/www/biumind-site;
  index index.html;

  # SPA-like fallback：trailing slash 与无 slash 都能命中目录里的 index.html
  location / {
    try_files $uri $uri/ $uri.html $uri/index.html =404;
  }

  # 静态资源长缓存
  location /_astro/ {
    expires 1y;
    add_header Cache-Control "public, immutable";
  }
}
```

CI 流程建议（在 Jenkinsfile 里加一个 stage）：

```groovy
stage('Build site') {
  steps {
    dir('web/site') {
      sh 'pnpm install --frozen-lockfile'
      sh 'pnpm build'
    }
  }
}
stage('Deploy site') {
  steps {
    sh 'rsync -avz --delete web/site/dist/ deploy@biumind.ai:/var/www/biumind-site/'
  }
}
```

## Hero 鼠标视差

`src/components/HeroIllustration.astro`。三层产品卡片用 `data-depth="-22 | 0 | 22"` 控制视差强度，鼠标位置驱动 `transform: translate3d`，rAF + 8% 阻尼，桌面才启用（移动端 / `prefers-reduced-motion` 跳过）。

要再加一层卡，复制 `parallax-card` div 设置 `data-depth` 即可。

## 加新页面

1. `src/pages/<page>.astro`（中文） + `src/pages/en/<page>.astro`（英文）
2. 文案进 `src/i18n/{zh,en}.ts`，类型先在 `src/i18n/types.ts` 加字段
3. 用 `BaseLayout` 包裹，自动获得 `<head>` meta + Nav + Footer
