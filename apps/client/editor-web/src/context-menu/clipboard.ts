// 剪贴板抽象：execCommand('paste') 在 WKWebView 常失败、
// navigator.clipboard.readText 权限不稳定 → native（inappwebview）走 bridge
// 落到 Flutter Clipboard；web/standalone 走 navigator.clipboard。
// P1 只写纯文本（text+html 双格式列 P2，Flutter Clipboard 本身只支持纯文本，
// html 需 macOS method channel 扩展）。

import type { BridgeClient } from '../bridge/client'

export interface ClipboardData {
  text: string
  /** 同一选区的 HTML 序列化（P2 双格式复制）；web 实现忽略，native 经
   *  bridge 带给 host（macOS 写 NSPasteboard 双格式，其余平台回退纯文本）。 */
  html?: string
  /** 单图复制的图片二进制（PNG base64）：web 写 ClipboardItem image/png，
   *  native 经 bridge 带给 host（macOS NSPasteboard.png），无能力的
   *  环境/平台忽略该字段，只写 text/html。 */
  image?: { base64: string; mime: string }
}

export interface ClipboardBackend {
  write(data: ClipboardData): Promise<void>
  /** null = 剪贴板为空 / 读取失败（调用方把粘贴项置灰，不崩） */
  read(): Promise<ClipboardData | null>
}

/** native 实现：经 bridge 消息落到 host 的 Flutter Clipboard。 */
export function createNativeClipboard(bridge: BridgeClient): ClipboardBackend {
  return {
    async write(data) {
      const payload: {
        text: string
        html?: string
        imageBase64?: string
        imageMime?: string
      } = { text: data.text }
      if (data.html) payload.html = data.html
      if (data.image) {
        payload.imageBase64 = data.image.base64
        payload.imageMime = data.image.mime
      }
      bridge.sendClipboardWrite(payload)
    },
    async read() {
      // 5s 超时回空对象（host 未实现 clipboardRead 的老版本）→ text undefined → null
      const reply = await bridge.requestClipboardRead()
      if (typeof reply.text !== 'string' || reply.text.length === 0) return null
      return { text: reply.text }
    },
  }
}

/** base64 → bytes（ClipboardItem 图片 blob 用）。 */
function base64ToBytes(base64: string): Uint8Array {
  const bin = atob(base64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes
}

/** web/standalone 实现：navigator.clipboard。readText 被拒绝时降级回 null +
 *  sendLog 提示（localhost/127.0.0.1 是 secure context，一般可用）。
 *  写入走多格式（ClipboardItem）：text/plain 打底，有 html 加 text/html、
 *  有 image 加图片 MIME —— 粘到外部应用（飞书/Word/微信）保留格式/出图；
 *  Safari 对 ClipboardItem 的 MIME 支持比 Chrome 窄，不支持/被拒时
 *  回退 writeText 纯文本，不崩。iframe 的 allow="clipboard-read;
 *  clipboard-write" 已声明（editor_web_view.dart），权限通路现成。 */
export function createWebClipboard(
  log: (msg: string) => void,
): ClipboardBackend {
  return {
    async write(data) {
      if (
        (data.html || data.image) &&
        typeof ClipboardItem !== 'undefined' &&
        typeof navigator.clipboard.write === 'function'
      ) {
        const items: Record<string, Blob> = {
          'text/plain': new Blob([data.text], { type: 'text/plain' }),
        }
        if (data.html) {
          items['text/html'] = new Blob([data.html], { type: 'text/html' })
        }
        if (data.image) {
          items[data.image.mime] = new Blob([base64ToBytes(data.image.base64).buffer as ArrayBuffer], {
            type: data.image.mime,
          })
        }
        try {
          const item = new ClipboardItem(items)
          await navigator.clipboard.write([item])
          return
        } catch (error) {
          log(`clipboard multi-format write failed, fallback to plain text: ${String(error)}`)
        }
      }
      await navigator.clipboard.writeText(data.text)
    },
    async read() {
      try {
        const text = await navigator.clipboard.readText()
        if (text.length === 0) return null
        return { text }
      } catch (error) {
        log(`clipboard readText denied/failed: ${String(error)}`)
        return null
      }
    },
  }
}

/** 按运行环境选实现：inappwebview → bridge，否则 navigator.clipboard。 */
export function createClipboard(
  bridge: BridgeClient,
  log: (msg: string) => void,
): ClipboardBackend {
  if (window.flutter_inappwebview) return createNativeClipboard(bridge)
  return createWebClipboard(log)
}
