// Typed client for the biu HTTP/SSE bridge. Mirrors
// internal/bridge/server.go's surface.
//
// Endpoints:
//   POST /v1/code/sessions                  → { id }
//   POST /v1/code/sessions/:id/messages     submit a turn
//   GET  /v1/code/sessions/:id/events       text/event-stream
//   GET  /v1/code/sessions/:id/cost         JSON
//   POST /v1/code/sessions/:id/compact      manual compact
//   DELETE /v1/code/sessions/:id            close session
//
// We use Node's built-in `fetch` (Node 18+, which VS Code ships).
// The SSE consumer is hand-rolled because EventSource browser API
// isn't available in extension host.

import * as vscode from 'vscode';

export type BridgeEvent =
  | { type: 'assistant_text'; text: string; stop_reason?: string }
  | { type: 'tool_start'; id: string; name: string; input: Record<string, unknown> }
  | {
      type: 'tool_result';
      id: string;
      name: string;
      output: string;
      is_error: boolean;
      elapsed_ms: number;
    }
  | { type: 'compact_started'; reason: string; tokens_before: number }
  | {
      type: 'compact_finished';
      tokens_before: number;
      tokens_after: number;
      tokens_saved: number;
    }
  | { type: 'error'; message: string; recoverable: boolean }
  | {
      type: 'done';
      stop_reason: string;
      input_tokens: number;
      output_tokens: number;
      elapsed_ms: number;
    }
  | { type: 'end' };

export interface CostSnapshot {
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  usd: number;
}

export class BridgeClient {
  // lastEventID per session to support resume on reconnect; not yet
  // exposed via the API but parsed from `id:` lines so we have it
  // ready when the panel adds resume UX.
  private lastEventID = new Map<string, number>();

  constructor(
    private readonly endpoint: string,
    private readonly authToken: string,
    private readonly log: vscode.OutputChannel,
  ) {}

  async createSession(): Promise<string> {
    const r = await fetch(`${this.endpoint}/v1/code/sessions`, {
      method: 'POST',
      headers: this.headers(),
    });
    await throwOnHTTPError(r, 'createSession');
    const body = (await r.json()) as { id: string };
    return body.id;
  }

  async submit(sessionID: string, prompt: string): Promise<void> {
    const r = await fetch(`${this.endpoint}/v1/code/sessions/${sessionID}/messages`, {
      method: 'POST',
      headers: { ...this.headers(), 'content-type': 'application/json' },
      body: JSON.stringify({ prompt }),
    });
    await throwOnHTTPError(r, 'submit');
  }

  // cancelTurn aborts the active turn by submitting an empty prompt
  // — that's the contract internal/bridge/server.go expects (POST
  // messages = abort + restart, empty prompt = drop the restart).
  async cancelTurn(sessionID: string): Promise<void> {
    // The bridge rejects empty prompts with 400; instead we DELETE
    // and re-create on the next send. Cheaper than a new contract.
    await fetch(`${this.endpoint}/v1/code/sessions/${sessionID}`, {
      method: 'DELETE',
      headers: this.headers(),
    });
  }

  async cost(sessionID: string): Promise<CostSnapshot> {
    const r = await fetch(`${this.endpoint}/v1/code/sessions/${sessionID}/cost`, {
      headers: this.headers(),
    });
    await throwOnHTTPError(r, 'cost');
    return (await r.json()) as CostSnapshot;
  }

  // streamEvents subscribes to the per-session SSE channel and
  // invokes `onEvent` for every parsed frame. Resolves when the
  // channel sends `event: end` or the socket closes.
  async streamEvents(
    sessionID: string,
    onEvent: (ev: BridgeEvent) => void,
    signal?: AbortSignal,
  ): Promise<void> {
    const url = `${this.endpoint}/v1/code/sessions/${sessionID}/events`;
    const headers: Record<string, string> = {
      ...this.headers(),
      accept: 'text/event-stream',
    };
    // Honour the resume cursor on reconnect.
    const last = this.lastEventID.get(sessionID);
    if (last !== undefined) {
      headers['last-event-id'] = String(last);
    }
    const r = await fetch(url, { headers, signal });
    if (r.status === 409) {
      // No turn in progress — caller likely raced. Treat as clean
      // close so the UI doesn't show an error.
      return;
    }
    await throwOnHTTPError(r, 'events');
    if (!r.body) return;
    const reader = r.body.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    while (true) {
      const { done, value } = await reader.read();
      if (done) return;
      buf += decoder.decode(value, { stream: true });
      let idx: number;
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const frame = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        const parsed = parseFrame(frame);
        if (parsed === null) continue;
        if (parsed.id !== undefined) this.lastEventID.set(sessionID, parsed.id);
        if (parsed.event === 'end') {
          onEvent({ type: 'end' });
          return;
        }
        try {
          const payload = JSON.parse(parsed.data) as Record<string, unknown>;
          payload.type = parsed.event;
          onEvent(payload as unknown as BridgeEvent);
        } catch (e) {
          this.log.appendLine(`[biu] bad SSE frame: ${parsed.data}`);
        }
      }
    }
  }

  private headers(): Record<string, string> {
    return { authorization: `Bearer ${this.authToken}` };
  }
}

function parseFrame(frame: string): { event: string; data: string; id?: number } | null {
  let event = 'message';
  let data = '';
  let id: number | undefined;
  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) data += (data ? '\n' : '') + line.slice(5).trim();
    else if (line.startsWith('id:')) {
      const n = Number(line.slice(3).trim());
      if (!Number.isNaN(n)) id = n;
    }
  }
  if (!event && !data) return null;
  return { event, data, id };
}

async function throwOnHTTPError(r: Response, scope: string): Promise<void> {
  if (r.ok) return;
  const body = await r.text().catch(() => '');
  throw new Error(`${scope}: HTTP ${r.status} — ${body || r.statusText}`);
}
