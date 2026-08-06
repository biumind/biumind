#!/usr/bin/env bash
# gen-app-icons.sh — 从原始 logo PNG 生成 macOS / iOS / Android app icon。
# 输出尺寸覆盖各平台 AppIcon.appiconset 需要的全套，紫底圆角白 mark。
# 依赖：potrace + magick + python3。
#
# 用法：tools/scripts/gen-app-icons.sh path/to/source-logo.png
#
# 已知 path 数据放在 web/site/src/components/biu-paths.ts，
# 这个脚本接受任意 PNG 重新 trace（设计师改了原图就重跑一次）。

set -euo pipefail

SOURCE="${1:?usage: $0 path/to/source-logo.png}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT_DIR="${ROOT}/apps/client/macos/Runner/Assets.xcassets/AppIcon.appiconset"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# 1. PNG → PBM → SVG paths (potrace)
magick "$SOURCE" -alpha remove -background white -resize 2048x2048 -threshold 55% "$WORK/in.pbm"
potrace -s -o "$WORK/full.svg" --tight --turdsize 4 --opttolerance 0.2 "$WORK/in.pbm"

# 2. SVG → 5 个 path data via Python (跟 site 端 biu-paths 数据同源)
python3 <<PY
import re, json
with open("$WORK/full.svg") as f:
    src = f.read()
paths = re.findall(r'<path d="([^"]*)"[^/]*/>', src, re.S)
parts = ['head', 'circuit_l', 'circuit_m', 'circuit_r', 'hand']
out = {n: ' '.join(d.split()) for n, d in zip(parts, paths)}
with open("$WORK/paths.json", 'w') as f:
    json.dump(out, f)
PY

# 3. 用 trace 出的 path 合成 1024x1024 紫底圆角白 mark icon SVG
python3 <<PY
import json
with open("$WORK/paths.json") as f:
    p = json.load(f)
tx = 'translate(-594.228457,1763.5) scale(0.1,-0.1)'
icon = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
  <rect width="1024" height="1024" rx="180" fill="#5B5BD6"/>
  <g transform="translate(266,102) scale(0.527) {tx}" fill="#fff" stroke="none">
    <path d="{p['head']}"/>
    <path d="{p['circuit_l']}"/>
    <path d="{p['circuit_m']}"/>
    <path d="{p['circuit_r']}"/>
    <path d="{p['hand']}"/>
  </g>
</svg>'''
with open("$WORK/icon.svg", 'w') as f:
    f.write(icon)
PY

# 4. 渲染各尺寸 PNG → 写入 macOS AppIcon.appiconset
for sz in 16 32 64 128 256 512 1024; do
  magick "$WORK/icon.svg" -resize ${sz}x${sz} "$OUT_DIR/app_icon_${sz}.png"
  echo "  ✓ app_icon_${sz}.png"
done

echo ""
echo "✓ macOS app icons updated → $OUT_DIR"
echo ""
echo "iOS / Android 图标尚未接入 (apps/client/ios/Runner/Assets.xcassets,"
echo "  apps/client/android/app/src/main/res/mipmap-*) — 后续按需扩展"
