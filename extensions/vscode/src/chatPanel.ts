// Singleton webview that renders the agent's streaming output.
//
// Render contract:
//
//   * each user prompt → blue blob (right-aligned)
//   * each assistant_text → grey blob (left-aligned)
//   * each tool_start → "⏺ ToolName" affordance with collapsible args
//   * each tool_result → "↳ output" with truncated body
//
// The panel owns its session ID so multiple commands can route to
// the same conversation without thrashing the bridge.

import * as vscode from 'vscode';
import { BridgeClient, BridgeEvent } from './bridgeClient';
import { StatusBar } from './statusBar';

export class ChatPanel {
  private static current: ChatPanel | undefined;

  static show(
    extensionUri: vscode.Uri,
    sessionID: string,
    client: BridgeClient,
    status: StatusBar,
    log: vscode.OutputChannel,
  ): ChatPanel {
    if (ChatPanel.current) {
      ChatPanel.current.panel.reveal(vscode.ViewColumn.Beside);
      ChatPanel.current.sessionID = sessionID;
      return ChatPanel.current;
    }
    const panel = vscode.window.createWebviewPanel(
      'biuChat',
      `BiuMind ${sessionID.slice(0, 8)}`,
      vscode.ViewColumn.Beside,
      { enableScripts: true, retainContextWhenHidden: true },
    );
    ChatPanel.current = new ChatPanel(panel, sessionID, client, status, log);
    return ChatPanel.current;
  }

  sessionID: string;

  private streamCtl?: AbortController;

  private constructor(
    private readonly panel: vscode.WebviewPanel,
    sessionID: string,
    private readonly client: BridgeClient,
    private readonly status: StatusBar,
    private readonly log: vscode.OutputChannel,
  ) {
    this.sessionID = sessionID;
    this.panel.webview.html = renderHTML();
    this.panel.onDidDispose(() => {
      ChatPanel.current = undefined;
      this.streamCtl?.abort();
    });
    this.panel.webview.onDidReceiveMessage((msg) => {
      if (msg?.type === 'send' && typeof msg.text === 'string') {
        this.send(msg.text).catch((err) => log.appendLine(`[biu] send: ${err}`));
      }
    });
  }

  // send fires a prompt and starts streaming the response into the
  // panel. Concurrent calls re-use the existing SSE stream — the
  // bridge contract is "POST messages = abort + restart" so a new
  // submit cancels the previous turn server-side.
  async send(prompt: string): Promise<void> {
    this.panel.webview.postMessage({ type: 'user', text: prompt });
    await this.client.submit(this.sessionID, prompt);
    if (this.streamCtl) {
      this.streamCtl.abort();
    }
    const ctl = new AbortController();
    this.streamCtl = ctl;
    try {
      await this.client.streamEvents(
        this.sessionID,
        (ev) => this.handleEvent(ev),
        ctl.signal,
      );
    } catch (err) {
      if ((err as Error).name === 'AbortError') return;
      this.log.appendLine(`[biu] stream error: ${err}`);
      this.panel.webview.postMessage({
        type: 'error',
        text: (err as Error).message,
      });
    }
  }

  dispose(): void {
    this.streamCtl?.abort();
    this.panel.dispose();
  }

  private handleEvent(ev: BridgeEvent): void {
    this.panel.webview.postMessage(ev);
    switch (ev.type) {
      case 'done':
        // refresh cost in the status bar
        this.client
          .cost(this.sessionID)
          .then((c) => this.status.setCost(c.usd))
          .catch(() => undefined);
        break;
    }
  }
}

