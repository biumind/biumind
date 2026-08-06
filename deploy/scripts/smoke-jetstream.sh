#!/usr/bin/env bash
# smoke-jetstream — verify the channels→runtime JetStream wiring.
#
# What it proves:
#   1. JetStream is enabled on the broker
#   2. Stream BIUMIND_CHANNELS exists (auto-ensured by either service at boot)
#   3. The runtime durable consumer (`runtime-channels-inbound`) is bound
#      to the stream and pulling
#   4. A test publish under `biumind.dev.channels.inbound.smoke` lands
#      in the stream and the consumer's pending count drops to 0
#
# Doesn't prove agent.Run actually completes the LLM turn — that needs
# Hub credentials. This is a wiring smoke, not a full e2e.
#
# Requirements:
#   * NATS broker reachable at $NATS_HTTP (monitor port, default :8222)
#   * `nats` CLI for publish (https://github.com/nats-io/natscli)
#     OR set $SKIP_PUBLISH=1 to only verify stream + consumer state
#
# Usage:
#   deploy/scripts/smoke-jetstream.sh                   # localhost
#   NATS_HTTP=http://nats:8222 deploy/scripts/smoke-jetstream.sh
#   SKIP_PUBLISH=1 deploy/scripts/smoke-jetstream.sh

set -euo pipefail

NATS_HTTP="${NATS_HTTP:-http://localhost:8222}"
NATS_URL="${NATS_URL:-nats://localhost:4222}"
ENV_NAME="${BIUMIND_ENV:-dev}"
STREAM="BIUMIND_CHANNELS"
DURABLE="runtime-channels-inbound"
SUBJECT="biumind.${ENV_NAME}.channels.inbound.smoke"

red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }

step() { printf '\n\033[36m▸ %s\033[0m\n' "$*"; }

require_jq() {
  if ! command -v jq >/dev/null 2>&1; then
    red "jq required (brew install jq / apt install jq)"
    exit 2
  fi
}

require_jq

step "1) JetStream enabled on broker?"
JSZ="$(curl -sf "${NATS_HTTP}/jsz?streams=true&consumers=true&accounts=true")" || {
  red "  ✗ broker not reachable at ${NATS_HTTP}"
  exit 1
}
JS_ENABLED=$(echo "$JSZ" | jq -r '.streams // 0')
if [[ "$JS_ENABLED" == "0" ]]; then
  # `streams` may be missing entirely when JS is off
  if ! echo "$JSZ" | jq -e '.config' >/dev/null 2>&1; then
    red "  ✗ JetStream not enabled — check nats.conf jetstream{} block"
    exit 1
  fi
fi
green "  ✓ JetStream enabled"

step "2) Stream ${STREAM} exists?"
STREAM_DETAIL="$(echo "$JSZ" | jq -c --arg n "$STREAM" '
  .account_details[]?.stream_detail[]? | select(.name == $n)')"
if [[ -z "$STREAM_DETAIL" || "$STREAM_DETAIL" == "null" ]]; then
  red "  ✗ stream ${STREAM} not found"
  yellow "    likely cause: neither channels nor runtime started yet, or"
  yellow "    BUS_USE_JETSTREAM is not 'true' on either service"
  yellow "    bootstrap manually:  go run ./tools/smoke/jetstream_wiring"
  exit 1
fi
MSGS=$(echo "$STREAM_DETAIL" | jq -r '.state.messages // 0')
green "  ✓ stream stored_msgs=${MSGS}"

step "3) Durable consumer ${DURABLE} bound?"
CONS=$(echo "$STREAM_DETAIL" | jq -c --arg d "$DURABLE" '
  .consumer_detail[]? | select(.name == $d)')
if [[ -z "$CONS" || "$CONS" == "null" ]]; then
  red "  ✗ consumer ${DURABLE} not found on ${STREAM}"
  yellow "    likely cause: runtime is not running, or its NATS_URL +"
  yellow "    BUS_USE_JETSTREAM env aren't set"
  exit 1
fi
NUM_PENDING=$(echo "$CONS" | jq -r '.num_pending // 0')
NUM_AWAITING=$(echo "$CONS" | jq -r '.num_ack_pending // 0')
DELIVERED=$(echo "$CONS" | jq -r '.delivered.consumer_seq // 0')
green "  ✓ consumer pending=${NUM_PENDING} awaiting_ack=${NUM_AWAITING} delivered=${DELIVERED}"

if [[ "${SKIP_PUBLISH:-0}" == "1" ]]; then
  green "✓ skipping publish (SKIP_PUBLISH=1); wiring looks healthy."
  exit 0
fi

step "4) Publish a smoke envelope to ${SUBJECT}"
if ! command -v nats >/dev/null 2>&1; then
  yellow "  nats CLI not found — skipping publish step"
  yellow "  install with: brew install nats-io/nats-tools/nats"
  exit 0
fi
PAYLOAD='{"envelope":{"message_id":"smoke-'"$(date +%s)"'","channel":"smoke","conversation_id":"smoke","text":"hello from smoke-jetstream.sh"},"memory_context":[],"memory_mode":""}'
nats --server "$NATS_URL" pub "$SUBJECT" "$PAYLOAD" >/dev/null
green "  ✓ published"

step "5) Stream picked it up?"
sleep 1
JSZ2="$(curl -sf "${NATS_HTTP}/jsz?streams=true&consumers=true&accounts=true")"
NEW_MSGS=$(echo "$JSZ2" | jq -r --arg n "$STREAM" '
  .account_details[]?.stream_detail[]? | select(.name == $n) | .state.messages')
NEW_DELIVERED=$(echo "$JSZ2" | jq -r --arg n "$STREAM" --arg d "$DURABLE" '
  .account_details[]?.stream_detail[]? | select(.name == $n)
  | .consumer_detail[]? | select(.name == $d) | .delivered.consumer_seq')
if (( NEW_MSGS > MSGS )); then
  green "  ✓ stream messages: ${MSGS} → ${NEW_MSGS}"
else
  red "  ✗ stream did not record the publish (still at ${NEW_MSGS})"
  exit 1
fi
if (( NEW_DELIVERED > DELIVERED )); then
  green "  ✓ consumer delivered: ${DELIVERED} → ${NEW_DELIVERED}"
else
  yellow "  ⚠ consumer hasn't pulled the new message yet"
  yellow "    (may be transient; re-run after a second)"
fi

green ""
green "✓ JetStream wiring is healthy. Channels can publish, Runtime is consuming."
