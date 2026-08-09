-- ============================================================================
-- 00001_baseline.sql — brain 服务数据库基线（squash）
--
-- 本文件是 00001–00068 共 60 个历史 migration（缺 00005、00023–00029）
-- 的终态折叠结果，仅面向全新部署；不考虑存量库升级路径。
-- 环境：PostgreSQL 16 + pgvector + ltree + zhparser（可选，见下方 DO 块）。
--
-- 主要消除 / 折叠清单（相对历史）：
--   * 00010 + 00046  code schema 四表（tasks/task_events/task_artifacts/
--     task_commands）整对消除，不进基线。
--   * 00037 + 00062  brain.wiki_lint_issues 整对消除，不进基线
--     （lint 收敛到 review_items kind='lint'）。
--   * 00047          noop 占位（BYOK P3 延迟删列），不进基线。
--   * brain.sources  00001 建、00057 并入 wiki_sources 后 DROP —— 基线只
--     建终态 brain.wiki_sources；00057 的 INSERT…SELECT 回填不进基线。
--   * chat.providers 直接建终态：不含 key_vaults_encrypted / fetch_mode /
--     internal 三列与 providers_internal_requires_client 约束
--     （00008 建、00030 加 internal、00048 全删）；source CHECK 用 00009
--     的三值版（official/builtin/custom）。
--   * chat.threads   直接建终态：含 workdir/auto_approve（00038）与
--     provider_id（00039），不含 execution_mode（00030 加、00040 删）。
--   * chat.touch_thread 只保留 00007 的 v2 版（含自动标题）。
--   * embedding 三处（graph_nodes / memories / wiki_chunks）直接建
--     vector(1024)，ivfflat 索引只建 1024 版；00051 整个蒸发。
--   * CHECK 全部用终态：memories.kind 用 habit 版（00013）；
--     agent_sessions.state 用 5 态版（00044）；review_items.kind 用
--     6 kind 版（00067，含 contradiction）。
--   * 一次性数据操作全部剔除：00012 events backfill、00013 kind UPDATE、
--     00032 messages backfill、00045 去重 DELETE、00049 models DELETE、
--     00057 sources 回填、00061 防御性 UPDATE、00067/00068 UPDATE。
--   * 00015 ingest_tasks.source_id FK 直接指向 brain.wiki_sources（00057
--     的 repoint 终态）。
--
-- 结构说明：
--   * files schema 提到 brain 之前 —— brain.wiki_sources.file_id 与
--     brain.note_attachments.file_id 的 FK 引用 files.objects。
--   * agent_* 六表按历史（00033 有意为之）建在 public schema，不加前缀。
--   * 全部 DDL 幂等（CREATE TABLE/INDEX 一律 IF NOT EXISTS；历史上
--     部分裸 CREATE INDEX 在基线统一补上 IF NOT EXISTS）。
-- ============================================================================

-- +goose Up

-- ── 0. 扩展 + schema ───────────────────────────────────────────
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;        -- gen_random_uuid + crypto
CREATE EXTENSION IF NOT EXISTS vector;          -- pgvector
CREATE EXTENSION IF NOT EXISTS ltree;           -- graph_nodes.path
CREATE EXTENSION IF NOT EXISTS pg_trgm;         -- 模糊搜索（与 deploy init 对齐）
CREATE EXTENSION IF NOT EXISTS btree_gist;      -- 与 deploy init 对齐

-- 中文分词：zhparser（可选）。不可用时 biumind_zhcn 退化为 simple 配置，
-- 保持 to_tsvector('biumind_zhcn', …) 不破坏。
-- 与 deploy/docker-compose/postgres/init/00-extensions.sql 相同。
DO $ext$
DECLARE
  tok text;
BEGIN
  IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'zhparser') THEN
    CREATE EXTENSION IF NOT EXISTS zhparser;
    IF NOT EXISTS (SELECT 1 FROM pg_ts_config WHERE cfgname = 'biumind_zhcn') THEN
      CREATE TEXT SEARCH CONFIGURATION biumind_zhcn (PARSER = zhparser);
      -- 动态映射：不同发行版（阿里云 RDS / 自建 / 旧 SCWS）的 zhparser
      -- 暴露的 token 词性表被裁剪程度不一，硬编码列表（如 n,v,a,...,nz）
      -- 会在缺词性的版本上报 SQLSTATE 22023 "token type X does not exist"。
      -- 逐个仅映射 ts_token_type 实际返回的 token，任意版本自适应。
      FOR tok IN SELECT alias FROM ts_token_type('zhparser') LOOP
        EXECUTE format('ALTER TEXT SEARCH CONFIGURATION biumind_zhcn ADD MAPPING FOR %I WITH simple', tok);
      END LOOP;
    END IF;
  ELSE
    -- Fallback: alias biumind_zhcn to the built-in 'simple' config so
    -- search migrations using to_tsvector('biumind_zhcn', …) keep working.
    IF NOT EXISTS (SELECT 1 FROM pg_ts_config WHERE cfgname = 'biumind_zhcn') THEN
      CREATE TEXT SEARCH CONFIGURATION biumind_zhcn (COPY = simple);
    END IF;
  END IF;
END
$ext$;

CREATE SCHEMA IF NOT EXISTS brain;
CREATE SCHEMA IF NOT EXISTS chat;
CREATE SCHEMA IF NOT EXISTS files;
-- +goose StatementEnd

-- ── 1. files schema（提前：brain.wiki_sources / note_attachments 的 FK 目标）──
-- +goose StatementBegin
-- Files — 通用文件存储元数据 (artifacts L3 / chat 附件 / 未来通用 file ref)
-- 实际 blob 落 MinIO (object_key 路径, bucket 一般 'biumind-files'),
-- Postgres 仅记录元数据 + sha256 dedup 索引。
CREATE TABLE IF NOT EXISTS files.objects (
    id              uuid        PRIMARY KEY,
    user_id         uuid        NOT NULL,
    sha256          text        NOT NULL,
    size_bytes      bigint      NOT NULL,
    mime_type       text,
    bucket          text        NOT NULL,
    object_key      text        NOT NULL,
    -- 来源 (e.g. 'code-artifact' / 'chat-attachment') — 给清理 job 按
    -- 来源删 / 给统计用; 业务侧不强约束。
    source          text        NOT NULL DEFAULT 'unknown',
    -- 任意业务元数据 (e.g. 'artifact_id': '...', 'task_id': '...')
    metadata        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- 两段式上传状态: pending（presigned 未 finalize）/ ready / orphan（GC 标记）。
    status          text        NOT NULL DEFAULT 'ready',
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- soft delete; 实际从 MinIO 删走清理 job 异步做
    deleted_at      timestamptz
);

-- Sha256 dedup: 同一用户同一 sha256 复用同一 object_key, 避免重复占空间。
-- 不跨用户 dedup — 隐私 + 配额隔离。
CREATE UNIQUE INDEX IF NOT EXISTS files_objects_user_sha256_alive
    ON files.objects (user_id, sha256)
    WHERE deleted_at IS NULL;

