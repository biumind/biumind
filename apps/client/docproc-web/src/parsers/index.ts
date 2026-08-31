// 格式分派入口：detectFormat → 对应 parser；不认识 → unsupported。

import { DocprocError } from '../bridge/protocol'
import { detectFormat } from './detect'
import { parseDocx } from './docx'
import { parseHtml } from './html'
import { parsePdf } from './pdf'
import { parseText } from './text'
import type { ParseInput, ParseOutput, Parser } from './types'

const PARSERS: Record<string, Parser> = {
  pdf: parsePdf,
  docx: parseDocx,
  html: parseHtml,
  md: parseText,
  txt: parseText,
}

export async function parseDocument(input: ParseInput): Promise<ParseOutput> {
  const format = detectFormat(input.fileName, input.mimeHint)
  if (format === null) {
    throw new DocprocError(
      'unsupported',
      `不支持的格式: ${input.fileName}` +
        (input.mimeHint ? ` (${input.mimeHint})` : ''),
    )
  }
  const output = await PARSERS[format](input)
  return { ...output, format }
}
