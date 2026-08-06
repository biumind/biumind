// HTTP helpers shared by HubClient + MemoryClient. Uses Node 18+'s
// global ``fetch``; no third-party deps.

import { fromStatus } from "./errors.js";

export async function requestJson(method, url, { token, timeoutMs, body, query } = {}) {
  const fullUrl = withQuery(url, query);
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), timeoutMs);
  let resp;
  try {
    resp = await fetch(fullUrl, {
      method,
      headers: buildHeaders(token, body !== undefined),
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: ac.signal,
    });
  } finally {
    clearTimeout(timer);
  }
  const text = await resp.text();
  if (!resp.ok) {
    throw fromStatus(resp.status, text,
      Number(resp.headers.get("retry-after") || 0) || 0);
  }
  return text ? JSON.parse(text) : {};
}

/**
 * Open an SSE stream. Yields each non-empty line as a string.
 * Caller is responsible for parsing ``data: ...`` shape.
 */
export async function* requestStreamLines(method, url, { token, timeoutMs, body } = {}) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), timeoutMs);
  let resp;
  try {
    resp = await fetch(url, {
      method,
      headers: { ...buildHeaders(token, body !== undefined), Accept: "text/event-stream" },
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: ac.signal,
    });
  } finally {
    clearTimeout(timer);
  }
  if (!resp.ok) {
    const text = await resp.text();
    throw fromStatus(resp.status, text,
      Number(resp.headers.get("retry-after") || 0) || 0);
  }
  if (!resp.body) return;
  const decoder = new TextDecoder();
  let buf = "";
  for await (const chunk of resp.body) {
    buf += decoder.decode(chunk, { stream: true });
    let idx;
    while ((idx = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, idx).replace(/\r$/, "");
      buf = buf.slice(idx + 1);
      if (line) yield line;
    }
  }
  const tail = buf.trim();
  if (tail) yield tail;
}

function buildHeaders(token, hasBody) {
  const h = {
    Authorization: `Bearer ${token}`,
    Accept: "application/json",
  };
  if (hasBody) h["Content-Type"] = "application/json";
  return h;
}

function withQuery(url, query) {
  if (!query) return url;
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v != null) usp.append(k, String(v));
  }
  const qs = usp.toString();
  if (!qs) return url;
  return url + (url.includes("?") ? "&" : "?") + qs;
}
