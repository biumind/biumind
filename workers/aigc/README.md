# biumind-aigc-worker

AIGC 生成 worker — 订阅 NATS `aigc.task.submit` (services/aigc 发出),
调上游 provider (DashScope / VolcEngine), 完成时转存到 MinIO 并 publish
`aigc.task.update` 让 services/aigc orchestrator 写库 + 通过 services/realtime
把进度推给客户端 SSE.

## 快速开始

```bash
cd workers/aigc
pip install -e ".[dev]"

# 单元测试 (不需要 NATS / 上游 key)
pytest

# 起 worker (要求 NATS 已起 — 见 deploy/docker-compose)
NATS_URL=nats://localhost:4222 \
DASHSCOPE_API_KEY=sk-xxx \
AIGC_S3_ENDPOINT=http://localhost:9000 \
AIGC_S3_ACCESS_KEY=biumind \
AIGC_S3_SECRET_KEY=biumind_minio_dev \
python -m biumind_aigc
```

## 架构

```
services/aigc          NATS aigc.task.submit       biumind-aigc-worker
                  ────────────────────────────►   (queue group: aigc-py)
                                                          │
                                                          ▼
                                                   provider.submit (DashScope...)
                                                          │ external_id
                                                          ▼
                                                   每 3s provider.poll
                                                          │
                                                          ▼ status terminal
                                                   storage.persist (P3-5)
                                                   → MinIO + sha256 + thumb
                                                          │
              NATS aigc.task.update                       ▼
              ◄───────────────────────────────  publish(outputs metadata)
services/aigc orchestrator
  ├─ store.UpdateTaskStatus
  ├─ store.CreateTaskOutput
  ├─ store.AddLineageEdge
  └─ fanout → services/realtime → SSE client
```

## 参考

- 设计: `docs/BiuMind-AIGC-Migration-Plan.md` §2.2
- 存储: `docs/BiuMind-AIGC-Storage-Design.md` §7
- Wire schema: `services/aigc/internal/orchestrator/orchestrator.go`
