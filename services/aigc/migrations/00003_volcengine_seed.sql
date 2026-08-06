-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- aigc seed —— VolcEngine (火山豆包 Ark) provider + 模型字典
--
-- API key 通过 worker 端 VOLCENGINE_ARK_API_KEY env 注入. 服务端只挂占位
-- credentials_ref. 与 dashscope 同模式.
--
-- 模型对齐 zhiying-portal 实际线上跑的:
--   doubao-seedream-4-0-250828      文生图 (2K/3K, 8 种比例, 多参考图)
--   doubao-seedance-1-5-pro-250428  文生视频 (首尾帧, 5/10s)
-- ═══════════════════════════════════════════════════════════════════

INSERT INTO aigc.providers
    (code, name, base_url, priority, enabled, config)
VALUES (
    'volcengine',
    '火山豆包 (VolcEngine Ark)',
    'https://ark.cn-beijing.volces.com',
    90,
    true,
    '{"region":"cn-beijing","api_version":"v3"}'::jsonb
) ON CONFLICT (code) DO UPDATE SET
    name       = EXCLUDED.name,
    base_url   = EXCLUDED.base_url,
    priority   = EXCLUDED.priority,
    enabled    = EXCLUDED.enabled,
    config     = EXCLUDED.config,
    updated_at = now();

-- ─── Doubao Seedream 4.0 文生图 ────────────────────

INSERT INTO aigc.models
    (code, type, display_name, provider_code, price_credits, config, enabled, sort_order)
VALUES (
    'doubao-seedream-4-0-250828',
    'image',
    '豆包 Seedream 4.0',
    'volcengine',
    25,
    '{
      "aspect_ratios": [
        {"key":"1:1","value":"2048x2048","label":"1:1"},
        {"key":"16:9","value":"2848x1600","label":"16:9"},
        {"key":"9:16","value":"1600x2848","label":"9:16"},
        {"key":"4:3","value":"2304x1728","label":"4:3"},
        {"key":"3:4","value":"1728x2304","label":"3:4"},
        {"key":"3:2","value":"2496x1664","label":"3:2"},
        {"key":"2:3","value":"1664x2496","label":"2:3"},
        {"key":"21:9","value":"3136x1344","label":"21:9"}
      ],
      "resolutions": [
        {"key":"2K","value":"2K","label":"2K"},
        {"key":"3K","value":"3K","label":"3K"}
      ],
      "features": {
        "reference_image": "Y",
        "reference_image_count": 5
      }
    }'::jsonb,
    true,
    300
) ON CONFLICT (code) DO UPDATE SET
    type          = EXCLUDED.type,
    display_name  = EXCLUDED.display_name,
    provider_code = EXCLUDED.provider_code,
    price_credits = EXCLUDED.price_credits,
    config        = EXCLUDED.config,
    enabled       = EXCLUDED.enabled,
    sort_order    = EXCLUDED.sort_order,
    updated_at    = now();

-- ─── Doubao Seedance 1.5 Pro 文生视频 ─────────────

INSERT INTO aigc.models
    (code, type, display_name, provider_code, price_credits, pricing_rule, config, enabled, sort_order)
VALUES (
    'doubao-seedance-1-5-pro-250428',
    'video',
    '豆包 Seedance 1.5 Pro',
    'volcengine',
    50,  -- 5s 720p 基础价
    '{
      "by_duration": [
        {"max_seconds": 5,  "multiplier": 1.0},
        {"max_seconds": 10, "multiplier": 1.8}
      ],
      "by_resolution": [
        {"resolution": "480p", "multiplier": 0.7},
        {"resolution": "720p", "multiplier": 1.0},
        {"resolution": "1080p","multiplier": 2.2}
      ]
    }'::jsonb,
    '{
      "aspect_ratios": [
        {"key":"16:9","value":"16:9","label":"16:9"},
        {"key":"9:16","value":"9:16","label":"9:16"},
        {"key":"1:1","value":"1:1","label":"1:1"},
        {"key":"4:3","value":"4:3","label":"4:3"},
        {"key":"21:9","value":"21:9","label":"21:9"}
      ],
      "resolutions": [
        {"key":"480p","value":"480p","label":"480P"},
        {"key":"720p","value":"720p","label":"720P"},
        {"key":"1080p","value":"1080p","label":"1080P"}
      ],
      "duration": {"min": 5, "max": 10, "step": 5, "default": 5},
      "features": {
        "first_frame": "Y",
        "last_frame":  "Y",
        "reference_image": "Y",
        "reference_image_count": 4,
        "generate_audio": "Y"
      }
    }'::jsonb,
    true,
    400
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

DELETE FROM aigc.models WHERE code IN ('doubao-seedream-4-0-250828', 'doubao-seedance-1-5-pro-250428');
DELETE FROM aigc.providers WHERE code = 'volcengine';

-- +goose StatementEnd
