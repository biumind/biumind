// 格式分派：扩展名优先，mimeHint 兜底，不认识 → null。

import { describe, expect, it } from 'vitest'

import { detectFormat } from '../src/parsers/detect'

describe('detectFormat', () => {
  it('按扩展名识别', () => {
    expect(detectFormat('a.pdf')).toBe('pdf')
    expect(detectFormat('a.PDF')).toBe('pdf')
    expect(detectFormat('报告.docx')).toBe('docx')
    expect(detectFormat('page.html')).toBe('html')
    expect(detectFormat('page.htm')).toBe('html')
    expect(detectFormat('README.md')).toBe('md')
    expect(detectFormat('notes.markdown')).toBe('md')
    expect(detectFormat('log.txt')).toBe('txt')
  })

  it('扩展名不认识时按 mimeHint 兜底', () => {
    expect(detectFormat('download', 'application/pdf')).toBe('pdf')
    expect(
      detectFormat(
        'doc.bin',
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      ),
    ).toBe('docx')
    expect(detectFormat('x', 'text/html; charset=utf-8')).toBe('html')
    expect(detectFormat('x', 'text/markdown')).toBe('md')
    expect(detectFormat('x', 'text/plain')).toBe('txt')
  })

  it('扩展名优先于冲突的 mimeHint', () => {
    expect(detectFormat('a.md', 'application/octet-stream')).toBe('md')
  })

  it('都不认识返回 null', () => {
    expect(detectFormat('a.xlsx')).toBeNull()
    expect(detectFormat('a.bin', 'application/octet-stream')).toBeNull()
    expect(detectFormat('noext')).toBeNull()
  })
})
