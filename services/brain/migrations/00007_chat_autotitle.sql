-- +goose Up
-- +goose StatementBegin

-- ─── Chat: auto-title threads from first user message ────────
--
-- When a user sends the first message in an empty-titled thread,
-- derive a title from the message content (LEFT 60 chars). This is
-- a server-side concern so all clients (web/desktop/mobile/CLI)
-- benefit without each one rolling its own logic.
--
-- The trigger from 00006_chat already touches the thread on every
-- message insert/update; we extend it to also seed title when blank.
-- "Blank" here means literal empty string (NOT NULL via the column
-- default), and we only consider role='user' rows.
--
-- Behaviour:
--   * Empty title + first user message  → title = LEFT(content, 60)
--   * Non-empty title                   → unchanged (user-renamed)
--   * Mid-stream assistant deltas       → unchanged (no role match)

CREATE OR REPLACE FUNCTION chat.touch_thread() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE chat.threads
       SET updated_at = NEW.updated_at,
           last_msg_preview = CASE
               WHEN NEW.status IN ('success','error','paused')
                 OR NEW.role = 'user'
               THEN LEFT(NEW.content, 200)
               ELSE last_msg_preview
           END,
           title = CASE
               WHEN title = ''
                AND NEW.role = 'user'
                AND length(trim(NEW.content)) > 0
               THEN LEFT(trim(NEW.content), 60)
               ELSE title
           END
     WHERE id = NEW.thread_id;
    RETURN NEW;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore the 00006 version (no title backfill).
CREATE OR REPLACE FUNCTION chat.touch_thread() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE chat.threads
       SET updated_at = NEW.updated_at,
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

-- +goose StatementEnd
