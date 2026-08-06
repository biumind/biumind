-- +goose Up
-- +goose StatementBegin

-- ─── Seed providers (M5 fix) ────────────────────────────────────────
--
-- Without at least one provider row, admin can't add credentials, can't
-- add channels, can't route any /v1/messages request. The original
-- 00003_seed.sql intentionally seeded only the default model_group +
-- fx_rates because the admin UI was supposed to expose "create
-- provider"; the dev rollout caught the gap — UI was read-only and
-- sync-upstream doesn't backfill providers (it only touches models).
--
-- 9 common providers cover ~99% of typical setups. Admins can add more
-- via POST /v1/admin/providers. Codes are stable identifiers; names
-- are display-only and editable by admins.
--
-- ON CONFLICT DO NOTHING — safe to re-run; admin's manual edits to
-- existing rows survive.

INSERT INTO model_relay.providers (code, name, protocol, icon, description, status) VALUES
  ('openai',     'OpenAI',           'openai_compat', '', 'GPT-4o / o-series / o1 / o3',                  'active'),
  ('anthropic',  'Anthropic',        'anthropic',     '', 'Claude family',                                 'active'),
  ('deepseek',   'DeepSeek',         'openai_compat', '', '深度求索（DeepSeek）',                          'active'),
  ('qwen',       '通义千问',          'openai_compat', '', '阿里云通义千问',                                'active'),
  ('kimi',       'Kimi (Moonshot)',  'openai_compat', '', '月之暗面',                                       'active'),
  ('zhipu',      '智谱 GLM',          'openai_compat', '', '智谱 AI',                                        'active'),
  ('gemini',     'Google Gemini',    'openai_compat', '', '通过 OpenAI 兼容代理（如 LiteLLM/OpenRouter）',  'active'),
  ('azure',      'Azure OpenAI',     'openai_compat', '', '需 base_url + api-version header',               'active'),
  ('openrouter', 'OpenRouter',       'openai_compat', '', '一键聚合多家上游',                                'active')
ON CONFLICT (code) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 撤销时只删本 migration 显式插入的 9 条种子，admin 自建的供应商
-- (code 不在列表里) 保留。
DELETE FROM model_relay.providers WHERE code IN
  ('openai', 'anthropic', 'deepseek', 'qwen', 'kimi',
   'zhipu', 'gemini', 'azure', 'openrouter');

-- +goose StatementEnd
