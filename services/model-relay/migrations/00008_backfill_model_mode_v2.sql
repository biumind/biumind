-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00008 backfill v2 — 修补 0007 漏掉的国产/小众品牌脏数据
--
-- 0007 之后 inferMode 启发式扩展了关键字 (commit B+ B), 但已落库的
-- mode='chat' 行不会被 sync 自动刷新 (Update 路径透传 existing.Mode).
-- 这次一次性扫所有 mode='chat' AND manual_override=false 的行, 按新关键字
-- 把误分类回滚到正确 mode.
--
-- 关键字与 sync.go:inferMode (commit ed046a8) 完全保持同步, 所以本迁移的
-- 修复结果跟"未来重新同步上游会得到的结果"一致.
--
-- 影响范围: admin DB 当前 31 条已知脏数据 + 同类品牌的潜在覆盖.
-- 安全约束: 仅动 manual_override=false 的行, 不覆盖管理员手工设置.
-- ═══════════════════════════════════════════════════════════════════

-- 1) embedding — m3e- / nomic-embed (0007 已覆盖 bge / embed / voyage / e5- / gte-)
UPDATE model_relay.models
   SET mode = 'embedding', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) ~ '(^|/)m3e-'
     OR lower(code) LIKE '%nomic-embed%'
   );

-- 2) image_generation — qwen-image / hidream / recraft / hunyuan-image / cogview / seedream
UPDATE model_relay.models
   SET mode = 'image_generation', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%qwen-image%'
     OR lower(code) LIKE '%qwen/qwen-image%'
     OR lower(code) LIKE '%hidream%'
     OR lower(code) LIKE '%recraft%'
     OR lower(code) LIKE '%hunyuan-image%'
     OR lower(code) LIKE '%cogview%'
     OR lower(code) LIKE '%seedream%'
   );

-- 3) video_generation — seedance / hailuo / vidu / minimax-video / wanx / mochi
UPDATE model_relay.models
   SET mode = 'video_generation', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%seedance%'
     OR lower(code) LIKE '%hailuo%'
     OR lower(code) LIKE '%/vidu%'
     OR lower(code) LIKE 'vidu%'
     OR lower(code) LIKE '%minimax-video%'
     OR lower(code) LIKE '%wanx%'
     OR lower(code) LIKE '%mochi%'
   );

-- 4) audio_transcription — paraformer / sensevoice / funasr / seed-asr
--   (放在 TTS 之前, 保持与 inferMode 优先级一致)
UPDATE model_relay.models
   SET mode = 'audio_transcription', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%paraformer%'
     OR lower(code) LIKE '%sensevoice%'
     OR lower(code) LIKE '%funasr%'
     OR lower(code) LIKE '%seed-asr%'
   );

-- 5) audio_speech — cosyvoice / chattts / fish-speech / fish-audio / spark-tts /
--                   indextts / melotts / styletts / voicecraft / maskgct
UPDATE model_relay.models
   SET mode = 'audio_speech', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%cosyvoice%'
     OR lower(code) LIKE '%chattts%'
     OR lower(code) LIKE '%fish-speech%'
     OR lower(code) LIKE '%fish-audio%'
     OR lower(code) LIKE '%spark-tts%'
     OR lower(code) LIKE '%indextts%'
     OR lower(code) LIKE '%melotts%'
     OR lower(code) LIKE '%styletts%'
     OR lower(code) LIKE '%voicecraft%'
     OR lower(code) LIKE '%maskgct%'
   );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 不可回滚: 数据修正没有"原 mode"信息可恢复. 如需个别行回退, 手工
-- UPDATE model_relay.models SET mode='chat' WHERE id IN (...).

-- +goose StatementEnd
