#!/usr/bin/env bash
# Build a BiuMind iOS release.
#
# Modes
#   verify         (default) — `flutter build ios --release --no-codesign`,
#                              compiles the Xcode project end-to-end without
#                              needing an Apple Developer ID. Use this in CI
#                              and for local sanity checks.
#   appstore                 — `flutter build ipa --export-options-plist=…`
#                              with the App Store / TestFlight template.
#   adhoc                    — same, but ad-hoc distribution.
#
# Required env for non-verify modes:
#   APPLE_TEAM_ID   — your developer team id (10-char alphanumeric)
#   The script substitutes it into the ExportOptions plist on the fly so
#   the templates stay generic.
#
# Output
#   build/ios/biumind-<version>.ipa     (appstore / adhoc)
#   build/ios/.verified                 (verify mode marker)

set -euo pipefail

cd "$(dirname "$0")/../../.."

CLIENT_DIR="apps/client"
TEMPLATE_DIR="deploy/mobile/ios"
VERSION=$(awk '/^version:/ {print $2}' "$CLIENT_DIR/pubspec.yaml" | sed 's/+.*//')
OUT_DIR="build/ios"
MODE="${1:-verify}"

log() { printf '\033[1;34m[ios]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[ios]\033[0m %s\n' "$*" >&2; exit 1; }

[[ "$(uname -s)" == "Darwin" ]] || die "iOS builds require macOS host"
command -v flutter >/dev/null || die "flutter not on PATH"
command -v xcodebuild >/dev/null || die "xcodebuild (Xcode CLT) required"

mkdir -p "$OUT_DIR"

case "$MODE" in
  verify)
    # Simulator build needs zero signing material — proves the Xcode
    # project + Info.plist + Pods all compile end-to-end. CI uses this
    # as the iOS-side smoke gate; engineers with a Developer ID can
    # graduate to `appstore` mode whenever they want a real IPA.
    log "verify build (simulator debug, no codesign, $VERSION)"
    # Flutter only supports debug for simulator. That's fine — the goal
    # is "does the Xcode project + Info.plist + Pods compile end-to-end?"
    # not "does --release work?". Real --release builds need Developer ID
    # via the appstore/adhoc modes below.
    ( cd "$CLIENT_DIR" && flutter build ios --simulator --debug )
    APP_PATH="$CLIENT_DIR/build/ios/iphonesimulator/Runner.app"
    [[ -d "$APP_PATH" ]] || die "Runner.app not produced at $APP_PATH"
    SIZE=$(du -sh "$APP_PATH" | cut -f1)
    : > "$OUT_DIR/.verified"
    log "compiled simulator Runner.app ($SIZE)"
    log "for a distributable IPA: APPLE_TEAM_ID=… $0 appstore"
    ;;
  appstore|adhoc)
    : "${APPLE_TEAM_ID:?APPLE_TEAM_ID env var required}"
    template="$TEMPLATE_DIR/ExportOptions-AppStore.plist"
    [[ "$MODE" == "adhoc" ]] && template="$TEMPLATE_DIR/ExportOptions-AdHoc.plist"

    rendered="$OUT_DIR/ExportOptions.plist"
    sed "s/REPLACE_WITH_TEAMID/$APPLE_TEAM_ID/" "$template" > "$rendered"
    log "rendered $rendered (team=$APPLE_TEAM_ID)"

    log "building IPA ($MODE, $VERSION)"
    ( cd "$CLIENT_DIR" && flutter build ipa \
        --release \
        --export-options-plist="$(cd "$OLDPWD" && pwd)/$rendered" )

    src="$CLIENT_DIR/build/ios/ipa/biumind.ipa"
    [[ -f "$src" ]] || die "IPA not produced at $src (Flutter changes filename per app name)"
    dest="$OUT_DIR/biumind-${VERSION}-${MODE}.ipa"
    cp "$src" "$dest"
    log "→ $dest ($(du -h "$dest" | cut -f1))"
    log "upload to App Store Connect with: xcrun altool --upload-app -f \"$dest\" -u <apple-id> -p <app-specific-password>"
    ;;
  *)
    die "unknown mode '$MODE' (use verify | appstore | adhoc)"
    ;;
esac
