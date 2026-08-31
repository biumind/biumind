-- ============================================================================
-- 00007_client_docproc.sql — 客户端文档解析（Client Docproc P1）
--
-- 契约：docs/BiuMind-Client-Docproc-Design.md（2026-08-31 决策已拍板）：
--
--   1. wiki_sources.parse_meta —— 解析 provenance（parser/version/format/
--      page_count）。客户端 docproc-web 本机解析后经 POST sources 随
--      raw_text 提交；服务端 wiki-parse worker 路径暂不写（留 NULL 即
--      服务端解析，无需回填）。合法性由 Go 写路径白名单校验，DB 不加
--      CHECK（与 metadata 一致，规则集中在 Go 侧）。
--   2. ingest_tasks.processor —— 任务处理方标记：server（默认，现有
--      worker 路径）/ client（P2 起客户端本机 ingest 镜像任务用，支持
--      客户端进程被杀后云端 reaper 接管续跑）。P1 只加列，无写入方。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE brain.wiki_sources
    ADD COLUMN IF NOT EXISTS parse_meta jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE brain.ingest_tasks
    ADD COLUMN IF NOT EXISTS processor text NOT NULL DEFAULT 'server',
    ADD CONSTRAINT ingest_tasks_processor_check
        CHECK (processor IN ('server', 'client'));
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
ALTER TABLE brain.ingest_tasks DROP CONSTRAINT IF EXISTS ingest_tasks_processor_check;
ALTER TABLE brain.ingest_tasks DROP COLUMN IF EXISTS processor;
ALTER TABLE brain.wiki_sources DROP COLUMN IF EXISTS parse_meta;
-- +goose StatementEnd
