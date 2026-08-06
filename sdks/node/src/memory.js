// MemoryClient — Brain memory service.
//
// Mirrors services/brain/internal/memory/api/api.go contract.

import { requestJson } from "./_http.js";

const VALID_KINDS = new Set(["recall", "preference", "skill"]);

export class MemoryClient {
  constructor(config) {
    this._cfg = config;
  }

  async store({ projectId, content, kind = "recall", salience }) {
    if (!VALID_KINDS.has(kind)) throw new Error(`invalid kind ${kind}`);
    const body = { project_id: projectId, kind, content };
    if (salience != null) body.salience = salience;
    const raw = await requestJson("POST", `${this._cfg.brainUrl}/v1/memory`, {
      token: this._cfg.token,
      timeoutMs: this._cfg.timeoutMs,
      body,
    });
    return parseMemory(raw);
  }

  async list({ projectId, kind, limit = 100 }) {
    const query = { project_id: projectId, limit };
    if (kind) query.kind = kind;
    const raw = await requestJson("GET", `${this._cfg.brainUrl}/v1/memory`, {
      token: this._cfg.token,
      timeoutMs: this._cfg.timeoutMs,
      query,
    });
    return (raw.memories || []).map(parseMemory);
  }

  async recall({ projectId, q, kind, limit = 10 }) {
    if (!q || !q.trim()) throw new Error("q (query) is required");
    const query = { project_id: projectId, q, limit };
    if (kind) query.kind = kind;
    const raw = await requestJson("GET",
      `${this._cfg.brainUrl}/v1/memory/recall`,
      {
        token: this._cfg.token,
        timeoutMs: this._cfg.timeoutMs,
        query,
      });
    return {
      memories: (raw.memories || []).map(parseMemory),
      mode: raw.mode || "unknown",
      query: raw.query || "",
    };
  }

  async delete(id) {
    await requestJson("DELETE", `${this._cfg.brainUrl}/v1/memory/${id}`, {
      token: this._cfg.token,
      timeoutMs: this._cfg.timeoutMs,
    });
  }
}

function parseMemory(j) {
  return {
    id: j.id,
    projectId: j.project_id || "",
    kind: j.kind || "recall",
    content: j.content || "",
    salience: Number(j.salience ?? 0.5),
    createdAt: j.created_at ? new Date(j.created_at) : null,
    lastAccessedAt: j.last_accessed_at ? new Date(j.last_accessed_at) : null,
    score: j.score != null ? Number(j.score) : null,
  };
}
