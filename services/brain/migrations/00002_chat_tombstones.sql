-- ============================================================================
-- 00002_chat_tombstones.sql — chat 删除墓碑（P1.1 多设备同步正确性）
--
-- threads/messages 仍是 hard delete，但删除同事务在 chat.tombstones 落一条
-- 墓碑，保留 30 天（写入侧惰性清理）。离线设备通过
-- GET /v1/chat/tombstones?since= 拉取墓碑，区分「本机新建未上行」与
-- 「他端已删除」从而收敛本地副本。设计：
-- docs/BiuMind-Local-Data-Isolation-Design.md §4.1
--
-- id 列与 chat.threads.id / chat.messages.id 同为 uuid（baseline §8）。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS chat.tombstones (
    id          uuid NOT NULL,
    kind        text NOT NULL
                CHECK (kind IN ('thread','message')),
    -- Redundant owner scope (no FK — the owner outlives the row's target)
    user_id     uuid NOT NULL,
    deleted_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (kind, id)
);

-- Sync pull: one user's deletions since a cursor, oldest first
CREATE INDEX IF NOT EXISTS tombstones_user_deleted
    ON chat.tombstones (user_id, deleted_at);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS chat.tombstones;
-- +goose StatementEnd