-- 用户级 list (按时间倒序)
CREATE INDEX IF NOT EXISTS files_objects_user_created
    ON files.objects (user_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- pending / orphan 才需要扫描; ready 的对象不进这个索引省空间。
CREATE INDEX IF NOT EXISTS files_objects_status_created
    ON files.objects (status, created_at)
    WHERE status <> 'ready';
-- +goose StatementEnd

-- ── 2. brain schema：wiki 核心（projects / pages / blocks / events）──
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS brain.projects (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid NOT NULL,
    name        text NOT NULL,
    template_id text,                            -- 00064：项目模板
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS projects_owner_idx ON brain.projects(owner_id);

CREATE TABLE IF NOT EXISTS brain.pages (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    parent_id     uuid REFERENCES brain.pages(id) ON DELETE SET NULL,
    title         text NOT NULL DEFAULT '',
    frontmatter   jsonb NOT NULL DEFAULT '{}'::jsonb,
    share_mode    text NOT NULL DEFAULT 'private',
    version       int  NOT NULL DEFAULT 1,
    deleted_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- 00002：物化 tsvector，title 权重 A
    tsv           tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('biumind_zhcn', coalesce(title, '')), 'A')
    ) STORED,
    -- 00019：Louvain 社区聚类 id（worker 填充；NULL = 未聚类）
    community_id  int,
    -- 00020：wikilink enrichment 高水位（NULL ⇒ 从未 enrich）
    enriched_at   timestamptz,
    -- 00066：Milkdown Path C，body_md 权威列（blocks 为派生投影）
    body_md       text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS pages_project_idx ON brain.pages(project_id, parent_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS pages_tsv_gin ON brain.pages USING GIN (tsv) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS pages_community_idx
    ON brain.pages(project_id, community_id)
    WHERE deleted_at IS NULL AND community_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS pages_enrich_pending_idx
    ON brain.pages (project_id, updated_at)
    WHERE deleted_at IS NULL
      AND (enriched_at IS NULL OR enriched_at < updated_at);

CREATE TABLE IF NOT EXISTS brain.blocks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     uuid NOT NULL REFERENCES brain.pages(id) ON DELETE CASCADE,
    parent_id   uuid REFERENCES brain.blocks(id) ON DELETE SET NULL,
    position    double precision NOT NULL,
    type        text NOT NULL,
    content     jsonb NOT NULL DEFAULT '{}'::jsonb,
    version     int  NOT NULL DEFAULT 1,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    -- 00002：物化 tsvector，取 JSON content 的 text/caption 字段，权重 B
    tsv           tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('biumind_zhcn',
          coalesce(content->>'text', '') ||
          ' ' ||
          coalesce(content->>'caption', '')
        ), 'B')
    ) STORED,
    -- 00021：vision-caption 高水位
    captioned_at  timestamptz
);
CREATE INDEX IF NOT EXISTS blocks_page_pos_idx ON brain.blocks(page_id, position) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS blocks_tsv_gin ON brain.blocks USING GIN (tsv) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS blocks_caption_pending_idx
    ON brain.blocks (updated_at)
    WHERE deleted_at IS NULL
      AND (captioned_at IS NULL OR captioned_at < updated_at);

CREATE TABLE IF NOT EXISTS brain.events (
    id          bigserial PRIMARY KEY,
    scope       text NOT NULL,                  -- "wiki:project:<uuid>"
    actor_type  text NOT NULL,                  -- "user" / "agent" / "system"
    actor_id    text NOT NULL,
    event_type  text NOT NULL,                  -- "page.created" / "block.updated" / ...
    payload     jsonb NOT NULL,
    schema_ver  int  NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- 00012：transactional outbox 标记（poller 扫 published_at IS NULL）
    published_at timestamptz
);
CREATE INDEX IF NOT EXISTS events_scope_id_idx ON brain.events(scope, id);
CREATE INDEX IF NOT EXISTS events_created_at_idx ON brain.events(created_at);
-- 00012：partial index，只扫未发布的尾部
CREATE INDEX IF NOT EXISTS events_outbox_pending_idx
    ON brain.events (id)
 WHERE published_at IS NULL;

-- LISTEN/NOTIFY trigger
CREATE OR REPLACE FUNCTION brain.notify_event() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('brain_events', json_build_object(
        'scope', NEW.scope,
        'id',    NEW.id,
        'type',  NEW.event_type,
        'payload', NEW.payload
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS events_notify ON brain.events;
CREATE TRIGGER events_notify
AFTER INSERT ON brain.events
FOR EACH ROW EXECUTE FUNCTION brain.notify_event();
-- +goose StatementEnd

-- ── 3. brain schema：graph（graph_nodes / graph_edges / graph_block_nodes）──
-- +goose StatementBegin
-- 知识图谱节点。Identity 为 (project_id, kind, lower(name))。
-- embedding 直接建 vector(1024)（bge-m3；历史 1536 → 00051 改 1024 已折叠）。
CREATE TABLE IF NOT EXISTS brain.graph_nodes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    name        text NOT NULL,
    aliases     text[] NOT NULL DEFAULT '{}',
    summary     text NOT NULL DEFAULT '',
    -- Embedding is opt-in: written by LLM extractor. NULL until then.
    embedding   vector(1024),
    -- ltree breadcrumb (e.g. `topic.programming.rust`)
    path        ltree,
    weight      real NOT NULL DEFAULT 1.0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, kind, name)
);

CREATE INDEX IF NOT EXISTS graph_nodes_project_kind_idx
    ON brain.graph_nodes(project_id, kind);

CREATE INDEX IF NOT EXISTS graph_nodes_path_gist
    ON brain.graph_nodes USING GIST (path)
    WHERE path IS NOT NULL;

-- ivfflat needs >= ~1k rows to be effective; brute-force scan is fine before that.
CREATE INDEX IF NOT EXISTS graph_nodes_embedding_ivf
    ON brain.graph_nodes USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

-- Typed directed edges。src_id/dst_id 可指 graph_nodes.id 或 brain.pages.id，
-- 有意不加 FK（graph 层把 page 当 node；page 删除已级联 graph_block_nodes）。
CREATE TABLE IF NOT EXISTS brain.graph_edges (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    src_id            uuid NOT NULL,
    dst_id            uuid NOT NULL,
    relation          text NOT NULL,
    weight            real NOT NULL DEFAULT 1.0,
    evidence_block_id uuid REFERENCES brain.blocks(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, src_id, dst_id, relation)
);

CREATE INDEX IF NOT EXISTS graph_edges_src_idx
    ON brain.graph_edges(src_id);
CREATE INDEX IF NOT EXISTS graph_edges_dst_idx
    ON brain.graph_edges(dst_id);
CREATE INDEX IF NOT EXISTS graph_edges_project_relation_idx
    ON brain.graph_edges(project_id, relation);

-- Junction: which blocks contain which nodes (drives backlinks).
CREATE TABLE IF NOT EXISTS brain.graph_block_nodes (
    block_id   uuid NOT NULL REFERENCES brain.blocks(id) ON DELETE CASCADE,
    node_id    uuid NOT NULL REFERENCES brain.graph_nodes(id) ON DELETE CASCADE,
    confidence real NOT NULL DEFAULT 1.0,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (block_id, node_id)
);

CREATE INDEX IF NOT EXISTS graph_block_nodes_node_idx
    ON brain.graph_block_nodes(node_id);
-- +goose StatementEnd

