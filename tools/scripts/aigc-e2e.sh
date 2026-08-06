#!/usr/bin/env bash
# AIGC 端到端冒烟脚本 (P3-7).
#
# 验证链路:
#   client (curl)
#     ─POST /v1/generations──▶ services/aigc
#                                ├── billing.Consume (identity 内部 RPC)
#                                ├── store.CreateTask
#                                └── NATS publish aigc.task.submit
#                                       ▼
#                                workers/aigc (Python)
#                                   ├── DashScope.submit + poll
#                                   ├── storage.persist (MinIO)
#                                   └── NATS publish aigc.task.update
#                                       ▼
#                                services/aigc orchestrator
#                                   ├── store.UpdateTaskStatus
#                                   ├── store.CreateTaskOutput (cas:<sha>)
#                                   └── NATS publish biumind.<env>.aigc.task.realtime
#                                       ▼
#                                services/realtime → SSE 客户端 (本脚本仅短轮询)
#
# 前置:
#   1. docker-compose up -d (--profile services)
#   2. .env 配 DASHSCOPE_API_KEY (没配 → 走 stub provider, 出 fake URL)
#   3. 用户已注册 (脚本会先 register/login)
#
# 用法:
#   bash tools/scripts/aigc-e2e.sh
#   AIGC_URL=http://localhost:7011 IDENTITY_URL=http://localhost:7004 \
#     bash tools/scripts/aigc-e2e.sh

set -uo pipefail

AIGC_URL="${AIGC_URL:-http://localhost:7011}"
IDENTITY_URL="${IDENTITY_URL:-http://localhost:7004}"
EMAIL="${EMAIL:-aigc-e2e@biumind.test}"
PASSWORD="${PASSWORD:-AigcE2e123!}"
DISPLAY_NAME="${DISPLAY_NAME:-AIGC E2E}"
MODEL_CODE="${MODEL_CODE:-wanx-2.6-t2i}"
TYPE="${TYPE:-image}"
PROMPT="${PROMPT:-一只柯基在草地上奔跑, 春日, 阳光明媚}"
POLL_TIMEOUT="${POLL_TIMEOUT:-180}"   # 秒
POLL_INTERVAL="${POLL_INTERVAL:-3}"

