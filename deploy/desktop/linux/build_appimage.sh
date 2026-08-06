#!/usr/bin/env bash
# Build a BiuMind Linux AppImage.
#
# Must run on a Linux host (cross-build from macOS is not supported by
# Flutter). On Debian/Ubuntu the prerequisites are:
#
#   sudo apt-get install -y clang cmake ninja-build pkg-config \
#     libgtk-3-dev liblzma-dev libstdc++-12-dev libsecret-1-dev libsqlite3-dev
#
# And `appimagetool` (https://github.com/AppImage/AppImageKit/releases) on
# $PATH. The script downloads it on demand if missing.
#
# Output: build/linux/biumind-<version>-<arch>.AppImage

set -euo pipefail

cd "$(dirname "$0")/../../.."

CLIENT_DIR="apps/client"
VERSION=$(awk '/^version:/ {print $2}' "$CLIENT_DIR/pubspec.yaml" | sed 's/+.*//')
ARCH=$(uname -m)
APPIMAGE_ARCH="$ARCH"
[[ "$ARCH" == "x86_64" ]] || true  # appimagetool conventions match
OUT_DIR="build/linux"
BUNDLE_DIR="$CLIENT_DIR/build/linux/${ARCH}/release/bundle"
APPIMAGE_PATH="$OUT_DIR/biumind-${VERSION}-${ARCH}.AppImage"

log() { printf '\033[1;34m[appimage]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[appimage]\033[0m %s\n' "$*" >&2; exit 1; }

[[ "$(uname -s)" == "Linux" ]] || die "must run on Linux (current: $(uname -s))"

log "building Flutter linux release ($VERSION-$ARCH)"
( cd "$CLIENT_DIR" && flutter build linux --release )

[[ -d "$BUNDLE_DIR" ]] || die "bundle not produced at $BUNDLE_DIR"

# Stage AppDir
APPDIR="$(mktemp -d)"
trap 'rm -rf "$APPDIR"' EXIT

mkdir -p "$APPDIR/usr/bin" "$APPDIR/usr/lib" "$APPDIR/usr/share/applications" \
         "$APPDIR/usr/share/icons/hicolor/512x512/apps"
cp -R "$BUNDLE_DIR"/* "$APPDIR/usr/bin/"

cat > "$APPDIR/biumind.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=BiuMind
Exec=biumind
Icon=biumind
Categories=Office;Utility;
Comment=BiuMind Agentics — your AI second brain
Terminal=false
DESKTOP
cp "$APPDIR/biumind.desktop" "$APPDIR/usr/share/applications/"

# Use the Flutter Linux runner's icon, falling back to a 1px PNG so
# appimagetool doesn't fail when no icon has been wired up yet.
ICON_SRC="$CLIENT_DIR/linux/runner/resources/app_icon.png"
if [[ -f "$ICON_SRC" ]]; then
  cp "$ICON_SRC" "$APPDIR/biumind.png"
else
  log "no app icon found at $ICON_SRC — using placeholder"
  printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDAT\x08\xd7c\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa3\x35\x81\x84\x00\x00\x00\x00IEND\xaeB`\x82' > "$APPDIR/biumind.png"
fi
cp "$APPDIR/biumind.png" "$APPDIR/usr/share/icons/hicolor/512x512/apps/"

cat > "$APPDIR/AppRun" <<'APPRUN'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
export PATH="$HERE/usr/bin:$PATH"
exec "$HERE/usr/bin/biumind" "$@"
APPRUN
chmod +x "$APPDIR/AppRun"

# Resolve appimagetool
if ! command -v appimagetool >/dev/null; then
  log "appimagetool not on PATH — downloading to /tmp"
  curl -fsSL -o /tmp/appimagetool \
    "https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-${ARCH}.AppImage"
  chmod +x /tmp/appimagetool
  TOOL=/tmp/appimagetool
else
  TOOL=$(command -v appimagetool)
fi

mkdir -p "$OUT_DIR"
log "packing AppImage → $APPIMAGE_PATH"
ARCH="$APPIMAGE_ARCH" "$TOOL" "$APPDIR" "$APPIMAGE_PATH"

log "done: $(du -h "$APPIMAGE_PATH" | cut -f1) — $APPIMAGE_PATH"