-- ── 4. brain schema：memory ─────────────────────────────────────
-- +goose StatementBegin
-- 多层记忆（recall / preference / habit 三层共表）。
-- kind CHECK 用 00013 终态（skill → habit）；embedding 直接 vector(1024)。
CREATE TABLE IF NOT EXISTS brain.memories (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id       uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id         uuid NOT NULL,
    kind             text NOT NULL DEFAULT 'recall'
                          CHECK (kind IN ('recall', 'preference', 'habit')),
    content          text NOT NULL,
    -- Optional pgvector embedding; populated by ingest worker.
    embedding        vector(1024),
    -- 0..1 score; updated by writes and decayed by reads.
    salience         real NOT NULL DEFAULT 0.5,
    last_accessed_at timestamptz NOT NULL DEFAULT now(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS memories_project_owner_kind_idx
    ON brain.memories(project_id, owner_id, kind);

CREATE INDEX IF NOT EXISTS memories_recent_idx
    ON brain.memories(project_id, last_accessed_at DESC);

CREATE INDEX IF NOT EXISTS memories_embedding_ivf
    ON brain.memories USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;
-- +goose StatementEnd

-- ── 5. brain schema：检索与来源（wiki_chunks / wiki_sources / page_sources / ingest_tasks）──
-- +goose StatementBegin
-- Embedding-bearing slices of wiki content（RRF vector 路）。
-- embedding 直接 vector(1024)；heading_path 来自 00063。
CREATE TABLE IF NOT EXISTS brain.wiki_chunks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    page_id     uuid NOT NULL REFERENCES brain.pages(id)    ON DELETE CASCADE,
    block_id    uuid          REFERENCES brain.blocks(id)   ON DELETE CASCADE,
    ord         int  NOT NULL DEFAULT 0,
    text        text NOT NULL,
    embedding   vector(1024),
    token_count int  NOT NULL DEFAULT 0,
    heading_path text,                          -- 00063
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 主键之外，按 page 重切块时按 (page_id, ord) 顺扫
CREATE INDEX IF NOT EXISTS wiki_chunks_page_ord_idx
    ON brain.wiki_chunks(page_id, ord);

-- 命中后按 project 过滤
CREATE INDEX IF NOT EXISTS wiki_chunks_project_idx
    ON brain.wiki_chunks(project_id);

-- worker 按 embedding IS NULL 扫描待 embed 队列
CREATE INDEX IF NOT EXISTS wiki_chunks_pending_idx
    ON brain.wiki_chunks(created_at)
    WHERE embedding IS NULL;

-- ANN 查询：cosine 距离 + ivfflat；WHERE 只覆盖已 embed 的行
CREATE INDEX IF NOT EXISTS wiki_chunks_embedding_ivf
    ON brain.wiki_chunks USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;

-- wiki 项目来源终态（00034 基础 + 00057 合并 brain.sources + 00061 硬化）。
-- 一行 = 一个上传到项目的文档 或 一条 webclip/voice 抓取。
-- kind: 'upload'（文件，file_id 指向 files.objects）| 'webclip' | ...
-- 去重走两个 partial unique（替代 00034 原全表 UNIQUE(project_id, rel_path)）：
--   upload 按 (project_id, rel_path)；webclip 按 (project_id, content_hash)。
CREATE TABLE IF NOT EXISTS brain.wiki_sources (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    file_id         uuid,                            -- files.objects.id；nullable 兼容外部 URL / webclip 来源
    rel_path        text NOT NULL,                   -- project-relative，e.g. "papers/attention.pdf"
    filename        text NOT NULL,                   -- 文件名（rel_path 末段；冗余便于查询）
    mime            text,
    byte_size       bigint NOT NULL DEFAULT 0,
    content_hash    bytea,                           -- sha256(content)，跨项目 dedup 用
    extracted_text  text,                            -- parser 抽取的纯文本；webclip 的 raw 也落这里
    parse_status    text NOT NULL DEFAULT 'queued',  -- queued / processing / done / error
    parse_error     text,
    external_id     text,                            -- 来源外键，e.g. notion page id / github URL
    -- 00057 合并 webclip 维度
    kind            text NOT NULL DEFAULT 'upload',
    url             text,
    user_id         uuid,
    title           text,
    page_id         uuid,                            -- 抓取落地页（有意不加 FK，同历史）
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- 00061：parse 失败重试计数（worker 用：retries < 3 才重入队列）
    retries         int  NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT wiki_sources_parse_status_check
        CHECK (parse_status IN ('queued', 'processing', 'done', 'error')),
    CONSTRAINT wiki_sources_file_id_fkey
        FOREIGN KEY (file_id) REFERENCES files.objects(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS wiki_sources_project_idx ON brain.wiki_sources(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS wiki_sources_hash_idx ON brain.wiki_sources(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS wiki_sources_external_id_idx ON brain.wiki_sources(project_id, external_id)
    WHERE external_id IS NOT NULL;
-- 00057：partial unique 替代旧全表 UNIQUE
CREATE UNIQUE INDEX IF NOT EXISTS wiki_sources_upload_path_uniq
    ON brain.wiki_sources(project_id, rel_path) WHERE kind = 'upload';
CREATE UNIQUE INDEX IF NOT EXISTS wiki_sources_webclip_hash_uniq
    ON brain.wiki_sources(project_id, content_hash)
    WHERE kind = 'webclip' AND content_hash IS NOT NULL;
-- 00057：按 kind 分页列表
CREATE INDEX IF NOT EXISTS wiki_sources_kind_idx
    ON brain.wiki_sources(project_id, kind, created_at DESC);
-- 00061：parse 队列索引
CREATE INDEX IF NOT EXISTS wiki_sources_parse_queue_idx
    ON brain.wiki_sources (parse_status, retries, created_at)
    WHERE kind = 'upload' AND file_id IS NOT NULL;

-- P1-4：page ↔ source 多对多归属表（relevance 第 4 信号 source overlap）
CREATE TABLE IF NOT EXISTS brain.page_sources (
    page_id    uuid        NOT NULL REFERENCES brain.pages(id)        ON DELETE CASCADE,
    source_id  uuid        NOT NULL REFERENCES brain.wiki_sources(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, source_id)
);
-- source→pages 反查（relevance loadGraph + delete-preview 用）
CREATE INDEX IF NOT EXISTS page_sources_source_idx ON brain.page_sources(source_id);

-- ingest tasks：跟踪「把 raw source 送给 LLM 转写成 wiki page」的异步任务。
-- source_id FK 直接指向 wiki_sources（00057 repoint 终态）。
CREATE TABLE IF NOT EXISTS brain.ingest_tasks (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id           uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id             uuid NOT NULL,
    source_id            uuid REFERENCES brain.wiki_sources(id) ON DELETE SET NULL,
    -- 直接 ingest 的入参（用户粘贴文本时用）。source_id NULL 时必填。
    raw_text             text NOT NULL DEFAULT '',
    -- 任务标题，便于 UI 列表展示，可空时 worker 会基于 source.title 兜底
    title                text NOT NULL DEFAULT '',
    status               text NOT NULL DEFAULT 'pending'
                              CHECK (status IN
                                ('pending','running','partial','done','failed','cancelled')),
    error                text NOT NULL DEFAULT '',
    progress             jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- 累积落地的 page ID 列表，同步推进
    result_pages         uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    cancel_requested_at  timestamptz,
    started_at           timestamptz,
    finished_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- 列表/详情常用查询
CREATE INDEX IF NOT EXISTS ingest_tasks_project_status_idx
    ON brain.ingest_tasks(project_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS ingest_tasks_owner_idx
    ON brain.ingest_tasks(owner_id, created_at DESC);

-- worker 启动时找 stuck running 任务做超时回收用
CREATE INDEX IF NOT EXISTS ingest_tasks_running_idx
    ON brain.ingest_tasks(status, started_at)
    WHERE status IN ('running','partial');
-- +goose StatementEnd

-- ── 6. brain schema：审阅 / 相关度 / 反馈 / captions / research ──
-- +goose StatementBegin
-- 通用「写入侧 AI 闭环」审阅队列。kind CHECK 用 00067 终态（6 kind，
-- 含 contradiction）；00067/00068 的历史数据 UPDATE 不进基线。
CREATE TABLE IF NOT EXISTS brain.review_items (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id      uuid NOT NULL,
    kind          text NOT NULL
                       CHECK (kind IN ('dedup','lint','sweep','merge','suggestion','contradiction')),
    status        text NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open','resolved','dismissed')),
    title         text NOT NULL,
    description   text NOT NULL DEFAULT '',
    -- 关联的 page id，dedup/merge 通常 2 个；lint/sweep 1 个；suggestion 0..N。
    page_ids      uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    -- 类型相关数据（相似度、lint 规则名、sweep 阈值天数 …）
    payload       jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- 幂等键，每 kind 自定义。UNIQUE 让 ON CONFLICT 直接生效。
    dedupe_key    text NOT NULL UNIQUE,
    resolved_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS review_items_project_status_idx
    ON brain.review_items(project_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS review_items_owner_idx
    ON brain.review_items(owner_id, status);

-- 用于 UI「最近 N 条 open 审阅」查询
CREATE INDEX IF NOT EXISTS review_items_open_idx
    ON brain.review_items(created_at DESC)
    WHERE status = 'open';

-- Page-to-page relevance：预计算页对相关度（wikilink 图 + 类型亲和）。
-- 存储 (page_a, page_b) 且 page_a < page_b，每对只出现一次。
CREATE TABLE IF NOT EXISTS brain.page_relevance (
    project_id  uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    page_a      uuid NOT NULL REFERENCES brain.pages(id)    ON DELETE CASCADE,
    page_b      uuid NOT NULL REFERENCES brain.pages(id)    ON DELETE CASCADE,
    score       real NOT NULL,
    -- Per-signal contribution for debug + future re-ranking.
    signals     jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, page_a, page_b),
    CHECK (page_a::text < page_b::text)
);

CREATE INDEX IF NOT EXISTS page_relevance_a_score_idx
    ON brain.page_relevance(page_a, score DESC);
CREATE INDEX IF NOT EXISTS page_relevance_b_score_idx
    ON brain.page_relevance(page_b, score DESC);
CREATE INDEX IF NOT EXISTS page_relevance_project_idx
    ON brain.page_relevance(project_id);

-- Search feedback：per-result thumbs up/down。
CREATE TABLE IF NOT EXISTS brain.search_feedback (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL,
    project_id   uuid REFERENCES brain.projects(id) ON DELETE CASCADE,
    query_lower  text NOT NULL,
    page_id      uuid NOT NULL,
    rank         int  NOT NULL DEFAULT 0,
    signal       text NOT NULL CHECK (signal IN ('up', 'down')),
    meta         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- One verdict per (user, query, page)。
    UNIQUE (user_id, query_lower, page_id)
);

CREATE INDEX IF NOT EXISTS search_feedback_user_idx
    ON brain.search_feedback(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS search_feedback_query_idx
    ON brain.search_feedback(query_lower, signal);
CREATE INDEX IF NOT EXISTS search_feedback_page_idx
    ON brain.search_feedback(page_id, signal);

-- Vision-caption 缓存：content-addressed，同 URL 跨页只跑一次 vision 调用。
CREATE TABLE IF NOT EXISTS brain.image_captions (
    url_hash    bytea PRIMARY KEY,
    url         text NOT NULL,
    caption     text NOT NULL,
    model       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Deep Research tasks（topic in → wiki page out）。
-- started_at/finished_at 来自 00056（crash recover 用）。
CREATE TABLE IF NOT EXISTS brain.research_tasks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id        uuid NOT NULL,
    topic           text NOT NULL,
    queries         text[] NOT NULL DEFAULT ARRAY[]::text[],
    status          text NOT NULL DEFAULT 'queued',
    page_id         uuid REFERENCES brain.pages(id) ON DELETE SET NULL,
    web_results     jsonb NOT NULL DEFAULT '[]'::jsonb,
    synthesis       text NOT NULL DEFAULT '',
    error_message   text,
    started_at      timestamptz,
    finished_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS research_tasks_project_idx
    ON brain.research_tasks (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS research_tasks_owner_idx
    ON brain.research_tasks (owner_id, created_at DESC);
-- boot-time「找 stuck in-flight 任务」扫描；只索引四个 active 状态
CREATE INDEX IF NOT EXISTS research_tasks_active_idx
    ON brain.research_tasks (updated_at)
    WHERE status IN ('queued', 'searching', 'synthesizing', 'saving');
-- +goose StatementEnd

-- ── 7. brain schema：wiki 杂项（conversations / suggestions / sync / revisions）──
-- +goose StatementBegin
-- 项目内对话（与顶层 chat 完全独立）
CREATE TABLE IF NOT EXISTS brain.wiki_conversations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES brain.projects(id) ON DELETE CASCADE,
    owner_id        uuid NOT NULL,
    title           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE INDEX IF NOT EXISTS wiki_conversations_project_idx
    ON brain.wiki_conversations(project_id, updated_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS wiki_conversations_owner_idx
    ON brain.wiki_conversations(owner_id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS brain.wiki_messages (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL REFERENCES brain.wiki_conversations(id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content         text NOT NULL DEFAULT '',
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS wiki_messages_conv_idx
    ON brain.wiki_messages(conversation_id, created_at);

-- 用户反馈 / 路线图（平台级，无项目维度）
CREATE TABLE IF NOT EXISTS brain.wiki_suggestions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id    uuid NOT NULL,
    title        text NOT NULL,
    body         text NOT NULL DEFAULT '',
    -- feature | bug | idea | other
    category     text NOT NULL DEFAULT 'feature',
    -- open | planned | shipped | rejected
    status       text NOT NULL DEFAULT 'open',
    deleted_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS wiki_suggestions_status_idx
    ON brain.wiki_suggestions(status, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS wiki_suggestions_author_idx
    ON brain.wiki_suggestions(author_id)
    WHERE deleted_at IS NULL;

-- 一人一票（多次 toggle 走 INSERT/DELETE 而非 count）
CREATE TABLE IF NOT EXISTS brain.wiki_suggestion_votes (
    suggestion_id  uuid NOT NULL REFERENCES brain.wiki_suggestions(id) ON DELETE CASCADE,
    voter_id       uuid NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (suggestion_id, voter_id)
);
CREATE INDEX IF NOT EXISTS wiki_suggestion_votes_voter_idx
    ON brain.wiki_suggestion_votes(voter_id);

-- per-(user, project) last-seen change_id，client sync catch-up 用
CREATE TABLE IF NOT EXISTS brain.wiki_sync_checkpoints (
    user_id    uuid        NOT NULL,
    project_id uuid        NOT NULL,
    change_id  text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, project_id)
);

-- Wiki 页版本历史（edit 窗口合并 + restore 永久保留；actor_id 记录操作者）。
-- body_md 来自 00066（snapshot 存写前 body_md 原文，restore 无损恢复）。
CREATE TABLE IF NOT EXISTS brain.page_revisions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id        uuid NOT NULL REFERENCES brain.pages(id) ON DELETE CASCADE,
    project_id     uuid NOT NULL,
    actor_id       text  NOT NULL DEFAULT '',
    title          text  NOT NULL DEFAULT '',
    frontmatter    jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- 写前全部 live blocks 序列化（[]*Block），restore 时反序列化对账回写。
    blocks_json    jsonb NOT NULL DEFAULT '[]'::jsonb,
    body_md        text  NOT NULL DEFAULT '',
    change_type    text  NOT NULL CHECK (change_type IN ('edit', 'restore')),
    -- restore 自动备份固定写「恢复前自动备份」；edit 快照为 NULL。
    change_summary text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS page_revisions_page_created_idx
    ON brain.page_revisions (page_id, created_at DESC);
CREATE INDEX IF NOT EXISTS page_revisions_project_idx
    ON brain.page_revisions (project_id);
-- +goose StatementEnd

-- ── 8. chat schema：threads / message_groups / messages ─────────
-- +goose StatementBegin
-- 终态 = 00006 + workdir/auto_approve（00038）+ provider_id（00039）；
-- 不含 execution_mode（00030 加、00040 删，语义由 agent_sessions.mode 表达）。
CREATE TABLE IF NOT EXISTS chat.threads (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    -- Optional brain project association (memory / wiki share project ids).
    -- ON DELETE SET NULL because deleting a project should NOT torch
    -- the user's chat history — that's a different mental model.
    project_id      uuid REFERENCES brain.projects(id) ON DELETE SET NULL,

    -- Display
    title             text NOT NULL DEFAULT '',
    last_msg_preview  text NOT NULL DEFAULT '',  -- avoids JOIN on list

    -- Per-thread overrides of user-level defaults
    model           text,
    system_prompt   text,

    -- Pinned + archived (replaces tags / folders for v0.1)
    pinned          boolean NOT NULL DEFAULT false,
    archived        boolean NOT NULL DEFAULT false,

    -- Long-context summarization cache
    summary                text,
    summary_until_position bigint,

    -- Multi-agent extension fields (see §14.5 design doc).
    agent_id         uuid,                         -- L1: bound agent
    agent_chain      jsonb,                        -- L3: orchestration policy
    parent_thread_id uuid,                         -- branch (no FK to avoid
                                                   --   cycles; app-level integ.)

    -- Direct mode privacy toggle (§3.5.3): off → only metadata stored,
    -- messages never sync to server.
    sync_enabled    boolean NOT NULL DEFAULT true,

    -- 00038：agent / task 模式 daemon spawn 的 working directory
    -- （PermissionUpdate.AddDirectories 协议初始值）；chat 模式 NULL。
    workdir         text,
    -- 00038：Agent 工具调用自治程度三档（默认 manual，安全优先）
    auto_approve    text NOT NULL DEFAULT 'manual',
    -- 00039：软关联 chat.providers.provider_id slug；**故意不加 FK** ——
    -- provider 删除后老 thread 的 provider_id 仍保留，router 降级 fallback。
    provider_id     text,

    metadata        jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT threads_auto_approve_chk
        CHECK (auto_approve IN ('auto', 'whitelist', 'manual'))
);

-- Primary list query: user's non-archived threads, recent first
CREATE INDEX IF NOT EXISTS threads_user_updated
    ON chat.threads (user_id, archived, updated_at DESC);

-- Pinned subset surfaces above the rest in the sidebar
CREATE INDEX IF NOT EXISTS threads_user_pinned
    ON chat.threads (user_id, archived, updated_at DESC)
    WHERE pinned;

-- Drilling in from a project page (filter by project)
CREATE INDEX IF NOT EXISTS threads_project_updated
    ON chat.threads (project_id, updated_at DESC)
    WHERE project_id IS NOT NULL;

-- ─── message_groups (multi-model parallel placeholder) ────────

CREATE TABLE IF NOT EXISTS chat.message_groups (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id         uuid NOT NULL REFERENCES chat.threads(id) ON DELETE CASCADE,
    user_id           uuid NOT NULL,
    parent_message_id uuid,        -- the user message that triggered the fanout
    type              text NOT NULL DEFAULT 'parallel'
                            CHECK (type IN ('parallel','compression')),
    metadata          jsonb NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS message_groups_thread
    ON chat.message_groups (thread_id, created_at DESC);

-- ─── messages ─────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS chat.messages (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id   uuid NOT NULL REFERENCES chat.threads(id) ON DELETE CASCADE,
    -- Redundant user_id avoids JOIN for owner-scoping
    user_id     uuid NOT NULL,

    role        text NOT NULL
                CHECK (role IN ('user','assistant','tool','system')),

    -- Plain text body. For richer content (images / tool calls /
    -- thinking) see `parts`.
    content     text NOT NULL DEFAULT '',
    parts       jsonb NOT NULL DEFAULT '[]',

    -- tool_use ↔ tool_result pairing (Anthropic toolu_XX).
    tool_call_id text,

    -- Branching: edit a message → archive subsequent + fork from this
    parent_id   uuid REFERENCES chat.messages(id) ON DELETE SET NULL,

    -- Model + token accounting
    model       text,
    prompt_tokens integer,
    completion_tokens integer,

    -- 6-state machine aligned with cherry-studio:
    --   pending / processing / streaming / success / error / paused
    status      text NOT NULL DEFAULT 'success'
                CHECK (status IN ('pending','processing','streaming',
                                  'success','error','paused')),
    error       text,

    -- Client-supplied UUID for dedup on retry / offline replay.
    client_id   text,

    -- Multi-agent extensions:
    agent_id         uuid,    -- L3: which agent produced (assistant only)
    message_group_id uuid REFERENCES chat.message_groups(id) ON DELETE SET NULL,

    -- Thread-scoped monotonic counter（单 sequence 跨 thread 共享）
    position    bigserial,

    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    -- 00032：消息搜索 tsvector（trigger 维护；仅 terminal 状态非 NULL）
    search_vector tsvector
);

-- Client-side dedup key
CREATE UNIQUE INDEX IF NOT EXISTS messages_thread_client_id
    ON chat.messages (thread_id, client_id)
    WHERE client_id IS NOT NULL;

-- Primary read path: list a thread in order
CREATE INDEX IF NOT EXISTS messages_thread_position
    ON chat.messages (thread_id, position);

-- Cleanup job: find orphan streaming messages
CREATE INDEX IF NOT EXISTS messages_user_streaming
    ON chat.messages (user_id, status, updated_at)
    WHERE status = 'streaming';

-- Grouping (multi-model parallel)
CREATE INDEX IF NOT EXISTS messages_group
    ON chat.messages (message_group_id)
    WHERE message_group_id IS NOT NULL;

-- 00032：GIN 部分索引 — 仅索引 terminal 状态消息
CREATE INDEX IF NOT EXISTS chat_messages_search_idx
    ON chat.messages USING GIN (search_vector)
    WHERE status IN ('success', 'error');

-- 00032：用户级时间索引 — 缩小 GIN bitmap-and 候选集
CREATE INDEX IF NOT EXISTS chat_messages_search_user_created
    ON chat.messages (user_id, created_at DESC)
    WHERE status IN ('success', 'error');

-- ─── trigger: bump thread.updated_at + last_msg_preview ───────
-- 00007 的 v2 版（含空标题自动命名：首条 user 消息 LEFT 60 字符）。

CREATE OR REPLACE FUNCTION chat.touch_thread() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE chat.threads
       SET updated_at = NEW.updated_at,
           last_msg_preview = CASE
               WHEN NEW.status IN ('success','error','paused')
                 OR NEW.role = 'user'
               THEN LEFT(NEW.content, 200)
               ELSE last_msg_preview
           END,
           title = CASE
               WHEN title = ''
                AND NEW.role = 'user'
                AND length(trim(NEW.content)) > 0
               THEN LEFT(trim(NEW.content), 60)
               ELSE title
           END
     WHERE id = NEW.thread_id;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS messages_touch_thread ON chat.messages;
CREATE TRIGGER messages_touch_thread
    AFTER INSERT OR UPDATE ON chat.messages
    FOR EACH ROW EXECUTE FUNCTION chat.touch_thread();

-- ─── 00032：消息搜索 trigger ──────────────────────────────────
-- 仅 terminal 状态 (success/error) 才计算 search_vector，
-- streaming/pending/processing/paused 期间保持 NULL。
-- content 给 A 权重，parts 里 type=text/thinking 块的 text 给 B 权重。
CREATE OR REPLACE FUNCTION chat.update_message_search_vector()
RETURNS trigger LANGUAGE plpgsql AS $fn$
DECLARE
    parts_text text;
BEGIN
    IF NEW.status NOT IN ('success', 'error') THEN
        NEW.search_vector := NULL;
        RETURN NEW;
    END IF;

    SELECT string_agg(elem->>'text', ' ')
      INTO parts_text
      FROM jsonb_array_elements(COALESCE(NEW.parts, '[]'::jsonb)) AS elem
     WHERE elem ? 'type'
       AND elem->>'type' IN ('text', 'thinking')
       AND elem ? 'text';

    NEW.search_vector :=
        setweight(to_tsvector('biumind_zhcn', COALESCE(NEW.content, '')), 'A')
      || setweight(to_tsvector('biumind_zhcn', COALESCE(parts_text, '')), 'B');
    RETURN NEW;
END;
$fn$;

DROP TRIGGER IF EXISTS chat_messages_search_vector_trigger ON chat.messages;
CREATE TRIGGER chat_messages_search_vector_trigger
    BEFORE INSERT OR UPDATE OF content, parts, status
    ON chat.messages
    FOR EACH ROW
    EXECUTE FUNCTION chat.update_message_search_vector();
-- +goose StatementEnd

-- ── 9. chat schema：providers / models ──────────────────────────
-- +goose StatementBegin
-- chat.providers 终态：per-user LLM provider 配置。
-- BYOK P3 后 brain 彻底去 key —— 不含 key_vaults_encrypted / fetch_mode /
-- internal 三列（key 数据在 identity.user_api_keys）。
-- source CHECK 用 00009 三值版：official（平台池）/ builtin / custom。
CREATE TABLE IF NOT EXISTS chat.providers (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- user_id is the JWT subject; we don't FK to identity.users because
    -- identity lives in a separate schema/service. Owner-scoped by app code.
    user_id               uuid NOT NULL,
    provider_id           text NOT NULL,            -- 'anthropic' | 'openai' | slug
    display_name          text NOT NULL DEFAULT '',
    base_url              text,                     -- override / required for custom
    enabled               boolean NOT NULL DEFAULT true,
    source                text NOT NULL DEFAULT 'builtin'
                              CHECK (source IN ('official','builtin','custom')),
    -- Misc per-provider knobs (e.g. response_api flag, custom headers).
    config_json           jsonb NOT NULL DEFAULT '{}'::jsonb,
    sort_order            integer NOT NULL DEFAULT 0,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- A given user has at most one row per provider id.
CREATE UNIQUE INDEX IF NOT EXISTS providers_user_provider_uniq
    ON chat.providers (user_id, provider_id);

CREATE INDEX IF NOT EXISTS providers_user_enabled
    ON chat.providers (user_id, enabled);

-- chat.models：per-user 模型元数据（启用 / 排序 / 能力 / 定价）。
CREATE TABLE IF NOT EXISTS chat.models (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL,
    provider_id     text NOT NULL,           -- matches chat.providers.provider_id
    model_id        text NOT NULL,           -- wire id ('claude-opus-4-7')
    display_name    text NOT NULL DEFAULT '',
    type            text NOT NULL DEFAULT 'chat'
                        CHECK (type IN ('chat','image','video','embedding','stt','tts')),
    -- abilities: {"vision": true, "audio": false, "functions": true, "reasoning": false}
    abilities       jsonb NOT NULL DEFAULT '{}'::jsonb,
    context_window  integer,
    -- pricing: {"input_per_m_usd": 5, "output_per_m_usd": 25, ...}.
    pricing_json    jsonb,
    released_at     date,
    enabled         boolean NOT NULL DEFAULT true,
    sort_order      integer NOT NULL DEFAULT 0,
    -- 'builtin' / 'remote' / 'custom'（global 缓存行已由 model-relay 接管，00049）
    source          text NOT NULL DEFAULT 'builtin'
                        CHECK (source IN ('builtin','remote','custom')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS models_user_provider_model
    ON chat.models (user_id, provider_id, model_id);

CREATE INDEX IF NOT EXISTS models_user_enabled
    ON chat.models (user_id, enabled);
-- +goose StatementEnd

-- ── 10. public schema：Agent Plane（agent_* 六表）────────────────
-- 按历史（00033）有意建在 public schema：Agent Plane 跨 chat/wiki/memory
-- 多领域；保持原样，不加 schema 前缀、不移动。
-- +goose StatementBegin
-- agent_environments：worker 注册表（biu daemon / biu CLI / runtime pod）。
-- 00043 加 device_id；00045 加 device_id partial unique（一台 device 至多一行）。
CREATE TABLE IF NOT EXISTS agent_environments (
    environment_id   UUID PRIMARY KEY,
    user_id          UUID,                                  -- NULL = 系统级（runtime 共享池），否则归属用户
    worker_kind      TEXT NOT NULL,                         -- 'biu_daemon' | 'biu_cli' | 'runtime'
    machine_name     TEXT NOT NULL,                         -- biu_daemon: hostname; runtime: pod name
    os_arch          TEXT,                                  -- "darwin/arm64" / "linux/amd64"
    git_info         JSONB,                                 -- {repo, branch, dir} for biu_daemon
    capabilities     TEXT[],                                -- ["sandbox", "mcp:supabase", "skills:5"]
    public_key       BYTEA,                                 -- X25519 public key
    pool_tag         TEXT,                                  -- runtime 副本池负载均衡标签
    state            TEXT NOT NULL DEFAULT 'online',        -- 'online' | 'offline' | 'draining'
    device_id        UUID,                                  -- 00043：签发它的 device（nullable）
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT agent_env_kind_chk CHECK (worker_kind IN ('biu_daemon', 'biu_cli', 'runtime')),
    CONSTRAINT agent_env_state_chk CHECK (state IN ('online', 'offline', 'draining'))
);

-- 列表查询：按用户找自己的 environments
CREATE INDEX IF NOT EXISTS agent_environments_user_state_idx
    ON agent_environments (user_id, state)
    WHERE state IN ('online', 'draining');

-- 池子查询：按 worker_kind 找空闲 runtime 副本
CREATE INDEX IF NOT EXISTS agent_environments_kind_state_idx
    ON agent_environments (worker_kind, state, last_seen_at DESC)
    WHERE state = 'online';

-- Janitor 扫描：找 last_seen_at 过老的 online 行
CREATE INDEX IF NOT EXISTS agent_environments_last_seen_idx
    ON agent_environments (last_seen_at)
    WHERE state = 'online';

-- 00043：session 创建按 environment→device 查 policy 的辅助索引
CREATE INDEX IF NOT EXISTS agent_environments_device_idx
    ON agent_environments (device_id) WHERE device_id IS NOT NULL;

-- 00045：env_id 按 device 稳定化 —— 一台 device 至多一行 environment
CREATE UNIQUE INDEX IF NOT EXISTS agent_environments_device_uniq
    ON agent_environments (device_id) WHERE device_id IS NOT NULL;

-- agent_sessions：跨 worker 对话 session 元数据（不存 frame，走 NATS）。
-- 00040 加 runtime_env_mode；00041 加 backend；state CHECK 用 00044 五态终态。
CREATE TABLE IF NOT EXISTS agent_sessions (
    session_id      UUID PRIMARY KEY,
    user_id         UUID NOT NULL,
    environment_id  UUID,                                   -- NULL = chat mode (无 worker)
    thread_id       UUID,                                   -- 关联 chat.threads（可空，Task 模式不一定有 thread）
    mode            TEXT NOT NULL,                          -- 'chat' | 'agent' | 'task'
    state           TEXT NOT NULL DEFAULT 'active',
    model           TEXT,                                   -- 创建时的 model id
    system_prompt   TEXT,                                   -- 创建时的 system prompt（不变；可空）
    -- 00040：工具在哪执行（none/local/cloud），与 mode 正交
    runtime_env_mode TEXT NOT NULL DEFAULT 'none'
        CHECK (runtime_env_mode IN ('none', 'local', 'cloud')),
    -- 00041：agent loop 用哪个 backend
    backend         TEXT NOT NULL DEFAULT 'biumindkit'
        CHECK (backend IN ('biumindkit', 'claude-cli', 'codex-cli')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT agent_sessions_mode_chk CHECK (mode IN ('chat', 'agent', 'task')),
    CONSTRAINT agent_sessions_state_chk CHECK (state IN ('active', 'paused', 'completed', 'failed', 'pending')),
    CONSTRAINT agent_sessions_env_fk FOREIGN KEY (environment_id) REFERENCES agent_environments(environment_id) ON DELETE SET NULL
);

-- 用户 list session：按 mode 筛 + 时间倒序
CREATE INDEX IF NOT EXISTS agent_sessions_user_mode_created_idx
    ON agent_sessions (user_id, mode, created_at DESC);

-- environment 反查 session
CREATE INDEX IF NOT EXISTS agent_sessions_env_state_idx
    ON agent_sessions (environment_id, state)
    WHERE state IN ('active', 'paused');

-- agent_session_results：Task 模式最终态摘要（仅 Task finalize 写一行）。
CREATE TABLE IF NOT EXISTS agent_session_results (
    session_id          UUID PRIMARY KEY REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    status              TEXT NOT NULL,                      -- 'completed' | 'failed' | 'cancelled'
    final_text          TEXT,                               -- assistant 最后一条文本
    final_parts         JSONB,                              -- assistant 最后一条 parts
    tool_calls_summary  JSONB,                              -- [{name, count, total_ms}]
    cost_usd            NUMERIC(12, 6),
    prompt_tokens       INTEGER,
    completion_tokens   INTEGER,
    duration_ms         BIGINT,
    error_message       TEXT,                               -- failed/cancelled 才填
    finished_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT agent_session_results_status_chk CHECK (status IN ('completed', 'failed', 'cancelled'))
);

-- 用户列任务结果（最近完成的）
CREATE INDEX IF NOT EXISTS agent_session_results_finished_idx
    ON agent_session_results (finished_at DESC);

-- agent_pairings：pending 设备配对（短命，TTL 5min，janitor sweep 清理）。
CREATE TABLE IF NOT EXISTS agent_pairings (
    pairing_id          UUID PRIMARY KEY,
    code                TEXT NOT NULL,              -- 8 位数字配对码
    pairing_secret_hash BYTEA NOT NULL,             -- SHA256(pairing_secret)
    machine_name        TEXT NOT NULL,
    os_arch             TEXT,
    worker_kind         TEXT,
    user_id             UUID,                       -- approve 时填
    status              TEXT NOT NULL DEFAULT 'pending',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    approved_at         TIMESTAMPTZ,

    CONSTRAINT agent_pairings_status_chk CHECK (status IN ('pending', 'approved', 'consumed'))
);

-- 按 code 查 pending 配对（approve 用）
CREATE INDEX IF NOT EXISTS agent_pairings_code_idx
    ON agent_pairings (code) WHERE status = 'pending';
-- janitor 按 expires_at 清
CREATE INDEX IF NOT EXISTS agent_pairings_expiry_idx ON agent_pairings (expires_at);

-- agent_devices：已签发的 device token（opaque，只存 hash，可吊销）。
-- 00043 加 tool_policy（readonly / workspace-write / full）。
CREATE TABLE IF NOT EXISTS agent_devices (
    device_id    UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    name         TEXT NOT NULL,                     -- 设备名（= machine_name）
    token_hash   BYTEA NOT NULL UNIQUE,             -- SHA256(full token)
    prefix       TEXT NOT NULL,                     -- token 前缀（展示/排查用，非密）
    tool_policy  TEXT NOT NULL DEFAULT 'workspace-write',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ,

    CONSTRAINT agent_devices_tool_policy_chk
        CHECK (tool_policy IN ('readonly', 'workspace-write', 'full'))
);

CREATE INDEX IF NOT EXISTS agent_devices_user_idx ON agent_devices (user_id);

-- agent_pending_work：离线设备的挂起 agent work（按稳定 device_id 持久化）。
CREATE TABLE IF NOT EXISTS agent_pending_work (
    pending_id       UUID PRIMARY KEY,
    session_id       UUID NOT NULL UNIQUE REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    user_id          UUID NOT NULL,
    device_id        UUID NOT NULL,         -- 重派锚点：设备重连按此匹配
    prompt           TEXT,
    model            TEXT,
    provider_id      TEXT,
    system_prompt    TEXT,
    thread_id        UUID,
    workdir          TEXT,
    runtime_env_mode TEXT,
    backend          TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL
);

-- 设备重连 sweep：按 device_id 取该设备的挂起 work
CREATE INDEX IF NOT EXISTS agent_pending_work_device_idx ON agent_pending_work (device_id);
-- janitor 过期清理
CREATE INDEX IF NOT EXISTS agent_pending_work_expiry_idx ON agent_pending_work (expires_at);
-- +goose StatementEnd

-- ── 11. brain schema：notes 模块（note_* 六表）──────────────────
-- +goose StatementBegin
-- 笔记本（单层，不做树；组织靠标签）
CREATE TABLE IF NOT EXISTS brain.note_notebooks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL,
    name        text NOT NULL,
    position    double precision NOT NULL DEFAULT 0,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 同一用户下活笔记本名（大小写不敏感）唯一
CREATE UNIQUE INDEX IF NOT EXISTS note_notebooks_user_name_alive
    ON brain.note_notebooks (user_id, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS note_notebooks_user_pos_idx
    ON brain.note_notebooks (user_id, position)
    WHERE deleted_at IS NULL;

-- 笔记主表（整篇 markdown 权威；is_todo + 完成时间戳统一 notes/todos；
-- version 乐观锁；软删回收站；浮点 position 插值排序）。
-- 00060 加 source_url / author / archived_at / promoted_page_id 四列。
CREATE TABLE IF NOT EXISTS brain.note_notes (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           uuid NOT NULL,
    -- NULL = 根（不属于任何笔记本）
    notebook_id       uuid REFERENCES brain.note_notebooks(id) ON DELETE SET NULL,
    title             text NOT NULL DEFAULT '',
    content_md        text NOT NULL DEFAULT '',
    is_todo           boolean NOT NULL DEFAULT false,
    todo_completed_at timestamptz,
    position          double precision NOT NULL DEFAULT 0,
    version           int NOT NULL DEFAULT 1,
    deleted_at        timestamptz,
    -- 00060：webclip 来源 / 归档 / 转入知识库回链
    source_url        text,
    author            text,
    archived_at       timestamptz,
    promoted_page_id  uuid,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    -- 中文全文索引：title 权重 A、content_md 权重 B
    tsv               tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('biumind_zhcn', coalesce(title, '')), 'A')
        || setweight(to_tsvector('biumind_zhcn', coalesce(content_md, '')), 'B')
    ) STORED
);

CREATE INDEX IF NOT EXISTS note_notes_user_nb_pos_idx
    ON brain.note_notes (user_id, notebook_id, position)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS note_notes_tsv_gin ON brain.note_notes USING GIN (tsv) WHERE deleted_at IS NULL;

-- 00060：归档列表（archived=only）查询路径
CREATE INDEX IF NOT EXISTS note_notes_user_archived_idx
    ON brain.note_notes (user_id, archived_at DESC)
    WHERE archived_at IS NOT NULL AND deleted_at IS NULL;

-- 标签与笔记-标签关联。scope_key 预留多空间语义（个人空间 'personal:<uid>'）。
CREATE TABLE IF NOT EXISTS brain.note_tags (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL,
    scope_key  text NOT NULL,               -- "personal:<uid>"
    name       text NOT NULL,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 同一 scope 下活标签名（大小写不敏感）唯一
CREATE UNIQUE INDEX IF NOT EXISTS note_tags_scope_name_alive
    ON brain.note_tags (scope_key, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS note_tags_user_idx
    ON brain.note_tags (user_id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS brain.note_note_tags (
    note_id uuid NOT NULL REFERENCES brain.note_notes(id) ON DELETE CASCADE,
    tag_id  uuid NOT NULL REFERENCES brain.note_tags(id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, tag_id)
);

CREATE INDEX IF NOT EXISTS note_note_tags_tag_idx ON brain.note_note_tags (tag_id);

-- 笔记附件关联表，复用 files.objects + MinIO 通道；正文引用 URI 为 biu-file://<uuid>
CREATE TABLE IF NOT EXISTS brain.note_attachments (
    note_id       uuid NOT NULL REFERENCES brain.note_notes(id) ON DELETE CASCADE,
    file_id       uuid NOT NULL REFERENCES files.objects(id) ON DELETE CASCADE,
    is_associated boolean NOT NULL DEFAULT false,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, file_id)
);

CREATE INDEX IF NOT EXISTS note_attachments_file_idx ON brain.note_attachments (file_id);

-- 版本历史：保存前快照 + 恢复自动备份。
-- edit 版本有窗口合并与定期清理；restore 版本永久保留。
CREATE TABLE IF NOT EXISTS brain.note_revisions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id        uuid NOT NULL REFERENCES brain.note_notes(id) ON DELETE CASCADE,
    user_id        uuid NOT NULL,
    title          text NOT NULL DEFAULT '',
    content_md     text NOT NULL DEFAULT '',
    change_type    text NOT NULL CHECK (change_type IN ('edit', 'restore')),
    -- restore 自动备份固定写「恢复前自动备份」；edit 快照为 NULL。
    change_summary text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS note_revisions_note_created_idx
    ON brain.note_revisions (note_id, created_at DESC);

CREATE INDEX IF NOT EXISTS note_revisions_user_idx
    ON brain.note_revisions (user_id);
-- +goose StatementEnd

-- +goose Down

-- ── notes ──────────────────────────────────────────────────────
-- +goose StatementBegin
DROP TABLE IF EXISTS brain.note_revisions;
DROP TABLE IF EXISTS brain.note_attachments;
DROP TABLE IF EXISTS brain.note_note_tags;
DROP TABLE IF EXISTS brain.note_tags;
DROP TABLE IF EXISTS brain.note_notes;
DROP TABLE IF EXISTS brain.note_notebooks;
-- +goose StatementEnd

-- ── public agent_*（逐个 DROP；不 DROP public schema）──────────
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_pending_work;
DROP TABLE IF EXISTS agent_devices;
DROP TABLE IF EXISTS agent_pairings;
DROP TABLE IF EXISTS agent_session_results;
DROP TABLE IF EXISTS agent_sessions;
DROP TABLE IF EXISTS agent_environments;
-- +goose StatementEnd

-- ── chat ───────────────────────────────────────────────────────
-- +goose StatementBegin
DROP TRIGGER IF EXISTS chat_messages_search_vector_trigger ON chat.messages;
DROP TRIGGER IF EXISTS messages_touch_thread ON chat.messages;
DROP FUNCTION IF EXISTS chat.update_message_search_vector();
DROP FUNCTION IF EXISTS chat.touch_thread();
DROP TABLE IF EXISTS chat.models;
DROP TABLE IF EXISTS chat.providers;
DROP TABLE IF EXISTS chat.messages;
DROP TABLE IF EXISTS chat.message_groups;
DROP TABLE IF EXISTS chat.threads;
-- +goose StatementEnd

-- ── brain ──────────────────────────────────────────────────────
-- +goose StatementBegin
DROP TRIGGER IF EXISTS events_notify ON brain.events;
DROP FUNCTION IF EXISTS brain.notify_event();
DROP TABLE IF EXISTS brain.page_revisions;
DROP TABLE IF EXISTS brain.wiki_sync_checkpoints;
DROP TABLE IF EXISTS brain.wiki_suggestion_votes;
DROP TABLE IF EXISTS brain.wiki_suggestions;
DROP TABLE IF EXISTS brain.wiki_messages;
DROP TABLE IF EXISTS brain.wiki_conversations;
DROP TABLE IF EXISTS brain.research_tasks;
DROP TABLE IF EXISTS brain.image_captions;
DROP TABLE IF EXISTS brain.search_feedback;
DROP TABLE IF EXISTS brain.page_relevance;
DROP TABLE IF EXISTS brain.review_items;
DROP TABLE IF EXISTS brain.ingest_tasks;
DROP TABLE IF EXISTS brain.page_sources;
DROP TABLE IF EXISTS brain.wiki_sources;
DROP TABLE IF EXISTS brain.wiki_chunks;
DROP TABLE IF EXISTS brain.memories;
DROP TABLE IF EXISTS brain.graph_block_nodes;
DROP TABLE IF EXISTS brain.graph_edges;
DROP TABLE IF EXISTS brain.graph_nodes;
DROP TABLE IF EXISTS brain.events;
DROP TABLE IF EXISTS brain.blocks;
DROP TABLE IF EXISTS brain.pages;
DROP TABLE IF EXISTS brain.projects;
-- +goose StatementEnd

-- ── files + schemas ────────────────────────────────────────────
-- +goose StatementBegin
DROP TABLE IF EXISTS files.objects;
DROP SCHEMA IF EXISTS files;
DROP SCHEMA IF EXISTS chat;
DROP SCHEMA IF EXISTS brain;
-- 扩展与 biumind_zhcn 文本搜索配置是 deploy init（00-extensions.sql）
-- 负责的共享基础设施，可能被其他服务使用，Down 不删。
-- +goose StatementEnd
