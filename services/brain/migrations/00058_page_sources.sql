-- +goose Up
-- +goose StatementBegin
-- P1-4 source overlap 第 4 信号的数据地基：page ↔ source 多对多归属表。
-- relevance worker 算页对相关度时，共享 source 的页对 +4.0（最高权重，
-- 对齐 reference/llm_wiki graph-relevance.ts:259-265）。
--
-- 之前 page→source 归属从未落库（sources/api.go 自述），relevance 第 4 路
-- deferred（relevance.go:12-14）。本表落地后 webclip 建页即写归属
--（subscriber.go applyPage），source overlap 覆盖 webclip；upload 待
-- Phase 3 parser 跑通能建页后自动覆盖（同表）。
CREATE TABLE IF NOT EXISTS brain.page_sources (
    page_id    uuid        NOT NULL REFERENCES brain.pages(id)      ON DELETE CASCADE,
    source_id  uuid        NOT NULL REFERENCES brain.wiki_sources(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, source_id)
);
-- source→pages 反查（relevance loadGraph + delete-preview 用）
CREATE INDEX IF NOT EXISTS page_sources_source_idx ON brain.page_sources(source_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.page_sources;
-- +goose StatementEnd
