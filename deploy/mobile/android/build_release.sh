#!/usr/bin/env bash
# Build a BiuMind Android release.
#
# Modes
#   aab       (default) — produces an Android App Bundle for Play Store upload
#   apk                 — universal APK + per-ABI splits for sideloading
#   both                — both of the above
#
# Signing:
#   * If apps/client/android/key.properties exists, the release is signed
#     with that keystore (production path).
#   * Otherwise the script generates a *dev keystore* in
#     apps/client/android/biumind-dev.jks the first time it runs and uses
#     it. The dev keystore is gitignored. NEVER use the dev keystore for
#     Play Store uploads — Google won't accept a different keystore later.
#
# Output
#   build/android/biumind-<version>-<flavor>.{aab,apk}

set -euo pipefail

cd "$(dirname "$0")/../../.."

CLIENT_DIR="apps/client"
ANDROID_DIR="$CLIENT_DIR/android"
VERSION=$(awk '/^version:/ {print $2}' "$CLIENT_DIR/pubspec.yaml" | sed 's/+.*//')
OUT_DIR="build/android"
MODE="${1:-aab}"

log() { printf '\033[1;34m[android]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[android]\033[0m %s\n' "$*" >&2; exit 1; }

[[ -d "$CLIENT_DIR" ]] || die "run from repo root; missing $CLIENT_DIR"
command -v flutter >/dev/null || die "flutter not on PATH"
command -v keytool >/dev/null || die "keytool (JDK) required for keystore generation"

KEY_PROPS="$ANDROID_DIR/key.properties"
DEV_KEYSTORE="$ANDROID_DIR/biumind-dev.jks"

if [[ ! -f "$KEY_PROPS" ]]; then
  if [[ ! -f "$DEV_KEYSTORE" ]]; then
    log "no key.properties found — generating dev keystore at $DEV_KEYSTORE"
    log "(production builds MUST replace this with a Play-issued keystore)"
    keytool -genkeypair \
      -alias biumind-dev \
      -keyalg RSA -keysize 2048 -validity 10000 \
      -keystore "$DEV_KEYSTORE" \
      -storepass biumind-dev-pass \
      -keypass biumind-dev-pass \
      -dname "CN=BiuMind Dev, O=BiuMind, C=US"
  fi
  cat > "$KEY_PROPS" <<EOF
storePassword=biumind-dev-pass
keyPassword=biumind-dev-pass
keyAlias=biumind-dev
storeFile=../biumind-dev.jks
EOF
  log "wrote $KEY_PROPS pointing at the dev keystore"
fi

mkdir -p "$OUT_DIR"

build_aab() {
  log "building AAB ($VERSION)"
  ( cd "$CLIENT_DIR" && flutter build appbundle --release )
  local src="$CLIENT_DIR/build/app/outputs/bundle/release/app-release.aab"
  [[ -f "$src" ]] || die "AAB not produced at $src"
  local dest="$OUT_DIR/biumind-${VERSION}.aab"
  cp "$src" "$dest"
  log "→ $dest ($(du -h "$dest" | cut -f1))"
}

build_apk() {
  log "building APK splits ($VERSION)"
  # 先清旧产物: --split-per-abi 不产 universal app-release.apk, 上次构建的
  # 残留会被下面的 glob 一起拷走 (0.1.1 差点把 0.1.0 的 universal 包发出去)。
  rm -f "$CLIENT_DIR/build/app/outputs/flutter-apk/"*-release.apk
  ( cd "$CLIENT_DIR" && flutter build apk --release --split-per-abi )
  for f in "$CLIENT_DIR/build/app/outputs/flutter-apk/"*-release.apk; do
    [[ -f "$f" ]] || continue
    local base
    base=$(basename "$f" .apk)
    local dest="$OUT_DIR/biumind-${VERSION}-${base}.apk"
    cp "$f" "$dest"
    log "→ $dest ($(du -h "$dest" | cut -f1))"
  done
}

case "$MODE" in
  aab) build_aab ;;
  apk) build_apk ;;
  both) build_aab; build_apk ;;
  *) die "unknown mode '$MODE' (use aab | apk | both)" ;;
esac

log "done."
