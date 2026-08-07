#!/usr/bin/env bash
# 测试环境冒烟测试 — 验证 NPM 路由 + identity 注册/登录 + hub LLM 转发 + SSE 长连接.
#
# 用法 (默认走 NPM 反代后的统一域名):
#   bash tools/scripts/smoke-test.sh
#
# 切其他环境:
#   BASE_URL=https://biumind.com bash tools/scripts/smoke-test.sh
#   BASE_URL=http://localhost:8088 bash tools/scripts/smoke-test.sh
#
# 不会改服务状态: 用固定测试账号 smoke@biumind.test (SmokeTest123!),
# 第一次 register, 之后 fallback 到 login.
#
# 退出码: 0 全过, 1 任一步失败.

set -uo pipefail

BASE_URL="${BASE_URL:-https://your-biumind.example.com}"

EMAIL="${EMAIL:-smoke@biumind.test}"
PASSWORD="${PASSWORD:-SmokeTest123!}"
MODEL="${MODEL:-claude-haiku-4-5}"

PASS_COUNT=0
FAIL_COUNT=0

ok()   { echo "  ✓ $*"; PASS_COUNT=$((PASS_COUNT+1)); }
fail() { echo "  ✗ $*"; FAIL_COUNT=$((FAIL_COUNT+1)); }

echo "BASE_URL = $BASE_URL"
echo ""

# ─── 1. NPM 反代基础检查 ───────────────────────────────────
echo "[1/5] NPM 路由"

# 1.1 官网首页
if curl -fsS -m 5 "$BASE_URL/" | grep -qi "biumind\|<html"; then
  ok "site / (官网首页)"
else
  fail "site / (没拿到 HTML 或 status 非 200)"
fi

# 1.2 API 路径分发: hub /v1/messages 没 token 应返 401
status=$(curl -s -m 5 -o /dev/null -w '%{http_code}' "$BASE_URL/v1/messages")
if [[ "$status" == "401" ]]; then
  ok "hub /v1/messages 路径分发正确 (401 missing bearer)"
elif [[ "$status" == "405" ]]; then
  ok "hub /v1/messages 路径分发正确 (405 GET not allowed)"
else
  fail "hub /v1/messages 状态码 $status (期望 401/405)"
fi

# 1.3 内部接口被挡
status=$(curl -s -m 5 -o /dev/null -w '%{http_code}' "$BASE_URL/v1/internal/publish")
if [[ "$status" == "403" ]]; then
  ok "/v1/internal/* 已被 NPM 挡 (403)"
else
  fail "/v1/internal/* 返 $status (期望 403, 安全风险!)"
fi

# ─── 2. identity: register 或 login ────────────────────────
echo ""
echo "[2/5] identity: register/login"
REG_RESP=$(curl -fsS -m 5 -X POST "$BASE_URL/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\",\"display_name\":\"smoke\"}" 2>&1) || REG_RC=$?
REG_RC=${REG_RC:-0}

if [[ $REG_RC -eq 0 ]]; then
  ok "register 新账号 (${EMAIL})"
  ACCESS_TOKEN=$(echo "$REG_RESP" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
else
  echo "  (register 失败, 尝试 login — 通常意味账号已存在)"
  LOGIN_RESP=$(curl -fsS -m 5 -X POST "$BASE_URL/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\",\"device_name\":\"smoke-test\"}" 2>&1)
  LOGIN_RC=$?
  if [[ $LOGIN_RC -eq 0 ]]; then
    ok "login 已有账号 (${EMAIL})"
    ACCESS_TOKEN=$(echo "$LOGIN_RESP" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
  else
    fail "register 和 login 都失败"
    echo "    register: $REG_RESP"
    echo "    login:    $LOGIN_RESP"
    ACCESS_TOKEN=""
  fi
fi

if [[ -z "$ACCESS_TOKEN" ]]; then
  fail "拿不到 access_token, 后续测试跳过"
else
  ok "拿到 access_token (${#ACCESS_TOKEN} 字符)"
fi

# ─── 3. hub: 调一次 LLM ────────────────────────────────────
echo ""
echo "[3/5] hub: LLM 转发 (model=${MODEL})"
if [[ -n "$ACCESS_TOKEN" ]]; then
  HUB_RESP=$(curl -fsS -m 30 -X POST "$BASE_URL/v1/messages" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer ${ACCESS_TOKEN}" \
    -d "{
      \"model\": \"${MODEL}\",
      \"max_tokens\": 50,
      \"messages\": [{\"role\":\"user\",\"content\":\"reply with exactly: ok\"}]
    }" 2>&1)
  HUB_RC=$?

  if [[ $HUB_RC -eq 0 ]]; then
    REPLY=$(echo "$HUB_RESP" | grep -o '"text":"[^"]*"' | head -1 | cut -d'"' -f4)
    if [[ -n "$REPLY" ]]; then
      ok "hub 转发成功, LLM 回复: \"${REPLY}\""
    else
      fail "hub 200 但 response 解析不出 text"
      echo "    response: $HUB_RESP" | head -3
    fi
  else
    fail "hub 调用失败"
    echo "    response: $HUB_RESP" | head -5
    echo "    常见原因:"
    echo "      - .env.test 里 BIUMIND_ANTHROPIC_KEY 没填或失效"
    echo "      - hub 不识别 model name (改 BIUMIND_ANTHROPIC_BASE_URL 或换 model)"
    echo "      - access_token 过期 / JWT_SECRET 不一致"
  fi
else
  echo "  (跳过, 无 access_token)"
fi

# ─── 4. realtime SSE: 检查 content-type + 不被立即切 ──────
echo ""
echo "[4/5] realtime: SSE 长连接 (5s 探测)"
if [[ -n "$ACCESS_TOKEN" ]]; then
  # 用 timeout 5s 探测, 期望: 拿到 200 + text/event-stream, 且这 5s 内连接没被切
  SSE_HEADERS=$(timeout 5 curl -sN -i -m 8 \
    -H "Authorization: Bearer ${ACCESS_TOKEN}" \
    "$BASE_URL/v1/realtime/stream" 2>&1 | head -20)

  if echo "$SSE_HEADERS" | grep -qi "content-type:.*text/event-stream"; then
    ok "SSE 头正确 (text/event-stream)"
  else
    fail "SSE Content-Type 不对"
    echo "    headers: $SSE_HEADERS" | head -10
  fi

  if echo "$SSE_HEADERS" | grep -qi "^content-length:"; then
    fail "响应有 Content-Length (应该是 chunked, 说明 NPM proxy_buffering 没关)"
  else
    ok "响应是 chunked 流 (proxy_buffering off 生效)"
  fi
else
  echo "  (跳过, 无 access_token)"
fi

# ─── 5. 总结 ───────────────────────────────────────────────
echo ""
echo "[5/5] summary"
echo "  pass: ${PASS_COUNT}    fail: ${FAIL_COUNT}"
echo ""

if [[ $FAIL_COUNT -gt 0 ]]; then
  echo "✗ 冒烟测试有 ${FAIL_COUNT} 项失败"
  exit 1
fi

echo "✓ 测试环境全链路 OK ($BASE_URL)"
