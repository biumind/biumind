-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- W5 — 支付通道配置占位 (system_config seed)
--
-- 三行 placeholder + secret=true. admin 后台 UI (SystemConfigView.vue)
-- 渲染 3 个表单卡片让 superadmin 填商户号 / 私钥. 实际值由 superadmin
-- 在 UI 里覆盖, 这里只决定:
--   - secret=true → API 响应给非 superadmin 时全脱敏 ***
--   - description → 后台列表页 hover 提示
--
-- 字段 schema 由 services/identity/internal/billing/wechat.go +
-- alipay.go + (Stripe by-env 暂定) 定义, jsonb 自由结构.
-- ═══════════════════════════════════════════════════════════════════

INSERT INTO biumind_system.config (key, value, secret, description) VALUES
  (
    'payment.stripe',
    '{"enabled":false,"secret_key":"","webhook_secret":"","price_to_plan":{}}'::jsonb,
    true,
    'Stripe 支付 — secret_key (sk_live_...) + webhook secret + Price ID → Plan 映射'
  ),
  (
    'payment.wechat',
    '{"enabled":false,"app_id":"","mch_id":"","apiv3_key":"","cert_serial_no":"","apiclient_key_pem":"","platform_public_key":"","notify_url":""}'::jsonb,
    true,
    '微信支付 v3 — 商户号 + APIv3 密钥 (32B) + 商户私钥 PEM + 平台公钥 PEM + https 回调 URL'
  ),
  (
    'payment.alipay',
    '{"enabled":false,"app_id":"","private_key_pem":"","alipay_public_key_pem":"","notify_url":"","return_url":""}'::jsonb,
    true,
    '支付宝 — AppID + 应用私钥 PEM + 支付宝公钥 PEM + https 回调 URL'
  )
ON CONFLICT (key) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM biumind_system.config
WHERE key IN ('payment.stripe', 'payment.wechat', 'payment.alipay');

-- +goose StatementEnd
