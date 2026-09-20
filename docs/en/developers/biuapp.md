# BiuApp Development Guide

A BiuApp is the App Center's unit of extension: a self-contained capability package that declares which actions it supports, which permissions it needs, and which views it renders; the platform mounts it into the agent's tool list and the client's app panel. The same app is a set of callable tools to the agent and a set of declaratively rendered pages to the user — the two consumption surfaces share one manifest and one backend implementation.

This document is for third-party developers who want to build their own BiuApp and publish it to the App Center. All fields, commands, and interfaces are defined by the in-repo implementation.

> [!TIP]
> If you just want an external AI agent to read and write your knowledge base, you don't need a BiuApp — the [MCP tool service](api.md) is enough. BiuApps fit the scenario of "exposing a new capability to both the agent and the user interface at the same time".

## Table of contents

- [App shapes](#app-shapes)
- [Quick start](#quick-start)
- [Local development and debugging](#local-development-and-debugging)
- [manifest.yaml reference](#manifestyaml-reference)
- [Go implementation: the App contract](#go-implementation-the-app-contract)
- [Packaging and signing](#packaging-and-signing)
- [Walkthrough: the RSS app](#walkthrough-the-rss-app)
- [Publishing to the App Center](#publishing-to-the-app-center)
- [Runtime behavior](#runtime-behavior)
- [Validation rules quick reference](#validation-rules-quick-reference)

## App shapes

The manifest's `kind` field declares the app's shape; the values are:

| kind | Meaning |
|---|---|
| `backend` | Pure backend actions, no standalone UI. Used by the agent via tool calls |
| `view` | Pure view shell with no Go backend of its own; view data comes from existing platform APIs or self-declared actions |
| `hybrid` | Backend + declarative views — the most common full form |
| `webview` | Embedded external web page |
| `container` | Container form; not yet available — rejected at install time |

The scaffolder provides three templates matching three typical starting points:

```bash
biu app new my-app --from minimal      # backend-only: single action, no UI
biu app new my-app --from view_only    # view-only: no Go backend
biu app new my-app --from hybrid_full  # full form (default): actions + views + triggers + sidebar
```

The `<slug>` must be lowercase kebab-case (first character a letter; only `a-z`, `0-9`, `-`), and the target directory must be empty, otherwise the scaffolder refuses to run. The `{{slug}}` placeholder in the templates is replaced by the slug you provide.

## Quick start

Prerequisite: install the [biu CLI](../cli/getting-started.md).

```bash
# 1. Scaffold (hybrid_full template by default)
biu app new feed-hub
cd feed-hub   # directory name = the slug you passed

# 2. Validate the manifest
biu app validate

# 3. Inspect the parsed result (human-readable, or --json)
biu app inspect
biu app inspect --json

# 4. Start the local dev server (see the next section)
biu app run --dev --mock fixtures/

# 5. Package
biu app pack
```

All `biu app` subcommands:

| Command | Purpose |
|---|---|
| `biu app new <slug> [--from <template>]` | Scaffold a new project from a template |
| `biu app validate [--manifest <path>]` | Validate the manifest against platform rules; defaults to `./manifest.yaml` |
| `biu app inspect [--manifest <path>] [--json]` | Output the parsed manifest |
| `biu app run --dev [...]` | Local dev server (see the next section) |
| `biu app pack [--source DIR] [--out FILE] [--key PATH] [--unsigned]` | Package into a `.biuapp` distributable |
| `biu app verify <file.biuapp> [--trust-key PATH]` | Verify the package hashes and signatures |
| `biu app keygen [--name publisher]` | Generate an ed25519 publisher key pair |

`validate` lists **all** problems in one pass (in the form `path [error code]: description`) — no need to fix one typo per run.

## Local development and debugging

```bash
biu app run --dev \
  [--source DIR]          # app source directory; defaults to the current directory
  [--addr 127.0.0.1:7099] # dev server bind address; loopback only
  [--mock fixtures/]      # mock mode: answer invokes from fixture files
  [--no-subproc]          # do not start the go run subprocess (for pure view apps)
```

The dev server exposes the following endpoints, which the desktop client uses to discover and load your app in the "In development" panel:

```text
GET  /v1/dev/health              liveness probe
GET  /v1/dev/apps                list of in-development apps + manifests
GET  /v1/dev/apps/{slug}/manifest
POST /v1/dev/apps/{slug}/invoke  invoke an action (mock mode reads a fixture)
GET  /v1/dev/events              SSE status stream (manifest reloads / subprocess logs, etc.)
```

Behavior details:

- On startup it parses and validates the manifest first; on failure it aborts immediately.
- It watches `manifest.yaml` and `*.go` for changes: a manifest change triggers reload and re-validation; a Go source change restarts the `go run` subprocess automatically.
- Keys while running: `r` manually restarts the subprocess, `q` quits, `h` shows help.

### Mock mode

In the current version the dev server does **not** proxy invokes into the Go subprocess — local debugging of backend actions is driven by mock fixtures:

```text
fixtures/
  list_recent.json        # matched first as <action>.json
  feed-hub.list_recent.json  # or as <slug>.<action>.json
```

A fixture's content is the action's JSON return value. Mock mode is fully sufficient for front-end view integration (layout, template interpolation, toolbar interaction); the real backend path is verified by packing with `biu app pack` and installing onto the server.

## manifest.yaml reference

The manifest is the app's single source of truth. The mapping between YAML keys and the internal model (only two places differ from intuition):

- `identifier:` → the app slug (the unique routing / registration key); it also fills the internal legacy Name field.
- `name:` → the **display name** (the human-facing title), not the slug.

Unknown YAML keys are ignored rather than rejected, so a manifest written by a newer SDK still parses on older tooling; strictness is enforced by `biu app validate` and the install path.

### Base fields

```yaml
identifier: feed-hub        # required. kebab-case, or the marketplace-scoped <author>/<slug> form
version: 0.1.0              # required. semantic version (x.y.z[-pre])
name: Feed Hub              # display name
description: Aggregate all your information sources # required, ≤ 200 characters
author: Zhang San           # either a string or an object (see below)
icon: 📡                    # empty / emoji (≤8 bytes) / https URL / cas:<sha256>
category: productivity      # productivity|content|data|comm|dev|utility
kind: hybrid                # backend|view|hybrid|webview|container (container not yet available)
```

The object form of `author` (`public_key` is required for signed marketplace listings):

```yaml
author:
  name: Zhang San
  url: https://example.com
  public_key: ed25519:<base64 public key>   # output of biu app keygen
```

### permissions

The app's declared list of platform permissions, shown to the user item by item at install time for confirmation. Each entry has the form `prefix` or `prefix:parameter`; the legal prefixes:

| Prefix | What it authorizes |
|---|---|
| `net.outbound` | Outbound network requests; may carry a parameter restricting domains, e.g. `net.outbound:*.example.com` |
| `model-relay.invoke` | Calling the platform model gateway (the unified egress for LLM / embedding / billing) |
| `wiki.read` / `wiki.write` | Knowledge base document read / write |
| `graph.read` / `graph.write` | Knowledge graph read / write |
| `memory.read` / `memory.write` | Memory read / write |
| `files.read` / `files.write` | File object read / write |
| `cron.register` | Register scheduled jobs |
| `webhook.register` | Register webhook callbacks |
| `notify.send` | Send notifications |
| `sandbox.exec` | Execute commands in the cloud sandbox |
| `oauth:<provider>` | Complete an OAuth authorization flow |
| `secrets.read:<provider>` | Read managed credentials |

> [!NOTE]
> `hub.invoke` is a legacy alias of `model-relay.invoke`, still accepted in old manifests; new apps should write `model-relay.invoke` directly.

The permissions granted by the user at install time must be a **subset** of the declared list; anything beyond is rejected by the server with `permissions_exceed` (HTTP 400). Permission declarations feed into the platform's Cedar policy engine for fine-grained decisions, so the narrower the declaration, the lower the trust cost for users installing the app.

### data_scopes

```yaml
data_scopes:
  - wiki:collection:feed-hub
```

Free-form data scope declarations, used to tell the user "which data domains this app will touch", and consumed by the platform's isolation policies.

### actions

Each action is a callable unit — a tool on the agent side, and usable on the view side as a `data_source` or form submit target:

```yaml
actions:
  - name: add_item                 # required. ^[a-z][a-z0-9_-]*$, unique within the app
    description: Add an entry        # strongly recommended; the agent relies on it to know when to call
    risk: low                      # low|medium|high; determines the runtime approval policy
    input_schema:                  # JSON Schema fragment (object type)
      type: object
      required: [title]
      properties:
        title:
          type: string
          title: Title
    output_schema: ...
    # Optional enhancement fields below:
    human_intervention: required   # never|optional|required; required forces human confirmation every time
    timeout_ms: 30000              # 0–600000; 0 = server default
    streamable: true               # when declared, uses the Stream interface instead of Invoke
    rate_limit:                    # rate limiting per install
      per_minute: 10
      per_hour: 100
      per_day: 1000
```

`risk` drives the platform's permission mode (auto-allow / ask / high-risk quarantine), and `human_intervention: required` can force an action to require human confirmation regardless of user preference — suitable for destructive or payment-type operations.

### views

Each view declares a client route and how it renders. Routes must start with `/apps/<identifier>`:

```yaml
views:
  - id: home                       # unique within the app
    route: /apps/feed-hub
    title: Home                    # supports i18n keys
    layout: list_detail            # see the table below
    data_source:
      action: list_items           # must be a declared action
      input:
        limit: 20
    refresh_on:                    # subscribe to Realtime topics to invalidate the cache; <self> is replaced at runtime with the install id
      - "app:install:<self>:item_added"
    item_template: ...
    toolbar: ...
```

Layout values and each layout's required fields:

| Layout | Description | Required fields |
|---|---|---|
| `list` | List | `item_template` |
| `list_detail` | List + detail page | `item_template`, `detail_view` (a sub-view id) |
| `form` | Form | `schema_ref` and/or `submit` |
| `webview` | Embedded web page | `url` |
| `grid` | Grid | `item_template`, optional `grid` |
| `dashboard` | Card panel | `cards` (each card has `id`, `kind: text\|number\|list\|chart`, `span` 1–12, optional `data_source` / `field` / `format`) |
| `agent_chat` | Embedded agent chat | `agent_id`; optional `agent_chat.initial_prompt` / `tool_filter` / `system_prompt_override` |
| `custom` | App-custom rendering | as agreed with the platform client |

**Template interpolation**: list-item fields reference elements of the `data_source` return value's items with `${item.<field>}`, with stackable filters, for example:

```text
${item.title}
${item.updated_at | relative_time}
${item.summary | truncate(120)}
${item.url | domain}
${item.last_status | default(waiting for first fetch)}
```

**Action bindings** (toolbar buttons, list-item operations) either call an action or perform a route navigation — one of the two is required:

```yaml
toolbar:
  - label: New
    icon: add
    route: /apps/feed-hub/add
  - label: Refresh
    icon: refresh
    action: refresh_all          # must be declared in actions[]
    on_success:
      toast: Refreshed
      refresh: true              # refresh the current view after a successful call
  - label: Clear all
    action: purge_all
    confirm: Clear all data?       # second-confirmation prompt
    risk_warning: This action cannot be undone     # extra warning for high-risk operations
    on_success:
      toast: Cleared
      navigate: /apps/feed-hub    # navigate after success
```

**Form views** reuse an action's schema directly, avoiding duplicate declarations:

```yaml
  - id: add
    route: /apps/feed-hub/add
    layout: form
    schema_ref: actions.add_item.input_schema   # a dot path within the manifest
    submit:
      action: add_item
      on_success:
        toast: Added
        navigate: /apps/feed-hub
```

**Route parameters**: a route may carry parameter segments (e.g. `/apps/feed-hub/boards/:board_id`), retrieved in `data_source.input` by interpolating `${route.board_id}`. Pagination is declared with `pagination.page_param` (the route parameter name) / `total_field` (the path to the total in the return value, e.g. `data.total`) / `page_size`.

`grid` supports responsive column counts `[narrow, medium, wide]` (each 1–6, matching the client's breakpoints), plus `spacing` and `aspect_ratio`.

### triggers

Declaratively registered autonomous entry points; when triggered, the platform calls the action you specify:

```yaml
triggers:
  - kind: cron               # scheduled
    name: hourly_refresh     # unique within the app
    expr: "5 * * * *"        # standard 5-field cron; minimum interval 1 minute, "* * * * *" is rejected
    if_inactive_for: 30m     # optional: skip this run when the user has been inactive for this long
    action: refresh_all
  - kind: webhook
    name: inbound
    path: /callback          # must start with /
    auth: hmac               # hmac|none
    accept_methods: [POST]
    action: ingest
  - kind: inbox              # message-channel routing
    name: chat_command
    pattern: "subscribe "
    action: subscribe_from_text
```

Every trigger's `action` must be declared in `actions[]`. Triggers may also carry static `input`, merged with the dynamic payload at trigger time and passed to the action.

### skills

Apps may bundle skills (SKILL.md) that are written into the platform's skill library on install and cascade-cleaned on uninstall:

```yaml
skills:
  - identifier: feed-hub-digest   # skill identifier, unique within the app
    file: skills/digest.md        # path relative to the package root
```

On the Go side you must implement `SkillContent(identifier)` returning the file's content (see the contract below), typically embedded at compile time with `//go:embed`.

### requires / billing / sidebar / i18n

```yaml
requires:                        # hard dependencies checked at install time; installation fails with a prompt to install them first
  - kind: app                    # app | mcp_server
    identifier: rss
    min_version: 0.2.0

billing:                         # marketplace billing declaration
  tier: free                     # free | pro | usage
  # pro:
  price: { currency: USD, amount: 4.99, period: monthly }   # period: monthly|yearly|lifetime
  trial_days: 14
  # usage:
  meters:
    - name: api_calls
      unit: call
      unit_price_micro: 1000     # micro-dollars (1e-6 USD)

sidebar:                         # sidebar behavior hints
  preferred_position: middle     # top|middle|bottom
  badge_action: unread_count     # an action returning {count, severity}, used as the sidebar badge
  badge_refresh: 120             # badge refresh interval in seconds, ≥ 60
  mobile_bottom_eligible: true   # eligible for the mobile bottom bar
  # default_pin is only available to platform/org installs; marketplace apps ignore it

i18n:
  default: zh-CN
  locales: [zh-CN, en-US]
  files: locales/                # directory inside the package, default locales/
```

## Go implementation: the App contract

Apps with a backend (`backend` / `hybrid`) implement the following interface (Go, based on `packages/go-sdk/biu/biuapp`):

```go
type App interface {
    Manifest() Manifest
    Init(ctx context.Context, deps Deps) error
    Invoke(ctx context.Context, action string, in json.RawMessage) (any, error)
}
```

- `Manifest()` returns the declared actions / permissions / views / triggers. In-process registration fails outright on duplicate slugs.
- `Init()` is called once at registration. The platform-injected `Deps` are described below.
- `Invoke()` handles every call. `in` is opaque JSON; the app validates it against its own declared `input_schema`; the return value can be any JSON-serializable value. For unknown action names, returning the package's predefined `biuapp.ErrUnknownAction` is recommended — the runtime maps it to a friendly "tool not found" error.

The platform capabilities injected via `Deps`:

```go
type Deps struct {
    HTTP    HTTPClient      // outbound HTTP client (a fake can be injected for tests)
    Logger  Logger          // optional; the zero value discards
    Now     func() any      // overridable clock, for deterministic tests
    Events  EventPublisher  // view data invalidation event outlet (see "Runtime behavior")
}
```

### Optional interfaces

Implement any subset as needed; the registry probes with type assertions, and unimplemented methods are silently skipped:

| Interface | Method | When |
|---|---|---|
| Lifecycle hooks | `OnInstall / OnUninstall / OnUpgrade / OnConfigUpdate` | Install / uninstall / upgrade / config change. These hooks are the right place for bookkeeping in the outside world (registering a remote webhook, revoking an OAuth grant); database cleanup is already cascaded by the platform. A failing `OnInstall` rolls back the installation; hooks run **after** the transaction commits, so slow operations don't hold the transaction |
| `TriggerHandler` | `OnTrigger(ctx, TriggerEvent)` | A trigger fired. If the trigger logic is just "call a certain action", you can skip implementing it — the platform routes `TriggerEvent` to `Invoke(action, input)` by default |
| `ViewDataProvider` | `OnViewData(ctx, ViewDataRequest)` | Override when view data should not go through the generic action path (e.g. expensive joins). If not implemented, the platform calls the view's `data_source.action` by default |
| `StreamingApp` | `Stream(ctx, action, in, emit)` | Actions declared `streamable: true` go here; push `log` / `progress` / `partial` / `final` events through `emit` (a push interval of ≥ 200ms is recommended; the client coalesces) |
| `BundledSkillProvider` | `SkillContent(identifier) ([]byte, error)` | Required for apps that declare `skills[]`; unknown identifiers return `biuapp.ErrSkillNotFound` |

The `Install` passed to lifecycle hooks carries the install id, slug, version, scope (`user`/`org`), scope id, and the installation config (without secrets — secrets go through the platform's credential hosting).

## Packaging and signing

### .biuapp.yaml

The packaging manifest at the project root, controlling which files `biu app pack` copies into the package:

```yaml
include:
  - manifest.yaml
  - README.md
  - LICENSE
  - skills/**
  - assets/**
exclude:
  - .git/**
  - "**/*_test.go"
```

Glob semantics: `manifest.yaml` matches exactly; `skills/**` is a recursive directory; `**/*_test.go` matches a trailing pattern at any depth. Dot-prefixed directories (`.git` / `.idea`, etc.) are skipped automatically. Without a `.biuapp.yaml`, the default packs only `manifest.yaml` + `README.md` + `LICENSE`.

### biu app pack

```bash
biu app pack \
  [--source DIR]   # project root; defaults to the current directory
  [--out FILE]     # output path; defaults to dist/<slug>-<version>.biuapp
  [--key PATH]     # signing private key; defaults to ~/.biumind/keys/publisher.ed25519
  [--unsigned]     # skip signing (local installs only; rejected by the marketplace)
```

The manifest is force-validated before packaging; a broken manifest cannot produce a package. The artifact is a zip-format `.biuapp`:

```text
manifest.yaml      # always present, written first in the zip
manifest.sig       # when signed: an ed25519 signature over the manifest.yaml bytes
<files you included>   # written in a deterministic order
SHA256SUMS         # one line per file: "<sha256 hex>  <relative path>"
SHA256SUMS.sig     # ed25519 signature over the SHA256SUMS bytes — the root of trust
```

The two signatures each defend against a different attack: `manifest.sig` prevents "swapping the manifest under a valid manifest file", and `SHA256SUMS.sig` prevents "swapping other files under a valid manifest". Packaging is deterministic — same source, same bytes, same hash — so CI can pin the output zip's sha256 directly (the pack command prints it).

### Keys: biu app keygen

```bash
biu app keygen [--name publisher]
```

Generates `<name>.ed25519` (private key, permissions 0600) and `<name>.ed25519.pub` (public key) under `~/.biumind/keys/`. **Existing keys are never overwritten** — once a publishing key is rotated, all historical signatures become invalid. The publisher id in the output has the form `ed25519:<base64 public key>`; put it into the manifest:

```yaml
author:
  name: Zhang San
  public_key: ed25519:MCowBQYDK2VwAyEA...
```

### biu app verify

```bash
biu app verify dist/feed-hub-0.1.0.biuapp \
  [--trust-key ~/.biumind/keys/publisher.ed25519]   # repeatable
```

Verifies three things: manifest.yaml is present; every file listed in SHA256SUMS matches its hash; when signed, both signatures verify against a trusted public key. Without `--trust-key`, a signed package gets "hashes verified but identity verification skipped" (the output is labeled signed/unsigned). Single files are capped at 50 MiB.

> [!WARNING]
> In the current version, `--trust-key` only accepts the path of a key pair containing the private key; passing a `.pub` public-key file directly is not yet supported.

## Walkthrough: the RSS app

The repo's built-in RSS subscription app (`packages/go-sdk/biu/biuapp/rss/`) is a complete reference for the hybrid shape. Its manifest (declared as a Go literal, equivalent to the YAML) shows a full set of declarations:

```go
func (a *App) Manifest() biuapp.Manifest {
    return biuapp.Manifest{
        Name:        "rss",
        Version:     "0.2.0",
        Description: "Subscribe to RSS / Atom feeds; AI digest into wiki",
        Author:      "BiuMind",
        Permissions: []string{"net.outbound", "hub.invoke", "wiki.write", "cron.register"},
        Actions:     []biuapp.ActionSpec{ /* see below */ },
        ManifestExt: biuapp.ManifestExt{
            Identifier: "rss",
            Title:      "RSS subscriptions",
            Category:   "content",
            Kind:       "hybrid",
            Views:      []biuapp.ViewSpec{ /* ... */ },
            Triggers:   []biuapp.TriggerSpec{ /* ... */ },
            Skills:     []biuapp.SkillRef{ /* ... */ },
            Sidebar:    &biuapp.SidebarHints{ /* ... */ },
        },
    }
}
```

Several practices worth copying:

**1. Declare risk per action.** `fetch` / `list_subscriptions` are `risk: low` (reading external sources, reading its own data); `digest` goes through the model gateway to summarize, so it's marked `risk: medium` — the approval policy the user sees on the permission confirmation page differs accordingly.

**2. Views cover all the common layouts.** A `list_detail` main list (card template + a per-item "unsubscribe" action with confirmation and `on_success` toast/refresh), a `form` add page reusing `actions.subscribe.input_schema`, a `grid` leaderboard page, and a detail page with route parameters:

```go
{
    ID: "board_detail",
    Route: "/apps/rss/boards/:board_id",   // route parameter
    Layout: biuapp.LayoutListDetail,
    DataSource: &biuapp.ViewDataSource{
        Action: "boards_snapshot",
        Input: map[string]any{
            "board_id": "${route.board_id}",  // parameter interpolated back into the input
            "limit":    30,
        },
    },
    ...
}
```

**3. Triggers drive unattended refreshes.** Two cron entries: `"5 * * * *"` full refresh and `"0 8 * * *"` an 8 a.m. digest (with static input `{"window":"24h","max_items":10}`). The app itself does not implement `TriggerHandler`; the platform's default routing lands directly on the corresponding action.

**4. Bundled skills + compile-time embedding.**

```go
//go:embed skills/summarize.md
var summarizeSkill []byte

func (a *App) SkillContent(identifier string) ([]byte, error) {
    if identifier == "rss-summarize" {
        return summarizeSkill, nil
    }
    return nil, biuapp.ErrSkillNotFound
}
```

The skill content ships with the binary and cannot drift between "the running code" and "what was installed into the skill library".

**5. Invoke dispatches via switch and records metrics.** `Invoke` wraps an `invokeInternal`, recording `ok`/`error` metrics on the return value before returning; the actual dispatch is just a switch from action name to handler. `Init` is an empty implementation — dependencies are all injected via `WithXxx` options (`WithBoards` / `WithRadar` / `WithLLM`...), and non-injected optional capabilities make the corresponding actions return an explicit "not wired up" error instead of panicking.

**6. Sidebar badge.** `Sidebar.BadgeAction: "unread_count"` + `BadgeRefreshSec: 120` — the platform calls this lightweight action every two minutes to get `{count, severity}` and refresh the sidebar badge.

## Publishing to the App Center

There are currently two ways to deliver an app to users; the app marketplace (catalog-based distribution of `.biuapp` packages) is under construction — the package format and signing system are ready.

### Path 1: GitHub repo app (repo-app)

Put the project on GitHub; users install it directly through the App Center and run it **on their own machine** (launched by the local CLI, listening on 127.0.0.1 only). Supported stacks are auto-detected: Node (a start script in `package.json`), Python (entry file / uv), Dockerfile, or a purely static site (`index.html`). Available on macOS / Linux (Windows not yet supported).

Server API (all require a Bearer JWT, routed to the App Center through the single-origin gateway under `/v1/apps/*`):

| Endpoint | Purpose |
|---|---|
| `POST /v1/apps/repo/analyze` | `{repo_url}` → a repository analysis draft (language, launch method, suggested config items) |
| `POST /v1/apps/repo/installs` | `{repo_url, ref_type: release\|branch, config}` → install and register in the catalog. The server **re-runs** the analysis; the client's draft is display-only |
| `GET /v1/apps/installs/{id}/runtime` | Runtime status `{mode:"local", status, url:null}` — the URL is resolved by the local CLI |
| `GET /v1/apps/installs/{id}/builds` | Build history (newest first, up to 20) |
| `POST /v1/apps/installs/{id}/redeploy` | Queue a redeploy; returns `{build_id, ref, sha}` |
| `POST /v1/apps/installs/{id}/builds/{build_id}/complete` | CLI reports the build result `{status: live\|failed, sha, log_ref}` |

The command-line interface on the user's machine:

```bash
biu repo-app install <github-url|owner/repo> [--ref v1.2.3]
biu repo-app ensure <name> [--env KEY=VALUE]...   # idempotent: installs if missing, starts if stopped
biu repo-app list
biu repo-app run <name> [--port 0] [--env KEY=VALUE]...
biu repo-app stop <name>
biu repo-app logs <name> [-f]
biu repo-app update <name> [--ref ...] [--install-id ID --build-id ID]
biu repo-app remove <name>
biu repo-app doctor                              # probes git/python3/uv/node/mise/docker
```

Key conventions for developers:

- After `run` / `ensure` pass the health check, they print one line to stdout: `BIU_REPOAPP_URL=http://127.0.0.1:<port>` — the desktop client relies on this line to get the address; don't pollute stdout.
- `--env KEY=VALUE` entries are merged into the instance's `.env` (permissions 0600); this is the delivery channel for configuration and secrets.
- When the desktop clicks "redeploy", the client takes the `build_id` returned by redeploy and runs `biu repo-app update <name> --install-id <id> --build-id <id>` locally; when the CLI finishes, it calls the complete endpoint to report the result.
- The repository must be auto-detectable: a runnable package.json script / Python entry / Dockerfile / index.html — one of the four; otherwise you must hand-edit `runtime.json`'s `start_cmd`.

### Path 2: compiled into the server (self-hosting)

A self-hosted deployment can modify the server code and `Register` a Go-implemented App directly into the App Center's registry (the built-in rss / translate / tasks / email / webclip apps are done this way). Suited to internal apps in private deployments; the cloud cannot compile for you — cloud distribution goes through repo-app and (eventually) the marketplace.

### Toward the marketplace

The `.biuapp` package + ed25519 signing is the format prepared for marketplace distribution: listing requires a signature (`--unsigned` is local installs only), and the marketplace form requires the `<author>/<slug>` scoped identifier. Catalogs, submissions, and the review process open with the marketplace launch, along with commands like `biu app publish`; you can already prepare your publishing key and `author.public_key` following the signing flow in this document.

## Runtime behavior

Understanding how the platform executes your app helps you write actions and views that behave as expected.

### The call path

A user or agent calling an action goes to `POST /v1/apps/{name}/invoke` with the body `{"action": "...", "input": {...}}`:

1. Bearer JWT verification;
2. look up the caller's install record — **not installed is an immediate 403** (`not_installed`); disabled installs are also 403;
3. the authz service makes a Cedar policy decision against the permissions granted at install time; a denial returns 403 (`permission_denied`);
4. the registry checks whether the action is declared in the manifest; undeclared is 400 (`unknown_action`);
5. your `Invoke` (or `Stream` for streamable actions) is called;
6. every call (including rejected ones) is written to the call audit table: caller, action, duration, status, error code.

So the manifest is a hard boundary: undeclared actions cannot be called, and ungranted permissions cannot pass authz.

### Events and view refreshes

Every state change in the App Center (install, uninstall, upgrade, start/stop, config update, action calls, trigger fires, ...) is accompanied by an event write, pushed to clients via Realtime — this is the platform's unified mechanism ensuring that no open view is left on stale state. For app authors, it lands in two places:

**Passive side**: views may declare `refresh_on` to subscribe to the event topics they care about (`<self>` is replaced at runtime with the install id); a hit re-fetches the `data_source`.

**Active side**: when backend data changes, proactively announce view invalidation through `Deps.Events` injected by `Init`:

```go
deps.Events.PublishViewDataChanged(ctx, installID, "home", "boards")
// empty viewIDs = invalidate all views of that install
```

This produces an `app.view_data_changed` event; the client re-fetches the corresponding views' data on receipt.

> [!WARNING]
> App code is not allowed to write to the platform's database directly. Events, skill content, and install records all go to storage through platform-provided outlets (`Deps.Events`, lifecycle hooks, the install API) — unified outlets are the precondition for events not being lost and views not going stale; writes that bypass the outlets do not work in hosted environments.

Event push is a best-effort, non-critical path: errors from `PublishViewDataChanged` should be logged but not returned as the action's main error; the source of truth for data is always your action's return value.

## Validation rules quick reference

Rules shared by `biu app validate` / the install path / packaging — the most frequently hit items:

| Field | Rule |
|---|---|
| `identifier` | Non-empty; `^[a-z][a-z0-9._-]*(/[a-z][a-z0-9-]*)?$` (the marketplace requires the `<author>/<slug>` form, checked by the listing path) |
| `version` | Semantic version `x.y.z`, with an optional pre-release suffix |
| `description` | Non-empty, ≤ 200 characters |
| `category` | One of six: productivity / content / data / comm / dev / utility |
| `kind` | One of five; `container` is currently rejected by the install path |
| `icon` | Empty / emoji (≤ 8 bytes) / `http(s)://` URL / `cas:<64 lowercase hex chars>` |
| `permissions[]` | Prefix must be in the platform whitelist (see the table above) |
| `actions[].name` | `^[a-z][a-z0-9_-]*$`, unique within the app |
| `actions[].timeout_ms` | 0–600000 |
| `views[].route` | Must start with `/apps/<your own identifier>` |
| actions referenced by `views[]` | Actions referenced by toolbar / item_template / data_source / submit / cards must all be declared in `actions[]` |
| form views | At least one of `schema_ref` and `submit` |
| webview views | Must have `url` |
| agent_chat views | Must have `agent_id` |
| dashboard views | At least one card; card `span` 1–12; `kind` ∈ text/number/list/chart |
| grid views | Must have `item_template`; column count 1–6 |
| cron expressions | Standard 5-field; `* * * * *` (every minute) is rejected; minimum interval 1 minute |
| webhook triggers | `path` starts with `/`; `auth` ∈ hmac / none |
| `sidebar.badge_action` | Must be a declared action |
| `sidebar.badge_refresh` | ≥ 60 seconds |
| `skills[].identifier` / `file` | Non-empty; identifier unique within the app |
| `requires[].kind` | app / mcp_server; `min_version` must be a semver |

---

- Up one level: [Developers](index.md)
- Authentication and API conventions: [API reference](api.md)
- Writing reusable skills for agents: [Skill development](skills.md)
- CLI installation and configuration: [CLI guide](../cli/getting-started.md)
