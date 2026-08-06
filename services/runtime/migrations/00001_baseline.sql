-- +goose Up
-- +goose StatementBegin

-- 基线 migration：由 00001_tasks.sql / 00002_skills.sql /
-- 00003_skill_update_of.sql 三个历史 migration squash 而来，
-- 仅面向全新部署场景（fresh deploy），不承担存量库升级。
--
-- 相比历史版本的差异：
--   1. CREATE SCHEMA runtime 提到最前 —— 历史上它在 00002，而
--      00001 已创建 runtime.tasks，顺序倒挂靠部署脚本兜底；基线
--      直接修正。
--   2. 00003 的 skills.update_of_id 列（自引用 FK）与
--      skills_update_of_idx 索引折叠进 skills 的 CREATE TABLE。
--   3. 全部 DDL 幂等：CREATE TABLE / CREATE INDEX 一律带
--      IF NOT EXISTS（原 00001 的两个索引是裸写，此处补上）。

CREATE SCHEMA IF NOT EXISTS runtime;

-- ─── Tasks ───────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS runtime.tasks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    project_id      uuid,
    agent           text NOT NULL DEFAULT 'biu',
    prompt          text NOT NULL,
    system_prompt   text NOT NULL DEFAULT '',
    model           text NOT NULL,
    permission_mode text NOT NULL DEFAULT 'ask',
    status          text NOT NULL DEFAULT 'pending',
    error_message   text NOT NULL DEFAULT '',
    thread_id       text NOT NULL,
    run_id          text NOT NULL,
    cost_tokens_in  bigint NOT NULL DEFAULT 0,
    cost_tokens_out bigint NOT NULL DEFAULT 0,
    cost_usd_micros bigint NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);

CREATE INDEX IF NOT EXISTS tasks_user_status_idx ON runtime.tasks (user_id, status);
CREATE INDEX IF NOT EXISTS tasks_run_idx ON runtime.tasks (run_id);

-- ─── Skills subsystem ────────────────────────────────────────
--
-- Design provenance: docs/BiuMind-Skills-Design.md §5 "数据库 Schema".
-- Field names match proto packages/proto/biumind/runtime/v1/skills.proto
-- so the registry can map both directions without a translation layer.
--
-- Cross-schema FKs are deliberately NOT used:
--   * runtime.skills.zip_file_sha256 stores the Files CAS sha256 as a
--     plain text reference, not a FK to brain.files.objects(sha256).
--     Rationale: services boundary; runtime should not block on
--     Files schema migrations or take a hard-coupled crash on a
--     dangling reference. The application layer validates existence
--     when needed (sandbox mount path).
--   * runtime.agent_skills.agent_id similarly does not reference an
--     agents table at the SQL level; agents are owned by Identity and
--     may be created / deleted out of band. We index for lookup speed
--     instead and rely on application-level cleanup.

