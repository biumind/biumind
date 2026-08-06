#!/usr/bin/env bash
# App Center 性能 baseline 驱动 — 三条关键路径压测：
#   1. catalog 读   GET  /v1/apps                              (read-only, hot path)
#   2. install 写   POST /v1/apps/installs                     (Authz + tx + events)
#   3. invoke 调用  POST /v1/apps/{name}/invoke                (Runtime tools 同步 dispatch)
#
# 每条路径独立跑，记录 RPS / P50 / P95 / P99 / 错误率，输出到
# stdout 与可选文件。脚本本身不固化阈值——baseline 报告
# (docs/perf/app-center-v1.5.md) 才是 SLO 表；这里只是一个可
# 重跑的产生器。
#
# 依赖工具（任选其一，按优先级）：
#   - hey     (https://github.com/rakyll/hey) — Go 二进制，最常用
#   - vegeta  — 更精确的延迟分布
#   - k6      — 脚本化并发场景
# 检测顺序：hey → vegeta → k6 → 失败退出
#
# 用法：
#   bash tools/scripts/perf-app-center.sh                           # smoke baseline
#   DURATION=60s CONCURRENCY=50 bash tools/scripts/perf-app-center.sh
#   OUT=docs/perf/run-$(date +%Y%m%d).md bash tools/scripts/perf-app-center.sh
#
# Notes：
#  · 安装路径每次 hit 都创建新 installation 行；脚本结束统一清理用
#    cleanup 段。Invoke 路径调 fetch（read-only），不污染状态。
#  · 不要直接打 prod，专门给 staging / dev compose 用。

set -uo pipefail

BASE_URL="${BASE_URL:-https://your-biumind.example.com}"
EMAIL="${EMAIL:-perf-app-center@biumind.test}"
PASSWORD="${PASSWORD:-PerfTest123!}"
DURATION="${DURATION:-15s}"
CONCURRENCY="${CONCURRENCY:-20}"
OUT="${OUT:-/dev/stdout}"

require_jq() {
  if ! command -v jq >/dev/null 2>&1; then
    echo "✗ jq is required" >&2; exit 1
  fi
}
require_jq

# ─── 选驱动 ───────────────────────────────────────────────
DRIVER=""
if command -v hey >/dev/null 2>&1; then DRIVER=hey
elif command -v vegeta >/dev/null 2>&1; then DRIVER=vegeta
elif command -v k6 >/dev/null 2>&1; then DRIVER=k6
else
  echo "✗ install hey or vegeta or k6 first" >&2
  echo "    brew install hey      # macOS easiest" >&2
  echo "    or: go install github.com/rakyll/hey@latest" >&2
  exit 1
fi
echo "→ driver: $DRIVER (DURATION=$DURATION CONCURRENCY=$CONCURRENCY)"

# ─── auth ────────────────────────────────────────────────
AUTH_RESP=$(curl -fsS -m 5 -X POST "$BASE_URL/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\",\"device_name\":\"perf\"}" 2>/dev/null) || \
AUTH_RESP=$(curl -fsS -m 5 -X POST "$BASE_URL/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\",\"display_name\":\"perf\"}" 2>/dev/null)
ACCESS_TOKEN=$(echo "$AUTH_RESP" | jq -r '.access_token')
[[ -z "$ACCESS_TOKEN" || "$ACCESS_TOKEN" == "null" ]] && { echo "✗ auth failed"; exit 1; }
AUTH_HDR="Authorization: Bearer $ACCESS_TOKEN"

# ─── 准备一个一次性 installation 给 invoke 路径用 ─────────
INSTALL_RESP=$(curl -sS -m 5 -X POST -H "$AUTH_HDR" \
  -H 'Content-Type: application/json' \
  -d '{"identifier":"rss","scope":"user","granted_permissions":["net.outbound","hub.invoke","wiki.write","cron.register"]}' \
  "$BASE_URL/v1/apps/installs" 2>/dev/null)
PERF_INSTALL_ID=$(echo "$INSTALL_RESP" | jq -r '.id // .ID // ""')
trap 'cleanup' EXIT

