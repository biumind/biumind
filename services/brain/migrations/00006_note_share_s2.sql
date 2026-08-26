-- ============================================================================
-- 00006_note_share_s2.sql — 笔记分享 S2：访问上限 + 会话级计数去重
--
-- 契约：docs/BiuMind-Technical-Architecture.md §7.6「S2 增量契约
-- （2026-08-26 冻结）」：
--
--   1. note_shares.max_views —— 访问次数上限，NULL = 不限；
--      view_count >= max_views 即 exhausted（公开端 410，管理列表
--      status 枚举加 exhausted）。合法性（正整数/0=移除）由 Go 写路径
--      校验，DB 不加 CHECK（与 events/软删一致，规则集中在 Go 侧）。
--   2. note_share_view_sessions —— 会话级去重：落地页每个浏览器会话
--      上送 X-Share-Session，服务端只存 sha256（不落原始 session id），
--      (share_id, session_hash) 主键——首次插入成功才 view_count+1。
--      随 note_shares ON DELETE CASCADE 连带清理；另有 30 天 TTL
--      周期清理（store.PruneShareViewSessions，main 周期任务挂载）。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE brain.note_shares
    ADD COLUMN IF NOT EXISTS max_views int;       -- NULL = 不限次数

CREATE TABLE IF NOT EXISTS brain.note_share_view_sessions (
    share_id     uuid NOT NULL REFERENCES brain.note_shares(id) ON DELETE CASCADE,
    session_hash text NOT NULL,                   -- X-Share-Session 的 sha256 hex
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (share_id, session_hash)
);

-- TTL 清理按 created_at 扫
CREATE INDEX IF NOT EXISTS note_share_view_sessions_created_idx
    ON brain.note_share_view_sessions (created_at);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS brain.note_share_view_sessions;
ALTER TABLE brain.note_shares DROP COLUMN IF EXISTS max_views;
-- +goose StatementEnd
