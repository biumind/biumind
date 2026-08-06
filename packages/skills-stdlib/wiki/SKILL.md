---
name: wiki
display_name: 知识库
description: BiuMind 知识库（文档 + 块编辑器）的工具集。当用户要查/读/写/搜知识库页面，或需要从自有知识库做检索、把素材交给 LLM 转 wiki 时使用。
icon: 📚
permissions: ["wiki.read"]
paths: ["wiki/**", "docs/**"]
---

# BiuMind Wiki tooling

Two surfaces — both backed by the same brain service, both project-
scoped to the calling user:

* **MCP tools** — `wiki.*` 6 tools served at `POST /v1/mcp` (JSON-RPC
  2.0, JWT-authenticated) and via the `memory-mcp` stdio binary for
  Claude Desktop / Cursor / Continue. Use these from AI clients.
* **REST endpoints** — `/v1/wiki/...` for typed Flutter / web client
  flows. Use these from BiuMind's own apps.

The MCP layer is the canonical surface for AI agents because it
returns structured envelopes that AI clients render natively. REST is
preferred for typed clients that benefit from ETags, If-Match, etc.

## MCP tools (10)

```
wiki.search           query, project_id?,            hybrid BM25 + vector
                      limit=20
wiki.list_pages       project_id, limit=100          recent first
wiki.get_page         page_id, include_blocks=true   page + first 200 blocks
wiki.create_page      project_id, title,             returns the new page
                      parent_id? frontmatter?
wiki.update_page      page_id, title?,               If-Match via version
                      frontmatter?, version?         (omit for last-writer-wins)
wiki.ingest           project_id, raw_text,          queues a CoT task
                      title?                         (worker streams pages back)
wiki.list_reviews     project_id, kind?,             auto-flagged audits:
                      status=open, limit=100         dedup / lint / sweep / merge / suggestion
wiki.dismiss_review   id                             "this isn't a real issue"
                                                     — won't be re-flagged
wiki.merge_pages      canonical_id, duplicate_id     fold duplicate into canonical;
                                                     auto-resolves matching dedup review
wiki.related_pages    page_id, limit=20              "see also" via 3-signal relevance
                                                     (direct wikilink + Adamic-Adar + type)
```

`wiki.search` returns RRF-fused hits with `mode: "bm25"` or
`mode: "hybrid"`. `wiki.ingest` returns a task id you can poll via
the REST endpoint `GET /v1/wiki/ingest/{task_id}` to watch the worker
stream pages in. `wiki.list_reviews` is the queue for the dedup
worker (and future lint/sweep producers) — review_items get an
`open → resolved | dismissed` lifecycle.

## REST endpoints (typed clients)

```
GET    /v1/wiki/projects                            list mine
POST   /v1/wiki/projects                            create
POST   /v1/wiki/projects/{pid}/pages                create page
GET    /v1/wiki/projects/{pid}/pages                list pages
GET    /v1/wiki/projects/{pid}/pages/{id}           get page
PUT    /v1/wiki/projects/{pid}/pages/{id}           update (If-Match)
POST   /v1/wiki/projects/{pid}/pages/{id}/blocks    append block
PUT    /v1/wiki/projects/{pid}/blocks/{id}          edit block (If-Match)
POST   /v1/wiki/projects/{pid}/sources/clip         webclip ingest
GET    /v1/wiki/projects/{pid}/changes?since={id}   catchup events (SSE alt)
POST   /v1/wiki/projects/{pid}/ingest               start CoT ingest task
GET    /v1/wiki/projects/{pid}/ingest               list tasks
GET    /v1/wiki/ingest/{task_id}                    task detail
DELETE /v1/wiki/ingest/{task_id}                    request cancel
GET    /v1/wiki/projects/{pid}/reviews              list audit queue
POST   /v1/wiki/reviews/{id}/resolve                accept the suggestion
POST   /v1/wiki/reviews/{id}/dismiss                reject (won't re-flag)
POST   /v1/wiki/pages/{id}/merge {from_id}          fold from_id into {id}
POST   /v1/search                                   scope=wiki|web|all (RRF)
```

## Block model

A page is an ordered list of blocks. Each block has:

```
id          uuid
type        paragraph | heading | code | quote | list | image | table | embed
content     {text, caption?, ...}     jsonb
position    float64                   ordering within page
version     int                       monotonic — pass as If-Match on update
parent_id   uuid?                     nested list / quote items
```

## Search ranking

`/v1/search?scope=all` and `wiki.search` both run RRF (k=60) over up
to three lists:

  1. **BM25** — Postgres tsvector + biumind_zhcn config, ts_rank_cd ordering
  2. **Vector** — pgvector cosine over `brain.wiki_chunks` (when an
     embedder is configured); chunk hits dedupe to one slot per page
  3. **Web** — SearxNG (when `SEARXNG_URL` is set)

Time decay applied per BM25 hit (default half-life 30 days).

## Decision tree

  - User says "search my wiki for X" → `wiki.search`
  - User says "what pages exist in this project" → `wiki.list_pages`
  - User says "show me page X / open the X page" → `wiki.get_page`
  - User says "create a wiki page about X" → `wiki.create_page`
  - User says "rename / fix the metadata on page X" → `wiki.update_page`
  - User pastes a long article and wants it broken into wiki pages →
    `wiki.ingest` (worker generates source/entity/concept pages CoT-style)
  - User asks "what's pending review / any duplicates / 哪些页有问题 /
    哪些页很久没动" → `wiki.list_reviews` (default status=open; pass
    kind=dedup|lint|sweep to filter):
      * dedup — 语义重复页对（cosine ≥ 0.92）
      * lint  — untitled / empty / stub / dead-wikilink
      * sweep — stale (>90d 未更新) / orphaned (无入链 + >60d 未更新)
  - User confirms two pages are duplicates and wants to merge →
    `wiki.merge_pages(canonical_id, duplicate_id)`. The duplicate gets
    soft-deleted with a merged_into hint; canonical absorbs the blocks.
  - User says "ignore that suggestion" / "they're different on purpose" →
    `wiki.dismiss_review`
  - User says "remember X" without a topic / page intent → `memory.store`
    (NOT wiki — memory is for opaque facts, wiki is for browsable docs)

User's request: $ARGS
