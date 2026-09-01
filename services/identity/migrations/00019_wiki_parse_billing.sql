-- ============================================================================
-- 00019_wiki_parse_billing.sql — wiki 云端解析计费（client-docproc W4）
--
-- 契约：docs/BiuMind-Client-Docproc-Design.md §4（决策②「本机免费 /
-- 云端花积分」）。wiki-parse worker 云端解析完成后，brain 经 model-relay
-- /v1/internal/usage/charge 按页扣费（Hold+Settle 即时结算）。
--
-- 新增 ref_type = 'wiki_parse_request'（沿用 *_request 多模态命名风）。
-- 两处 CHECK 同步放宽：credit_logs（账本）+ credit_holds（预扣）。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE identity.credit_logs DROP CONSTRAINT IF EXISTS credit_logs_ref_type_check;
ALTER TABLE identity.credit_logs ADD CONSTRAINT credit_logs_ref_type_check
    CHECK (ref_type IN
        ('aigc_task','chat_message','recharge','plan_grant','refund','reward','admin',
         'embedding_request','rerank_request','audio_speech_request',
         'image_request','video_request','wiki_parse_request'));

ALTER TABLE identity.credit_holds DROP CONSTRAINT IF EXISTS credit_holds_ref_type_check;
ALTER TABLE identity.credit_holds ADD CONSTRAINT credit_holds_ref_type_check
    CHECK (ref_type IN
        ('chat_message','agent_step','aigc_task',
         'embedding_request','rerank_request','audio_speech_request',
         'image_request','video_request','wiki_parse_request'));
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
ALTER TABLE identity.credit_logs DROP CONSTRAINT IF EXISTS credit_logs_ref_type_check;
ALTER TABLE identity.credit_logs ADD CONSTRAINT credit_logs_ref_type_check
    CHECK (ref_type IN
        ('aigc_task','chat_message','recharge','plan_grant','refund','reward','admin',
         'embedding_request','rerank_request','audio_speech_request',
         'image_request','video_request'));

ALTER TABLE identity.credit_holds DROP CONSTRAINT IF EXISTS credit_holds_ref_type_check;
ALTER TABLE identity.credit_holds ADD CONSTRAINT credit_holds_ref_type_check
    CHECK (ref_type IN
        ('chat_message','agent_step','aigc_task',
         'embedding_request','rerank_request','audio_speech_request',
         'image_request','video_request'));
-- +goose StatementEnd
