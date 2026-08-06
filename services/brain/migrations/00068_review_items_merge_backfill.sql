-- +goose Up
-- S3 §7.4 分叉 6=A: 旧 dedup 高分对升级 kind=merge（强合并信号，蓝徽章）。
-- dedupe_key 不变（pair 级 "dedup:..."，WritePairs 统一用 DedupKeyForPair），
-- 仅 kind 升级，所以同一对不会重复出条目。idempotent — WHERE 已过滤。
UPDATE brain.review_items
SET kind = 'merge'
WHERE kind = 'dedup'
  AND COALESCE((payload->>'similarity')::float, 0) >= 0.92;

-- +goose Down
UPDATE brain.review_items
SET kind = 'dedup'
WHERE kind = 'merge';