PASS=0; FAIL=0
ok()   { echo "  ✓ $*"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $*"; FAIL=$((FAIL+1)); }
section() { echo; echo "━━━ $* ━━━"; }

command -v jq >/dev/null || { echo "✗ jq required (brew install jq)"; exit 1; }
command -v curl >/dev/null || { echo "✗ curl required"; exit 1; }

# ─── 0. 健康检查 ─────────────────────────────────────

section "健康检查"
if curl -sS -o /dev/null -w "%{http_code}\n" "${AIGC_URL}/healthz" | grep -q 200; then
    ok "aigc /healthz"
else
    fail "aigc /healthz unreachable at ${AIGC_URL}"
    echo "请先 cd deploy/docker-compose && docker compose --profile services up -d"
    exit 1
fi
if curl -sS -o /dev/null -w "%{http_code}\n" "${IDENTITY_URL}/healthz" | grep -q 200; then
    ok "identity /healthz"
else
    fail "identity /healthz unreachable at ${IDENTITY_URL}"
    exit 1
fi

# ─── 1. 注册 / 登录拿 token ─────────────────────────

section "登录"
REGISTER_RESP=$(curl -sS -X POST "${IDENTITY_URL}/v1/auth/register" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\",\"display_name\":\"${DISPLAY_NAME}\"}")
# 第一次 200, 后续 409 — 都接受
REG_CODE=$(echo "$REGISTER_RESP" | jq -r '.error.code // ""')
if [ -z "$REG_CODE" ] || [ "$REG_CODE" = "already_exists" ]; then
    ok "register (or exists)"
else
    fail "register: $REGISTER_RESP"
    exit 1
fi

LOGIN_RESP=$(curl -sS -X POST "${IDENTITY_URL}/v1/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\"}")
TOKEN=$(echo "$LOGIN_RESP" | jq -r '.access_token // empty')
if [ -z "$TOKEN" ]; then
    fail "login: $LOGIN_RESP"
    exit 1
fi
USER_ID=$(echo "$LOGIN_RESP" | jq -r '.user.id')
ok "login user_id=${USER_ID}"

# ─── 2. 充积分 (mock recharge 选 1500 积分包) ────────

section "充积分 (mock)"
OPTIONS=$(curl -sS "${IDENTITY_URL}/v1/credits/recharge-options")
OPTION_ID=$(echo "$OPTIONS" | jq -r '.options[] | select(.credits_amount == 1500) | .id' | head -1)
if [ -z "$OPTION_ID" ]; then
    OPTION_ID=$(echo "$OPTIONS" | jq -r '.options[0].id')
fi
RECHARGE=$(curl -sS -X POST "${IDENTITY_URL}/v1/identity/me/credits/recharge" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"option_id\":\"${OPTION_ID}\",\"idempotency_key\":\"e2e-$(date +%s)\"}")
TOTAL=$(echo "$RECHARGE" | jq -r '.balance.total // 0')
if [ "$TOTAL" -gt 0 ]; then
    ok "recharge → balance=${TOTAL}"
else
    fail "recharge: $RECHARGE"
    exit 1
fi

# ─── 3. 提交生成 ─────────────────────────────────────

section "提交生成"
SUBMIT=$(curl -sS -X POST "${AIGC_URL}/v1/generations" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg type "$TYPE" --arg model "$MODEL_CODE" --arg prompt "$PROMPT" \
        '{type: $type, model_code: $model, prompt: $prompt, params: {aspect_ratio: "16:9"}, idempotency_key: ("e2e-" + (now | tostring))}')")
TASK_ID=$(echo "$SUBMIT" | jq -r '.task.id // empty')
COST=$(echo "$SUBMIT" | jq -r '.cost_credits // 0')
if [ -z "$TASK_ID" ]; then
    fail "submit: $SUBMIT"
    exit 1
fi
ok "submit task_id=${TASK_ID} cost=${COST}"

# ─── 4. 轮询任务 ─────────────────────────────────────

section "轮询任务进度"
ELAPSED=0
while [ "$ELAPSED" -lt "$POLL_TIMEOUT" ]; do
    GET=$(curl -sS -H "Authorization: Bearer ${TOKEN}" \
        "${AIGC_URL}/v1/generations/${TASK_ID}")
    STATUS=$(echo "$GET" | jq -r '.task.status')
    PROGRESS=$(echo "$GET" | jq -r '.task.progress')
    echo "  [${ELAPSED}s] status=${STATUS} progress=${PROGRESS}%"

    case "$STATUS" in
        completed)
            URLS=$(echo "$GET" | jq -r '.task.outputs[].url' | tr '\n' ' ')
            SHAS=$(echo "$GET" | jq -r '.task.outputs[].sha256' | tr '\n' ' ')
            ok "completed urls=[${URLS}] shas=[${SHAS}]"
            break
            ;;
        failed|blocked|cancelled)
            ERR=$(echo "$GET" | jq -r '.task.error_code')
            MSG=$(echo "$GET" | jq -r '.task.error_message')
            REFUND=$(echo "$GET" | jq -r '.task.refunded_credits')
            fail "task ${STATUS}: ${ERR} — ${MSG} (refunded=${REFUND})"
            break
            ;;
    esac
    sleep "$POLL_INTERVAL"
    ELAPSED=$((ELAPSED + POLL_INTERVAL))
done
if [ "$ELAPSED" -ge "$POLL_TIMEOUT" ]; then
    fail "timeout waiting for terminal status (last=${STATUS})"
fi

# ─── 5. /v1/generations/mine ────────────────────────

section "我的作品列表"
MINE=$(curl -sS -H "Authorization: Bearer ${TOKEN}" "${AIGC_URL}/v1/generations/mine")
COUNT=$(echo "$MINE" | jq -r '.tasks | length')
if [ "$COUNT" -gt 0 ]; then
    ok "mine count=${COUNT}"
else
    fail "mine empty: $MINE"
fi

# ─── 6. 余额校验 ─────────────────────────────────────

section "余额校验"
BAL=$(curl -sS -H "Authorization: Bearer ${TOKEN}" "${IDENTITY_URL}/v1/identity/me/credits/balance")
NEW_TOTAL=$(echo "$BAL" | jq -r '.total // 0')
EXPECTED=$((TOTAL - COST))
# 如果失败/退款, balance 可能就是 TOTAL (退完); 这里只做软断言提示
echo "  期望 (扣减后)=${EXPECTED}  实际=${NEW_TOTAL}"
if [ "$NEW_TOTAL" -ge "$EXPECTED" ]; then
    ok "balance valid (${NEW_TOTAL} >= ${EXPECTED})"
else
    fail "balance unexpected"
fi

# ─── 总结 ─────────────────────────────────────────────

section "结果"
echo "  通过: $PASS"
echo "  失败: $FAIL"
exit $((FAIL > 0))
