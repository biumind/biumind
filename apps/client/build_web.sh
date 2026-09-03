#!/bin/bash
# Build the Flutter Web app for production.
#
# Output goes to apps/client/build/web. Suitable for static hosting
# (Vercel, CloudFlare Pages, S3+CloudFront, plain Nginx).
#
# Usage:
#   ./build_web.sh                              # default base-href '/'
#   ./build_web.sh --base-href /app/             # for path-based hosting
#
# Notes:
#   * The build inlines a base href into index.html. For app.biumind.com
#     hosting at root, '/' (default) is correct.
#   * sqlite3.wasm + drift_worker.dart.js must be served alongside
#     main.dart.js — they're already in apps/client/web/.
#   * Strict CSP is not enabled (Flutter uses eval'd JS for the bootstrap).
#     If you need CSP, switch the renderer to canvaskit and read the
#     Flutter docs.

set -euo pipefail

cd "$(dirname "$0")"

BASE_HREF="${1:-/}"

# ── editor / docproc web bundles ──────────────────────
# Milkdown 编辑器（editor-web）与本机文档解析（docproc-web）bundle 不入仓
#（.gitignore 忽略 {web,assets}/{editor,docproc}）；Flutter web 经同源
# iframe 加载 web/<name>/index.html —— 缺产物则编辑器白屏 / 本机解析静默
# 回退云端。做法对齐 release-client.yml（现场构建 + test 断言）：产物
# 比源新则跳过，否则 npm ci（node_modules 缺失时）+ vite build，失败经
# set -e fail-fast，产物缺失经断言 fail。
ensure_web_bundle() {
  local name="$1" dir="$2"
  local out="web/$name/index.html"
  if [[ -f "$out" ]] && \
     [[ -z "$(find "$dir" \
        \( -path '*/node_modules' -o -path '*/dist' \) -prune \
        -o -type f -newer "$out" -print -quit)" ]]; then
    echo "==> $name bundle up to date — skipping"
  else
    command -v npm >/dev/null || { echo "npm not on PATH (required to build $name bundle)" >&2; exit 1; }
    echo "==> building $name bundle ($dir)"
    (
      cd "$dir"
      if [[ ! -d node_modules ]]; then npm ci; fi
      npm run build
    )
  fi
  [[ -f "$out" ]] || { echo "ERROR: $name bundle missing $out after build" >&2; exit 1; }
  [[ -d "web/$name/assets" ]] || { echo "ERROR: $name bundle missing web/$name/assets" >&2; exit 1; }
}

ensure_web_bundle editor editor-web
ensure_web_bundle docproc docproc-web

echo "==> flutter pub get"
flutter pub get

echo "==> flutter build web --release --base-href $BASE_HREF"
flutter build web --release --base-href "$BASE_HREF"

echo "==> Output: $(pwd)/build/web ($(du -sh build/web | awk '{print $1}'))"
echo "    Deploy any static host. SPA fallback: serve index.html for 404."
