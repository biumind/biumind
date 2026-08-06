-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS brain.projects (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid NOT NULL,
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX projects_owner_idx ON brain.projects(owner_id);

CREATE TABLE IF NOT EXISTS brain.pages (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    parent_id     uuid REFERENCES brain.pages(id) ON DELETE SET NULL,
    title         text NOT NULL DEFAULT '',
    frontmatter   jsonb NOT NULL DEFAULT '{}'::jsonb,
    share_mode    text NOT NULL DEFAULT 'private',
    version       int  NOT NULL DEFAULT 1,
    deleted_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pages_project_idx ON brain.pages(project_id, parent_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS brain.blocks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     uuid NOT NULL REFERENCES brain.pages(id) ON DELETE CASCADE,
    parent_id   uuid REFERENCES brain.blocks(id) ON DELETE SET NULL,
    position    double precision NOT NULL,
    type        text NOT NULL,
    content     jsonb NOT NULL DEFAULT '{}'::jsonb,
    version     int  NOT NULL DEFAULT 1,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX blocks_page_pos_idx ON brain.blocks(page_id, position) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS brain.events (
    id          bigserial PRIMARY KEY,
    scope       text NOT NULL,                  -- "wiki:project:<uuid>"
    actor_type  text NOT NULL,                  -- "user" / "agent" / "system"
    actor_id    text NOT NULL,
    event_type  text NOT NULL,                  -- "page.created" / "block.updated" / ...
    payload     jsonb NOT NULL,
    schema_ver  int  NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_scope_id_idx ON brain.events(scope, id);
CREATE INDEX events_created_at_idx ON brain.events(created_at);

-- LISTEN/NOTIFY trigger
CREATE OR REPLACE FUNCTION brain.notify_event() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('brain_events', json_build_object(
        'scope', NEW.scope,
        'id',    NEW.id,
        'type',  NEW.event_type,
        'payload', NEW.payload
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS events_notify ON brain.events;
CREATE TRIGGER events_notify
AFTER INSERT ON brain.events
FOR EACH ROW EXECUTE FUNCTION brain.notify_event();

-- Source captures from clip extension
CREATE TABLE IF NOT EXISTS brain.sources (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL,
    kind        text NOT NULL,                  -- "webclip" / "upload" / "voice" / ...
    url         text,
    title       text NOT NULL DEFAULT '',
    raw         text NOT NULL DEFAULT '',
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    sha256      text NOT NULL,                  -- for dedup
    page_id     uuid REFERENCES brain.pages(id) ON DELETE SET NULL,
    status      text NOT NULL DEFAULT 'pending',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sources_project_idx ON brain.sources(project_id);
CREATE INDEX sources_sha_idx ON brain.sources(project_id, sha256);
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS events_notify ON brain.events;
DROP FUNCTION IF EXISTS brain.notify_event();
DROP TABLE IF EXISTS brain.sources;
DROP TABLE IF EXISTS brain.events;
DROP TABLE IF EXISTS brain.blocks;
DROP TABLE IF EXISTS brain.pages;
DROP TABLE IF EXISTS brain.projects;
