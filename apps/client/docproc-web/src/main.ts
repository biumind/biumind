// Docproc bundle entry：无 UI 纯计算。加载完成发 `ready`；收到 `parse`
// 后分派解析器，progress / result / error 全部经 bridge 回传 host。

import { BridgeClient } from './bridge/client'
import {
  DocprocError,
  PARSER_VERSION,
  type ParsePayload,
} from './bridge/protocol'
import { parseDocument } from './parsers'
import { CancelledError } from './parsers/types'

const bridge = new BridgeClient()

/** 在途解析的 cancel 标记：host 发 cancel 后对应 id 置位。 */
const cancelled = new Set<string>()

bridge.start({
  onPing: () => {
    // WebView 就绪握手：host 可能晚于 bundle 加载 attach，重发 ready。
    bridge.sendReady()
  },
  onParse: (payload) => {
    void handleParse(payload)
  },
  onCancel: (payload) => {
    cancelled.add(payload.id)
  },
})

bridge.sendReady()

async function handleParse(payload: ParsePayload): Promise<void> {
  const { id } = payload
  try {
    const data = decodeBase64(payload.dataBase64)
    const output = await parseDocument({
      fileName: payload.fileName,
      mimeHint: payload.mimeHint,
      data,
      onProgress: (phase, percent) => {
        if (!cancelled.has(id)) bridge.sendProgress(id, phase, percent)
      },
      isCancelled: () => cancelled.has(id),
    })
    if (cancelled.has(id)) return
    bridge.sendResult(id, {
      text: output.text,
      format: output.format,
      pageCount: output.pageCount,
      parserVersion: PARSER_VERSION,
      warnings: output.warnings,
    })
  } catch (err) {
    cancelled.delete(id)
    if (err instanceof CancelledError) return
    if (err instanceof DocprocError) {
      bridge.sendError(id, err.code, err.message, err.retryable)
      return
    }
    // 非预期异常：归为 corrupt（文件/解析器问题），让 host 落云端兜底。
    bridge.sendError(
      id,
      'corrupt',
      err instanceof Error ? err.message : String(err),
      false,
    )
  } finally {
    cancelled.delete(id)
  }
}

function decodeBase64(base64: string): Uint8Array {
  // atob 单次调用参数过大有栈上限，按 32KB 分片解码。
  const out = new Uint8Array(Math.floor((base64.length * 3) / 4))
  let offset = 0
  const CHUNK = 32 * 1024
  for (let i = 0; i < base64.length; i += CHUNK) {
    const bin = atob(base64.slice(i, i + CHUNK))
    for (let j = 0; j < bin.length; j++) {
      out[offset++] = bin.charCodeAt(j)
    }
  }
  return out.subarray(0, offset)
}
