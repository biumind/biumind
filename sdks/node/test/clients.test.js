// Smoke tests for the Node SDK against an in-process http server.
// Uses node:test (built-in, no deps).

import { test } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";

import {
  BiuMindConfig,
  HubClient,
  MemoryClient,
  RateLimitError,
  AuthError,
} from "../src/index.js";

function startServer(routes) {
  return new Promise((resolve) => {
    const server = http.createServer(async (req, res) => {
      const u = new URL(req.url, "http://x");
      const handler = routes[`${req.method} ${u.pathname}`];
      if (!handler) {
        res.statusCode = 404;
        res.end('{"error":"no route"}');
        return;
      }
      const chunks = [];
      for await (const c of req) chunks.push(c);
      const body = Buffer.concat(chunks).toString("utf-8");
      handler(req, res, body, u);
    });
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      resolve({
        url: `http://127.0.0.1:${port}`,
        close: () => new Promise((r) => server.close(r)),
      });
    });
  });
}

test("HubClient.messages blocking", async () => {
  const srv = await startServer({
    "POST /v1/messages": (_req, res, body) => {
      const payload = JSON.parse(body);
      assert.equal(payload.model, "claude-test");
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ id: "msg_1", content: [{ text: "hi" }] }));
    },
  });
  try {
    const cfg = new BiuMindConfig({ hubUrl: srv.url, token: "t" });
    const client = new HubClient(cfg);
    const resp = await client.messages({
      model: "claude-test",
      messages: [{ role: "user", content: "x" }],
    });
    assert.equal(resp.id, "msg_1");
  } finally {
    await srv.close();
  }
});

test("HubClient.messagesStream yields text deltas", async () => {
  const sse = [
    'data: {"type":"message_start"}',
    "",
    'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hel"}}',
    "",
    'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}',
    "",
    "data: [DONE]",
    "",
  ].join("\n");

  const srv = await startServer({
    "POST /v1/messages": (_req, res) => {
      res.setHeader("Content-Type", "text/event-stream");
      res.end(sse);
    },
  });
  try {
    const cfg = new BiuMindConfig({ hubUrl: srv.url, token: "t" });
    const client = new HubClient(cfg);
    let buf = "";
    for await (const chunk of client.messagesStream({
      model: "m",
      messages: [{ role: "user", content: "x" }],
    })) {
      buf += chunk;
    }
    assert.equal(buf, "hello");
  } finally {
    await srv.close();
  }
});

test("HubClient surfaces RateLimitError with retry-after", async () => {
  const srv = await startServer({
    "POST /v1/messages": (_req, res) => {
      res.statusCode = 429;
      res.setHeader("Retry-After", "12");
      res.end('{"error":"rpm"}');
    },
  });
  try {
    const cfg = new BiuMindConfig({ hubUrl: srv.url, token: "t" });
    const client = new HubClient(cfg);
    await assert.rejects(
      client.messages({ model: "m", messages: [] }),
      (e) => e instanceof RateLimitError && e.retryAfter === 12,
    );
  } finally {
    await srv.close();
  }
});

test("MemoryClient.store + recall round-trip", async () => {
  const srv = await startServer({
    "POST /v1/memory": (_req, res, body) => {
      const payload = JSON.parse(body);
      assert.equal(payload.project_id, "p");
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({
        id: "mem_1",
        project_id: "p",
        kind: "recall",
        content: "hi",
        salience: 0.5,
        created_at: "2026-01-01T00:00:00Z",
        last_accessed_at: "2026-01-01T00:00:00Z",
      }));
    },
    "GET /v1/memory/recall": (_req, res) => {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({
        memories: [{
          id: "mem_1",
          project_id: "p",
          kind: "recall",
          content: "hi",
          salience: 0.5,
          score: 1.23,
          created_at: "2026-01-01T00:00:00Z",
          last_accessed_at: "2026-01-01T00:00:00Z",
        }],
        mode: "hybrid",
        query: "hi",
      }));
    },
  });
  try {
    const cfg = new BiuMindConfig({ hubUrl: srv.url, token: "t" });
    const client = new MemoryClient(cfg);
    const m = await client.store({ projectId: "p", content: "hi" });
    assert.equal(m.id, "mem_1");
    const r = await client.recall({ projectId: "p", q: "hi" });
    assert.equal(r.mode, "hybrid");
    assert.equal(r.memories[0].score, 1.23);
  } finally {
    await srv.close();
  }
});

test("MemoryClient.list raises AuthError on 401", async () => {
  const srv = await startServer({
    "GET /v1/memory": (_req, res) => {
      res.statusCode = 401;
      res.end('{"error":"bad token"}');
    },
  });
  try {
    const cfg = new BiuMindConfig({ hubUrl: srv.url, token: "t" });
    const client = new MemoryClient(cfg);
    await assert.rejects(
      client.list({ projectId: "p" }),
      (e) => e instanceof AuthError,
    );
  } finally {
    await srv.close();
  }
});

test("invalid kind rejects", async () => {
  const cfg = new BiuMindConfig({ hubUrl: "http://localhost", token: "t" });
  const client = new MemoryClient(cfg);
  await assert.rejects(
    client.store({ projectId: "p", content: "x", kind: "garbage" }),
    /invalid kind/,
  );
});