cleanup() {
  if [[ -n "${PERF_INSTALL_ID:-}" && "$PERF_INSTALL_ID" != "null" ]]; then
    curl -sS -m 5 -X DELETE -H "$AUTH_HDR" \
      "$BASE_URL/v1/apps/installs/$PERF_INSTALL_ID" >/dev/null 2>&1 || true
  fi
}

# ─── 通用驱动调度 ─────────────────────────────────────────
# bench LABEL METHOD URL [BODY_FILE]
# 输出格式统一三行：rps p95 errors_pct（其它指标按驱动自带格式）
bench() {
  local label="$1"; shift
  local method="$1"; shift
  local url="$1"; shift
  local body_file="${1:-}"
  echo ""
  echo "── ${label} ─────────────────────────────────"
  case "$DRIVER" in
    hey)
      local args=( -z "$DURATION" -c "$CONCURRENCY" -m "$method"
        -H "$AUTH_HDR" )
      if [[ -n "$body_file" ]]; then
        args+=( -H 'Content-Type: application/json' -D "$body_file" )
      fi
      hey "${args[@]}" "$url"
      ;;
    vegeta)
      local target_file
      target_file=$(mktemp)
      printf "%s %s\n" "$method" "$url" > "$target_file"
      printf "%s\n" "$AUTH_HDR" > "${target_file}.headers"
      [[ -n "$body_file" ]] && printf "@%s\n" "$body_file" >> "$target_file"
      vegeta attack -duration="$DURATION" -rate="$CONCURRENCY" \
        -targets="$target_file" -header="$AUTH_HDR" \
        ${body_file:+-body="$body_file"} \
        | vegeta report
      rm -f "$target_file"
      ;;
    k6)
      cat <<EOF | k6 run --duration "$DURATION" --vus "$CONCURRENCY" --quiet -
import http from 'k6/http';
const url = '$url';
const auth = '$AUTH_HDR';
const body = $(if [[ -n "$body_file" ]]; then echo "open('$body_file')"; else echo "null"; fi);
export default function() {
  http.request('$method', url, body, {
    headers: { 'Content-Type': 'application/json', 'Authorization': auth.split(': ')[1] }
  });
}
EOF
      ;;
  esac
}

# ─── 1. catalog 读 ────────────────────────────────────────
bench "GET /v1/apps (catalog 读)" "GET" "$BASE_URL/v1/apps"

# ─── 2. install 写（每次新建 → 立即清理） ─────────────────
# 注意：每次 install 都需要唯一 (scope, scope_id, identifier) — 但
# 同 user 同 slug 唯一约束意味着重复 install 会 409。压测时我们故
# 意复用同一组合，期望服务在 ErrAlreadyInstalled 路径上仍快速失败
# 而不是慢路径。这反映"用户重复点安装"现实场景。
INSTALL_BODY=$(mktemp)
echo '{"identifier":"webclip","scope":"user","granted_permissions":["net.outbound"]}' > "$INSTALL_BODY"
bench "POST /v1/apps/installs (重复安装快速 409)" "POST" \
  "$BASE_URL/v1/apps/installs" "$INSTALL_BODY"
# 清掉为这个 bench 临时建的 webclip install
WEBCLIP_INSTALLS=$(curl -fsS -m 5 -H "$AUTH_HDR" \
  "$BASE_URL/v1/apps/installs?scope=user" | \
  jq -r '.installations[] | select(.identifier=="webclip") | .id')
for id in $WEBCLIP_INSTALLS; do
  curl -sS -m 5 -X DELETE -H "$AUTH_HDR" \
    "$BASE_URL/v1/apps/installs/$id" >/dev/null 2>&1 || true
done
rm -f "$INSTALL_BODY"

# ─── 3. invoke 调用 ───────────────────────────────────────
INVOKE_BODY=$(mktemp)
echo '{"action":"list_subscriptions","input":{}}' > "$INVOKE_BODY"
bench "POST /v1/apps/rss/invoke (list_subscriptions)" "POST" \
  "$BASE_URL/v1/apps/rss/invoke" "$INVOKE_BODY"
rm -f "$INVOKE_BODY"

echo ""
echo "✓ baseline 跑完。把 stdout 复制到 docs/perf/app-center-v1.5.md"
