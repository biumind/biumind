# Skill Development and Usage

A skill is a reusable agent capability package: one `SKILL.md` (YAML frontmatter + Markdown body), optionally accompanied by scripts, reference documents, and asset files. The agent loads the skill body on demand at runtime, thereby "learning" the fixed way of doing a class of tasks. Skills sit parallel to [BiuApps](biuapp.md): a skill is a Markdown instruction package; a BiuApp is a full mini-program with a UI.

This document covers three parts: the **SKILL.md format** (for skill authors), the **cloud lifecycle** (install / propose / approval / sharing / per-agent enable/disable), and the **CLI toolchain** (packaging / signing / syncing).

## SKILL.md format

A skill is a directory; the directory name is the skill identifier (kebab-case), and the root must contain `SKILL.md`:

```text
my-skill/
├── SKILL.md          # required: frontmatter + body
├── scripts/          # optional: executable scripts (bash / python, etc.)
├── references/       # optional: supporting documents (detailed manuals, long reference material)
└── assets/           # optional: templates, icons, and other assets
```

An example `SKILL.md`:

```markdown
---
name: my-skill
display_name: My Skill
description: One sentence saying what this skill does and when to use it. This text goes into the agent's available-skills list and directly determines whether the agent picks the right skill.
icon: 🛠
permissions: ["wiki.read"]
paths: ["docs/**"]
---

# Body

Operational instructions written for the agent. Keep it lean — the body is injected into the prompt,
and long content should be split into references/ and read on demand.

Arguments: $ARGS
```

### frontmatter fields

The frontmatter is a flat `key: value` block (only top-level scalars and comma-separated lists are supported; no nested structures). The fields fall into two groups by consumer:

**Parsed when installing from the cloud and loading built-in skills** (install URL / `.biuskill` archive, built-in skill directory):

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Skill identifier, kebab-case, unique within the organization; installation is rejected when missing |
| `description` | Yes | One-line description; goes into the agent's available-skills list and drives skill-selection matching; installation is rejected when missing |
| `display_name` | No | Display name (falls back to `name` by default) |
| `icon` | No | Icon: a single emoji, or an `https://` image URL |
| `paths` | No | Auto-mount glob list (see "Auto-mounting" below) |
| `permissions` | No | Declared permissions (see "Permission declarations" below) |
| `version` | No | semver version |
| `license` | No | SPDX identifier |
| `repository` | No | Upstream git repository URL |
| `source_url` | No | Source URL (import provenance) |
| `author` | No | Author name |

**Parsed by the CLI for local loading** (`~/.biumind/skills/`, `<project>/.biumind/skills/`, and other local directories):

| Field | Required | Description |
|---|---|---|
| `name` | No | Skill name (falls back to the directory name when missing) |
| `description` | No | One-line description |
| `when-to-use` | No | Trigger-condition description, helping the agent decide when to use the skill (alias `whentouse`) |
| `user-invocable` | No | Boolean: whether the user may invoke it directly as `/skill-name` (alias `userinvocable`; accepts `true/yes/1`) |
| `paths` | No | Auto-mount glob list; rules as above |

> [!NOTE]
> The two parsers are currently independent: local loading does not consume `display_name` / `icon` / `permissions`, and cloud installation does not consume `when-to-use` / `user-invocable`. Writing both groups of fields in one SKILL.md is perfectly legal — that is what the built-in standard library does.

### The body and `$ARGS`

The body is the post-frontmatter Markdown, injected into the prompt in its entirety. The convention: keep the body under roughly 50KB; split longer content into `references/`, which the agent reads on demand through the `skill.read_reference` tool.

The CLI invocation side supports argument expansion: `$ARGS` (and its alias `$1`) appearing in the body is replaced at call time with the argument string the user passed. For example, `biu skill run my-skill every Friday` or `/my-skill every Friday` in a session both substitute `every Friday` into the body. Unknown placeholders are left as-is.

### Auto-mounting (`paths`)

For a skill that declares `paths`, when the session's working directory or recently touched files match any glob, the body is automatically injected into the system prompt without an explicit call. Matching rules:

