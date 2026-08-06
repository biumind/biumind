// Spawns and supervises a `biu bridge --listen 127.0.0.1:<port>`
// subprocess.
//
// Why we own the process rather than expecting the user to run one:
//
//   * single-click activation experience (zero terminal dance)
//   * deterministic auth token, never written to disk
//   * automatic cleanup on extension reload / VS Code shutdown
//
// We pick a random port (when configured port is 0), resolve the
// actual bound address by parsing the bridge's stderr "[biu] listening
// at …" line, and surface that as `endpoint`. The auth token is
// generated on this side and passed via `--auth-token`.

import * as crypto from 'crypto';
import { ChildProcessWithoutNullStreams, spawn } from 'child_process';
import * as net from 'net';
import * as vscode from 'vscode';

export class BridgeProcess {
  readonly endpoint: string;
  readonly authToken: string;

  private constructor(
    private readonly proc: ChildProcessWithoutNullStreams,
    endpoint: string,
    authToken: string,
  ) {
    this.endpoint = endpoint;
    this.authToken = authToken;
  }

  // start spawns biu bridge and resolves once the bridge logs that
  // it's listening. Rejects on:
  //   * missing binary (ENOENT)
  //   * bridge exits before logging "listening at"
  //   * timeout (5s) waiting for the listening line
  static async start(
    binary: string,
    requestedPort: number,
    log: vscode.OutputChannel,
  ): Promise<BridgeProcess> {
    const port = requestedPort > 0 ? requestedPort : await freePort();
    const authToken = crypto.randomBytes(24).toString('hex');

    const proc = spawn(
      binary,
      ['bridge', '--listen', `127.0.0.1:${port}`, '--auth-token', authToken],
      {
        stdio: ['ignore', 'pipe', 'pipe'],
        // Inherit env so users' ANTHROPIC_API_KEY / BIU_CONFIG flow
        // through. Prefer the bundled BIUMIND_HUB_URL etc. unchanged.
        env: process.env,
      },
    );

    proc.stdout.on('data', (d: Buffer) => log.appendLine(`[bridge] ${d.toString().trim()}`));
    proc.stderr.on('data', (d: Buffer) => log.appendLine(`[bridge] ${d.toString().trim()}`));

    // Resolve when bridge is healthy. We don't actually parse a
    // banner line — we just probe the port until it accepts a TCP
    // connection or we time out.
    const endpoint = `http://127.0.0.1:${port}`;
    const ready = waitForListen(port, 5000);

    return Promise.race([
      ready.then(() => new BridgeProcess(proc, endpoint, authToken)),
      onExit(proc).then((code) => {
        throw new Error(`bridge exited with code ${code} before listening`);
      }),
    ]);
  }

  // stop kills the subprocess. Idempotent.
  async stop(): Promise<void> {
    if (this.proc.killed) return;
    this.proc.kill('SIGTERM');
    // Give it 2s to drain SSE listeners cleanly.
    await new Promise((resolve) => setTimeout(resolve, 2000));
    if (!this.proc.killed) {
      this.proc.kill('SIGKILL');
    }
  }
}

// freePort returns an unused TCP port assigned by the kernel. Race
// window between us closing and the bridge binding is small but
// real — tolerable for a developer-machine extension.
function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      if (typeof addr === 'object' && addr) {
        const port = addr.port;
        srv.close(() => resolve(port));
      } else {
        srv.close(() => reject(new Error('failed to resolve free port')));
      }
    });
  });
}

// waitForListen polls TCP every 100ms until something accepts on
// localhost:port, or the timeout elapses.
function waitForListen(port: number, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    const tick = (): void => {
      const sock = new net.Socket();
      sock.setTimeout(500);
      sock.once('connect', () => {
        sock.destroy();
        resolve();
      });
      sock.once('error', () => {
        sock.destroy();
        if (Date.now() > deadline) {
          reject(new Error(`bridge not listening on :${port} after ${timeoutMs}ms`));
        } else {
          setTimeout(tick, 100);
        }
      });
      sock.once('timeout', () => sock.destroy());
      sock.connect(port, '127.0.0.1');
    };
    tick();
  });
}

function onExit(proc: ChildProcessWithoutNullStreams): Promise<number> {
  return new Promise((resolve) => {
    proc.once('exit', (code) => resolve(code ?? -1));
  });
}
