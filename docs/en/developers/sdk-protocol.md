# SDK Protocol (WebSocket bidirectional stream)

The bidirectional streaming protocol between the client / CLI and the agent runtime: JSON text frames over a single WebSocket, carrying three groups of frames — the **data plane** (chat messages), the **control plane** (permission requests / interrupts / configuration), and **lifecycle** (heartbeats / disconnect recovery). The authoritative definitions of the protocol are the in-repo JSON Schema ([`schema/sdk/v1/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/README.md)) and the Go implementation ([`packages/go-sdk/biu/sdkproto/v1/`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/unmarshal.go)); everything in this document defers to those two.

The protocol has two access surfaces:

| Access surface | Server | Use case |
|---|---|---|
| **Cloud Agent Plane** | Brain service (`/v1/agent/sessions/...`) | The Flutter client, or any third-party UI remotely driving BiuMind-hosted sessions |
| **Local biu daemon bridge** | `biu serve` / `biu bridge` process (loopback HTTP + WS) | IDEs / local tools driving the biu engine on the user's own machine |

The two surfaces use **exactly the same frame format** (the same `sdkproto.Frame` set); they differ only in how sessions are established and in reconnect mechanics (see [Transport](#2-transport-layer)).

## 1. Overview: four frame planes

Every WS message is a single JSON object; the first-level discriminator field is `type`:

| Plane | `type` values | Direction | Definition |
|---|---|---|---|
| Data plane | `user` / `assistant` / `stream_event` / `result` / `system` / `auth_status` / `rate_limit_event` / `prompt_suggestion` / `tool_progress` / `tool_use_summary` / `streamlined_text` / `streamlined_tool_use_summary` | Bidirectional (mostly server → client) | [`schema/sdk/v1/data/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/data/index.json) |
| Control plane | `control_request` / `control_response` / `control_cancel_request` | Bidirectional | [`schema/sdk/v1/control/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/control/initialize.json) |
| Lifecycle | `keep_alive` / `update_environment_variables` / `biumind.*` (8 in total) | Bidirectional (with direction restrictions) | [`schema/sdk/v1/lifecycle.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/lifecycle.json) |
| Coding module | `code_request` / `code_response` / `code_pty_*` / `code_session_event` (7 in total) | Bidirectional | [`packages/go-sdk/biu/sdkproto/v1/code.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/code.go); used only by the local bridge's `/v1/code/ws` channel |

The general parsing entry point is [`UnmarshalFrame`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/service.go): first dispatch on `type` to a plane, then apply that plane's second-level discrimination (see below). The protocol is a **closed set** — the `isFrame()` method of the `Frame` interface is unexported, so external packages cannot add wire types (the comments in `service.go` state this constraint explicitly).

## 2. Transport layer

### 2.1 Cloud Agent Plane

#### Endpoint addressing

The client configures a single "server address". In the official deployment, all paths are reverse-proxied through the site nginx (the `location /v1/agent/` in [`web/site/nginx.conf`](https://github.com/biumind/biumind/blob/main/web/site/nginx.conf), upstream `http://brain:7003`). That location has the necessary WS configuration: `proxy_http_version 1.1` + `Upgrade` header passthrough, `proxy_read_timeout 600s`, and **`proxy_buffering off`** (without disabling buffering, nginx holds server frames back instead of flushing them).

Session establishment is two steps — "HTTP first, then WS":

**Step 1: create a session** (`POST /v1/agent/sessions`, authenticated with `Authorization: Bearer <JWT or PAT>`)

Core request body fields (the full definition is `CreateSessionAPIReq` in [`services/brain/internal/agentplane/router.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/router.go)):

```json
{
  "mode": "chat",
  "thread_id": "optional; links to a chat thread",
  "model": "optional; when left empty the server resolves it via the default chain",
  "prompt": "user input for this turn (the first message in chat / agent / task mode)",
  "environment_id": "required in agent mode; leave empty for chat / task",
  "workdir": "working directory for agent / task mode",
  "images": [{ "mime_type": "image/png", "data": "<base64, without the data: prefix>" }]
}
```

`mode` is one of three (the `Mode` enum in `biumind_ext.json`):

- `chat`: the Brain process drives the model directly, with no execution environment attached;
- `agent`: dispatched to your own device (an environment registered by `biu_daemon` / `biu_cli`); `environment_id` is required;
- `task`: automatically picks an execution environment from the online runtime pool.

Success response `201` (`writeSessionCreated`, router.go):

```json
{
  "session_id": "b0b6a3e2-…",
  "session_token": "<short-lived JWT, 30 minutes>",
  "expires_at": 1735689600000,
  "mode": "chat",
  "state": "active",
  "jetstream_subject_in": "biu.session.<sid>.in",
  "jetstream_subject_out": "biu.session.<sid>.out",
  "created_at": 1735687800000
}
```

> [!NOTE]
> A `state` of `pending` means the target device is currently offline and the task is queued (it is dispatched automatically once the device comes online) — do not connect the WS in this state; resume after the device is back online. Sessions bound to an execution environment (`agent` / `task`) additionally return `environment_id`; sessions created with a `thread_id` additionally return `thread_id`.

**Step 2: connect the WS stream**

```text
GET /v1/agent/sessions/{session_id}/stream?session_token=<30-minute JWT>[&since_seq=<n>]
```

- Authentication uses `session_token`: the query parameter is preferred (browser WS clients cannot easily set headers), but the `Authorization: Bearer` header is also accepted. The token's scope must match the session_id in the URL (connecting to session B with session A's token is rejected).
- Before the token expires, call `POST /v1/agent/sessions/{id}/refresh-token` (with a long-lived credential) to obtain a new one. The token is validated when the connection is established, so a new token takes effect on the next (re)connect — the Flutter client refreshes on a timer 5 minutes before expiry (`_scheduleTokenRefresh`).
- **No WS subprotocol negotiation** (the server's gorilla `Upgrader` has no `Subprotocols` configured; clients need not send `Sec-WebSocket-Protocol`); frames are always **WS text frames**, one JSON object per message, with no outer envelope.
- If the session is already in a terminal state (`completed` / `failed` / `cancelled`), connecting returns `409 session_finalized`; the response points you to `GET /v1/agent/sessions/{id}/result` for the final result.

Implementation: [`services/brain/internal/agentplane/ingress.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/ingress.go).

#### Heartbeats and frame size limits

- **WS protocol layer**: the server sends a ping every 30 seconds and disconnects if no pong arrives within 60 seconds (`ingressPingPeriod` / `ingressPongWait`). Clients only need to answer with a standard pong; no application-level heartbeat is required.
- **Frame size**: a single inbound frame is capped at 32 KB (`ingressMaxFrameSize`); LLM streaming chunks are usually far smaller than this.
- The cloud path does **not** periodically send `keep_alive` application frames (there is no KeepAlive publishing code on the brain side); `keep_alive` frames currently appear only on the local bridge (see 2.2).

#### Reconnect: `?since_seq=N`

The server writes every downstream frame of each session into NATS JetStream (stream `BIU_SESSIONS`, subject `biu.session.<sid>.out`, **retained for 1 hour**). On reconnect, pass `?since_seq=<highest sequence number you have seen>`:

- `since_seq` omitted or 0: live pull — only new frames;
- `since_seq > 0`: the server uses an OrderedConsumer to **replay historical frames** starting from `since_seq + 1`, then seamlessly continues into the live stream;
- if the history you want has already been trimmed by the stream retention policy (`since_seq + 1 < the stream's current earliest sequence number`), the server pushes one `biumind.session_desynced` frame and then closes the connection normally — the client falls back to fetching the session's final result via the frame's `final_result_url`.

```json
{
  "type": "biumind.session_desynced",
  "session_id": "b0b6a3e2-…",
  "final_result_url": "/v1/agent/sessions/b0b6a3e2-…/result",
  "since_seq": 42,
  "reason": "requested seq 42 before stream first seq 100"
}
```

> [!WARNING]
> `since_seq` is "the highest sequence number the client **has already seen**", not "where to start reading from". Clients must persist the last-seen sequence number themselves (the Flutter `BiuSessionConnection` persists once every 10 frames processed). The session stream is retained in JetStream for only 1 hour; earlier frames cannot be replayed. Note also: v1 downstream frames **carry no server-side sequence-number field**, so the Flutter client approximates with a local received-frame count (`biu_client.dart` comments explicitly list "seq embedded in frames" as future work) — the replay starting point may drift slightly when other messages are mixed into the stream; integrators who need exact replay should assess this themselves (the local bridge's `last_event_id` does not have this problem, as its buffer stores only downstream frames).

Reference client reconnect implementation: [`apps/client/lib/data/api/biu_client.dart`](https://github.com/biumind/biumind/blob/main/apps/client/lib/data/api/biu_client.dart) — exponential backoff from 1 to 30 seconds, at most 8 attempts; more than 2 consecutive failures trigger the token-refresh callback.

### 2.2 Local biu daemon bridge

`biu serve` (the daemon embedded in the desktop app) or `biu bridge` starts an HTTP + WS service on the local machine ([`apps/cli/biu/internal/bridge/`](https://github.com/biumind/biumind/blob/main/apps/cli/biu/internal/bridge/server.go)). Authentication is optional: when `Options.AuthToken` is non-empty, every request requires `Authorization: Bearer <token>`; when empty, there is no authentication (loopback / development environments only).

Routes (comments at the top of server.go):

| Route | Purpose |
|---|---|
| `POST /v1/code/sessions` | Create a session; returns `{ "id": "…" }` |
| `POST /v1/code/sessions/{id}/messages` | Submit a turn, body `{"prompt":"…"}`; the semantics are "abort the current turn + start a new one" |
| `GET /v1/code/sessions/{id}/ws` | WS stream (frame format identical to the cloud) |
| `GET /v1/code/ws` | Coding-module channel (`code_*` frames: PTY / Git / file system) |
| `GET /v1/code/sessions/{id}/cost` | Cost snapshot |
| `POST /v1/code/sessions/{id}/compact` | Manually compact the context |
| `POST /v1/code/sessions/{id}/attachments` | Upload an attachment |
| `DELETE /v1/code/sessions/{id}` | Close a session |

#### Reconnect: `?last_event_id=N` (ring buffer)

Unlike the cloud's JetStream approach, the local bridge keeps an **in-memory ring buffer of 256 frames** per session (`eventBufferCap`, server.go): each frame is assigned a monotonically increasing id; on reconnect, pass `?last_event_id=N` to replay the frames with ids greater than N, then continue with the live stream.

- If no task is running and no resume cursor is passed, the connection returns `409 no turn in progress`;
- when the task has already finished (and the buffer is still present), the server pushes one `keep_alive` frame as the "stream ended normally" sentinel after replay, then closes the connection with `CloseNormalClosure("done")`.

Heartbeats are likewise WS protocol-level pings (every 30 seconds; disconnected after 60 seconds without a pong), with parameters identical to the cloud's (`wsPingPeriod` / `wsPongWait`, [`ws.go`](https://github.com/biumind/biumind/blob/main/apps/cli/biu/internal/bridge/ws.go)).

> [!NOTE]
> The recovery cursors of the two surfaces are **not interchangeable**: the cloud uses `since_seq` (a JetStream message sequence number), the local one uses `last_event_id` (an in-process ring buffer sequence number). Both are only meaningful within the lifetime of their own process / stream.

### 2.3 How frames flow on the server (cloud)

Understanding a frame's path helps with troubleshooting. The journey of one cloud frame:

```text
Client ──WS──▶ brain ingress ──publish──▶ biu.session.<sid>.in (JetStream)
brain ingress ◀─subscribe── biu.session.<sid>.out ◀─publish── executor (chat: in the brain process; agent/task: a worker)
```

Two kinds of inbound frames **bypass** the `.in` subject and go straight to the executor through a control queue (ingress.go):

- `control_cancel_request` — triggers a session interrupt;
- `control_response` — permission answers / elicitation form answers (routed by `request_id`).

All other inbound frames are published verbatim to the `.in` subject for archival. **The executor currently does not consume data frames on the `.in` subject** (multi-turn user messages sent directly over WS are not wired up yet), so the standard way to run a cloud turn today is: the prompt goes in the session-creation HTTP request, and the WS only receives frames and sends control frames.

## 3. Message model

### 3.1 Discrimination rules (union discriminator)

Frame discrimination is two steps — "first `type`, then a second-level field" ([`unmarshal.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/unmarshal.go) and [`wrappers.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/wrappers.go)):

1. the `type` field decides the plane (data / control / lifecycle / coding);
2. within the data plane:
   - frames with `type: "system"` further use `subtype` to distinguish 16 subtypes;
   - `type: "result"` uses **`is_error`** to distinguish success / failure (not `subtype` — the two result shapes share most fields, with `subtype` values of `"success"` / `"error_*"` respectively);
   - every other `type` maps one-to-one to a struct;
3. within the control plane: the `request` object of `control_request` uses `subtype` to distinguish 21 request kinds.

Most structs on each plane carry these common fields (see each struct definition for specifics):

- `uuid`: the unique identifier of this frame (frame-level, not session-level);
- `session_id`: the owning session;
- `parent_tool_use_id`: in nested sub-agent scenarios, the id of the parent tool call this message belongs to (may appear on `user` / `assistant` / `stream_event` / `tool_progress`);
- frames with `type: "system"` additionally carry `subtype`.

At the schema level all objects are **open by default** (`additionalProperties: true`; see the [`_common.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/_common.json) description): the server may attach future fields, and client parsing must tolerate unknown fields.

> [!NOTE]
> Some code comments still cite the historical counts of "24 / 29 variants", which do not match the implementation. **The authoritative list is `isSDKMessage()` in [`unmarshal.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/unmarshal.go): 28 data-plane variants in total** (listed one by one in the table below; the union in `data/index.json` also has 28 references).

### 3.2 Data plane: 28 variants

| `type` | (`subtype`) | Go struct | Meaning |
|---|---|---|---|
| `user` | — | `SDKUserMessage` | User message (including tool-result passback via the `tool_use_result` field). The replay form with `isReplay=true` is listed separately in the schema as `SDKUserMessageReplay`; the Go side uses the same struct |
| `assistant` | — | `SDKAssistantMessage` | A complete assistant message for the turn; `message.content` is an array of content blocks (`text` / `tool_use`, etc.) |
| `stream_event` | — | `SDKPartialAssistantMessage` | Raw incremental events from the upstream model stream (`event` is passed-through JSON) |
| `result` | `success` | `SDKResultSuccess` | End of a turn (success). Includes `duration_ms` / `num_turns` / `total_cost_usd` / `usage` / `modelUsage` / `stop_reason` |
| `result` | `error_*` (`is_error=true`) | `SDKResultError` | End of a turn (failure). Same as above + an `errors` array |
| `system` | `init` | `SDKSystemInit` | Session initialization snapshot (tool inventory / model / permission mode, etc.) |
| `system` | `status` | `SDKSystemStatus` | Engine status |
| `system` | `compact_boundary` | `SDKCompactBoundary` | Context-compaction completion boundary |
| `system` | `api_retry` | `SDKAPIRetry` | Upstream model call retry notification |
| `system` | `local_command_output` | `SDKLocalCommandOutput` | Local command output |
| `system` | `hook_started` | `SDKHookStarted` | A hook started executing |
| `system` | `hook_progress` | `SDKHookProgress` | Intermediate output of a running hook |
| `system` | `hook_response` | `SDKHookResponse` | Hook execution result |
| `system` | `files_persisted` | `SDKFilesPersisted` | File persistence completed |
| `system` | `task_notification` | `SDKTaskNotification` | Background task notification |
| `system` | `task_started` | `SDKTaskStarted` | Background task started |
| `system` | `task_progress` | `SDKTaskProgress` | Background task progress |
| `system` | `session_state_changed` | `SDKSessionStateChanged` | Session state change (`idle` / `running` / `requires_action`) |
| `system` | `elicitation_complete` | `SDKElicitationComplete` | An MCP-server-initiated elicitation flow finished |
| `system` | `form_answer` | `SDKFormAnswer` | Terminal state of an elicitation form (`action`: `accept` / `decline` / `cancel` / `timeout`) |
| `system` | `post_turn_summary` | `SDKPostTurnSummary` | Structured summary produced after a turn |
| `auth_status` | — | `SDKAuthStatus` | Authentication status change |
| `rate_limit_event` | — | `SDKRateLimitEvent` | Upstream rate-limit event |
| `prompt_suggestion` | — | `SDKPromptSuggestion` | Input suggestion |
| `tool_progress` | — | `SDKToolProgress` | A tool call in progress (with `tool_use_id` / `tool_name` / elapsed time) |
| `tool_use_summary` | — | `SDKToolUseSummary` | Tool call result summary (`preceding_tool_use_ids` links back to the `tool_use_id`) |
| `streamlined_text` | — | `SDKStreamlinedText` | **The recommended streaming text delta** — a lightweight frame the client concatenates and renders directly |
| `streamlined_tool_use_summary` | — | `SDKStreamlinedToolUseSummary` | Lightweight tool result summary |

(In the table, the `success` / `error_*` entries in the "(`subtype`)" column are the two shapes of the `result` frame; the 16 `system` subtypes each count as one variant.)

Field-level definitions per category are in [`schema/sdk/v1/data/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/data/index.json) (`user.json` / `assistant.json` / `result.json` / `system.json` / `tool.json` / `post_turn.json` / `streamlined.json`) and [`data.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/data.go).

> [!NOTE]
> The same piece of streaming text may arrive in two forms: as `streamlined_text` delta frames, and as `text` blocks inside the `assistant` frame's `message.content` (the authoritative complete copy). The Flutter client's policy: when a streaming text block already exists, skip the `text` items in the `assistant` frame to avoid double rendering (`_onAssistantMessage` in `biu_session_connection.dart`).

### 3.3 Control plane

Three top-level frames:

**`control_request`** (`{type, request_id, request:{subtype, …}}`), with 21 possible `request.subtype` values ([`control.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/control.go)):

| `subtype` | Purpose |
|---|---|
| `initialize` | Session initialization configuration (hooks / MCP servers / system prompt / agents, etc.) |
| `interrupt` | Interrupt the current turn (local bridge path) |
| `can_use_tool` | **Server → client** permission request (`tool_name` / `input` / `tool_use_id`, etc.) |
| `set_model` | Switch the model |
| `set_permission_mode` | Switch the permission mode (`default` / `acceptEdits` / `bypassPermissions` / `plan` / `dontAsk`) |
| `set_max_thinking_tokens` | Adjust the thinking budget |
| `mcp_status` / `mcp_message` / `mcp_set_servers` / `mcp_reconnect` / `mcp_toggle` | MCP server management |
| `get_context_usage` | Query context usage |
| `hook_callback` | Answer a pending server-side hook callback |
| `rewind_files` | Roll files back to the state at a given user message |
| `cancel_async_message` | Cancel a specific async message |
| `seed_read_state` | Pre-seed file read state |
| `reload_plugins` | Reload plugins |
| `stop_task` | Stop a background task |
| `apply_flag_settings` / `get_settings` | Read / write settings |
| `elicitation` | **Server → client** elicitation form (`message` / `mode: form\|url` / `requested_schema`) |

**`control_response`** (`{type, kind?, response:{subtype, request_id, response?, error?}}`) — the answer to a given `request_id`, where `subtype` is `success` / `error`. The `kind` field (`elicitation_response` / `permission_response`) explicitly declares the reply type; the server remains compatible with older clients that omit it (distinguishing by the shape of the reply body). Two common reply bodies:

- Permission answer: `{"behavior": "allow" | "deny", "updatedInput": …, "updatedPermissions": …}` (full definition in `PermissionResult` in [`permissions.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/permissions.json));
- Form answer: `{"action": "accept" | "decline" | "cancel", "content": {…}}`.

**`control_cancel_request`** (`{type, request_id}`) — cancels the current execution of the whole session; it is a standalone top-level type, distinct from `cancel_async_message` (a subtype).

### 3.4 Lifecycle: 8 BiuMind-proprietary frames

[`lifecycle.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/lifecycle.json) / [`lifecycle.go`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/lifecycle.go):

| `type` | Direction | Meaning |
|---|---|---|
| `keep_alive` | Bidirectional | Heartbeat / stream-end sentinel (`ts` is a millisecond timestamp) |
| `update_environment_variables` | Client → server | Update session environment variables |
| `biumind.session_desynced` | Server → client | The requested history sequence number has been trimmed; fetch the final result via `final_result_url` |
| `biumind.session_paused` | Server → client | Session paused (e.g. the executor went offline while waiting for a form answer) |
| `biumind.session_resumed` | Server → client | A paused session has been resumed; the stream continues |
| `biumind.session_primary_promoted` | Server → client | Primary-replica switch notification in multi-replica scenarios |
| `biumind.compact_started` | Server → client | Context compaction started (`reason` / `tokens_before`) |
| `biumind.compact_finished` | Server → client | Compaction finished (`tokens_before` / `tokens_after` / `tokens_saved`) |

### 3.5 Coding module: 7 `code_*` frames

Only on the local bridge's `GET /v1/code/ws` channel (`code.go`): `code_request` / `code_response` are the generic RPC envelope (`method` dispatches `git.status` / `fs.read` / `pty.open`, etc.); `code_pty_chunk` / `code_pty_input` / `code_pty_resize` / `code_pty_exit` are the PTY byte stream (bytes are automatically base64-encoded in JSON, lossless transport); `code_session_event` is a structured session event (the parsed JSONL events of an external coding-agent session).

### 3.6 Direction rules

The same frame set is used in both directions; the two unions in [`service.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/service.json) define the nominal directions:

- **StdinMessage (client → server)**: all `SDKMessage` + `control_request` + `control_cancel_request` + `update_environment_variables`;
- **StdoutMessage (server → client)**: all `SDKMessage` + `control_request` (reverse requests) + `control_response` + the 7 lifecycle frames other than `update_environment_variables`.

> [!NOTE]
> `control_response` actually **appears in both directions**: the server uses it to answer the client's `control_request` (interrupt requests on the local bridge), and the client uses it to answer server-initiated permission / form requests (servers on both surfaces handle this direction explicitly — see the read pump in [`ws.go`](https://github.com/biumind/biumind/blob/main/apps/cli/biu/internal/bridge/ws.go) and `maybeRoutePermissionResponse` in [`ingress.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/ingress.go)). The Go-side `IsStdinMessage` helper classifies `control_response` as not upstream, which is inconsistent with the actual send/receive paths and is used only for unit-test assertions — when integrating, follow the actual paths described above.

### 3.7 JSON Schema and validation

- Schema root entry points: [`schema/sdk/v1/index.json`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/index.json) (the `StdinMessage` / `StdoutMessage` unions) and `data/index.json` (the `SDKMessage` union);
- Instance samples (one validatable sample per frame): [`schema/sdk/v1/fixtures/`](https://github.com/biumind/biumind/blob/main/schema/sdk/v1/fixtures/system_init.json);
- Validation tooling: `make schema-validate` (repo root; the tool lives in `tools/schema-validate/main.go`, schema draft 2020-12).

> [!WARNING]
> A fixture's `$schema` field must point to a concrete `$defs/<Type>` (the schema files have only `$defs` at the top level and no constraints — pointing at the file root makes validation vacuously pass).

## 4. Session lifecycle and event stream

### 4.1 The standard frame sequence of a turn (cloud chat mode)

The first user message arrives with the session-creation HTTP request (the `prompt` field); afterwards the following arrive in order over the WS (frame generation logic: [`FrameEmitter`](https://github.com/biumind/biumind/blob/main/services/brain/internal/chat/frame_emitter.go) in the brain process; the daemon / runtime worker paths are isomorphic):

```text
Server → client                     Description
streamlined_text          ×N        streaming text deltas (a small chunk per frame, concatenated by the client)
tool_progress                       a tool started executing
streamlined_tool_use_summary        (or tool_use_summary) tool result summary
streamlined_text          ×N        continued output after the tool result
assistant                           full message snapshot for the turn (content block array)
result (subtype=success)            end of turn: duration / turn count / usage / cost / stop_reason
```

The `result` frame is the **terminal signal of a turn**: once received, the client can finalize rendering. A `stop_reason` of `interrupted` means the user's cancellation took the clean-stop path — render it as "cancelled", not "failed".

On error, the turn ends with a `result` (`is_error: true`, `subtype: error_*`, with an `errors` array). Once the session enters a terminal state, the `result` frame determines the session's state (`completed` / `failed` / `cancelled`).

### 4.2 Permission requests (server → client → server)

When the executor wants to run a tool that requires authorization, it sends a reverse `control_request`:

```json
{
  "type": "control_request",
  "request_id": "9f2c…",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "Bash",
    "input": { "command": "rm -rf build" },
    "tool_use_id": "toolu_01…",
    "decision_reason": "destructive command"
  }
}
```

The client shows a card; after the user decides, it replies:

```json
{
  "type": "control_response",
  "kind": "permission_response",
  "response": {
    "subtype": "success",
    "request_id": "9f2c…",
    "response": { "behavior": "deny", "message": "user rejected" }
  }
}
```

> [!WARNING]
> Permission requests have a **30-second timeout**; a timeout or a failure to parse the reply is treated as `deny` ("deny by default" is the safe side — see the bridge's `askPermission` and the worker's same-named implementation). Pending requests are also denied when the client disconnects.

### 4.3 Elicitation forms

When the executor needs structured user input (choices / forms), it sends a `control_request` (`request.subtype: "elicitation"`, `mode: "form"`, with `requested_schema` being a JSON Schema-style description). The client renders the form and answers with a `control_response` (`kind: "elicitation_response"`, where `response.response` is `{"action":"accept","content":{"answer":"…"}}`). The server then pushes a `system` + `subtype: form_answer` terminal frame to settle the answer. If there is no answer, the server falls back to a roughly 5-minute timeout — the session does not hang.

### 4.4 Interrupts

- **Cloud**: the client sends `control_cancel_request` (`{"type":"control_cancel_request","request_id":"cancel-…"}`). The executor takes the clean-stop path: it emits a final `result` (`stop_reason: "interrupted"`) and synthesizes results for unfinished tool calls;
- **Local bridge**: send a `control_request` over the WS (`request.subtype: "interrupt"`); the server replies with a `control_response` (success or the `"no turn in progress"` error).

> [!NOTE]
> After sending a cancel, **do not close the connection immediately** — the clean stop needs time to deliver the final `result` frame. The Flutter client uses a 3-second grace window: receiving `stop_reason: "interrupted"` within the window counts as a clean cancel; otherwise it force-finalizes as cancelled (`_cancelGraceWindow`).

### 4.5 Pause and resume (durable resume)

If the executor goes offline while waiting for a form answer, brain marks the session `paused` and pushes `biumind.session_paused`. The client may still answer the pending form (late answers are persisted), then call `POST /v1/agent/sessions/{id}/resume` to re-run; the server pushes `biumind.session_resumed` and the streaming frames continue. Pending forms can be fetched back with `GET /v1/agent/sessions/{id}/elicitations` (routing in [`resume.go`](https://github.com/biumind/biumind/blob/main/services/brain/internal/agentplane/resume.go)).

### 4.6 Session teardown

- **Cloud**: after the `result` frame, the connection is **not** automatically closed by the server (you can keep waiting for the next turn / re-subscribe); the client closes it proactively once it confirms receipt. Connecting to `/stream` after the session has reached a terminal state yields `409 session_finalized`;
- **Local bridge**: when the current turn finishes, the server pushes one `keep_alive` sentinel frame and then proactively closes the connection with `CloseNormalClosure("done")`.

## 5. Pointers to the three implementations

| Language | Location | Notes |
|---|---|---|
| Go | [`packages/go-sdk/biu/sdkproto/v1/`](https://github.com/biumind/biumind/blob/main/packages/go-sdk/biu/sdkproto/v1/service.go) | The authoritative implementation. `UnmarshalFrame` (any frame) / `UnmarshalSDKMessage` / `UnmarshalControlRequestInner` / `UnmarshalLifecycle`; 105 unit tests cover round-trip fidelity |
| Dart | [`apps/client/lib/data/api/sdkproto/v1/`](https://github.com/biumind/biumind/blob/main/apps/client/lib/data/api/sdkproto/v1/service.dart) | Sealed classes + the `ServiceFrame.fromJson` factory (data classes like `sdk_message.dart`, with `json_serializable` generating the `.g.dart` files); on top of it [`BiuClient`](https://github.com/biumind/biumind/blob/main/apps/client/lib/data/api/biu_client.dart) (connection / reconnect / offline queue) and [`BiuSessionConnection`](https://github.com/biumind/biumind/blob/main/apps/client/lib/features/chat/data/biu_session_connection.dart) (frames → message-block rendering) |
| TypeScript | [`apps/miniapp/src/lib/sdkproto/v1/index.ts`](https://github.com/biumind/biumind/blob/main/apps/miniapp/src/lib/sdkproto/v1/index.ts) | Placeholder type aliases (plan is to vendor the Zod schema); currently only the `Mode` constants are usable |

The least-effort path for a homegrown client: generate parsing code directly from the JSON Schema in `schema/sdk/v1/` (the Go side was hand-written against it), and implement the parsing entry point with the two-step discrimination from 3.1.

## 6. Minimal integration example

The sequences below come from the code and schema fixtures (`fixtures/system_init.json`, `fixtures/user__basic.json`, `fixtures/result_success.json`, `fixtures/streamlined_text.json`, plus the serialized fields of `BiuClient.sendUserText` and the bridge's `askPermission`) and can be used directly as integration reference samples.

**Step 1 — create a session:**

```bash
curl -X POST https://your-biumind.example.com/v1/agent/sessions \
  -H "Authorization: Bearer <your PAT or JWT>" \
  -H "Content-Type: application/json" \
  -d '{"mode":"chat","prompt":"Take a look at the structure of this repo"}'
```

**Step 2 — connect the WS** (any language's standard WebSocket client):

```text
wss://your-biumind.example.com/v1/agent/sessions/<session_id>/stream?session_token=<session_token>
```

**Step 3 — receive frames and handle them.** Browser JavaScript skeleton:

```javascript
const ws = new WebSocket(
  `wss://your-biumind.example.com/v1/agent/sessions/${sessionId}/stream` +
  `?session_token=${sessionToken}`
);

let text = "";
ws.onmessage = (e) => {
  const frame = JSON.parse(e.data);          // one message = one JSON frame
  switch (frame.type) {
    case "streamlined_text":                 // streaming text: concatenate and render
      text += frame.text;
      break;
    case "assistant":                        // full snapshot of this turn (includes tool_use blocks)
      break;
    case "result":                           // end-of-turn signal
      if (frame.is_error) { /* show frame.errors */ }
      else { /* show frame.result / frame.usage / frame.total_cost_usd */ }
      ws.close();                            // the client closes proactively
      break;
    case "control_request":                  // reverse request: permission / form
      if (frame.request.subtype === "can_use_tool") {
        ws.send(JSON.stringify({
          type: "control_response",
          kind: "permission_response",
          response: {
            subtype: "success",
            request_id: frame.request_id,
            response: { behavior: "allow" }  // or { behavior: "deny", message: "…" }
          }
        }));
      }
      break;
    case "biumind.session_desynced":         // reconnect cursor expired: fetch the final result
      fetch(frame.final_result_url, { headers: { Authorization: "Bearer <long-lived credential>" } });
      break;
    default:
      break;                                 // unknown type / fields: tolerate, don't error
  }
};
```

**Reference: the wire shape of a user data frame** (client-upstream in multi-turn scenarios, or server-downstream when replaying history):

```json
{
  "type": "user",
  "message": { "role": "user", "content": [{ "type": "text", "text": "hi" }] },
  "uuid": "u1",
  "session_id": "s1"
}
```

**Reference: a successful result frame** (fixture `result_success.json`):

```json
{
  "type": "result",
  "subtype": "success",
  "duration_ms": 1000,
  "duration_api_ms": 800,
  "is_error": false,
  "num_turns": 3,
  "result": "done",
  "stop_reason": "end_turn",
  "total_cost_usd": 0.01,
  "usage": {},
  "modelUsage": {
    "claude-3-7": {
      "inputTokens": 10,
      "outputTokens": 20,
      "cacheReadInputTokens": 0,
      "cacheCreationInputTokens": 0,
      "webSearchRequests": 0,
      "costUSD": 0.01,
      "contextWindow": 200000,
      "maxOutputTokens": 4096
    }
  },
  "permission_denials": [],
  "uuid": "r1",
  "session_id": "s1"
}
```

**Reconnect**: record the number of frames you have processed as the cursor and pass it on reconnect (cloud `since_seq` / local `last_event_id`); the server first re-sends the missed frames, then seamlessly continues the live stream.
