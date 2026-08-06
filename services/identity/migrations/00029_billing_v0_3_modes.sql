-- v0.3 M5: 让 billing 链路支持新增的 modality (embedding / rerank /
-- audio_speech). image_generation / video_generation 复用现有
-- aigc_image / aigc_video, 不动.
--
-- 三处 ref_type CHECK + 一处 cost_basis CHECK 同步扩.
--
-- 风险评估:
--   - 这三个 CHECK 加 enum 值是 add-only 操作, 不影响现有数据
--   - 现有行的 ref_type / cost_basis 都在原 enum 里, ALTER 之后照旧合法
--   - 所有 chat / aigc 老路径完全不受影响
--   - 上线后 model-relay 才会写新 ref_type, identity 上线一定要在
--     model-relay 之前部署 (本次 M5 deploy 顺序: identity → model-relay)

-- +goose Up
-- +goose StatementBegin

-- 1. pricing_book.ref_type 加 embedding / rerank / audio_speech
ALTER TABLE billing.pricing_book DROP CONSTRAINT IF EXISTS pricing_book_ref_type_check;
ALTER TABLE billing.pricing_book
    ADD CONSTRAINT pricing_book_ref_type_check
    CHECK (ref_type IN (
        'chat',
        'aigc_image',
        'aigc_video',
        'digital_human',
        'hotparse_parse',
        'embedding',         -- M2 新增
        'rerank',            -- M2.5 新增
        'audio_speech'       -- M1 新增
    ));

-- 2. pricing_book.cost_basis 加 per_search_unit / per_kchar
ALTER TABLE billing.pricing_book DROP CONSTRAINT IF EXISTS pricing_book_cost_basis_check;
ALTER TABLE billing.pricing_book
    ADD CONSTRAINT pricing_book_cost_basis_check
    CHECK (cost_basis IN (
        'per_call',
        'per_mtok',
        'per_second',
        'per_image_megapixel',
        'per_search_unit',   -- M2.5 新增 (Cohere/dashscope rerank 计费单位)
        'per_kchar'          -- M1 新增 (cosyvoice TTS 按千字符)
    ));

-- 3. credit_holds.ref_type 加 _request 后缀新值 (跟 chat_message / agent_step
--    / aigc_task 同结构, 后缀语义: 这是一次 sync API request 的 hold).
ALTER TABLE identity.credit_holds DROP CONSTRAINT IF EXISTS credit_holds_ref_type_check;
ALTER TABLE identity.credit_holds
    ADD CONSTRAINT credit_holds_ref_type_check
    CHECK (ref_type IN (
        'chat_message',
        'agent_step',
        'aigc_task',
        'embedding_request',       -- M2
        'rerank_request',          -- M2.5
        'audio_speech_request',    -- M1
        'image_request',           -- M3 (跟 aigc_task 区分: aigc_task 走 /v1/jobs 异步, image_request 走 sync facade /v1/images/generations)
        'video_request'            -- M4
    ));

-- 4. credit_logs.ref_type 同步扩 (settle 时 logs 行的 ref_type 跟 holds 一致)
ALTER TABLE identity.credit_logs DROP CONSTRAINT IF EXISTS credit_logs_ref_type_check;
ALTER TABLE identity.credit_logs
    ADD CONSTRAINT credit_logs_ref_type_check
    CHECK (ref_type IN (
        'aigc_task',
        'chat_message',
        'recharge',
        'plan_grant',
        'refund',
        'reward',
        'admin',
        'embedding_request',
        'rerank_request',
        'audio_speech_request',
        'image_request',
        'video_request'
    ));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 回滚: 把新写入的行 ref_type 拉回 'chat_message' / 'aigc_task' (近似归类)
-- 然后还原 CHECK. 实际上 down 通常不会被跑 — 这里只为完整性.

-- credit_logs / credit_holds 里把新 ref_type 行归并到 chat_message
UPDATE identity.credit_holds
   SET ref_type = 'chat_message'
 WHERE ref_type IN ('embedding_request','rerank_request','audio_speech_request',
                    'image_request','video_request');
UPDATE identity.credit_logs
   SET ref_type = 'chat_message'
 WHERE ref_type IN ('embedding_request','rerank_request','audio_speech_request',
                    'image_request','video_request');

-- pricing_book 里把新 ref_type 改成 'chat' (近似)
UPDATE billing.pricing_book
   SET ref_type = 'chat'
 WHERE ref_type IN ('embedding','rerank','audio_speech');
-- per_search_unit / per_kchar 改成 per_call (近似)
UPDATE billing.pricing_book
   SET cost_basis = 'per_call'
 WHERE cost_basis IN ('per_search_unit','per_kchar');

-- 重新加旧 CHECK
ALTER TABLE billing.pricing_book DROP CONSTRAINT IF EXISTS pricing_book_ref_type_check;
ALTER TABLE billing.pricing_book
    ADD CONSTRAINT pricing_book_ref_type_check
    CHECK (ref_type IN ('chat','aigc_image','aigc_video','digital_human','hotparse_parse'));

ALTER TABLE billing.pricing_book DROP CONSTRAINT IF EXISTS pricing_book_cost_basis_check;
ALTER TABLE billing.pricing_book
    ADD CONSTRAINT pricing_book_cost_basis_check
    CHECK (cost_basis IN ('per_call','per_mtok','per_second','per_image_megapixel'));

ALTER TABLE identity.credit_holds DROP CONSTRAINT IF EXISTS credit_holds_ref_type_check;
ALTER TABLE identity.credit_holds
    ADD CONSTRAINT credit_holds_ref_type_check
    CHECK (ref_type IN ('chat_message','agent_step','aigc_task'));

ALTER TABLE identity.credit_logs DROP CONSTRAINT IF EXISTS credit_logs_ref_type_check;
ALTER TABLE identity.credit_logs
    ADD CONSTRAINT credit_logs_ref_type_check
    CHECK (ref_type IN ('aigc_task','chat_message','recharge','plan_grant','refund','reward','admin'));

-- +goose StatementEnd
