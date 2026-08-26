-- ============================================================================
-- 00005_note_shares.sql — 笔记分享（公开只读链接）
--
-- 产品设计 docs/BiuMind-Notes-Share-Design.md v1.0，技术契约
-- docs/BiuMind-Technical-Architecture.md §7.6（SQL 与该节冻结一致）：
--
--   * 一篇笔记 → 一条公开链接 /s/{token}，访问者免登录只读；
--     可选密码（bcrypt）/ 有效期，可停用 / 重置（rotate）。
--   * credential_version：改密 / 重置 +1，已签发的访问 JWT（HS256，
--     claims 绑 share_id+credential_version，exp 2h）立即全失效。
--   * 「一篇笔记一个活跃分享」（D2）靠 note_id 上的部分唯一索引保证
--     （WHERE disabled_at IS NULL）；软停用后可再建/恢复。
--   * 实时语义：note_notes 硬删经 ON DELETE CASCADE 连带删分享行；
--     软删（回收站）不级联，由公开校验链每请求复核 deleted_at → 410。
--
-- FGC：本表不引用 files.objects，无需登记 orphan 扫描（§7.6 明确）。
-- 审计事件写 brain.events，scope='note_share'（创建/改密/停用/rotate/
-- 密码失败），不走本迁移。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS brain.note_shares (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id            uuid NOT NULL REFERENCES brain.note_notes(id) ON DELETE CASCADE,
    user_id            uuid NOT NULL,               -- 分享者（管理/审计）
    token              text NOT NULL UNIQUE,        -- crypto/rand 24B → 32 字符 base64url
    password_hash      text,                        -- bcrypt；NULL = 无密码
    expires_at         timestamptz,                 -- NULL = 永久
    credential_version int  NOT NULL DEFAULT 1,     -- 改密/重置 +1，已签发访问 JWT 全失效
    view_count         bigint NOT NULL DEFAULT 0,   -- 匿名聚合计数（会话级去重 S2）
    disabled_at        timestamptz,                 -- 软停用；NULL = 生效中
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- 一篇笔记一个活跃分享（D2）
CREATE UNIQUE INDEX IF NOT EXISTS note_shares_one_active_per_note
    ON brain.note_shares (note_id) WHERE disabled_at IS NULL;

-- 「我的分享」管理列表按用户扫
CREATE INDEX IF NOT EXISTS note_shares_user_idx
    ON brain.note_shares (user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS brain.note_shares;
-- +goose StatementEnd
