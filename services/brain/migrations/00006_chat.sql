-- +goose Up
-- +goose StatementBegin

-- ─── Chat schema (threads + messages) ────────────────────────
--
-- Persistent multi-turn conversations. Design doc: docs/Chat-Threads-Design.md.
--
-- Two main tables:
--   * chat.threads   — one logical conversation (sidebar entry)
--   * chat.messages  — turns inside a thread (user / assistant / tool / system)
--
-- Plus chat.message_groups stub for §14.5 L2 (multi-model parallel),
-- created here so future migrations don't need to add the FK.
--
-- Schema lives under its own `chat` namespace to keep wiki / memory /
-- graph schemas independent.

CREATE SCHEMA IF NOT EXISTS chat;

-- ─── threads ──────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS chat.threads (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    -- Optional brain project association (memory / wiki share project ids).
    -- ON DELETE SET NULL because deleting a project should NOT torch
    -- the user's chat history — that's a different mental model.
    project_id      uuid REFERENCES brain.projects(id) ON DELETE SET NULL,

    -- Display
    title             text NOT NULL DEFAULT '',
    last_msg_preview  text NOT NULL DEFAULT '',  -- avoids JOIN on list

    -- Per-thread overrides of user-level defaults
    model           text,
    system_prompt   text,

    -- Pinned + archived (replaces tags / folders for v0.1)
    pinned          boolean NOT NULL DEFAULT false,
    archived        boolean NOT NULL DEFAULT false,

    -- Long-context summarization cache
    summary                text,
    summary_until_position bigint,

    -- Multi-agent extension fields (see §14.5 design doc).
    -- Wired in but kept nullable so 0.1 functionality doesn't depend
    -- on them; later phases can use without schema migration.
    agent_id         uuid,                         -- L1: bound agent
    agent_chain      jsonb,                        -- L3: orchestration policy
    parent_thread_id uuid,                         -- branch (no FK to avoid
                                                   --   cycles; app-level integ.)

    -- Direct mode privacy toggle (§3.5.3): off → only metadata stored,
    -- messages never sync to server.
    sync_enabled    boolean NOT NULL DEFAULT true,

    metadata        jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Primary list query: user's non-archived threads, recent first
CREATE INDEX IF NOT EXISTS threads_user_updated
    ON chat.threads (user_id, archived, updated_at DESC);

-- Pinned subset surfaces above the rest in the sidebar
CREATE INDEX IF NOT EXISTS threads_user_pinned
    ON chat.threads (user_id, archived, updated_at DESC)
    WHERE pinned;

-- Drilling in from a project page (filter by project)
CREATE INDEX IF NOT EXISTS threads_project_updated
    ON chat.threads (project_id, updated_at DESC)
    WHERE project_id IS NOT NULL;

-- ─── message_groups (multi-model parallel placeholder) ────────

CREATE TABLE IF NOT EXISTS chat.message_groups (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id         uuid NOT NULL REFERENCES chat.threads(id) ON DELETE CASCADE,
    user_id           uuid NOT NULL,
    parent_message_id uuid,        -- the user message that triggered the fanout
    type              text NOT NULL DEFAULT 'parallel'
                            CHECK (type IN ('parallel','compression')),
    metadata          jsonb NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS message_groups_thread
    ON chat.message_groups (thread_id, created_at DESC);

-- ─── messages ─────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS chat.messages (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id   uuid NOT NULL REFERENCES chat.threads(id) ON DELETE CASCADE,
    -- Redundant user_id avoids JOIN for owner-scoping
    user_id     uuid NOT NULL,

    role        text NOT NULL
                CHECK (role IN ('user','assistant','tool','system')),

    -- Plain text body. For assistant messages this is the rendered
    -- string (markdown). For richer content (images / tool calls /
    -- thinking) see `parts`.
    content     text NOT NULL DEFAULT '',
    parts       jsonb NOT NULL DEFAULT '[]',

    -- tool_use ↔ tool_result pairing (Anthropic toolu_XX).
    tool_call_id text,

    -- Branching: edit a message → archive subsequent + fork from this
    parent_id   uuid REFERENCES chat.messages(id) ON DELETE SET NULL,

    -- Model + token accounting
    model       text,
    prompt_tokens integer,
    completion_tokens integer,

    -- 6-state machine aligned with cherry-studio (§14.4 modification):
    --   pending     — created but not yet started
    --   processing  — server is preparing (loading history / calling Hub)
    --   streaming   — receiving stream chunks
    --   success     — done
    --   error       — failed
    --   paused      — user-cancelled mid-stream
    status      text NOT NULL DEFAULT 'success'
                CHECK (status IN ('pending','processing','streaming',
                                  'success','error','paused')),
    error       text,

    -- Client-supplied UUID for dedup on retry / offline replay.
    client_id   text,

    -- Multi-agent extensions (§14.5):
    agent_id         uuid,    -- L3: which agent produced (assistant only)
    message_group_id uuid REFERENCES chat.message_groups(id) ON DELETE SET NULL,

    -- Position is a thread-scoped monotonic counter. We use a single
    -- sequence shared across threads (cheaper than per-thread) — the
    -- index includes thread_id so cross-thread global ordering doesn't
    -- matter.
    position    bigserial,

    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Client-side dedup key
CREATE UNIQUE INDEX IF NOT EXISTS messages_thread_client_id
    ON chat.messages (thread_id, client_id)
    WHERE client_id IS NOT NULL;

-- Primary read path: list a thread in order
CREATE INDEX IF NOT EXISTS messages_thread_position
    ON chat.messages (thread_id, position);

-- Cleanup job: find orphan streaming messages
CREATE INDEX IF NOT EXISTS messages_user_streaming
    ON chat.messages (user_id, status, updated_at)
    WHERE status = 'streaming';

-- Grouping (multi-model parallel)
CREATE INDEX IF NOT EXISTS messages_group
    ON chat.messages (message_group_id)
    WHERE message_group_id IS NOT NULL;

-- ─── trigger: bump thread.updated_at + last_msg_preview ───────

CREATE OR REPLACE FUNCTION chat.touch_thread() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE chat.threads
       SET updated_at = NEW.updated_at,
           -- Don't pollute preview with mid-stream content; only commit
           -- on terminal states or for user messages.
           last_msg_preview = CASE
               WHEN NEW.status IN ('success','error','paused')
                 OR NEW.role = 'user'
               THEN LEFT(NEW.content, 200)
               ELSE last_msg_preview
           END
     WHERE id = NEW.thread_id;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS messages_touch_thread ON chat.messages;
CREATE TRIGGER messages_touch_thread
    AFTER INSERT OR UPDATE ON chat.messages
    FOR EACH ROW EXECUTE FUNCTION chat.touch_thread();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS messages_touch_thread ON chat.messages;
DROP FUNCTION IF EXISTS chat.touch_thread();
DROP TABLE IF EXISTS chat.messages;
DROP TABLE IF EXISTS chat.message_groups;
DROP TABLE IF EXISTS chat.threads;
DROP SCHEMA IF EXISTS chat CASCADE;

-- +goose StatementEnd
