#!/usr/bin/env bash
# Build the BiuMind Web release.
#
# Output:
#   build/web/biumind-web-<version>.tgz   tarball ready for Deploy service
#   apps/client/build/web/                raw bundle (also kept for inspection)
#
# Optional inputs:
#   BASE_HREF     — passed through to `flutter build web --base-href`,
#                   useful when serving from a sub-path. Default "/".
#   RENDERER      — "canvaskit" | "html". Default "canvaskit" (better
#                   parity with desktop; "html" is smaller / less GPU
#                   demand for low-spec devices).

set -euo pipefail

cd "$(dirname "$0")/../.."

CLIENT_DIR="apps/client"
VERSION=$(awk '/^version:/ {print $2}' "$CLIENT_DIR/pubspec.yaml" | sed 's/+.*//')
OUT_DIR="build/web"
TGZ_PATH="$OUT_DIR/biumind-web-${VERSION}.tgz"

BASE_HREF="${BASE_HREF:-/}"
RENDERER="${RENDERER:-canvaskit}"

log() { printf '\033[1;34m[web]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[web]\033[0m %s\n' "$*" >&2; exit 1; }

[[ -d "$CLIENT_DIR" ]] || die "run from repo root; missing $CLIENT_DIR"
command -v flutter >/dev/null || die "flutter not on PATH"
command -v dart >/dev/null || die "dart (compile js) not on PATH"

# ── editor / docproc web bundles ──────────────────────
# Milkdown 编辑器（editor-web）与本机文档解析（docproc-web）bundle 不入仓
#（.gitignore 忽略 apps/client/{web,assets}/{editor,docproc}）；Flutter web
# 经同源 iframe 加载 web/<name>/index.html —— 缺产物则编辑器白屏 / 本机
# 解析静默回退云端。做法对齐 release-client.yml（现场构建 + test 断言）：
# 产物比源新则跳过，否则 npm ci（node_modules 缺失时）+ vite build，
# 失败经 set -e fail-fast，产物缺失经断言 fail。
ensure_web_bundle() {
  local name="$1" dir="$2"
  local out="$CLIENT_DIR/web/$name/index.html"
  if [[ -f "$out" ]] && \
     [[ -z "$(find "$CLIENT_DIR/$dir" \
        \( -path '*/node_modules' -o -path '*/dist' \) -prune \
        -o -type f -newer "$out" -print -quit)" ]]; then
    log "$name bundle up to date — skipping"
  else
    command -v npm >/dev/null || die "npm not on PATH (required to build $name bundle)"
    log "building $name bundle ($dir)"
    (
      cd "$CLIENT_DIR/$dir"
      if [[ ! -d node_modules ]]; then npm ci; fi
      npm run build
    )
  fi
  [[ -f "$out" ]] || die "$name bundle missing $out after build"
  [[ -d "$CLIENT_DIR/web/$name/assets" ]] || die "$name bundle missing $CLIENT_DIR/web/$name/assets"
}

ensure_web_bundle editor editor-web
ensure_web_bundle docproc docproc-web

# ── drift WASM artifacts ────────────────────────────────
# The Web build relies on two assets that aren't produced by
# `flutter build web`:
#   sqlite3.wasm          — sqlite engine, copied from drift's bundle
#   drift_worker.dart.js  — drift's worker, compiled from
#                           web/drift_worker.dart
# Both files MUST sit alongside main.dart.js. We rebuild the worker
# every time so a drift upgrade can't leave a stale .js around; the
# .wasm is treated as cached if already present.
DRIFT_PUB=$(find "$HOME/.pub-cache/hosted/pub.dev" -maxdepth 1 -type d -name 'drift-*' 2>/dev/null | sort | tail -1)
WEB="$CLIENT_DIR/web"

if [[ ! -f "$WEB/sqlite3.wasm" ]]; then
  if [[ -z "$DRIFT_PUB" ]]; then
    die "drift not in ~/.pub-cache; run 'flutter pub get' in $CLIENT_DIR first"
  fi
  log "copying sqlite3.wasm from $DRIFT_PUB"
  cp "$DRIFT_PUB/extension/devtools/build/sqlite3.wasm" "$WEB/sqlite3.wasm"
fi

if [[ ! -f "$WEB/drift_worker.dart" ]]; then
  cp "$DRIFT_PUB/web/drift_worker.dart" "$WEB/drift_worker.dart"
fi

log "compiling drift_worker.dart → drift_worker.dart.js"
( cd "$CLIENT_DIR" && dart compile js -O2 web/drift_worker.dart -o web/drift_worker.dart.js )

log "building Flutter web ($VERSION, base=$BASE_HREF, renderer=$RENDERER)"
( cd "$CLIENT_DIR" && flutter build web \
    --release \
    --base-href "$BASE_HREF" \
    --pwa-strategy offline-first )

BUNDLE="$CLIENT_DIR/build/web"
[[ -d "$BUNDLE" ]] || die "build did not produce $BUNDLE"

# Flutter's web build copies arbitrary files from web/ into build/web/
# unless they're explicitly excluded — so sqlite3.wasm + the .dart.js
# the worker compiled to should already be there.
for f in sqlite3.wasm drift_worker.dart.js; do
  [[ -f "$BUNDLE/$f" ]] || die "missing $f in $BUNDLE — Flutter must have skipped it"
done

mkdir -p "$OUT_DIR"
log "packing tarball → $TGZ_PATH"
tar -czf "$TGZ_PATH" -C "$BUNDLE" .

# Sanity check: tarball must contain everything the runtime needs.
for f in index.html main.dart.js manifest.json flutter_service_worker.js \
         sqlite3.wasm drift_worker.dart.js \
         editor/index.html docproc/index.html; do
  if ! tar -tzf "$TGZ_PATH" | grep -q "^./$f$"; then
    die "tarball missing required file: $f"
  fi
done

log "done: $(du -h "$TGZ_PATH" | cut -f1) — $TGZ_PATH"
log "deploy via:"
log "  curl -X POST http://localhost:7006/v1/deploys \\"
log "    -H 'Authorization: Bearer \$TOKEN' \\"
log "    -F kind=static -F label=biumind-web -F tarball=@$TGZ_PATH"
