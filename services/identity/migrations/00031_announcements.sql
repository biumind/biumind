-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- PERI-4 — 公告 / 通知 inbox
--
-- admin 在后台发布公告,客户端 NotificationBell 拉取 + 显示未读角标。读态服务端
-- per-user 入库(跨设备同步)。新公告经 Realtime 推送即时下发(由 identity 发事件、
-- realtime 中继)。
--
-- 版本门槛:min/max_app_version 控制公告对哪些客户端可见(灰度/兼容)。
-- 生命周期:published=false 为草稿(不下发);expires_at 过期后不再返回。
-- ═══════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS identity.announcements (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    level            text NOT NULL DEFAULT 'info',   -- info | warning | error
    title            text NOT NULL,
    body             text NOT NULL DEFAULT '',
    body_zh          text NOT NULL DEFAULT '',
    url              text NOT NULL DEFAULT '',
    min_app_version  text NOT NULL DEFAULT '',
    max_app_version  text NOT NULL DEFAULT '',
    published        boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz
);

CREATE INDEX IF NOT EXISTS announcements_active
    ON identity.announcements (published, created_at DESC);

-- per-user 读态:一行表示该用户读过该公告。无行 = 未读。
CREATE TABLE IF NOT EXISTS identity.announcement_reads (
    user_id          uuid NOT NULL,
    announcement_id  uuid NOT NULL REFERENCES identity.announcements (id) ON DELETE CASCADE,
    read_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, announcement_id)
);

CREATE INDEX IF NOT EXISTS announcement_reads_user
    ON identity.announcement_reads (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS identity.announcement_reads;
DROP TABLE IF EXISTS identity.announcements;

-- +goose StatementEnd
