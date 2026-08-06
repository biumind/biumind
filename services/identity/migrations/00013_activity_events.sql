-- +goose Up
-- +goose StatementBegin
--
-- P2-I-3 Activity Feed — user-facing event stream.
--
-- Distinct from audit.events (security/compliance, admin-only): this table
-- powers the "what's happening" UI — page edits, skill installs, PAT mints,
-- session changes. Each event has an actor (who) plus an audience scope
-- (whose feed it lands in: a user, a workspace, or both).
--
-- Detail is jsonb so emitters can stash UI-relevant context (icons, urls,
-- counts) without schema migrations per kind.
--
-- Retention: not enforced yet. ~10 events/user/day keeps the table small
-- enough to defer partitioning until v2.

CREATE TABLE IF NOT EXISTS identity.activity_events (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id              uuid NOT NULL,
    audience_user_id      uuid,
    audience_workspace_id uuid,
    kind                  text NOT NULL,
    target_type           text,
    target_id             text,
    summary               text NOT NULL,
    detail                jsonb,
    created_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS activity_events_audience_user_at_idx
    ON identity.activity_events (audience_user_id, created_at DESC)
    WHERE audience_user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS activity_events_audience_workspace_at_idx
    ON identity.activity_events (audience_workspace_id, created_at DESC)
    WHERE audience_workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS activity_events_actor_at_idx
    ON identity.activity_events (actor_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS identity.activity_events;
-- +goose StatementEnd
