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

export interface BridgeFeatures {
  wikilink: boolean
  mermaid: boolean
  /**
   * 右键菜单载体：custom = bundle 内自绘 HTML 菜单（默认，桌面/Web）；
   * native = 平台系统菜单（iOS/Android 移动端，长按 callout 是强平台习惯）。
   */
  contextMenu?: 'custom' | 'native'
  /** host 已接 AI 动作（选区询问/编辑 overlay）；缺省 false，菜单不渲染 AI 组 */
  aiActions?: boolean
  /** host 已接图片上传链路（选图 → presign 直传，notes 专属能力）；
   *  缺省 false，图片菜单不渲染「替换图片…」 */
  imageUpload?: boolean
  /** 平台标记（M1 移动端）：bundle 据此在 <html data-platform> 标注，
   *  分流 CSS（iOS callout 抑制）、入场动画与移动端裁剪；host 从
   *  PlatformCaps 填。缺省 = 非移动端（不出选区浮动工具条，行为不变）。 */
  platform?: 'ios' | 'android' | 'macos' | 'web'
}

// Host → Editor

export interface InitPayload {
  markdown: string
  theme: Theme
  readOnly: boolean
  locale: string
  features: BridgeFeatures
  /** 文档纪元初值：编辑器以此初始化 docEpoch，与 host 的 setDoc 计数对齐。 */
  epoch?: number
}

export interface SetDocPayload {
  markdown: string
  revision: number
  preserveSelection: boolean
}

export interface SetOptionsPayload {
  theme?: Theme
  readOnly?: boolean
  /** 运行时切换 UI 语言（菜单等现构建的文案即刻生效；crepe 自身文案维持 init） */
  locale?: string
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

export interface PresignGetReplyPayload {
  /** 可匿名 GET 的临时 URL；空串 = 换取失败（图片裂开但编辑器不崩）。 */
  url: string
}

/** clipboardRead 的应答：null = 剪贴板为空 / 读取失败（粘贴项置灰）。 */
export interface ClipboardReadReplyPayload {
  text: string | null
}

// Editor → Host

export interface ReadyPayload {
  editorVersion: string
  protocolV: number
}

export interface DocChangedPayload {
  markdown: string
  revision: number
  /**
   * 文档纪元：本条变更发生时，编辑器最近一次 setDoc 的 revision
   * （切换笔记 = host 发新 setDoc = 新纪元）。host 据此丢弃上一篇
   * 笔记迟到/在途的 docChanged（防抖队列、异步 postMessage），
   * 防止旧内容存进新笔记。缺省 = 旧版编辑器，host 退回 revision 守卫。
   */
  epoch?: number
}

export interface WikilinkQueryPayload {
  prefix: string
}

/** 渲染时换取附件临时 URL：正文里只存 biu-file://<uuid>，<img> 展示前
 *  经此请求向 host 换 presigned GET URL（15 分钟 TTL，过期可重换）。 */
export interface PresignGetPayload {
  fileId: string
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
  /** 选区前/后各约 1200 字符纯文本上下文（doc.textBetween 窗口），
   *  host 原样传给 selection-edit 接口做 BEFORE/AFTER 段。 */
  before: string
  after: string
  empty: boolean
  coords: { left: number; top: number; right: number; bottom: number }
}

/** 复制/剪切落系统剪贴板（execCommand 在 WKWebView 常失败，走 host）。
 *  text = markdown 序列化；html（P2 新增，可选）= 同一选区的 HTML 序列化，
 *  host 在 macOS 上写 NSPasteboard 双格式，其他平台忽略该字段只写 text。
 *  imageBase64/imageMime（可选）= 单图复制时的图片二进制（PNG base64），
 *  host 有能力就写系统剪贴板图片格式（macOS NSPasteboard.png），
 *  无能力的平台忽略，只写 text/html。 */
export interface ClipboardWritePayload {
  text: string
  html?: string
  imageBase64?: string
  imageMime?: string
}

/** 读系统剪贴板（request/reply，应答见 ClipboardReadReplyPayload）。 */
export type ClipboardReadPayload = Record<string, never>

/** 右键菜单 AI 动作（协议 P1 预留；菜单组 P2 才渲染，见 features.aiActions）。 */
export interface AiActionPayload {
  action: 'ask' | 'edit'
  /** 打开菜单时快照的选区 */
  from: number
  to: number
  text: string
}

/** 图片菜单「替换图片…」（P2）：请 host 走既有上传链路（选图 → presign
 *  直传），应答见 ImageUploadReplyPayload。上传能力为 notes 专属
 *  （features.imageUpload 声明），wiki 等无链路场景菜单项不渲染。 */
export type ImageUploadPayload = Record<string, never>

/** imageUpload 的应答：uri = biu-file://<uuid> 规范 URI；null = 用户取消
 *  / 上传失败（编辑器不改动图片节点）。 */
export interface ImageUploadReplyPayload {
  uri: string | null
}

/** 粘贴/拖入图片上传（onUpload 链路）：编辑器手里已有 File（不弹选图器），
 *  读成 base64 发给 host；host 走与「插入图片」同一条 presign 直传链路。
 *  能力声明同 features.imageUpload（notes 专属）。 */
export interface ImageFileUploadPayload {
  name: string
  mime: string
  dataBase64: string
}

/** imageFileUpload 的应答：uri = biu-file://<uuid> 规范 URI；null = 上传
 *  失败 / host 未接线（编辑器侧 onUpload 抛错，图片节点不插入——绝不回落
 *  blob URL，防止不可持久化的引用落库）。 */
export interface ImageFileUploadReplyPayload {
  uri: string | null
}

// Discriminated union — exhaustive for safety.

export type HostToEditorMessage =
  | Msg<'init', InitPayload>
  | Msg<'setDoc', SetDocPayload>
  | Msg<'setOptions', SetOptionsPayload>
  | Msg<'command', CommandPayload>
  | Msg<'wikilinkQuery.reply', WikilinkQueryReplyPayload>
  | Msg<'presignGet.reply', PresignGetReplyPayload>
  | Msg<'clipboardRead.reply', ClipboardReadReplyPayload>
  | Msg<'imageUpload.reply', ImageUploadReplyPayload>
  | Msg<'imageFileUpload.reply', ImageFileUploadReplyPayload>

export type EditorToHostMessage =
  | Msg<'ready', ReadyPayload>
  | Msg<'docChanged', DocChangedPayload>
  | Msg<'wikilinkQuery', WikilinkQueryPayload>
  | Msg<'presignGet', PresignGetPayload>
  | Msg<'navigate', NavigatePayload>
  | Msg<'log', LogPayload>
  | Msg<'selectionChanged', SelectionChangedPayload>
  | Msg<'clipboardWrite', ClipboardWritePayload>
  | Msg<'clipboardRead', ClipboardReadPayload>
  | Msg<'aiAction', AiActionPayload>
  | Msg<'imageUpload', ImageUploadPayload>
  | Msg<'imageFileUpload', ImageFileUploadPayload>

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
