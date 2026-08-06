-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- aigc seed —— 默认 provider + 起步模型字典
--
-- 让 P4 阶段 Flutter 客户端进入「创作」页时立刻能拉到模型选项。
-- 真正的 API key 通过 model_relay.credentials 表 + DASHSCOPE_API_KEY env
-- 注入 (这里只挂 credentials_ref 占位字符串, P3 阶段 worker 接入时再绑定)。
--
-- 模型 config JSON 字段约定（前端 generation_form_controller 解析此结构）:
--   {
--     "aspect_ratios": [{"key":"16:9","value":"16:9","label":"16:9"}, ...],
--     "resolutions":   [{"key":"720p","value":"720p","label":"720P"}, ...],
--     "duration":      {"min":5,"max":60,"step":1,"default":5},  -- 视频专用
--     "features": {
--       "reference_image":     "Y",
--       "reference_image_count": 5,
--       "first_frame":         "Y",  -- 视频专用
--       "last_frame":          "Y"
--     }
--   }
--
-- 设计来源:
--   docs/BiuMind-AIGC-Migration-Plan.md §1 / §6
--   docs/BiuMind-AIGC-Dev-Plan.md §2 P2-9
--   zhiying tb_ai_model.video_config / image_config （行为基线）
-- ═══════════════════════════════════════════════════════════════════

-- ─── Providers (1 个) ─────────────────────────────────

INSERT INTO aigc.providers
    (code, name, base_url, priority, enabled, config)
VALUES (
    'dashscope',
    'DashScope (阿里通义)',
    'https://dashscope.aliyuncs.com',
    100,
    true,
    '{"region":"cn-beijing"}'::jsonb
) ON CONFLICT (code) DO UPDATE SET
    name       = EXCLUDED.name,
    base_url   = EXCLUDED.base_url,
    priority   = EXCLUDED.priority,
    enabled    = EXCLUDED.enabled,
    config     = EXCLUDED.config,
    updated_at = now();

-- ─── Models (2 个: 文生图 + 文生视频) ───────────────

-- 通义万相 2.6 文生图
INSERT INTO aigc.models
    (code, type, display_name, provider_code, price_credits, config, enabled, sort_order)
VALUES (
    'wanx-2.6-t2i',
    'image',
    '通义万相 2.6 (图)',
    'dashscope',
    20,
    '{
      "aspect_ratios": [
        {"key":"1:1","value":"1024x1024","label":"1:1"},
        {"key":"16:9","value":"1280x720","label":"16:9"},
        {"key":"9:16","value":"720x1280","label":"9:16"},
        {"key":"4:3","value":"1024x768","label":"4:3"},
        {"key":"3:4","value":"768x1024","label":"3:4"}
      ],
      "resolutions": [
        {"key":"720p","value":"720p","label":"720P"},
        {"key":"1080p","value":"1080p","label":"1080P"}
      ],
      "features": {
        "reference_image": "Y",
        "reference_image_count": 3
      }
    }'::jsonb,
    true,
    100
) ON CONFLICT (code) DO UPDATE SET
    type          = EXCLUDED.type,
    display_name  = EXCLUDED.display_name,
    provider_code = EXCLUDED.provider_code,
    price_credits = EXCLUDED.price_credits,
    config        = EXCLUDED.config,
    enabled       = EXCLUDED.enabled,
    sort_order    = EXCLUDED.sort_order,
    updated_at    = now();

-- 通义万相 2.6 文生视频
INSERT INTO aigc.models
    (code, type, display_name, provider_code, price_credits, pricing_rule, config, enabled, sort_order)
VALUES (
    'wanx-2.6-t2v',
    'video',
    '通义万相 2.6 (视频)',
    'dashscope',
    40,  -- 5s 720p 基础价
    -- 视频按时长 / 分辨率加价 (worker 在 Consume 前按此规则计算 final cost_credits)
    '{
      "by_duration": [
        {"max_seconds": 5,  "multiplier": 1.0},
        {"max_seconds": 10, "multiplier": 1.8},
        {"max_seconds": 15, "multiplier": 2.6}
      ],
      "by_resolution": [
        {"resolution": "720p",  "multiplier": 1.0},
        {"resolution": "1080p", "multiplier": 2.0}
      ]
    }'::jsonb,
    '{
      "aspect_ratios": [
        {"key":"16:9","value":"16:9","label":"16:9"},
        {"key":"9:16","value":"9:16","label":"9:16"},
        {"key":"1:1","value":"1:1","label":"1:1"}
      ],
      "resolutions": [
        {"key":"720p","value":"720p","label":"720P"},
        {"key":"1080p","value":"1080p","label":"1080P"}
      ],
      "duration": {"min": 5, "max": 15, "step": 5, "default": 5},
      "features": {
        "first_frame": "Y",
        "last_frame":  "Y",
        "reference_image": "Y",
        "reference_image_count": 1
      }
    }'::jsonb,
    true,
    200
) ON CONFLICT (code) DO UPDATE SET
    type          = EXCLUDED.type,
    display_name  = EXCLUDED.display_name,
    provider_code = EXCLUDED.provider_code,
    price_credits = EXCLUDED.price_credits,
    pricing_rule  = EXCLUDED.pricing_rule,
    config        = EXCLUDED.config,
    enabled       = EXCLUDED.enabled,
    sort_order    = EXCLUDED.sort_order,
    updated_at    = now();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM aigc.models WHERE code IN ('wanx-2.6-t2i', 'wanx-2.6-t2v');
DELETE FROM aigc.providers WHERE code = 'dashscope';

-- +goose StatementEnd
