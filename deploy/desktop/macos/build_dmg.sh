#!/usr/bin/env bash
# Build a distributable BiuMind macOS DMG.
#
# Modes
#   ad-hoc   (default) — codesign --sign -    No notarization, runs locally.
#   developer-id       — codesign with Developer ID Application (DEVELOPER_ID env);
#                        notarized with `xcrun notarytool` if APPLE_ID + APPLE_TEAM_ID
#                        + APPLE_PASSWORD (app-specific) are exported.
#
# Output
#   build/macos/biumind-<version>-<arch>.dmg
#
# Required tools: flutter, codesign, hdiutil. (Pure hdiutil — no create-dmg
# dependency; the fancy Finder layout is skipped in favour of a reliable
# read-only compressed image that always builds in CI.)

set -euo pipefail

cd "$(dirname "$0")/../../.."

CLIENT_DIR="apps/client"
APP_PATH="$CLIENT_DIR/build/macos/Build/Products/Release/biumind.app"
VERSION=$(awk '/^version:/ {print $2}' "$CLIENT_DIR/pubspec.yaml" | sed 's/+.*//')
ARCH=$(uname -m)
OUT_DIR="build/macos"
DMG_PATH="$OUT_DIR/biumind-${VERSION}-${ARCH}.dmg"

MODE="${1:-ad-hoc}"

log() { printf '\033[1;34m[dmg]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[dmg]\033[0m %s\n' "$*" >&2; exit 1; }

[[ -d "$CLIENT_DIR" ]] || die "run from repo root; missing $CLIENT_DIR"
command -v hdiutil >/dev/null || die "missing hdiutil (should ship with macOS)"

log "building Flutter macOS release ($VERSION-$ARCH)"
( cd "$CLIENT_DIR" && flutter build macos --release )

[[ -d "$APP_PATH" ]] || die "build did not produce $APP_PATH"

ENTITLEMENTS="$CLIENT_DIR/macos/Runner/Release.entitlements"

# biu CLI sidecar —— BiuDaemonManager (apps/client/lib/data/agent_plane/
# biu_daemon_manager.dart) 在 app 启动时自动 spawn `biu serve --register`
# 注册成 brain worker_kind=biu_daemon environment。binary 查找第 4 优先级就是
# app bundle Resources/biu (生产 codesigned binary)。dmg 不带 biu → daemon
# 不起 → 默认 agent 模式会话无 env → 发消息报 "environment_id required for
# mode=agent"。打包时 build 出 arm64 biu 塞进 Resources/, 与 frameworks 一起
# codesign, Gatekeeper 才放行子进程 spawn。
BIU_SRC="apps/cli/biu"
BIU_BIN="$APP_PATH/Contents/Resources/biu"
log "building biu CLI sidecar → $BIU_BIN"
mkdir -p "$APP_PATH/Contents/Resources"
# 注意:go build -C dir 的 -o 相对路径是相对 chdir 后的 dir (apps/cli/biu),
# 不是相对原 cwd。必须给绝对路径, 否则 biu 会被写到 apps/cli/biu/$BIU_BIN。
go build -C "$BIU_SRC" -o "$PWD/$BIU_BIN" ./cmd/biu
[[ -x "$BIU_BIN" ]] || die "biu build did not produce $BIU_BIN"

# Sign frameworks individually first (inside-out). `--deep` is unreliable on
# Flutter bundles with multiple .frameworks — and on macOS 15+ ad-hoc + deep
# + hardened runtime combine to produce "different Team IDs" dyld errors
# when the app is launched from /Applications. Sequenced signing fixes it.
sign_frameworks() {
  local opts=("$@")
  find "$APP_PATH/Contents/Frameworks" -type d -name "*.framework" -maxdepth 2 \
    | while read -r fw; do
        codesign --force "${opts[@]}" "$fw" >/dev/null
      done
}

case "$MODE" in
  ad-hoc)
    log "ad-hoc codesigning (no notarization, no hardened runtime)"
    # Hardened runtime + ad-hoc trips Gatekeeper Team ID checks under
    # /Applications on macOS 15+. Skip it for local builds.
    sign_frameworks --sign -
    # biu sidecar 在 Contents/Resources/ 下, 签 app 主包前先签 (inside-out),
    # 否则 hardened runtime / Gatekeeper 拒 spawn 子进程。
    codesign --force --sign - --entitlements "$ENTITLEMENTS" "$BIU_BIN"
    codesign --force --sign - --entitlements "$ENTITLEMENTS" "$APP_PATH"
    ;;
  developer-id)
    : "${DEVELOPER_ID:?DEVELOPER_ID env var required (e.g. 'Developer ID Application: Foo Bar (TEAMID)')}"
    log "codesigning with Developer ID: $DEVELOPER_ID"
    # Real Developer ID can use hardened runtime + timestamp safely; that's
    # what notarization expects.
    sign_frameworks --timestamp --options=runtime --sign "$DEVELOPER_ID"
    codesign --force --timestamp --options=runtime \
      --entitlements "$ENTITLEMENTS" --sign "$DEVELOPER_ID" "$BIU_BIN"
    codesign --force --timestamp --options=runtime \
      --entitlements "$ENTITLEMENTS" \
      --sign "$DEVELOPER_ID" "$APP_PATH"
    ;;
  *)
    die "unknown mode '$MODE' (use ad-hoc | developer-id)"
    ;;
esac

codesign --verify --strict "$APP_PATH"

mkdir -p "$OUT_DIR"
rm -f "$DMG_PATH"

log "creating DMG → $DMG_PATH"
STAGE_DIR="$(mktemp -d -t biumind-dmg-stage.XXXXXX)"
trap 'rm -rf "$STAGE_DIR"' EXIT
cp -R "$APP_PATH" "$STAGE_DIR/biumind.app"
ln -s /Applications "$STAGE_DIR/Applications"
hdiutil create \
  -volname "BiuMind $VERSION" \
  -srcfolder "$STAGE_DIR" \
  -fs HFS+ \
  -format UDZO \
  -imagekey zlib-level=9 \
  -ov \
  "$DMG_PATH" >/dev/null

if [[ "$MODE" == "developer-id" ]]; then
  if [[ -n "${APPLE_ID:-}" && -n "${APPLE_TEAM_ID:-}" && -n "${APPLE_PASSWORD:-}" ]]; then
    log "notarizing DMG via notarytool (this can take several minutes)"
    xcrun notarytool submit "$DMG_PATH" \
      --apple-id "$APPLE_ID" \
      --team-id "$APPLE_TEAM_ID" \
      --password "$APPLE_PASSWORD" \
      --wait
    xcrun stapler staple "$DMG_PATH"
  else
    log "skipping notarization — APPLE_ID/APPLE_TEAM_ID/APPLE_PASSWORD not set"
  fi
fi

log "done: $(du -h "$DMG_PATH" | cut -f1) — $DMG_PATH"
