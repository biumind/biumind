// MD / TXT 直读：utf-8 解码 + 格式标注；不支持格式经 parseDocument 报 unsupported。

import { describe, expect, it } from 'vitest'

import { DocprocError } from '../src/bridge/protocol'
import { parseDocument } from '../src/parsers'

const enc = (s: string) => new TextEncoder().encode(s)

describe('parseDocument: text', () => {
  it('md 直读，内容原样', async () => {
    const out = await parseDocument({
      fileName: 'README.md',
      data: enc('# 标题\n\n正文 **加粗**\n'),
    })
    expect(out.format).toBe('md')
    expect(out.text).toBe('# 标题\n\n正文 **加粗**\n')
    expect(out.warnings).toEqual([])
  })

  it('txt 直读（含中文）', async () => {
    const out = await parseDocument({
      fileName: '笔记.txt',
      data: enc('第一行\n第二行 中文内容'),
    })
    expect(out.format).toBe('txt')
    expect(out.text).toContain('中文内容')
  })

  it('无扩展名按 mimeHint=text/plain 走 txt', async () => {
    const out = await parseDocument({
      fileName: 'clipboard',
      mimeHint: 'text/plain',
      data: enc('plain'),
    })
    expect(out.format).toBe('txt')
    expect(out.text).toBe('plain')
  })

  it('不支持格式抛 unsupported', async () => {
    const err = await parseDocument({
      fileName: '表.xlsx',
      data: enc('PK'),
    }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DocprocError)
    expect((err as DocprocError).code).toBe('unsupported')
    expect((err as DocprocError).retryable).toBe(false)
  })
})
