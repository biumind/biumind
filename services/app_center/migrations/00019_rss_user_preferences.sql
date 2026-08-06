-- M11.5 (v3): RSS 用户偏好. Settings 页落地 — 默认刷新频率 / AI 摘要
-- 预算开关 / 通知通道 / 主题 / Newsletter alias 等都存这张 jsonb.
--
-- 用单行 jsonb 而非每偏好一列: 偏好项会随产品迭代增删, jsonb 让加项
-- 不用改 schema (对照客户端缓存层存原始 payload 的同样理由). 读写都是
-- 整对象 upsert, 单用户单行, 量极小.

-- +goose Up
CREATE TABLE IF NOT EXISTS rss.user_preferences (
    user_id     text        PRIMARY KEY,
    config      jsonb       NOT NULL DEFAULT '{}',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS rss.user_preferences;
