-- M9.5 (v3): 周报 cron 落库. 每周日 08:00 UTC 给每个有活动的用户生成
-- 一份"上周回顾" markdown 写到 wiki, weekly_runs 做幂等键.
--
-- iso_week = 'YYYY-Www' (例如 '2026-W24'), ISO 8601 周编号. 同 (user_id,
-- iso_week) 只允许一行 — 保证 cron 重启 / 多副本不会重复写.

-- +goose Up
CREATE TABLE IF NOT EXISTS rss.weekly_runs (
    user_id    text        NOT NULL,
    iso_week   text        NOT NULL,
    ran_at     timestamptz NOT NULL DEFAULT now(),
    -- wiki page id (UUID 字符串). 客户端 today_tab "上周回顾" 卡用此跳转.
    page_id    text        NOT NULL DEFAULT '',
    -- 简要 (top-3 topics + 阅读统计) 供前端不点开 wiki 也能预览.
    summary    text        NOT NULL DEFAULT '',
    -- LLM 生成失败时也记一行, error 非空表示这一轮失败 (避免 5min tick
    -- 反复重试浪费配额; 用户下周或手动重跑).
    error      text        NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, iso_week)
);

-- 给 cron 找 "本周还没跑过的 user" 用. 不带 partial WHERE — 就让 PG 走
-- 主键扫, 用户量上 1k 也只扫 1k 行.
CREATE INDEX IF NOT EXISTS weekly_runs_recent_idx
    ON rss.weekly_runs (ran_at DESC);

-- +goose Down
DROP INDEX IF EXISTS rss.weekly_runs_recent_idx;
DROP TABLE IF EXISTS rss.weekly_runs;
