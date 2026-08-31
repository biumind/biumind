// PDF 文本层抽取：pdfjs-dist legacy build（纯 JS，无 WASM）。
// worker 走 vite `?url` 显式产物文件，GlobalWorkerOptions.workerSrc 指向
// 打包后的 pdf.worker —— InAppLocalhostServer / 同源 iframe 都能按相对
// 路径加载。vitest 下 ?url 返回值不可加载，测试在 setup 里自行覆盖
// workerSrc 为 node_modules 真实路径（见 tests/pdf.spec.ts）。

import { getDocument, GlobalWorkerOptions } from 'pdfjs-dist/legacy/build/pdf.mjs'
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url'

import { DocprocError } from '../bridge/protocol'
import { throwIfCancelled, type Parser } from './types'

GlobalWorkerOptions.workerSrc = workerUrl

export const parsePdf: Parser = async (input) => {
  input.onProgress?.('load', 10)
  throwIfCancelled(input.isCancelled)

  const task = getDocument({
    data: input.data,
    isEvalSupported: false,
    disableFontFace: true,
  })

  let pdf: Awaited<typeof task.promise>
  try {
    pdf = await task.promise
  } catch (err) {
    throw mapLoadError(err)
  }
  input.onProgress?.('load', 40)

  const warnings: string[] = []
  const parts: string[] = []
  try {
    for (let pageNo = 1; pageNo <= pdf.numPages; pageNo++) {
      throwIfCancelled(input.isCancelled)
      const page = await pdf.getPage(pageNo)
      const content = await page.getTextContent()
      const pageText = content.items
        .map((item) => ('str' in item ? item.str : ''))
        .join(' ')
        .replace(/\s+/g, ' ')
        .trim()
      if (pageText.length > 0) parts.push(pageText)
      input.onProgress?.(
        'extract',
        40 + Math.round((pageNo / pdf.numPages) * 60),
      )
    }
  } catch (err) {
    if (err instanceof Error && err.name === 'CancelledError') throw err
    throw mapLoadError(err)
  } finally {
    void pdf.destroy()
  }

  const text = parts.join('\n\n')
  if (text.trim().length === 0) {
    // 纯扫描件没有文本层 —— P1 不做 OCR（决策③），交由 host 走云端。
    warnings.push('no-text-layer: 未抽取到文本层（可能是扫描件）')
  }
  return { text, format: 'pdf', pageCount: pdf.numPages, warnings }
}

function mapLoadError(err: unknown): DocprocError {
  const name = err instanceof Error ? err.name : ''
  const message = err instanceof Error ? err.message : String(err)
  if (name === 'PasswordException') {
    return new DocprocError('encrypted', 'PDF 已加密，无法本机解析')
  }
  return new DocprocError('corrupt', `PDF 解析失败: ${message}`)
}
