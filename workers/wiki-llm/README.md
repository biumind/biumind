# biumind-wiki-llm-worker

Multi-page CoT wiki ingest worker. Subscribes to NATS, drives one
source through analyze/generate prompts via biumind model-relay, streams
emitted wiki pages back as they finish (streaming partial-save).

Sits alongside `workers/wiki-parse`:

- `workers/wiki-parse`: source file → extracted text (PDF / DOCX / XLSX /
  PPTX / EPUB / MD / TXT / HTML), written back to `wiki_sources`.
- `workers/wiki-llm` (this): text → many wiki pages via two-stage CoT;
  consumes `brain.wiki.ingest.requested`.

## Wire

```
brain.ingest_tasks (Postgres) ──┬─ POST /v1/wiki/projects/{pid}/ingest
                                │
brain.wiki.ingest.requested ←───┘
        │
        ▼
   wiki-llm worker
        │  per-page Update
        ▼
brain.wiki.ingest.update ───→ brain subscriber
                                │
                                ▼
                          brain.pages / blocks
                          brain.ingest_tasks.result_pages
```

The brain-side subscriber for `brain.wiki.ingest.update` is added in
P1-8 alongside the worker's real LLM-driven page emission. Until then
the worker is a stub: every accepted task immediately publishes
`running` then `failed` so brain task rows resolve to a terminal state
instead of staying pending forever.

## Env

| Var | Default | Purpose |
| --- | --- | --- |
| `BIUMIND_NATS_URL` | `nats://localhost:4222` | NATS server (or fall back to `NATS_URL`) |
| `BIUMIND_ENV` | `dev` | Subject prefix; `biumind.<env>.brain.wiki.ingest.*` |
| `BIUMIND_WIKI_LLM_QUEUE` | `brain-wiki-llm` | NATS queue group; replicas share work |
| `BIUMIND_WIKI_LLM_TIMEOUT_S` | `600` | Per-task budget (10 min) |
| `BIUMIND_HUB_URL` | (empty) | biumind model-relay base URL for LLM calls |
| `BIUMIND_RELAY_INTERNAL_TOKEN` | (empty) | model-relay 内部车道共享密钥（= relay 的 `IDENTITY_INTERNAL_TOKEN`）；LLM 计费按任务 owner（body `user_id`）归属 |
| `BIUMIND_WIKI_LLM_MODEL` | `claude-haiku-4-5-20251001` | Default model |
| `BIUMIND_LOG_LEVEL` | `INFO` | Python logging level |

## Run

```bash
cd workers/wiki-llm
pip install -e '.[dev]'
python -m wiki_llm
```

```bash
# Tests (no NATS required — `runner.handle_message` accepts a stub publish)
pytest
```

## License

Apache-2.0. The pipeline is re-implemented from llm_wiki TS source
to stay license-clean; knowcode's GPL Python is reference only.
