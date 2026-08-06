-- 爆款解析 (hotparse) 字典模型 seed.
--
-- aigc 服务的 GET /v1/models?type=hotparse 与 POST /v1/generations 都读
-- model_relay.models (store/models.go: mode→type 反向映射, family→provider_code)。
-- 客户端要能选到「爆款解析」模型, 故 seed 一行 mode='hotparse' 的字典项:
--   - mode='hotparse'   → aigc type='hotparse'
--   - family='hotparse' → NATS provider_code='hotparse' → worker 路由到
--                         HotparseProvider (providers/__init__.py get())
--   - status='active'   → aigc enabled=true (客户端可见)
--
-- 这是纯字典项: 它本身**不需要 channel / credential** —— 真正的上游调用是
-- worker 经 /v1/internal/transcribe(whisper-1) + /v1/internal/chat
-- (claude-opus-4-8), 这两个模型各有自己的渠道。capabilities 里记下默认
-- STT/LLM model_code 备查 (worker 当前从 env 默认值取, task.params 可覆盖)。
--
-- 价格: hotparse 真实成本 = STT + LLM 两次 relay 调用的实际用量 (各自在
-- model_relay.pricing 按真实计费, ref_type=audio_transcription / chat)。这里
-- 写一行 cost_per_image 仅供 aigc 前端「约 N 积分」展示估值 (computeCostCredits
-- → GREATEST(cost_per_*)), 不参与真实扣费 (hotparse-v1 不会被 relay 生成调用)。

-- +goose Up
-- +goose StatementBegin
INSERT INTO model_relay.models
    (code, display_name, family, mode, pricing_strategy, dispatch_mode,
     context_window, max_output, capabilities, status, sort_order, manual_override)
VALUES
    ('hotparse-v1', '爆款解析', 'hotparse', 'hotparse', 'fixed', 'async',
     0, 0,
     '{"stt_model":"whisper-1","llm_model":"claude-opus-4-8","sources":["upload"]}'::jsonb,
     'active', 100, false)
ON CONFLICT (code) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    family       = EXCLUDED.family,
    mode         = EXCLUDED.mode,
    capabilities = EXCLUDED.capabilities,
    status       = EXCLUDED.status,
    updated_at   = now();
-- +goose StatementEnd

-- +goose StatementBegin
-- 展示用估值价格 (仅 aigc 前端 "约 N 积分"; 不参与真实扣费)。
INSERT INTO model_relay.pricing (model_id, currency, cost_per_image)
SELECT m.id, 'CNY', 20
FROM model_relay.models m
WHERE m.code = 'hotparse-v1'
  AND NOT EXISTS (
      SELECT 1 FROM model_relay.pricing p WHERE p.model_id = m.id
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM model_relay.pricing
WHERE model_id IN (SELECT id FROM model_relay.models WHERE code = 'hotparse-v1');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM model_relay.models WHERE code = 'hotparse-v1';
-- +goose StatementEnd
