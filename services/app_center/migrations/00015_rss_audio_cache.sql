-- M8.4 (v3): TTS 简报缓存. 24h TTL — 同一用户当天 Today 不变就直接返
-- cache, 不重复花 model-relay TTS 配额. 每户每天 1 条简报 (date 是 PK
-- 的一部分), 长度 ~30s mp3 ≈ 100KB → bytea 存 PG 完全 OK.
--
-- 主键 (user_id, generated_date) 而不是 content_hash:
--   - 简报内容跟当天的 today_picks 强绑定; today 自身有缓存 ttl 5min,
--     所以同一天 user 反复触发简报基本是同一段脚本
--   - 真要算 hash 失效, 拿 expires_at 兜底就够; 没必要强一致
--
-- 不缓存脚本(text), 只缓存 mp3 — 脚本可以从 today_picks 即时拼,
-- TTS 调用才是真贵的部分 (单字符 0.0xxx ¥, 30s 简报 ~ 200 字 → 几分钱).
--
-- 跟现有 rss.entries.embedding 同库同 schema; 不引新表分隔.

-- +goose Up
CREATE TABLE IF NOT EXISTS rss.audio_cache (
    user_id        text        NOT NULL,
    generated_date date        NOT NULL,
    -- 内容签名: SHA256(headline_ids || missed_ids || generated_at_truncated_hour).
    -- 同 (user_id, date) 下 content_hash 不同就 overwrite — 用户当天反复
    -- 刷新 today 拿到不同 picks 时, 对应简报会重新合成而不是返旧的.
    content_hash   bytea       NOT NULL,
    script         text        NOT NULL,        -- 朗读脚本 (debug/降级回退用)
    mp3            bytea       NOT NULL,        -- audio/mpeg, 24kbps mono, ~ 100KB
    voice          text        NOT NULL,        -- 'longanyang' / future
    model          text        NOT NULL,        -- 'cosyvoice-v3-plus' / future
    characters     int         NOT NULL DEFAULT 0,
    duration_ms    int         NOT NULL DEFAULT 0,  -- 0 = 未估算; client 加载后写回
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,         -- created_at + 24h
    PRIMARY KEY (user_id, generated_date)
);

-- GC 用 — 走全表索引足够 (audio_cache 行数小, 跟 user 数 × 1 对齐).
-- 用 partial index 的话 predicate 里有 now() 会被拒 (CREATE INDEX 要求
-- IMMUTABLE 表达式).
CREATE INDEX IF NOT EXISTS audio_cache_expires_idx
    ON rss.audio_cache (expires_at);

-- +goose Down
DROP INDEX IF EXISTS rss.audio_cache_expires_idx;
DROP TABLE IF EXISTS rss.audio_cache;