-- 1. Catalogue. One row per skill, scoped to org. owner_id is set for
--    user-private skills and NULL for org-shared / bundled.
CREATE TABLE IF NOT EXISTS runtime.skills (
    id              text PRIMARY KEY,                     -- "skill_<ulid>" assigned by application layer
    org_id          uuid NOT NULL,
    owner_id        uuid,                                 -- NULL = org-shared (bundled / org)
    identifier      text NOT NULL,                        -- kebab-case slug
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    source          text NOT NULL
                         CHECK (source IN ('bundled', 'org', 'user', 'marketplace', 'imported')),

    -- SKILL.md frontmatter, stored typed-via-JSONB so we don't bake
    -- column changes for every new field a third-party manifest adds.
    -- Application layer projects the typed proto Manifest back out.
    manifest        jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- SKILL.md body (post-frontmatter). Inlined so activate() does not
    -- need a second round-trip; large skills should split into
    -- resources/ rather than balloon this column.
    content         text NOT NULL DEFAULT '',
    content_hash    char(64) NOT NULL,                    -- sha256 of raw SKILL.md

    -- Bundled resources. Map<vpath, ResourceMeta>. Inline for ≤4KB
    -- text; larger files referenced by sha256 → files.objects.
    resources       jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Whole-bundle archive (.biuskill tarball) sha256, for sandbox
    -- mount. NULL when the skill has only inline / per-file resources.
    zip_file_sha256 char(64),

    -- Auto-attach globs (relative to project root). NULL = never auto.
    paths           text[],
    -- Declared permissions (Cedar policy translation lands in PS3.4).
    permissions     text[],

    status          text NOT NULL DEFAULT 'active'
                         CHECK (status IN ('active', 'disabled', 'staged', 'staged_org', 'suspended')),

    -- update_of_id — pointer from a staged skill to the previously-active
    -- row it intends to replace. Set on propose when client carries
    -- `update_of`; null on fresh skills. Lets the approver UI render a
    -- diff against the predecessor without round-tripping through the
    -- propose response cache.
    --
    -- Self-referential FK is intentional. Loop detection isn't needed:
    --   - propose flow only sets it once at creation;
    --   - approve flow doesn't touch it (the staged row stays linked to
    --     its predecessor for audit even after activation).
    -- ON DELETE SET NULL because the predecessor might be hard-deleted
    -- (rare, but admin tooling exists). Losing the pointer is fine —
    -- the staged row stands on its own.
    update_of_id    text REFERENCES runtime.skills(id) ON DELETE SET NULL,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    -- One identifier slug per org. Personal-vs-org collision is fine
    -- because user-private rows still live under the same org_id; the
    -- collision should be surfaced to the user as "name taken".
    UNIQUE (org_id, identifier)
);

CREATE INDEX IF NOT EXISTS skills_org_status_idx
    ON runtime.skills (org_id, status);

CREATE INDEX IF NOT EXISTS skills_owner_idx
    ON runtime.skills (owner_id)
    WHERE owner_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS skills_source_idx
    ON runtime.skills (source);

CREATE INDEX IF NOT EXISTS skills_content_hash_idx
    ON runtime.skills (content_hash);

CREATE INDEX IF NOT EXISTS skills_update_of_idx
    ON runtime.skills (update_of_id)
    WHERE update_of_id IS NOT NULL;

-- 2. Per-agent enablement. Composite PK (agent_id, skill_id) — at
--    most one row per pair. ON DELETE CASCADE on skill_id only;
--    agent_id has no FK so we index it for cleanup queries instead.
CREATE TABLE IF NOT EXISTS runtime.agent_skills (
    agent_id    uuid NOT NULL,
    skill_id    text NOT NULL REFERENCES runtime.skills(id) ON DELETE CASCADE,
    is_enabled  boolean NOT NULL DEFAULT true,
    -- pinned=true injects body into system prompt directly, skipping
    -- the activate() round-trip. Use sparingly; prompt budget is finite.
    pinned      boolean NOT NULL DEFAULT false,
    added_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, skill_id)
);

CREATE INDEX IF NOT EXISTS agent_skills_skill_idx
    ON runtime.agent_skills (skill_id);

-- 3. Activation audit ledger. Append-only; consumed by Realtime
--    fanout (interactive UI) and StreamSkillActivations (batch).
--    No FK on session_id / skill_id intentionally — the ledger
--    survives upstream deletes for forensic / billing replay.
CREATE TABLE IF NOT EXISTS runtime.skill_activations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  uuid NOT NULL,
    skill_id    text NOT NULL,
    trigger     text NOT NULL
                     CHECK (trigger IN ('explicit', 'auto_attach', 'tool_call', 'pinned')),
    trace_id    text NOT NULL DEFAULT '',
    tokens_in   integer NOT NULL DEFAULT 0,
    tokens_out  integer NOT NULL DEFAULT 0,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS skill_act_session_idx
    ON runtime.skill_activations (session_id);

CREATE INDEX IF NOT EXISTS skill_act_skill_recent_idx
    ON runtime.skill_activations (skill_id, occurred_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop in reverse FK order.
DROP TABLE IF EXISTS runtime.skill_activations;
DROP TABLE IF EXISTS runtime.agent_skills;
DROP TABLE IF EXISTS runtime.skills;
DROP TABLE IF EXISTS runtime.tasks;

-- We do NOT drop the runtime schema; it may be shared with other
-- services' objects beyond this migration's tables.

-- +goose StatementEnd
