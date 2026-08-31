// Bridge protocol v1 between the BiuMind Flutter host and the embedded
// docproc (document parsing) bundle. Mirror of
// client/lib/core/docproc/docproc_bridge_protocol.dart.
// Both definitions MUST evolve in lockstep.
//
// Wire format: every message is `{ type, v, id?, payload }`（同 editor bridge
// 形态，但两个 bundle 相互独立，不共享代码）。
//   `type`     — one of the message kinds below.
//   `v`        — protocol version, currently 1. A mismatch is fatal.
//   `id`       — correlation id set on `parse` and echoed on progress /
//                result / error replies.
//   `payload`  — type-specific body.

export const PROTOCOL_VERSION = 1 as const

/** bundle 版本，随 ready / result 上报，host 落 parse_meta.version。 */
export const PARSER_VERSION = 'docproc-web@0.1.0'

export const SUPPORTED_FORMATS = ['pdf', 'docx', 'html', 'md', 'txt'] as const
export type DocFormat = (typeof SUPPORTED_FORMATS)[number]

export type DocprocErrorCode = 'unsupported' | 'encrypted' | 'corrupt' | 'oom'

// Host → Docproc

export interface ParsePayload {
  /** 解析任务 id；progress / result / error 经消息级 id 回传同一个。 */
  id: string
  fileName: string
  mimeHint?: string
  /** P1 整文件 base64；>50MB host 侧已拒绝，不会到达这里。 */
  dataBase64: string
}

export type PingPayload = Record<string, never>

export interface CancelPayload {
  id: string
}

// Docproc → Host

export interface ReadyPayload {
  version: string
  formats: DocFormat[]
}

export interface ProgressPayload {
  /** load = 字节解码 / 文档加载；extract = 正文抽取中。 */
  phase: 'load' | 'extract'
  percent: number
}

export interface ResultPayload {
  text: string
  format: DocFormat
  pageCount?: number
  parserVersion: string
  warnings: string[]
}

export interface ErrorPayload {
  code: DocprocErrorCode
  message: string
  retryable: boolean
}

// Discriminated union — exhaustive for safety.

export type HostToDocprocMessage =
  | Msg<'ping', PingPayload>
  | Msg<'parse', ParsePayload>
  | Msg<'cancel', CancelPayload>

export type DocprocToHostMessage =
  | Msg<'ready', ReadyPayload>
  | Msg<'progress', ProgressPayload>
  | Msg<'result', ResultPayload>
  | Msg<'error', ErrorPayload>

export interface Msg<T extends string, P> {
  type: T
  v: number
  id?: string
  payload: P
}

export type AnyMessage = HostToDocprocMessage | DocprocToHostMessage

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

/** parser 侧抛出的结构化错误，main.ts 映射为 `error` 消息。 */
export class DocprocError extends Error {
  readonly code: DocprocErrorCode
  readonly retryable: boolean

  constructor(code: DocprocErrorCode, message: string, retryable = false) {
    super(message)
    this.name = 'DocprocError'
    this.code = code
    this.retryable = retryable
  }
}
