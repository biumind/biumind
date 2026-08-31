// MD / TXT 直读：utf-8 解码即全文，无结构转换。

import type { Parser } from './types'

export const parseText: Parser = async (input) => {
  input.onProgress?.('load', 50)
  const text = new TextDecoder('utf-8', { fatal: false }).decode(input.data)
  input.onProgress?.('extract', 100)
  return {
    text,
    format: input.fileName.toLowerCase().endsWith('.txt') ||
      input.mimeHint === 'text/plain'
      ? 'txt'
      : 'md',
    warnings: [],
  }
}
