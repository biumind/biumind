// zip-bomb 预检：条目数 / 声明展开大小 / 压缩比防线。大体积用例靠篡改
// central directory 的 uncompressed size 字段触发，不真造 512MB 数据。

import JSZip from 'jszip'
import { describe, expect, it } from 'vitest'

import { DocprocError } from '../src/bridge/protocol'
import { parseDocument } from '../src/parsers'
import {
  checkZipBomb,
  ZIP_MAX_ENTRIES,
  ZIP_MAX_ENTRY_UNCOMPRESSED,
} from '../src/parsers/zipguard'

async function makeManyEntryZip(n: number): Promise<Uint8Array> {
  const zip = new JSZip()
  for (let i = 0; i < n; i++) zip.file(`e${i}.txt`, '')
  return zip.generateAsync({ type: 'uint8array' })
}

async function makeFakeSizeZip(
  declaredSize: number,
  entries = 1,
  declaredCompressed?: number,
): Promise<Uint8Array> {
  const zip = new JSZip()
  for (let i = 0; i < entries; i++) zip.file(`f${i}.xml`, 'tiny')
  const data = await zip.generateAsync({ type: 'uint8array' })
  // central directory record：签名 PK\x01\x02，
  // compressed size 在 +20，uncompressed size 在 +24
  const sig = [0x50, 0x4b, 0x01, 0x02]
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength)
  let found = 0
  for (let i = 0; i < data.length - 4; i++) {
    if (sig.every((b, j) => data[i + j] === b)) {
      if (declaredCompressed !== undefined) {
        view.setUint32(i + 20, declaredCompressed, true)
      }
      view.setUint32(i + 24, declaredSize, true)
      found++
    }
  }
  if (found !== entries) throw new Error(`patched ${found}/${entries} records`)
  return data
}

describe('checkZipBomb', () => {
  it('条目数超上限拒绝', async () => {
    const data = await makeManyEntryZip(ZIP_MAX_ENTRIES + 1)
    expect(() => checkZipBomb(data, 'DOCX')).toThrowError(/疑似 zip bomb/)
  })

  it('条目数贴上限放行', async () => {
    const data = await makeManyEntryZip(ZIP_MAX_ENTRIES)
    expect(() => checkZipBomb(data, 'DOCX')).not.toThrow()
  })

  it('声明总展开大小超 512MB 拒绝', async () => {
    // 9 个条目各声明 60MB（单条目 < 64MB 上限）→ 合计 540MB 超总上限；
    // compressed 同步篡改为 1MB（压缩比 60 < 200），确认总展开防线
    // 独立于单条目 / 压缩比防线生效
    const data = await makeFakeSizeZip(60 * 1024 * 1024, 9, 1024 * 1024)
    expect(() => checkZipBomb(data, 'DOCX')).toThrowError(/总展开大小/)
  })

  it('单条目声明大小超 64MB 拒绝', async () => {
    const data = await makeFakeSizeZip(ZIP_MAX_ENTRY_UNCOMPRESSED + 1)
    expect(() => checkZipBomb(data, 'DOCX')).toThrowError(/单条目/)
  })

  it('压缩比超 200 拒绝', async () => {
    // 'tiny' 压后 ~6 字节；声明 6000 字节 → 压缩比 ~1000（同时低于单条目上限）
    const data = await makeFakeSizeZip(6000)
    expect(() => checkZipBomb(data, 'DOCX')).toThrowError(/压缩比/)
  })

  it('非 zip 字节放行（交给下层解析器报错）', () => {
    expect(() =>
      checkZipBomb(new TextEncoder().encode('not a zip at all'), 'DOCX'),
    ).not.toThrow()
  })
})

describe('parseDocument: docx zip-bomb 集成', () => {
  it('超限 docx 报 corrupt 而非撑爆内存', async () => {
    const data = await makeFakeSizeZip(ZIP_MAX_ENTRY_UNCOMPRESSED + 1)
    const err = await parseDocument({ fileName: 'bomb.docx', data }).catch(
      (e: unknown) => e,
    )
    expect(err).toBeInstanceOf(DocprocError)
    expect((err as DocprocError).code).toBe('corrupt')
    expect((err as DocprocError).message).toContain('zip bomb')
  })
})
