-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════
-- W2-2 — 4 档套餐种子数据
--
-- benefits jsonb 字段对齐 services/identity/internal/billing/billing.go
-- 的 PlanLimits struct (snake_case key):
--   hub_rpm / hub_tpm / sandbox_daily / sandbox_concurrent /
--   memory_quota / brain_projects
--
-- 价格按设计稿 (BiuMind-Billing-Redesign.md §4.2):
--   free        — $0 / ¥0
--   pro         — $19/mo  / $190/yr   (¥ 138/mo / ¥ 1380/yr)
--   team        — $99/mo  / $990/yr
--   enterprise  — $0 (custom) — Stripe 不直接支付, 销售联系
--
-- monthly_credits 是 W4 月度积分配额, W2 仅落字段:
--   free       — 0 (BYOK only, 无平台积分)
--   pro        — 10K 积分/月
--   team       — 50K 积分/月
--   enterprise — 0 (定制, 走专门 contract)
--
-- Stripe price ID 留 NULL — 当前 Stripe 集成走 env-driven (Server.PriceToPlan
-- map), W2-7 webhook 升级时把这些填进 stripe_price_monthly/yearly.
-- ═══════════════════════════════════════════════════════════════════

INSERT INTO billing.plans
    (code, name, description, sort_order,
     price_currency, price_monthly, price_yearly, monthly_credits,
     benefits, status)
VALUES
    ('free',
     'Free',
     '免费体验, BYOK 自带 API key. 适合个人尝试.',
     0,
     'USD', 0, 0, 0,
     '{
       "hub_rpm": 60,
       "hub_tpm": 50000,
       "sandbox_daily": 10,
       "sandbox_concurrent": 1,
       "memory_quota": 100,
       "brain_projects": 3
     }'::jsonb,
     'active'),

    ('pro',
     'Pro',
     '个人专业版, 每月 10K 平台积分, BYOK 仍可用.',
     1,
     'USD', 19, 190, 10000,
     '{
       "hub_rpm": 600,
       "hub_tpm": 500000,
       "sandbox_daily": 100,
       "sandbox_concurrent": 5,
       "memory_quota": 5000,
       "brain_projects": 50
     }'::jsonb,
     'active'),

    ('team',
     'Team',
     '团队版, 每月 50K 积分, 高 RPM, 长上下文模型, 优先支持.',
     2,
     'USD', 99, 990, 50000,
     '{
       "hub_rpm": 6000,
       "hub_tpm": 5000000,
       "sandbox_daily": 1000,
       "sandbox_concurrent": 20,
       "memory_quota": 100000,
       "brain_projects": 1000
     }'::jsonb,
     'active'),

    ('enterprise',
     'Enterprise',
     '企业定制, 联系销售. 专属上下游, 私有部署, SLA 99.9%.',
     3,
     'USD', 0, 0, 0,
     '{
       "hub_rpm": 60000,
       "hub_tpm": 50000000,
       "sandbox_daily": 10000,
       "sandbox_concurrent": 100,
       "memory_quota": 10000000,
       "brain_projects": 100000
     }'::jsonb,
     'active')
ON CONFLICT (code) DO UPDATE SET
    name            = EXCLUDED.name,
    description     = EXCLUDED.description,
    sort_order      = EXCLUDED.sort_order,
    price_currency  = EXCLUDED.price_currency,
    price_monthly   = EXCLUDED.price_monthly,
    price_yearly    = EXCLUDED.price_yearly,
    monthly_credits = EXCLUDED.monthly_credits,
    benefits        = EXCLUDED.benefits,
    status          = EXCLUDED.status,
    updated_at      = now();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM billing.plans WHERE code IN ('free', 'pro', 'team', 'enterprise');

-- +goose StatementEnd
