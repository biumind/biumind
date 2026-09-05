// zip 类容器（DOCX）展开预检 —— 手解 central directory（只读目录头部
// 声明值，不解压任何条目）。mammoth 内部经 jszip 全量展开且无上限，
// 恶意构造的 zip-bomb 会把 WebView 内存撑爆，因此先在这里拦。
// 阈值对齐 reference/llm_wiki src-tauri/src/commands/ebook.rs 与
// workers/wiki-parse parser.py：条目 ≤1 万、总展开 ≤512MB、
// 单条目 ≤64MB、压缩比 ≤200。源文件 >50MB 由 host 侧在进 bundle 前拒绝。

import { DocprocError } from '../bridge/protocol'

export const ZIP_MAX_ENTRIES = 10_000
export const ZIP_MAX_TOTAL_UNCOMPRESSED = 512 * 1024 * 1024
export const ZIP_MAX_ENTRY_UNCOMPRESSED = 64 * 1024 * 1024
export const ZIP_MAX_COMPRESS_RATIO = 200

const EOCD_SIG = 0x06054b50
const CEN_SIG = 0x02014b50
const EOCD_MIN_LEN = 22
const EOCD_MAX_COMMENT = 65_535

function bomb(message: string): never {
  throw new DocprocError('corrupt', message)
}

/**
 * zip-bomb 预检。非 zip / 找不到 EOCD / zip64 时直接放行（交给下层
 * 解析器自己报错；zip64 罕见且源文件已被 host 限在 50MB 内）。
 */
export function checkZipBomb(data: Uint8Array, format: string): void {
  if (data.length < EOCD_MIN_LEN) return
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength)

  // 从尾部倒扫 EOCD 签名
  let eocd = -1
  const scanStart = Math.max(0, data.length - EOCD_MIN_LEN - EOCD_MAX_COMMENT)
  for (let i = data.length - EOCD_MIN_LEN; i >= scanStart; i--) {
    if (view.getUint32(i, true) === EOCD_SIG) {
      eocd = i
      break
    }
  }
  if (eocd === -1) return

  const entryCount = view.getUint16(eocd + 10, true)
  const cdOffset = view.getUint32(eocd + 16, true)
  if (entryCount === 0xffff || cdOffset === 0xffffffff) return // zip64，放行

  if (entryCount > ZIP_MAX_ENTRIES) {
    bomb(`${format} 条目数 ${entryCount} 超过上限 ${ZIP_MAX_ENTRIES}（疑似 zip bomb）`)
  }

  let total = 0
  let totalCompressed = 0
  let pos = cdOffset
  for (let n = 0; n < entryCount; n++) {
    if (pos + 46 > data.length || view.getUint32(pos, true) !== CEN_SIG) {
      return // 目录损坏 —— 交给下层解析器报错
    }
    const compressed = view.getUint32(pos + 20, true)
    const uncompressed = view.getUint32(pos + 24, true)
    if (uncompressed === 0xffffffff || compressed === 0xffffffff) return // zip64
    const nameLen = view.getUint16(pos + 28, true)
    const extraLen = view.getUint16(pos + 30, true)
    const commentLen = view.getUint16(pos + 32, true)

    if (uncompressed > ZIP_MAX_ENTRY_UNCOMPRESSED) {
      bomb(
        `${format} 条目声明展开大小 ${uncompressed} 超过单条目上限 ` +
          `${ZIP_MAX_ENTRY_UNCOMPRESSED}（疑似 zip bomb）`,
      )
    }
    if (compressed > 0 && uncompressed > ZIP_MAX_COMPRESS_RATIO * compressed) {
      bomb(
        `${format} 条目压缩比 ${Math.floor(uncompressed / compressed)} 超过上限 ` +
          `${ZIP_MAX_COMPRESS_RATIO}（疑似 zip bomb）`,
      )
    }
    total += uncompressed
    totalCompressed += compressed
    pos += 46 + nameLen + extraLen + commentLen
  }

  if (total > ZIP_MAX_TOTAL_UNCOMPRESSED) {
    bomb(
      `${format} 声明总展开大小 ${total} 超过上限 ` +
        `${ZIP_MAX_TOTAL_UNCOMPRESSED}（疑似 zip bomb）`,
    )
  }
  if (totalCompressed > 0 && total > ZIP_MAX_COMPRESS_RATIO * totalCompressed) {
    bomb(
      `${format} 总压缩比 ${Math.floor(total / totalCompressed)} 超过上限 ` +
        `${ZIP_MAX_COMPRESS_RATIO}（疑似 zip bomb）`,
    )
  }
}
