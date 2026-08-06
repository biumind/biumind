-- T10.4.3 (v3): 跨设备续读 — 每用户每篇文章的滚动位置。
--
-- 为什么不复用 rss.reading_log:reading_log 是「追加型行为账本」,驱动兴趣
-- 重算 / Today 统计,每个事件一行。滚动是高频写,塞进账本会污染 ML 信号且
-- 让账本暴涨。续读要的是「当前位置」语义 —— 每 (user, entry) 一行、可覆盖,
-- 所以独立 upsert 表,与账本解耦。
--
-- pct = 已滚动比例 0..1(maxScrollExtent 的分数,与设备无关,故跨端可续)。
-- 位置是「我个人读到哪」,天然按 user_id 键,与 feed 的 scope(user/org)无关。

-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS rss.reading_progress (
    user_id    text NOT NULL,
    entry_id   uuid NOT NULL REFERENCES rss.entries(id) ON DELETE CASCADE,
    pct        real NOT NULL DEFAULT 0 CHECK (pct >= 0 AND pct <= 1),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, entry_id)
);

-- entries ON DELETE CASCADE 按 entry_id 找行;无此索引则级联删要全表扫。
CREATE INDEX IF NOT EXISTS reading_progress_entry_idx
    ON rss.reading_progress (entry_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS rss.reading_progress_entry_idx;
DROP TABLE IF EXISTS rss.reading_progress;

-- +goose StatementEnd
