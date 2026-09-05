// DOCX：mammoth convertToHtml（保留标题 / 列表 / 表格结构）→ turndown
// 转 Markdown。优先结构保留方案；mammoth 的转换消息透传为 warnings。

import mammoth from 'mammoth'

import { DocprocError } from '../bridge/protocol'
import { createTurndown } from './turndown'
import { throwIfCancelled, type Parser } from './types'
import { checkZipBomb } from './zipguard'

export const parseDocx: Parser = async (input) => {
  input.onProgress?.('load', 20)
  throwIfCancelled(input.isCancelled)

  // mammoth（经 jszip）全量展开且无上限，先做 zip-bomb 预检。
  checkZipBomb(input.data, 'DOCX')

  // mammoth 要求 ArrayBuffer；input.data 可能是大 buffer 的视图，切片复制。
  const arrayBuffer = input.data.slice().buffer as ArrayBuffer
  // 产物（vite 浏览器构建）走 mammoth.browser.js，认 `arrayBuffer`；
  // vitest 走 node 构建（lib/），只认 `buffer` —— 双键兼容两环境。
  const mammothInput: Record<string, unknown> = { arrayBuffer }
  if (typeof Buffer !== 'undefined') {
    mammothInput.buffer = Buffer.from(arrayBuffer)
  }
  let result: { value: string; messages: Array<{ type: string; message: string }> }
  try {
    result = await mammoth.convertToHtml(mammothInput as { arrayBuffer: ArrayBuffer })
  } catch (err) {
    throw new DocprocError(
      'corrupt',
      `DOCX 解析失败: ${err instanceof Error ? err.message : String(err)}`,
    )
  }
  input.onProgress?.('extract', 60)
  throwIfCancelled(input.isCancelled)

  if (result.value.trim().length === 0) {
    throw new DocprocError('corrupt', 'DOCX 文档没有可抽取的正文内容')
  }
  const text = createTurndown().turndown(result.value)
  const warnings = result.messages.map((m) => `${m.type}: ${m.message}`)
  input.onProgress?.('extract', 100)
  return { text, format: 'docx', warnings }
}
