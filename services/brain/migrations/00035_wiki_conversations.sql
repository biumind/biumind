-- +goose Up
-- +goose StatementBegin
-- 项目内对话（与 brain.chat_sessions/chat_messages 顶层 chat 完全独立）。
-- 顶层 chat 是 ProvenAI session 模型（流式 + 工具调用 + token 计费）；
-- wiki 项目内 chat 是简单的"对项目内容提问 + LLM 综合"模型，conversations
-- 横向归属到 wiki 项目而非用户全局。
--
-- 后续 worker 上线后会消费 wiki_messages.role='user' 行，调 LLM 写
-- role='assistant' 行 + write brain.events ('conversation.message.created'
-- entity_id=conversation_id) 让前端 syncws 推送实时增量。

CREATE TABLE IF NOT EXISTS brain.wiki_conversations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id        uuid NOT NULL,
    title           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE INDEX wiki_conversations_project_idx
    ON brain.wiki_conversations(project_id, updated_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX wiki_conversations_owner_idx
    ON brain.wiki_conversations(owner_id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS brain.wiki_messages (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL REFERENCES brain.wiki_conversations(id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content         text NOT NULL DEFAULT '',
    -- 后续可扩展：cited_pages jsonb / model text / token_usage jsonb / etc.
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX wiki_messages_conv_idx
    ON brain.wiki_messages(conversation_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.wiki_messages;
DROP TABLE IF EXISTS brain.wiki_conversations;
-- +goose StatementEnd
