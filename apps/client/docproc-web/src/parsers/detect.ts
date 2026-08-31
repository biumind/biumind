// 格式分派：优先扩展名，mimeHint 兜底。两者都不认识返回 null（→ unsupported）。

import type { DocFormat } from '../bridge/protocol'

const EXT_TO_FORMAT: Record<string, DocFormat> = {
  pdf: 'pdf',
  docx: 'docx',
  html: 'html',
  htm: 'html',
  md: 'md',
  markdown: 'md',
  txt: 'txt',
  text: 'txt',
}

const MIME_TO_FORMAT: Record<string, DocFormat> = {
  'application/pdf': 'pdf',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document':
    'docx',
  'text/html': 'html',
  'text/markdown': 'md',
  'text/x-markdown': 'md',
  'text/plain': 'txt',
}

export function detectFormat(fileName: string, mimeHint?: string): DocFormat | null {
  const dot = fileName.lastIndexOf('.')
  if (dot >= 0 && dot < fileName.length - 1) {
    const ext = fileName.slice(dot + 1).toLowerCase()
    const byExt = EXT_TO_FORMAT[ext]
    if (byExt) return byExt
  }
  if (mimeHint) {
    const mime = mimeHint.split(';')[0].trim().toLowerCase()
    const byMime = MIME_TO_FORMAT[mime]
    if (byMime) return byMime
  }
  return null
}
