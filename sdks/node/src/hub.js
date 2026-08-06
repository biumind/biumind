// HubClient — Anthropic-compatible relay client.

import { requestJson, requestStreamLines } from "./_http.js";

export class HubClient {
  constructor(config) {
    this._cfg = config;
  }

  async messages({ model, messages, system, maxTokens = 1024, extra }) {
    const body = this._body(model, messages, system, maxTokens, false, extra);
    return requestJson("POST", `${this._cfg.hubUrl}/v1/messages`, {
      token: this._cfg.token,
      timeoutMs: this._cfg.timeoutMs,
      body,
    });
  }

  /** Yields text deltas only (drops other event types). */
  async *messagesStream({ model, messages, system, maxTokens = 1024, extra }) {
    const body = this._body(model, messages, system, maxTokens, true, extra);
    for await (const line of requestStreamLines("POST",
      `${this._cfg.hubUrl}/v1/messages`,
      { token: this._cfg.token, timeoutMs: this._cfg.timeoutMs, body })) {
      if (!line.startsWith("data:")) continue;
      const data = line.slice(5).trim();
      if (!data || data === "[DONE]") continue;
      let event;
      try { event = JSON.parse(data); } catch { continue; }
      if (event?.type === "content_block_delta"
          && event?.delta?.type === "text_delta") {
        yield event.delta.text || "";
      }
    }
  }

  /** Yields each parsed SSE event (full upstream shape). */
  async *rawStream(args) {
    const body = this._body(args.model, args.messages, args.system,
                            args.maxTokens ?? 1024, true, args.extra);
    for await (const line of requestStreamLines("POST",
      `${this._cfg.hubUrl}/v1/messages`,
      { token: this._cfg.token, timeoutMs: this._cfg.timeoutMs, body })) {
      if (!line.startsWith("data:")) continue;
      const data = line.slice(5).trim();
      if (!data || data === "[DONE]") continue;
      try { yield JSON.parse(data); } catch { /* skip */ }
    }
  }

  _body(model, messages, system, maxTokens, stream, extra) {
    const body = { model, messages, max_tokens: maxTokens, stream };
    if (system) body.system = system;
    if (extra) Object.assign(body, extra);
    return body;
  }
}
