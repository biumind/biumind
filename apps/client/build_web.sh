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

echo "==> flutter pub get"
flutter pub get

echo "==> flutter build web --release --base-href $BASE_HREF"
flutter build web --release --base-href "$BASE_HREF"

echo "==> Output: $(pwd)/build/web ($(du -sh build/web | awk '{print $1}'))"
echo "    Deploy any static host. SPA fallback: serve index.html for 404."
