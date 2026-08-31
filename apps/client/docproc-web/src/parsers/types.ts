// 解析器公共类型：统一返回 {text, format, pageCount?, warnings[]}
// （设计文档 §3.1 输出契约）。

import type { DocFormat } from '../bridge/protocol'

export interface ParseInput {
  fileName: string
  mimeHint?: string
  /** 已解码的文件字节（host 侧 base64 解码后传入）。 */
  data: Uint8Array
  /** 进度回调（phase, percent 0-100）。 */
  onProgress?: (phase: 'load' | 'extract', percent: number) => void
  /** host 发来 cancel 后返回 true；解析器在安全的断点放弃并抛 CancelledError。 */
  isCancelled?: () => boolean
}

export interface ParseOutput {
  text: string
  format: DocFormat
  pageCount?: number
  warnings: string[]
}

export type Parser = (input: ParseInput) => Promise<ParseOutput>

/** cancel 中断标记：不是失败，main.ts 收到后静默丢弃（host 已完成该任务）。 */
export class CancelledError extends Error {
  constructor() {
    super('parse cancelled')
    this.name = 'CancelledError'
  }
}

export function throwIfCancelled(isCancelled?: () => boolean): void {
  if (isCancelled?.()) throw new CancelledError()
}