- A pure literal path (no wildcards) matches by "containment": `apps/cli` matches any path under its subtree;
- patterns containing `**` are translated to regex (`*` → `[^/]*`, `**` → `.*`) matching path segments;
- other globs: `filepath.Match` against the full path and the file name;
- a trailing `/**` is stripped; a bare `*` / `**` left over is treated as "matches everywhere", equivalent to no auto-mounting.

Skills without `paths` (or with it effectively empty) are always callable via the `skill.activate` tool or `/skill-name`; they are just not auto-injected.

### Permission declarations (`permissions`)

`permissions` is a comma-separated list of permission strings; common values include `sandbox.exec`, `network.fetch`, `wiki.read`, `memory.recall`. The declaration is stored with the skill row and passed as attributes into the authz policy engine for decisions; callers should declare only the minimal set of permissions the skill actually needs.

## Cloud lifecycle

Skills on the cloud (the Runtime service; REST endpoints in [API reference](api.md), all mounted under the single origin's `/v1/skills`) are managed along two dimensions: **source** and **status**.

### Source

| Value | Meaning |
|---|---|
| `bundled` | Built into the platform (skills-stdlib is auto-loaded when Runtime starts); visible to all organizations, cannot be deleted |
| `org` | Organization-shared, maintained by admins |
| `user` | User-created (the propose flow or a local `.biuskill` install) |
| `marketplace` | Pulled from the skill marketplace (signed package) |
| `imported` | Imported from an external URL |

### State machine

```text
                 propose                    approve
   (absent) ───────────────▶ staged ──────────────────▶ active
                                │                        │    │
                            reject│                 reject(admin)│ │
                                ▼                        ▼    │ share-org
                             disabled ◀────────────── staged_org
                                │                     (admin approval)
                                ▼                          │
                            [terminal state]           approve(admin)
                                                              │
                                                              ▼
                                         (back to active, now org-shared)
```

The full transition table validated by the server:

| Current state | Allowed targets | Trigger |
|---|---|---|
| `staged` | `active` / `disabled` | Approval / rejection |
| `staged_org` | `active` / `disabled` | Admin approval / rejection |
| `active` | `disabled` / `staged_org` | Owner disables / requests org sharing |
| `disabled` | (none) | Terminal state; to revive, re-propose with `update_of` pointing at the original skill |
| `suspended` | (none) | Platform-level ban; unreachable by user actions |

> [!WARNING]
> Built-in skills (`source=bundled`) cannot be deleted. Identifiers are unique within the organization; creating a duplicate returns a conflict error.

### The self-service authoring approval flow (propose / approve / reject)

After a user (or an agent in a session, via the `skill.propose` tool) submits a draft, the skill enters `staged` and awaits approval:

```bash
# Propose (identifier / name / description / body are all required; update_of is optional —
# when it points to an existing skill ID, the approval card renders a diff against the old version)
curl -X POST https://<server-address>/v1/skills/propose \
  -H "Authorization: Bearer <PAT>" \
  -H "Content-Type: application/json" \
  -d '{
    "identifier": "weekly-report",
    "name": "Weekly report",
    "description": "Summarize this week's git commits and document changes into a weekly report",
    "body": "# Steps\n…"
  }'

# Approve; with enable_on_default_agent=true it is also enabled and pinned to the default agent
curl -X POST https://<server-address>/v1/skills/<skill_id>/approve \
  -H "Authorization: Bearer <PAT>" \
  -d '{"enable_on_default_agent": true}'

# Reject; the reason appears in the proposer's audit records
curl -X POST https://<server-address>/v1/skills/<skill_id>/reject \
  -H "Authorization: Bearer <PAT>" \
  -d '{"reason": "missing permissions declaration"}'
```

Every state change writes an audit event (`skill.status_changed`, etc.) that can be replayed.

### Org sharing (share-org)

A personal skill in `active` state can be submitted to become organization-shared: `POST /v1/skills/{id}/share-org` moves it to `staged_org`; once an org admin approves, it becomes an `org` shared skill visible to all members. Org-shared skills only appear in the available list by default — they are neither auto-pinned nor auto-mounted until the user explicitly enables them.

### Per-agent enable/disable and pinning

Between each agent and each skill there is an independent enable relation (`agent_id` + `skill_id` + `is_enabled` + `pinned`):

```bash
# Enable on an agent (is_enabled=true)
curl -X POST https://<server-address>/v1/skills/<skill_id>/toggle \
  -H "Authorization: Bearer <PAT>" \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "<agent_uuid>", "is_enabled": true, "pinned": false}'
```

`pinned=true` means pinned: the body is injected directly into the system prompt in every session, skipping the `skill.activate` round trip. Prompt budget is limited — use pinning only for high-frequency core skills.

At runtime, skills are injected in three tiers:

| Tier | Trigger | Injection |
|---|---|---|
| Pinned | `agent_skills.pinned=true` | Body goes directly into the system prompt |
| Auto-mount (auto_attach) | `paths` matches the working directory | Body goes directly into the system prompt |
| Available | Enabled but neither of the above | Only name + description go into the list; the agent loads the body on demand via `skill.activate` |

The agent side has six skill tools: `skill.list` (enumerate), `skill.activate` (load the body), `skill.read_reference` (read bundled resources), `skill.exec_script` (run scripts in the sandbox), `skill.export_file` (export a file), `skill.propose` (author and propose a new skill within a session).

## Built-in standard library

The platform ships 8 built-in skills with the Runtime (`source=bundled`, visible to all organizations); their core purpose is "teaching the agent to use BiuMind itself":

| Identifier | Display name | Purpose | Declared permissions |
|---|---|---|---|
| `biumind` | BiuMind | The platform's general guide and task router: when unfamiliar with the platform or unsure which module/tool/skill to use, start here | — |
| `wiki` | Wiki | Knowledge base (documents + block editor) toolset: query/read/write/search knowledge base pages, run retrieval against your own knowledge base | `wiki.read` |
| `memory` | Memory | The memory system (recall / preference / habit): persist facts, recall user context across sessions | `memory.recall` |
| `graph` | Knowledge graph | Map entity relationships, track dependencies, analyze thematic overviews from accumulated notes | `wiki.read` |
| `sandbox` | Cloud sandbox | Execute code, run commands, and manage files in an isolated environment (gVisor / Firecracker) | `sandbox.exec` |
| `app-center` | App Center | Call the App Center's first-class apps (RSS / email / stocks / slides, etc.) | — |
| `artifacts` | Artifacts | Generate and preview interactive UI components, SVG graphics, charts, and visualizations | — |
| `skill-creator` | Skill creator | Package a workflow that just worked into a reusable skill (describe the steps in natural language and it generates a SKILL.md) | — |

`wiki` additionally declares `paths: ["wiki/**", "docs/**"]` — it auto-mounts when you work under a documents directory.

## CLI toolchain

The `biu skill` command family covers local management and cloud sync. Except for `run` / `pack` / `unpack` / `keygen` / `sign` / `verify`, which are purely local operations, the other commands need the Runtime address: the `--runtime-url` flag or the `BIUMIND_RUNTIME_URL` environment variable; the Bearer token comes from `--token` or `model-relay.virtual_key` in the config file.

```bash
# Install: URL (the server fetches the SKILL.md, source=imported) or a local .biuskill (source=user)
biu skill install https://example.com/my-skill/SKILL.md
biu skill install ./my-skill.biuskill --agent <agent_uuid> --pin
biu skill install ./my-skill.biuskill --dry-run   # only parses the archive and lists the files to be written; no network

# List cloud skills (organization-wide)
biu skill list
biu skill list --status staged        # active / disabled / staged / staged_org / suspended
biu skill list --source bundled       # bundled / org / user / marketplace / imported

# Cloud → local (writes ~/.biumind/skills/<identifier>/SKILL.md)
biu skill pull

# Local → cloud (creates if absent, updates if the content changed, no-op if identical)
biu skill push weekly-report

# Compare local and cloud SKILL.md hashes
biu skill diff weekly-report          # in-sync / diverged / local-only / cloud-only

# Offline expansion: substitutes $ARGS into the body and prints it; no model call — suited to scripts and CI
biu skill run weekly-report due this Friday

# Enable/disable on a specific agent (enable can take --pin)
biu skill enable skill_xxx --agent <agent_uuid> --pin
biu skill disable skill_xxx --agent <agent_uuid>
```

> [!TIP]
> `biu skill install` adapts to page links on public skill directory sites: it automatically rewrites the catalog-page URL into a directly fetchable SKILL.md address (the terminal notes which adapter was used). GitHub raw and any direct SKILL.md link work without adaptation.

> [!WARNING]
> When `pull` hits a skill that "changed locally and also changed in the cloud", it neither auto-merges nor overwrites — it marks a conflict and exits non-zero. First run `biu skill diff <name>` to inspect, then decide whether to `push` over the cloud version or delete the local copy and accept the cloud version.

## `.biuskill` archives and signing

`.biuskill` is the distribution format for skills — essentially a **deterministic ZIP**: packaging the same source directory repeatedly yields byte-identical output. This is the precondition of the signature chain: the signature covers the archive's literal bytes, not a normalized form.

### Archive layout and limits

```text
my-skill.biuskill
├── SKILL.md          # required, at the archive root
├── scripts/…         # optional
├── references/…      # optional
└── assets/…          # optional
```

- Files outside the three directories and outside the root `SKILL.md` are ignored (with a warning) at both packaging and install time;
- archive cap 8MB; `SKILL.md` cap 256KB; a single bundled resource cap 64KB (larger files must go through the object-storage path; the current version rejects them outright);
- deterministic packaging rules: entries sorted lexicographically by path, mtime fixed at 1980-01-01, file permission bits fixed at `0644`;
- entries containing `..` or absolute paths are rejected on both the packaging and install sides (path-traversal protection).

### Packaging and signing flow

```bash
# 1. Generate an ed25519 key pair (private key PKCS#8, public key SPKI, both PEM)
biu skill keygen                        # produces biuskill.key + biuskill.key.pub
biu skill keygen --prefix acme          # produces acme.key + acme.key.pub

# 2. Package (default output <dir>.biuskill)
biu skill pack ./my-skill               # or -o /tmp/my-skill.biuskill

# 3. Sign: ed25519-sign the archive bytes, base64, one line into <pack>.sig
biu skill sign my-skill.biuskill --key acme.key

# 4. (Recipient) verify
biu skill verify my-skill.biuskill --pubkey acme.key.pub
biu skill verify my-skill.biuskill --pubkey acme.key.pub --sig other.sig

# Unpack to inspect / hand-edit
biu skill unpack my-skill.biuskill      # unpacks into ./my-skill/
```

Keep the private key secret (`keygen` refuses to overwrite an existing private-key file); publish the public key through your distribution channel. Key files can be inspected with `openssl pkey -in <file> -text -noout`.

### Server-side trust model

The Runtime decides verification strength via its trust store:

- **No trust store configured** (default, compatibility mode): signatures are optional; any archive can be installed;
- **Trust store configured** (strict mode): `.biuskill` install requests must carry a `signature_b64` verifiable by at least one trusted public key, otherwise they are rejected (403).

The trust store is configured via environment variables, directory first: `BIUMIND_SKILL_TRUSTED_PUBKEY_DIR` (each `*.pub` file in the directory is one trusted publisher; the file name is the publisher id in the audit log) or `BIUMIND_SKILL_TRUSTED_PUBKEY_PEM` (a single inline public key). The URL and inline install paths do not go through signature verification.

## Client usage

The "Skills" page in the desktop / mobile clients offers visual operations equivalent to the CLI:

- **List and filter**: filter across six tabs — All / Built-in / Organization / Mine / Marketplace / Pending — and click into the detail page (content, permissions, enabled status, call count and last-called time);
- **Install**: from a URL or a local `.biuskill`, optionally binding to an agent at the same time;
- **Approval**: approve / reject operations for pending drafts (`staged` / `staged_org`) happen on the detail page;
- **Multi-device sync**: the page subscribes to the organization-level skill event stream; when someone remotely approves your draft, the list refreshes automatically with a toast.

## Next steps

- Endpoint details (authentication, error codes): [API reference](api.md)
- Driving agents in the session stream (including skill tools): [SDK Protocol](sdk-protocol.md)
- Building a full app with a UI rather than an instruction package: [BiuApp development](biuapp.md)
