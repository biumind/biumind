-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- 00007 backfill — 修正 sync-upstream 历史 bug 引入的脏数据
--
-- BUG 背景:
--   sync.go reconcile() 创建新模型时 ModelInput 没有 Mode 字段, INSERT
--   SQL 也没列 mode 列, 全部走 schema DEFAULT 'chat'. 导致 bge-m3 /
--   dall-e / whisper 这种非 LLM 模型全被打成 chat, 在 admin 「对话(LLM)」
--   tab 里出现.
--
-- 本迁移用关键字推断把已有的 mode='chat' 行回滚到正确 mode.
-- 只动 manual_override = false 的行, 避免覆盖管理员手工设的值.
--
-- 关键字与 sync.go:inferMode 保持同步:
--   embedding:           bge / embed / voyage / e5- / gte-
--   image_generation:    dall-e / stable-diffusion / sd- / flux / midjourney / imagen / kolors / ideogram
--   video_generation:    sora / runway / pika / kling / veo / cogvideo / hunyuan-video / wan-
--   audio_transcription: whisper / transcrib / -asr / asr-
--   audio_speech:        tts / elevenlabs / speech (排除 speech-to-text)
-- ═══════════════════════════════════════════════════════════════════

-- 1) embedding
UPDATE model_relay.models
   SET mode = 'embedding', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) ~ '(^|/)bge[-_]'
     OR lower(code) ~ '(^|/)bge$'
     OR lower(code) LIKE '%embed%'
     OR lower(code) LIKE '%voyage-%'
     OR lower(code) ~ '(^|/)e5-'
     OR lower(code) ~ '(^|/)gte-'
   );

-- 2) image_generation
UPDATE model_relay.models
   SET mode = 'image_generation', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%dall-e%'
     OR lower(code) LIKE '%stable-diffusion%'
     OR lower(code) ~ '(^|/)sd-'
     OR lower(code) LIKE '%flux%'
     OR lower(code) LIKE '%midjourney%'
     OR lower(code) LIKE '%imagen%'
     OR lower(code) LIKE '%kolors%'
     OR lower(code) LIKE '%ideogram%'
   );

-- 3) video_generation
UPDATE model_relay.models
   SET mode = 'video_generation', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%sora%'
     OR lower(code) LIKE '%runway%'
     OR lower(code) LIKE '%pika%'
     OR lower(code) LIKE '%kling%'
     OR lower(code) ~ '(^|/)veo'
     OR lower(code) LIKE '%cogvideo%'
     OR lower(code) LIKE '%hunyuan-video%'
     OR lower(code) LIKE '%wan-%'
   );

-- 4) audio_transcription (放在 TTS 之前, 否则 "tts-whisper" 命名会冲)
UPDATE model_relay.models
   SET mode = 'audio_transcription', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%whisper%'
     OR lower(code) LIKE '%transcrib%'
     OR lower(code) LIKE '%-asr%'
     OR lower(code) LIKE 'asr-%'
   );

-- 5) audio_speech (TTS)
UPDATE model_relay.models
   SET mode = 'audio_speech', updated_at = now()
 WHERE mode = 'chat'
   AND manual_override = false
   AND (
        lower(code) LIKE '%tts%'
     OR lower(code) LIKE '%elevenlabs%'
     OR (lower(code) LIKE '%speech%' AND lower(code) NOT LIKE '%speech-to-text%')
   );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 不可回滚: 这是数据修正, 没有"原 mode"信息可以恢复.
-- 如确需回退, 手工把目标行 mode 改回 'chat':
--   UPDATE model_relay.models SET mode = 'chat' WHERE id IN (...);

-- +goose StatementEnd
