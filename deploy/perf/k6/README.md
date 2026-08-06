# Performance benchmarks

[k6](https://k6.io) scripts that exercise model-relay relay + brain memory
recall against a running cluster. Designed to:

1. Run on a schedule against staging — catches latency regressions
   before they reach prod.
2. Be runnable locally against `docker-compose` for ad-hoc spike tests.
3. Fail the GH Action when SLO thresholds slip (p95 / p99 budgets).

## Local

```bash
# Boot the docker-compose stack.
make -C deploy/docker-compose up

# Generate a JWT for the load runner.
HUB_BEARER=$(biu auth token --user perf-runner)

# model-relay relay smoke (requires ANTHROPIC_API_KEY in model-relay env).
HUB_URL=http://localhost:7001 HUB_BEARER=$HUB_BEARER \
  k6 run deploy/perf/k6/model-relay-relay.js

# Brain recall — assumes you've populated brain.memories with a few
# hundred rows and have a project_id.
BRAIN_URL=http://localhost:7003 \
  BRAIN_BEARER=$HUB_BEARER \
  PROJECT_ID=<uuid> \
  k6 run deploy/perf/k6/brain-recall.js
```

## Thresholds (will fail CI)

| Script | Metric | Budget |
|---|---|---|
| model-relay-relay | p95 ttfb | < 500ms |
| model-relay-relay | p99 ttfb | < 1500ms |
| model-relay-relay | non-2xx/non-429 rate | < 1% |
| model-relay-relay | rate-limited count | > 1 (gate must work) |
| brain-recall | p95 latency | < 200ms |
| brain-recall | p99 latency | < 800ms |
| brain-recall | failure rate | < 1% |

These are **relay-layer** budgets. End-to-end LLM streaming time is
dominated by the upstream provider and isn't enforceable here; track
that separately in dashboards.

## What's NOT covered

- **Sandbox exec throughput** — Pod-create dominates and varies by
  cluster type / image cache state.
- **Multi-replica budget rendezvous** — covered by Go integration
  tests (`TestPGLimiter_MultiReplica`).
- **Provider cost** — k6 doesn't measure `$/req`. See Grafana token
  spend dashboards.
