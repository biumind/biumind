// Bridge client running inside the docproc bundle. Talks to the Flutter
// host in two transports (同 editor bundle，见 editor-web/src/bridge/client.ts）：
//   * Web (iframe): window.parent.postMessage / window message listener
//   * Native (inappwebview): window.flutter_inappwebview.callHandler('bridge', ...)
//                            and window.__kcDocprocReceive(data)（host 经
//                            evaluateJavascript 推 Host → Bundle 消息）

import {
  PARSER_VERSION,
  PROTOCOL_VERSION,
  SUPPORTED_FORMATS,
  isMessage,
  makeMessage,
  type CancelPayload,
  type DocprocErrorCode,
  type ErrorPayload,
  type HostToDocprocMessage,
  type ParsePayload,
  type ProgressPayload,
  type ResultPayload,
} from './protocol'

export interface BridgeHandlers {
  onPing: () => void
  onParse: (payload: ParsePayload) => void
  onCancel: (payload: CancelPayload) => void
}

declare global {
  interface Window {
    flutter_inappwebview?: {
      callHandler: (name: string, ...args: unknown[]) => Promise<unknown>
    }
    /**
     * Native (inappwebview) transport for Host → Bundle messages.
     * Flutter calls this via `evaluateJavascript`.
     */
    __kcDocprocReceive?: (data: unknown) => void
  }
}

export class BridgeClient {
  private handlers: BridgeHandlers | null = null

  start(handlers: BridgeHandlers): void {
    this.handlers = handlers
    window.addEventListener('message', this.onWindowMessage)
    window.__kcDocprocReceive = (data: unknown) => {
      this.onIncoming(data)
    }
  }

  // Bundle → Host

  sendReady(): void {
    this.send(
      makeMessage('ready', {
        version: PARSER_VERSION,
        formats: [...SUPPORTED_FORMATS],
      }),
    )
  }

  sendProgress(id: string, phase: ProgressPayload['phase'], percent: number): void {
    this.send(makeMessage('progress', { phase, percent }, id))
  }

  sendResult(id: string, payload: ResultPayload): void {
    this.send(makeMessage('result', payload, id))
  }

  sendError(
    id: string,
    code: DocprocErrorCode,
    message: string,
    retryable: boolean,
  ): void {
    const payload: ErrorPayload = { code, message, retryable }
    this.send(makeMessage('error', payload, id))
  }

  // Internal

  private send(msg: { type: string; v: number; payload: unknown; id?: string }): void {
    const json = JSON.parse(JSON.stringify(msg)) as typeof msg
    if (window.flutter_inappwebview) {
      void window.flutter_inappwebview.callHandler('bridge', json)
      return
    }
    if (window.parent && window.parent !== window) {
      window.parent.postMessage(json, '*')
      return
    }
    // Standalone dev: log so the developer can see traffic.
    // eslint-disable-next-line no-console
    console.debug('[docproc-bridge] (no host) →', json)
  }

  private onWindowMessage = (ev: MessageEvent): void => {
    this.onIncoming(ev.data)
  }

  private onIncoming(data: unknown): void {
    if (!isMessage(data)) return
    if (data.v !== PROTOCOL_VERSION) return
    this.dispatchHostMessage(data as HostToDocprocMessage)
  }

  private dispatchHostMessage(msg: HostToDocprocMessage): void {
    if (!this.handlers) return
    switch (msg.type) {
      case 'ping':
        this.handlers.onPing()
        return
      case 'parse':
        this.handlers.onParse(msg.payload)
        return
      case 'cancel':
        this.handlers.onCancel(msg.payload)
        return
    }
  }
}