// renderHTML is the static webview body. We inline a tiny chat UI;
// no external bundles to keep the extension self-contained.
function renderHTML(): string {
  return /* html */ `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta http-equiv="content-security-policy"
  content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline';" />
<style>
  :root {
    --user: var(--vscode-textLink-foreground);
    --assistant: var(--vscode-foreground);
    --tool: var(--vscode-descriptionForeground);
    --bg-user: var(--vscode-editorWidget-background);
    --bg-assistant: var(--vscode-editor-background);
    --border: var(--vscode-input-border);
  }
  body {
    margin: 0; padding: 0;
    font-family: var(--vscode-font-family);
    color: var(--vscode-foreground);
    background: var(--vscode-editor-background);
    height: 100vh; display: flex; flex-direction: column;
  }
  #log { flex: 1; overflow-y: auto; padding: 1rem; }
  .turn { margin-bottom: 1rem; }
  .role { font-weight: 600; margin-bottom: 0.25rem; }
  .user .role { color: var(--user); }
  .assistant .role { color: var(--assistant); }
  .tool { color: var(--tool); margin: 0.25rem 0 0.25rem 1rem; font-family: var(--vscode-editor-font-family, monospace); }
  pre { background: var(--bg-user); padding: 0.5rem 0.75rem; border-radius: 4px;
        white-space: pre-wrap; word-break: break-word; margin: 0.25rem 0; }
  details summary { cursor: pointer; }
  #composer { display: flex; gap: 0.5rem; padding: 0.5rem; border-top: 1px solid var(--border); }
  #composer textarea { flex: 1; resize: vertical; min-height: 2.5rem; max-height: 10rem;
    padding: 0.5rem; background: var(--bg-user); color: var(--vscode-foreground);
    border: 1px solid var(--border); border-radius: 4px; font-family: inherit; }
  #composer button { padding: 0.4rem 1rem; background: var(--vscode-button-background);
    color: var(--vscode-button-foreground); border: none; border-radius: 4px; cursor: pointer; }
  .err { color: var(--vscode-errorForeground); padding: 0.5rem; }
</style>
</head>
<body>
<div id="log"></div>
<form id="composer">
  <textarea id="prompt" placeholder="Send a message…"></textarea>
  <button type="submit">Send</button>
</form>
<script>
  const vscode = acquireVsCodeApi();
  const log = document.getElementById('log');
  const form = document.getElementById('composer');
  const ta = document.getElementById('prompt');
  let currentAssistant = null;

  function appendTurn(role, text) {
    const div = document.createElement('div');
    div.className = 'turn ' + role;
    const r = document.createElement('div');
    r.className = 'role';
    r.textContent = role === 'user' ? '👤 You' : '🤖 BiuMind';
    div.appendChild(r);
    const pre = document.createElement('pre');
    pre.textContent = text;
    div.appendChild(pre);
    log.appendChild(div);
    log.scrollTop = log.scrollHeight;
    return div;
  }

  function appendTool(line) {
    const d = document.createElement('div');
    d.className = 'tool';
    d.textContent = line;
    log.appendChild(d);
    log.scrollTop = log.scrollHeight;
    return d;
  }

  window.addEventListener('message', (msg) => {
    const e = msg.data;
    switch (e.type) {
      case 'user':
        appendTurn('user', e.text);
        currentAssistant = null;
        break;
      case 'assistant_text':
        currentAssistant = appendTurn('assistant', e.text);
        break;
      case 'tool_start':
        appendTool('⏺ ' + e.name + ' ' + JSON.stringify(e.input).slice(0, 200));
        break;
      case 'tool_result':
        const marker = e.is_error ? '✗' : '✓';
        const out = (e.output || '').slice(0, 400);
        appendTool('  ' + marker + ' ' + e.name + ' [' + (e.elapsed_ms||0) + 'ms]  ' + out);
        break;
      case 'compact_started':
        appendTool('↻ compacting (was ' + e.tokens_before + ' tokens)…');
        break;
      case 'compact_finished':
        appendTool('✓ compacted; saved ' + e.tokens_saved + ' tokens');
        break;
      case 'done':
        appendTool('—— ' + e.input_tokens + ' in / ' + e.output_tokens + ' out tokens, ' + e.elapsed_ms + 'ms');
        break;
      case 'error':
        const div = document.createElement('div');
        div.className = 'err';
        div.textContent = '✗ ' + e.message;
        log.appendChild(div);
        break;
    }
  });

  form.addEventListener('submit', (ev) => {
    ev.preventDefault();
    const text = ta.value.trim();
    if (!text) return;
    vscode.postMessage({ type: 'send', text });
    ta.value = '';
  });

  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      form.requestSubmit();
    }
  });
</script>
</body>
</html>`;
}
