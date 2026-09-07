-- ============================================================================
-- 00012_durable_resume.sql — elicitation 中断跨进程恢复（durable resume）
-- （BiuMind-Agent-Experience-Design §6.3 P3-c）
--
-- agent_elicitations：AskUserQuestion 提问（control_request
-- {subtype:elicitation, mode:form}）的持久化真相源。此前 elicitation 只有
-- 进程内 pending map（ElicitationCenter / daemon pendingAsks），brain 或
-- daemon 重启 = 未答表单按超时 soft error 收场、chat 僵尸 session 永远卡
-- active。本表把提问落库（Queue.PublishSessionFrame 的 FrameObserver 链
-- 看到提问帧即落 pending 行；form_answer 终态帧 / 迟到作答 CAS 置
-- answered），使 loop 死亡后用户仍能作答、resume 端点能注入答案重跑。
--
-- status 机：
--   pending   → 已落库待作答
--   answered  → 拿到终态（accept/decline/cancel/timeout 都落 answer JSONB）
--   expired   → janitor sweep：expires_at（创建后 7 天）到期仍未答
--   cancelled → 同 session 落了新提问（daemon work redeliver 重跑重问），
--               旧 pending 行批量作废，防新旧两张表单并存
-- consumed_at：resume 端点把答案注入 synthetic user 消息后打戳，防止下次
--   resume 重复注入同一份答案（status 仍保持 answered，不加第五态）。
--
-- 无 files.objects 引用，不涉及 FGC 孤儿扫描清单。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_elicitations (
    request_id   UUID PRIMARY KEY,              -- == control_request 帧的 request_id / elicitation_id
    session_id   UUID NOT NULL REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    thread_id    UUID,                          -- 冗余自 agent_sessions（INSERT...SELECT 一次填入）
    user_id      UUID NOT NULL,
    payload      JSONB NOT NULL,                -- {question, header, multi_select, options}（requested_schema 的 x-biumind-question）
    status       TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'answered', 'expired', 'cancelled')),
    answer       JSONB,                         -- {action, content}（action: accept|decline|cancel|timeout）
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    answered_at  TIMESTAMPTZ,
    consumed_at  TIMESTAMPTZ,                   -- resume 已把答案注入 synthetic user 消息
    expires_at   TIMESTAMPTZ NOT NULL           -- 创建后 7 天
);

-- resume / GET elicitations：按 session 找 pending / answered 行
CREATE INDEX IF NOT EXISTS agent_elicitations_session_status_idx
    ON agent_elicitations (session_id, status);

-- janitor 过期 sweep：只扫 pending 行
CREATE INDEX IF NOT EXISTS agent_elicitations_pending_expiry_idx
    ON agent_elicitations (status, expires_at) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS agent_elicitations;
-- +goose StatementEnd
