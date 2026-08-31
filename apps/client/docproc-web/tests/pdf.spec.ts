// PDF：现场构造一个最小合法单页 PDF（文本 "Hello BiuMind"），
// 验证 pdfjs 文本层抽取 + 损坏文件报 corrupt。
//
// vitest 下 `?url` 产物 URL 不可动态加载，这里把 workerSrc 覆盖为
// node_modules 里的真实 worker 文件路径（fake worker 直接 import 它）。

import { createRequire } from 'node:module'

import { GlobalWorkerOptions } from 'pdfjs-dist/legacy/build/pdf.mjs'
import { describe, expect, it } from 'vitest'

import { DocprocError } from '../src/bridge/protocol'
import { parseDocument } from '../src/parsers'

const require = createRequire(import.meta.url)
GlobalWorkerOptions.workerSrc = require.resolve(
  'pdfjs-dist/legacy/build/pdf.worker.min.mjs',
)

/** 构造带正确 xref 偏移的最小单页 PDF。 */
function makeMinimalPdf(text: string): Uint8Array {
  const content = `BT /F1 24 Tf 72 720 Td (${text}) Tj ET`
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] ' +
      '/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    `<< /Length ${content.length} >>\nstream\n${content}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]
  let pdf = '%PDF-1.4\n'
  const offsets: number[] = []
  objects.forEach((body, i) => {
    offsets.push(pdf.length)
    pdf += `${i + 1} 0 obj\n${body}\nendobj\n`
  })
  const xrefStart = pdf.length
  pdf += `xref\n0 ${objects.length + 1}\n`
  pdf += '0000000000 65535 f \n'
  for (const off of offsets) {
    pdf += `${String(off).padStart(10, '0')} 00000 n \n`
  }
  pdf +=
    `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\n` +
    `startxref\n${xrefStart}\n%%EOF\n`
  return new TextEncoder().encode(pdf)
}

describe('parseDocument: pdf', () => {
  it('抽出文本层 + pageCount', async () => {
    const out = await parseDocument({
      fileName: 'hello.pdf',
      data: makeMinimalPdf('Hello BiuMind'),
    })
    expect(out.format).toBe('pdf')
    expect(out.pageCount).toBe(1)
    expect(out.text).toContain('Hello BiuMind')
  })

  it('损坏文件报 corrupt', async () => {
    const err = await parseDocument({
      fileName: 'broken.pdf',
      data: new TextEncoder().encode('%PDF-1.4 garbage without structure'),
    }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DocprocError)
    expect((err as DocprocError).code).toBe('corrupt')
  })
})
