#!/bin/sh
# Fake stdio MCP server for biu Layer I integration tests.
#
# Implements the JSON-RPC subset biu's internal/mcp/stdio.go talks to:
#
#   initialize             → returns serverInfo + capabilities
#   notifications/initialized  (no response)
#   tools/list             → 1 echo tool
#   tools/call name=echo   → echoes args.msg
#   resources/list         → 1 example resource
#   resources/read uri=... → returns text body
#   prompts/list           → 1 example prompt
#   prompts/get name=...   → returns prompt text
#
# The fixture's "shape" is configurable via env so a single script
# can stand in for several scenarios:
#
#   BIU_FIXTURE_NAME           server name reported in initialize (default "fake")
#   BIU_FIXTURE_TOOLSET        "default" (echo only) | "extended" (echo+upper)
#                              | "minimal" (no tools at all). Used by I5 to
#                              prove the catalog-diff log fires.
#   BIU_FIXTURE_PING           "1" (default) ⇒ honour ping requests
#                              "0"           ⇒ silent on ping (forces health monitor
#                                              to call retry path; used by I4)
#   BIU_FIXTURE_INIT_FAIL      "1" ⇒ reply with error to initialize. Used to
#                              exercise the bootstrap-failure path.

case "${BIU_FIXTURE_TOOLSET:-default}" in
  minimal)
    TOOLS_JSON='[]'
    ;;
  extended)
    TOOLS_JSON='[{"name":"echo","description":"echoes its input","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}},{"name":"upper","description":"uppercases the input","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]'
    ;;
  *)
    TOOLS_JSON='[{"name":"echo","description":"echoes its input","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}}]'
    ;;
esac

NAME="${BIU_FIXTURE_NAME:-fake}"
PING_OK="${BIU_FIXTURE_PING:-1}"
INIT_FAIL="${BIU_FIXTURE_INIT_FAIL:-0}"

while IFS= read -r line; do
  # Strip the request id once for reuse below.
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')

  case "$line" in
    *'"method":"initialize"'*)
      if [ "$INIT_FAIL" = "1" ]; then
        echo '{"jsonrpc":"2.0","id":'"${id:-1}"',"error":{"code":-32603,"message":"forced init failure"}}'
      else
        echo '{"jsonrpc":"2.0","id":'"${id:-1}"',"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"'"$NAME"'","version":"0.1"},"capabilities":{"tools":{},"resources":{},"prompts":{}}}}'
      fi
      ;;

    *'"method":"notifications/initialized"'*)
      # No response per spec.
      ;;

    *'"method":"ping"'*)
      if [ "$PING_OK" = "1" ]; then
        echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{}}'
      fi
      # PING_OK=0 → silent: the client's read loop times out and
      # the health monitor flags this server unhealthy.
      ;;

    *'"method":"tools/list"'*)
      echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{"tools":'"$TOOLS_JSON"'}}'
      ;;

    *'"method":"tools/call"'*)
      # Pull the tool name + msg / text arg out of the params blob.
      tool=$(echo "$line" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
      msg=$(echo "$line" | sed -n 's/.*"msg":"\([^"]*\)".*/\1/p')
      text=$(echo "$line" | sed -n 's/.*"text":"\([^"]*\)".*/\1/p')
      case "$tool" in
        echo)
          echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{"content":[{"type":"text","text":"echoed: '"$msg"'"}]}}'
          ;;
        upper)
          # Naive uppercase via tr (POSIX-portable).
          up=$(echo "$text" | tr '[:lower:]' '[:upper:]')
          echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{"content":[{"type":"text","text":"'"$up"'"}]}}'
          ;;
        *)
          echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"error":{"code":-32601,"message":"unknown tool: '"$tool"'"}}'
          ;;
      esac
      ;;

    *'"method":"resources/list"'*)
      echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{"resources":[{"uri":"fake://docs/readme","name":"readme","description":"Fake README","mimeType":"text/plain"}]}}'
      ;;

    *'"method":"resources/read"'*)
      uri=$(echo "$line" | sed -n 's/.*"uri":"\([^"]*\)".*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{"contents":[{"uri":"'"$uri"'","mimeType":"text/plain","text":"FIXTURE-RESOURCE-BODY-ZX9K"}]}}'
      ;;

    *'"method":"prompts/list"'*)
      echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{"prompts":[{"name":"greet","description":"greets a person","arguments":[{"name":"name","required":true}]}]}}'
      ;;

    *'"method":"prompts/get"'*)
      name=$(echo "$line" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"${id:-0}"',"result":{"description":"greets","messages":[{"role":"user","content":{"type":"text","text":"Greet '"$name"' enthusiastically."}}]}}'
      ;;

    *)
      # Fall through: return method-not-found if we got an id, else silent.
      if [ -n "$id" ]; then
        echo '{"jsonrpc":"2.0","id":'"$id"',"error":{"code":-32601,"message":"method not implemented in fixture"}}'
      fi
      ;;
  esac
done
