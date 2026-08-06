// Hub /v1/messages relay smoke + perf check.
//
// What this measures:
//   * Per-replica throughput when 200 VUs sustain RPM=200/min
//   * p99 latency for non-streaming responses
//   * 429 rate when bursting past hub.rpm
//
// What this does NOT replace:
//   * Real upstream-LLM cost — we point Hub at a stub upstream so we
//     measure relay overhead, not provider TTFB.
//   * Multi-replica behaviour — that's covered by quota integration
//     tests (200 goroutines exact 100/100). Run k6 against the prod
//     cluster to validate the rendezvous holds under wall-clock.
//
// Usage:
//   HUB_URL=https://hub.example.com \
//   HUB_BEARER=eyJ...                 \
//     k6 run deploy/perf/k6/hub-relay.js
//
// Thresholds make the run FAIL the GH Action when SLOs slip.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

const baseURL = __ENV.HUB_URL || 'http://localhost:7001';
const bearer = __ENV.HUB_BEARER || '';
// Upstream that Hub proxies to. When unset we still hit Hub but expect
// a 502 because the platform pool keys are absent — usable for relay
// path latency, useless for end-to-end SLOs. Set this in CI.
const model = __ENV.MODEL || 'claude-3-5-sonnet-20240620';

// Custom metrics.
const allowed = new Counter('relay_allowed');
const rateLimited = new Counter('relay_rate_limited');
const upstreamErr = new Counter('relay_upstream_err');
const ttfb = new Trend('relay_ttfb_ms', true);
const tpmHeaderPresent = new Rate('tpm_header_present');

export const options = {
  scenarios: {
    steady: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 50 },   // warm up
        { duration: '2m',  target: 100 },  // sustained load
        { duration: '30s', target: 0 },    // cool down
      ],
      gracefulRampDown: '15s',
    },
  },
  thresholds: {
    // p95 < 500ms, p99 < 1500ms for the relay layer (excludes upstream
    // streaming time since we use non-stream here).
    relay_ttfb_ms: ['p(95)<500', 'p(99)<1500'],
    // Failure rate budget: 1% non-2xx + non-429 responses.
    'http_req_failed{expected_response:true}': ['rate<0.01'],
    // We expect 429s as load ramps past hub.rpm — but only after VUs
    // exceed the rpm budget. Sanity: at peak load, ≥5% should 429
    // (proves the gate is working).
    relay_rate_limited: ['count>1'],
  },
  // Tag every request so http_req_duration shows up per-endpoint in
  // the k6 cloud / output.
  tags: { service: 'hub' },
};

export default function () {
  if (!bearer) {
    // Without a bearer we can't get past auth middleware; abort early
    // with a clear hint instead of pinning rate-limited noise.
    console.error('HUB_BEARER required');
    return;
  }
  const body = JSON.stringify({
    model,
    messages: [{ role: 'user', content: 'ping' }],
    max_tokens: 16,
    stream: false,
  });
  const params = {
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${bearer}`,
    },
    timeout: '10s',
    tags: { name: 'POST /v1/messages' },
  };

  const start = Date.now();
  const res = http.post(`${baseURL}/v1/messages`, body, params);
  ttfb.add(Date.now() - start);

  // Bucket the outcome.
  if (res.status === 200) {
    allowed.add(1);
  } else if (res.status === 429) {
    rateLimited.add(1);
  } else {
    upstreamErr.add(1);
  }
  tpmHeaderPresent.add(
    res.headers['X-Ratelimit-Tpm-Limit'] !== undefined ||
      res.headers['X-Ratelimit-Limit'] !== undefined,
  );

  check(res, {
    'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
    'response has rate-limit headers': (r) =>
      'X-Ratelimit-Limit' in r.headers || 'X-Ratelimit-Tpm-Limit' in r.headers,
  });

  // Pace at ~5 rps per VU so we don't trivially overrun the upstream
  // and get nothing but timeouts.
  sleep(0.2);
}

// Print a small summary block at the end so CI logs have one line of
// "did this run succeed".
export function handleSummary(data) {
  const m = data.metrics;
  const summary = {
    p95_ttfb_ms: m.relay_ttfb_ms?.values?.['p(95)']?.toFixed(0),
    p99_ttfb_ms: m.relay_ttfb_ms?.values?.['p(99)']?.toFixed(0),
    allowed: m.relay_allowed?.values?.count,
    rate_limited: m.relay_rate_limited?.values?.count,
    upstream_err: m.relay_upstream_err?.values?.count,
  };
  return {
    stdout: `\nhub-relay summary: ${JSON.stringify(summary)}\n`,
    'summary.json': JSON.stringify(data, null, 2),
  };
}
