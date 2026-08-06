// Brain.Memory recall perf check.
//
// Measures /v1/memory/recall p99 latency under sustained load.
// Hybrid (semantic + lexical) is the slow path because each call
// embeds the query before the SQL hit; lexical-only skips embedding
// and is trivially fast. CI runs hybrid.
//
// Usage:
//   BRAIN_URL=https://brain.example.com \
//   BRAIN_BEARER=eyJ...                  \
//   PROJECT_ID=<uuid>                    \
//     k6 run deploy/perf/k6/brain-recall.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate } from 'k6/metrics';

const baseURL = __ENV.BRAIN_URL || 'http://localhost:7003';
const bearer = __ENV.BRAIN_BEARER || '';
const projectID = __ENV.PROJECT_ID || '';

const recallLatency = new Trend('recall_latency_ms', true);
const hybridRate = new Rate('recall_hybrid');

export const options = {
  scenarios: {
    recall: {
      executor: 'constant-vus',
      vus: 30,
      duration: '2m',
    },
  },
  thresholds: {
    // p95 < 200 ms for cached embeddings (no provider call).
    // p99 < 800 ms for fresh queries (embedding API roundtrip).
    recall_latency_ms: ['p(95)<200', 'p(99)<800'],
    // 99% of recalls succeed; the 1% budget tolerates planned restarts.
    'http_req_failed{expected_response:true}': ['rate<0.01'],
  },
  tags: { service: 'brain' },
};

const queries = [
  'vim',
  'deployment workflow',
  'preferred response language',
  'RAG strategies',
  'database schema for memories',
  'ssh keys',
  'biumind features',
  'hybrid search',
];

export default function () {
  if (!bearer || !projectID) {
    console.error('BRAIN_BEARER and PROJECT_ID required');
    return;
  }
  const q = queries[Math.floor(Math.random() * queries.length)];
  const url = `${baseURL}/v1/memory/recall?project_id=${projectID}&q=${encodeURIComponent(q)}`;
  const params = {
    headers: { Authorization: `Bearer ${bearer}` },
    timeout: '5s',
    tags: { name: 'GET /v1/memory/recall' },
  };
  const start = Date.now();
  const res = http.get(url, params);
  recallLatency.add(Date.now() - start);

  check(res, {
    'recall ok': (r) => r.status === 200,
    'mode is hybrid or lexical': (r) => {
      try {
        const m = r.json('mode');
        hybridRate.add(m === 'hybrid');
        return m === 'hybrid' || m === 'lexical';
      } catch {
        return false;
      }
    },
  });
  sleep(0.5);
}

export function handleSummary(data) {
  const m = data.metrics;
  return {
    stdout: `\nbrain-recall summary: p95=${m.recall_latency_ms?.values?.['p(95)']?.toFixed(0)}ms p99=${m.recall_latency_ms?.values?.['p(99)']?.toFixed(0)}ms hybrid_rate=${(m.recall_hybrid?.values?.rate * 100)?.toFixed(1)}%\n`,
    'summary.json': JSON.stringify(data, null, 2),
  };
}
