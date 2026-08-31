// DOCX：用 JSZip 现场打一个最小合法 docx，验证 mammoth → turndown 链路。

import JSZip from 'jszip'
import { describe, expect, it } from 'vitest'

import { DocprocError } from '../src/bridge/protocol'
import { parseDocument } from '../src/parsers'

async function makeMinimalDocx(): Promise<Uint8Array> {
  const zip = new JSZip()
  zip.file(
    '[Content_Types].xml',
    `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`,
  )
  zip.folder('_rels')?.file(
    '.rels',
    `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
  )
  zip.folder('word')?.file(
    'document.xml',
    `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>季度报告</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>正文内容 BiuMind docx fixture</w:t></w:r></w:p>
  </w:body>
</w:document>`,
  )
  return zip.generateAsync({ type: 'uint8array' })
}

describe('parseDocument: docx', () => {
  it('mammoth → turndown：标题结构保留', async () => {
    const out = await parseDocument({
      fileName: 'report.docx',
      data: await makeMinimalDocx(),
    })
    expect(out.format).toBe('docx')
    expect(out.text).toContain('# 季度报告')
    expect(out.text).toContain('正文内容 BiuMind docx fixture')
  })

  it('损坏文件报 corrupt', async () => {
    const err = await parseDocument({
      fileName: 'broken.docx',
      data: new TextEncoder().encode('this is not a zip at all'),
    }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DocprocError)
    expect((err as DocprocError).code).toBe('corrupt')
  })
})
