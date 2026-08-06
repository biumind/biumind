-- +goose Up
-- +goose StatementBegin
-- chat 消息搜索 — tsvector + GIN 部分索引 + trigger
--
-- 设计: docs/BiuMind-Chat-Search-Design.md
--
-- chat.messages 没有 deleted_at (走 CASCADE 真删), 所以部分索引仅按
-- status 过滤掉 streaming 中间态。

ALTER TABLE chat.messages
    ADD COLUMN IF NOT EXISTS search_vector tsvector;
-- +goose StatementEnd

-- +goose StatementBegin
-- Trigger: 仅 terminal 状态 (success/error) 才计算 search_vector,
-- streaming/pending/processing/paused 期间保持 NULL — 部分索引会自动
-- 排除它们, 流式过程中也搜不到中间态 (符合设计意图)。
--
-- content (用户输入或最终答复) 给 A 权重, parts 里 type=text/thinking
-- 块的 text 给 B 权重 — 推理/工具内容相关性低于正文。
CREATE OR REPLACE FUNCTION chat.update_message_search_vector()
RETURNS trigger LANGUAGE plpgsql AS $fn$
DECLARE
    parts_text text;
BEGIN
    IF NEW.status NOT IN ('success', 'error') THEN
        NEW.search_vector := NULL;
        RETURN NEW;
    END IF;

    -- jsonb_array_elements 在 parts 不是 array 时会爆 — 但 schema 默认 '[]'::jsonb
    -- + NOT NULL, 实际不会遇到。仍 wrap COALESCE 防御。
    SELECT string_agg(elem->>'text', ' ')
      INTO parts_text
      FROM jsonb_array_elements(COALESCE(NEW.parts, '[]'::jsonb)) AS elem
     WHERE elem ? 'type'
       AND elem->>'type' IN ('text', 'thinking')
       AND elem ? 'text';

    NEW.search_vector :=
        setweight(to_tsvector('biumind_zhcn', COALESCE(NEW.content, '')), 'A')
      || setweight(to_tsvector('biumind_zhcn', COALESCE(parts_text, '')), 'B');
    RETURN NEW;
END;
$fn$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS chat_messages_search_vector_trigger ON chat.messages;
CREATE TRIGGER chat_messages_search_vector_trigger
    BEFORE INSERT OR UPDATE OF content, parts, status
    ON chat.messages
    FOR EACH ROW
    EXECUTE FUNCTION chat.update_message_search_vector();
-- +goose StatementEnd

-- +goose StatementBegin
-- GIN 部分索引 — 仅索引 terminal 状态消息, ~50% 大小节约。
CREATE INDEX IF NOT EXISTS chat_messages_search_idx
    ON chat.messages USING GIN (search_vector)
    WHERE status IN ('success', 'error');
-- +goose StatementEnd

-- +goose StatementBegin
-- 用户级时间索引 — 缩小 GIN bitmap-and 候选集
CREATE INDEX IF NOT EXISTS chat_messages_search_user_created
    ON chat.messages (user_id, created_at DESC)
    WHERE status IN ('success', 'error');
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill 已有消息. 单批 UPDATE; 大用户量级 (~100k 消息) 应在 30s 内完成。
-- 万一卡住, 单独跑这条 SQL 不阻塞 schema 演进 (索引已建, 新写入 trigger 已生效)。
UPDATE chat.messages
   SET search_vector = (
       setweight(to_tsvector('biumind_zhcn', COALESCE(content, '')), 'A')
     || setweight(to_tsvector('biumind_zhcn', COALESCE((
            SELECT string_agg(elem->>'text', ' ')
              FROM jsonb_array_elements(COALESCE(parts, '[]'::jsonb)) AS elem
             WHERE elem ? 'type'
               AND elem->>'type' IN ('text', 'thinking')
               AND elem ? 'text'
        ), '')), 'B')
   )
 WHERE status IN ('success', 'error')
   AND search_vector IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS chat_messages_search_vector_trigger ON chat.messages;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS chat.update_message_search_vector();
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS chat.chat_messages_search_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS chat.chat_messages_search_user_created;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE chat.messages DROP COLUMN IF EXISTS search_vector;
-- +goose StatementEnd
