// Bridge client running inside the editor bundle. Talks to the Flutter host
// in two transports:
//   * Web (iframe): window.parent.postMessage / window message listener
//   * Native (inappwebview): window.flutter_inappwebview.callHandler('bridge', ...)
//                            and window.addEventListener('message') (inappwebview
//                            forwards Flutter→JS messages as MessageEvents)

import {
  PROTOCOL_VERSION,
  isMessage,
  makeMessage,
  type CommandPayload,
  type DocChangedPayload,
  type SelectionChangedPayload,
  type HostToEditorMessage,
  type InitPayload,
  type LogPayload,
  type NavigatePayload,
  type SetDocPayload,
  type SetOptionsPayload,
  type WikilinkQueryPayload,
  type WikilinkQueryReplyPayload,
} from './protocol'

type ReplyResolver = (payload: unknown) => void

export interface BridgeHandlers {
  onInit: (payload: InitPayload) => void
  onSetDoc: (payload: SetDocPayload) => void
  onSetOptions: (payload: SetOptionsPayload) => void
  onCommand: (payload: CommandPayload) => void
}

declare global {
  interface Window {
    flutter_inappwebview?: {
      callHandler: (name: string, ...args: unknown[]) => Promise<unknown>
    }
    /**
     * Native (inappwebview) transport for Host → Editor messages.
     * Flutter calls this via `evaluateJavascript` because postMessage
     * across the WKWebView/WebView boundary is awkward.
     */
    __kcEditorReceive?: (data: unknown) => void
  }
}

export class BridgeClient {
  private handlers: BridgeHandlers | null = null
  private pendingReplies = new Map<string, ReplyResolver>()
  private replyTimers = new Map<string, ReturnType<typeof setTimeout>>()
  private replyTimeoutMs = 5000

  start(handlers: BridgeHandlers): void {
    this.handlers = handlers
    window.addEventListener('message', this.onWindowMessage)
    window.__kcEditorReceive = (data: unknown) => {
      this.onIncoming(data)
    }
  }

  // Editor → Host

  sendReady(editorVersion: string): void {
    this.send(
      makeMessage('ready', { editorVersion, protocolV: PROTOCOL_VERSION }),
    )
  }

  sendDocChanged(payload: DocChangedPayload): void {
    this.send(makeMessage('docChanged', payload))
  }

  sendNavigate(payload: NavigatePayload): void {
    this.send(makeMessage('navigate', payload))
  }

  sendLog(payload: LogPayload): void {
    this.send(makeMessage('log', payload))
  }

  // S3 P1-6 selection-edit: push the current selection so the host can show
  // a follow overlay (Ask/Edit) anchored at viewport-relative coords.
  sendSelectionChanged(payload: SelectionChangedPayload): void {
    this.send(makeMessage('selectionChanged', payload))
  }

  requestWikilinkQuery(
    payload: WikilinkQueryPayload,
  ): Promise<WikilinkQueryReplyPayload> {
    return this.request<WikilinkQueryPayload, WikilinkQueryReplyPayload>(
      'wikilinkQuery',
      payload,
    )
  }

  // Internal

  private request<Req, Resp>(type: string, payload: Req): Promise<Resp> {
    const id = randomId()
    const msg = makeMessage(type, payload, id)
    return new Promise<Resp>((resolve) => {
      this.pendingReplies.set(id, resolve as ReplyResolver)
      const timer = setTimeout(() => {
        if (this.pendingReplies.delete(id)) {
          this.replyTimers.delete(id)
          // Empty / safe default rather than reject — host may legitimately
          // have nothing to say (e.g. wikilink suggest with no matches).
          resolve({} as Resp)
        }
      }, this.replyTimeoutMs)
      this.replyTimers.set(id, timer)
      this.send(msg)
    })
  }

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
    console.debug('[bridge] (no host) →', json)
  }

  private onWindowMessage = (ev: MessageEvent): void => {
    this.onIncoming(ev.data)
  }

  private onIncoming(data: unknown): void {
    if (!isMessage(data)) return
    if (data.v !== PROTOCOL_VERSION) {
      this.sendLog({
        level: 'error',
        msg: `protocol version mismatch: editor v=${PROTOCOL_VERSION}, host v=${data.v}`,
      })
      return
    }
    if (data.id && this.pendingReplies.has(data.id)) {
      const resolver = this.pendingReplies.get(data.id)!
      this.pendingReplies.delete(data.id)
      const timer = this.replyTimers.get(data.id)
      if (timer !== undefined) {
        clearTimeout(timer)
        this.replyTimers.delete(data.id)
      }
      resolver(data.payload)
      return
    }
    this.dispatchHostMessage(data as HostToEditorMessage)
  }

  private dispatchHostMessage(msg: HostToEditorMessage): void {
    if (!this.handlers) return
    switch (msg.type) {
      case 'init':
        this.handlers.onInit(msg.payload)
        return
      case 'setDoc':
        this.handlers.onSetDoc(msg.payload)
        return
      case 'setOptions':
        this.handlers.onSetOptions(msg.payload)
        return
      case 'command':
        this.handlers.onCommand(msg.payload)
        return
      case 'wikilinkQuery.reply':
        // already handled by request() id matching above; defensively no-op
        return
    }
  }
}

function randomId(): string {
  // RFC4122-ish; sufficient for in-process correlation only.
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}
