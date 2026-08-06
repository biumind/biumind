// VS Code extension that drives the biu CLI agent loop via its
// local HTTP+SSE bridge.
//
// On activation we spawn `biu bridge --listen 127.0.0.1:<port>`,
// auto-pick a free port if `biu.bridgePort` is 0, and route every
// editor command (send selection, send prompt) through a typed
// BridgeClient.
//
// Streamed events from the bridge land in a webview that renders
// assistant text + tool calls. The status bar shows current
// permission mode and a cumulative cost counter.
//
// Architecture:
//
//   activate()
//     ├─ spawn biu bridge (BridgeProcess)
//     ├─ BridgeClient — typed POST + SSE consumer
//     ├─ ChatPanel    — singleton webview
//     └─ StatusBar    — mode + cost
//
// All three components are disposable; deactivate() chains them.

import * as vscode from 'vscode';
import { BridgeProcess } from './bridgeProcess';
import { BridgeClient } from './bridgeClient';
import { ChatPanel } from './chatPanel';
import { StatusBar } from './statusBar';

let bridge: BridgeProcess | undefined;
let client: BridgeClient | undefined;
let panel: ChatPanel | undefined;
let status: StatusBar | undefined;
let log: vscode.OutputChannel;

export async function activate(ctx: vscode.ExtensionContext): Promise<void> {
  log = vscode.window.createOutputChannel('BiuMind');
  ctx.subscriptions.push(log);

  const cfg = vscode.workspace.getConfiguration('biu');
  const autoStart = cfg.get<boolean>('autoStart', true);
  const binary = cfg.get<string>('binaryPath', 'biu') || 'biu';
  const port = cfg.get<number>('bridgePort', 0) || 0;
  const permissionMode = cfg.get<string>('permissionMode', 'default');

  status = new StatusBar(permissionMode);
  ctx.subscriptions.push(status);

  if (autoStart) {
    try {
      bridge = await BridgeProcess.start(binary, port, log);
      client = new BridgeClient(bridge.endpoint, bridge.authToken, log);
      log.appendLine(`[biu] bridge ready at ${bridge.endpoint}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      vscode.window.showErrorMessage(
        `BiuMind: failed to launch bridge — ${msg}. ` +
        `Set "biu.binaryPath" or run "biu doctor" to debug.`,
      );
    }
  }

  // ── commands ───────────────────────────────────────
  ctx.subscriptions.push(
    vscode.commands.registerCommand('biu.newSession', async () => {
      if (!client) return notReady();
      try {
        const id = await client.createSession();
        panel = ChatPanel.show(ctx.extensionUri, id, client, status!, log);
        log.appendLine(`[biu] new session ${id}`);
      } catch (err) {
        showErr('new session', err);
      }
    }),
  );

  ctx.subscriptions.push(
    vscode.commands.registerCommand('biu.sendSelection', async () => {
      if (!client) return notReady();
      const editor = vscode.window.activeTextEditor;
      if (!editor || editor.selection.isEmpty) {
        vscode.window.showInformationMessage('Select some code first.');
        return;
      }
      const text = editor.document.getText(editor.selection);
      const path = vscode.workspace.asRelativePath(editor.document.uri);
      const prompt = `Explain the code below from \`${path}\`:\n\n\`\`\`\n${text}\n\`\`\``;
      await sendPrompt(ctx, prompt);
    }),
  );

  ctx.subscriptions.push(
    vscode.commands.registerCommand('biu.sendPrompt', async () => {
      const prompt = await vscode.window.showInputBox({
        prompt: 'Send a prompt to BiuMind',
        placeHolder: 'e.g. find every TODO in this repo',
      });
      if (!prompt) return;
      await sendPrompt(ctx, prompt);
    }),
  );

  ctx.subscriptions.push(
    vscode.commands.registerCommand('biu.showStatus', () => {
      const lines: string[] = [];
      lines.push(`bridge   : ${bridge?.endpoint ?? '(not running)'}`);
      lines.push(`session  : ${panel?.sessionID ?? '(none)'}`);
      lines.push(`mode     : ${status?.mode ?? 'default'}`);
      lines.push(`cost USD : ${status?.costUSD?.toFixed(4) ?? '-'}`);
      vscode.window.showInformationMessage(lines.join('  ·  '));
    }),
  );

  ctx.subscriptions.push(
    vscode.commands.registerCommand('biu.cancelTurn', async () => {
      if (!client || !panel?.sessionID) return notReady();
      try {
        // Submitting an empty prompt is the bridge contract for "abort".
        await client.cancelTurn(panel.sessionID);
        log.appendLine('[biu] cancel sent');
      } catch (err) {
        showErr('cancel', err);
      }
    }),
  );
}

export async function deactivate(): Promise<void> {
  panel?.dispose();
  status?.dispose();
  if (bridge) {
    await bridge.stop();
  }
}

// sendPrompt is the small entry point for both `sendPrompt` and
// `sendSelection` — they only differ on how they build the text.
async function sendPrompt(
  ctx: vscode.ExtensionContext,
  prompt: string,
): Promise<void> {
  if (!client) return notReady();
  try {
    if (!panel) {
      const id = await client.createSession();
      panel = ChatPanel.show(ctx.extensionUri, id, client, status!, log);
    }
    await panel.send(prompt);
  } catch (err) {
    showErr('submit', err);
  }
}

function notReady(): void {
  vscode.window.showWarningMessage(
    'BiuMind bridge is not running. Run `biu doctor` or check Output → BiuMind.',
  );
}

function showErr(scope: string, err: unknown): void {
  const msg = err instanceof Error ? err.message : String(err);
  log.appendLine(`[biu] ${scope}: ${msg}`);
  vscode.window.showErrorMessage(`BiuMind ${scope}: ${msg}`);
}
