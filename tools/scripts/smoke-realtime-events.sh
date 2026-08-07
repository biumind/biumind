#!/usr/bin/env bash
# AG-UI 事件端到端冒烟 — 验证 outbox poller → Realtime SSE 全链路。
#
# 路径：
#   1. SSE 客户端订阅 sidebar:user:<uid>
#   2. 触发 PUT /v1/sidebar/layout
#   3. 服务端：INSERT app_center.events → LISTEN/NOTIFY →
#      outbox poller → Realtime publish → 客户端收到 frame
#
# 这条链路的任意一段坏掉都会让 v1.5#3（侧边栏多设备 toast）+ M17 全
# 部 AG-UI app.* 事件静默失败 —— 客户端能拿到 200 但永远收不到事件。
# 必须有端到端 smoke 兜底。
#
# 用法：
#   bash tools/scripts/smoke-realtime-events.sh
#   BASE_URL=http://localhost:8088 bash tools/scripts/smoke-realtime-events.sh

set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8088}"
EMAIL="${EMAIL:-smoke-realtime@biumind.test}"
PASSWORD="${PASSWORD:-SmokeTest123!}"

PASS=0; FAIL=0
ok()   { echo "  ✓ $*"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $*"; FAIL=$((FAIL+1)); }

command -v jq >/dev/null || { echo "✗ jq required (brew install jq)"; exit 1; }
command -v python3 >/dev/null || { echo "✗ python3 required for JWT decode"; exit 1; }

echo "BASE_URL = $BASE_URL"

# ─── 1. login（自动注册 + 跳过邮箱验证经由 DB 直改，所以用户预先要存在） ───
TOKEN=$(curl -fsS -m 5 -X POST "$BASE_URL/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"display_name\":\"smoke-rt\"}" 2>/dev/null \
  | jq -r '.access_token // empty')
if [[ -z "$TOKEN" ]]; then
  TOKEN=$(curl -fsS -m 5 -X POST "$BASE_URL/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_name\":\"smoke-rt\"}" 2>/dev/null \
    | jq -r '.access_token // empty')
fi
if [[ -z "$TOKEN" ]]; then
  fail "无法登录 (邮箱可能未验证；compose 内可执行：UPDATE identity.users SET email_verified_at=now() WHERE email='$EMAIL';)"
  exit 1
fi
USER_ID=$(printf '%s' "$TOKEN" | cut -d. -f2 \
  | python3 -c "import sys,base64,json; s=sys.stdin.read().strip(); s+='='*((4-len(s)%4)%4); print(json.loads(base64.urlsafe_b64decode(s))['sub'])")
ok "login (user_id=${USER_ID:0:8}...)"

# ─── 2. SSE 订阅（后台 8s）───
TOPIC="sidebar:user:$USER_ID"
SSE_OUT=$(mktemp)
trap "rm -f $SSE_OUT" EXIT

curl -sN --max-time 8 -H "Authorization: Bearer $TOKEN" \
  "$BASE_URL/v1/realtime/stream?topics=$TOPIC&device_id=smoke-rt" > "$SSE_OUT" 2>&1 &
SSE_PID=$!
sleep 1

if ! kill -0 $SSE_PID 2>/dev/null; then
  fail "SSE 连接 1s 内已关闭"
  cat "$SSE_OUT" | head -5
  exit 1
fi
ok "SSE 订阅 $TOPIC (pid=$SSE_PID)"

# ─── 3. 触发 sidebar PUT ───
PUT_RESP=$(curl -fsS -m 5 -X PUT \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"scope":"desktop","items":[{"kind":"system","ref":"chat"}],"expected_version":0,"device":"smoke-rt"}' \
  "$BASE_URL/v1/sidebar/layout" 2>/dev/null)
NEW_VER=$(echo "$PUT_RESP" | jq '.version // 0')
if (( NEW_VER > 0 )); then
  ok "PUT /v1/sidebar/layout (version=$NEW_VER)"
else
  fail "PUT 失败: $PUT_RESP"
fi

# ─── 4. 等待事件到达（outbox poller 5s 周期 + LISTEN 即时通道）───
sleep 6
wait $SSE_PID 2>/dev/null

FRAME=$(grep -E '"kind":"biumind\.sidebar\.layout_changed"' "$SSE_OUT" | head -1)
if [[ -n "$FRAME" ]]; then
  RECV_VER=$(echo "$FRAME" | sed 's/^data: //' | jq '.payload.data.version // 0')
  if [[ "$RECV_VER" == "$NEW_VER" ]]; then
    ok "收到 biumind.sidebar.layout_changed 事件 (version=$RECV_VER)"
  else
    fail "事件 version 不匹配: got=$RECV_VER want=$NEW_VER"
  fi
else
  fail "未收到 biumind.sidebar.layout_changed 事件"
  echo "  --- raw SSE output ---"
  cat "$SSE_OUT" | head -20
  echo "  ----------------------"
fi

# ─── summary ───
echo ""
echo "[summary]"
echo "  pass: $PASS    fail: $FAIL"
if [[ $FAIL -eq 0 ]]; then
  echo "✓ AG-UI 事件端到端 OK"
  exit 0
fi
echo "✗ AG-UI 事件端到端 失败"
exit 1
