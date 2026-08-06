-- +goose Up
-- +goose StatementBegin

-- Runtime v3 R4-B：channel cooldown_until —— auto_disabled channel 的精确恢复
-- 时刻（绝对时间），取代原 supervisor 的固定 age-based cooldown。
--   * 429（限流）→ cooldown_until = now + Retry-After（上游指定）
--   * 401/403（鉴权失效）/ 402（计费）→ 长 cooldown（人工介入级）
--   * 5xx/网络（瞬态）→ 指数退避 cooldown（base × 2^n 封顶）
-- sweep 改为捞 cooldown_until <= now 的行重探；NULL = 老语义（立即可探）。
ALTER TABLE model_relay.channels
    ADD COLUMN IF NOT EXISTS cooldown_until timestamptz;

CREATE INDEX IF NOT EXISTS channels_cooldown_idx
    ON model_relay.channels (status, cooldown_until)
    WHERE status = 'auto_disabled';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS model_relay.channels_cooldown_idx;
ALTER TABLE model_relay.channels DROP COLUMN IF EXISTS cooldown_until;
-- +goose StatementEnd
