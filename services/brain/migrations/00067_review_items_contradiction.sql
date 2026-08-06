-- +goose Up
-- S3 P0-2: review_items 加 contradiction kind。
--
-- semantic.go 的 contradiction 检测原本落 kind=lint + payload.category=
-- 'contradiction'（reviews/semantic.go:301-340 upsertFinding 硬编 KindLint）。
-- 升级为独立 top-level kind 让审阅队列视觉/过滤/处理动作能按矛盾单独走，
-- 与 dedup/lint/sweep/merge/suggestion 平级。
--
-- 约束名沿用 00016 创建时的 Postgres 默认名 review_items_kind_check
-- （00016 未显式 CONSTRAINT name；\d 已确认）。
ALTER TABLE brain.review_items DROP CONSTRAINT review_items_kind_check;
ALTER TABLE brain.review_items ADD CONSTRAINT review_items_kind_check
  CHECK (kind IN ('dedup','lint','sweep','merge','suggestion','contradiction'));

-- backfill: 历史 contradiction review 升 kind。dedupe_key 不变（key 内已含
-- category=contradiction，见 semanticDedupeKey），所以升级后不会重复出新条目。
-- idempotent — WHERE 已过滤，重跑零行。
UPDATE brain.review_items
SET kind = 'contradiction'
WHERE kind = 'lint' AND payload->>'category' = 'contradiction';

-- +goose Down
UPDATE brain.review_items SET kind = 'lint' WHERE kind = 'contradiction';
ALTER TABLE brain.review_items DROP CONSTRAINT review_items_kind_check;
ALTER TABLE brain.review_items ADD CONSTRAINT review_items_kind_check
  CHECK (kind IN ('dedup','lint','sweep','merge','suggestion'));
