// Bridge protocol v1 between the Knowcode Flutter host and the embedded
// Milkdown editor bundle. Mirror of client/lib/features/pages/editor_bridge_protocol.dart.
//
// Wire format: every message is `{ type, v, id?, payload }`.
//   `type`     — one of the message kinds below.
//   `v`        — protocol version, currently 1. A mismatch is fatal.
//   `id`       — uuid set on req messages and echoed on the matching reply.
//   `payload`  — type-specific body.

export const PROTOCOL_VERSION = 1 as const

export type Theme = 'light' | 'dark'

export type EditorEngine = 'milkdown' | 'cm6'

export interface BridgeFeatures {
  wikilink: boolean
  mermaid: boolean
  /** 编辑器内核。缺省走 Milkdown Crepe（wiki），笔记页传 'cm6'。 */
  engine?: EditorEngine
}

// Host → Editor

export interface InitPayload {
  markdown: string
  theme: Theme
  readOnly: boolean
  locale: string
  features: BridgeFeatures
}

export interface SetDocPayload {
  markdown: string
  revision: number
  preserveSelection: boolean
}

export interface SetOptionsPayload {
  theme?: Theme
  readOnly?: boolean
}

export type CommandName =
  | 'focus'
  | 'undo'
  | 'redo'
  | 'insertText'
  | 'replaceSelection'

export interface ReplaceSelectionArgs {
  /** Replacement markdown (already stripped of outer fence by the host). */
  markdown: string
  /** Selection range captured when the edit was generated; TOCTOU guard. */
  from: number
  to: number
  /** The exact text that was in [from, to] at capture time. If the doc
   *  changed since (user typed while the LLM was running), the editor
   *  rejects the replace and the host surfaces "selection changed". */
  expectedText: string
}

export interface CommandPayload {
  name: CommandName
  args?: Record<string, unknown>
}

export interface WikilinkQueryReplyPayload {
  items: Array<{ slug: string; title: string }>
}

// Editor → Host

export interface ReadyPayload {
  editorVersion: string
  protocolV: number
}

export interface DocChangedPayload {
  markdown: string
  revision: number
}

export interface WikilinkQueryPayload {
  prefix: string
}

export interface NavigatePayload {
  kind: 'wikilink' | 'external'
  slug?: string
  url?: string
}

export interface LogPayload {
  level: 'debug' | 'info' | 'warn' | 'error'
  msg: string
  stack?: string
}

/** selection-edit (S3 P1-6): editor pushes the current ProseMirror/CM6
 *  selection whenever it changes, so the host can show a follow overlay
 *  (Ask/Edit) anchored at `coords` (viewport-relative; host adds the
 *  WebView's screen origin). `empty` = collapsed caret → host hides. */
export interface SelectionChangedPayload {
  from: number
  to: number
  text: string
  empty: boolean
  coords: { left: number; top: number; right: number; bottom: number }
}

// Discriminated union — exhaustive for safety.

export type HostToEditorMessage =
  | Msg<'init', InitPayload>
  | Msg<'setDoc', SetDocPayload>
  | Msg<'setOptions', SetOptionsPayload>
  | Msg<'command', CommandPayload>
  | Msg<'wikilinkQuery.reply', WikilinkQueryReplyPayload>

export type EditorToHostMessage =
  | Msg<'ready', ReadyPayload>
  | Msg<'docChanged', DocChangedPayload>
  | Msg<'wikilinkQuery', WikilinkQueryPayload>
  | Msg<'navigate', NavigatePayload>
  | Msg<'log', LogPayload>
  | Msg<'selectionChanged', SelectionChangedPayload>

export interface Msg<T extends string, P> {
  type: T
  v: number
  id?: string
  payload: P
}

export type AnyMessage = HostToEditorMessage | EditorToHostMessage

export function makeMessage<T extends string, P>(
  type: T,
  payload: P,
  id?: string,
): Msg<T, P> {
  const msg: Msg<T, P> = { type, v: PROTOCOL_VERSION, payload }
  if (id !== undefined) msg.id = id
  return msg
}

export function isMessage(value: unknown): value is AnyMessage {
  if (!value || typeof value !== 'object') return false
  const obj = value as Record<string, unknown>
  return (
    typeof obj.type === 'string' &&
    typeof obj.v === 'number' &&
    typeof obj.payload === 'object' &&
    obj.payload !== null
  )
}
