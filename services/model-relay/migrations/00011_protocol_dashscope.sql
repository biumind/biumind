-- v0.3 M1 — model_relay.providers.protocol 接受 'dashscope' (阿里云 DashScope 私有协议).
--
-- 现状: 00001 schema 把 protocol CHECK 限定在 ('openai_compat','anthropic').
-- 阿里云 cosyvoice / paraformer / wanx 等模型走 dashscope native API
-- (非 OpenAI-compat 形态), 需要新增协议枚举值, 并由独立 dashscope.Adaptor
-- 路由处理. 见 docs/BiuMind-Multimodal-Gateway-Design.md §3.
--
-- 现有 dashscope chat 模型通常以 protocol=openai_compat 入库 (走 openai
-- adaptor), 本次迁移**不改动它们**. 用户为 cosyvoice/paraformer/wanx
-- 创建新 provider 行时显式选 protocol=dashscope.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE model_relay.providers DROP CONSTRAINT IF EXISTS providers_protocol_check;
ALTER TABLE model_relay.providers
  ADD CONSTRAINT providers_protocol_check
  CHECK (protocol IN ('openai_compat', 'anthropic', 'dashscope'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 回滚: 先把 dashscope 行回写成 openai_compat (chat 还能走 openai adaptor),
-- 但 audio/image AIGC 模型会失能 — 这是预期 (回滚 = 放弃 dashscope 协议).
UPDATE model_relay.providers SET protocol = 'openai_compat' WHERE protocol = 'dashscope';
ALTER TABLE model_relay.providers DROP CONSTRAINT IF EXISTS providers_protocol_check;
ALTER TABLE model_relay.providers
  ADD CONSTRAINT providers_protocol_check
  CHECK (protocol IN ('openai_compat', 'anthropic'));
-- +goose StatementEnd
