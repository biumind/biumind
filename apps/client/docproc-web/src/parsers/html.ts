// HTML：@mozilla/readability 抽正文 → turndown 转 Markdown。
// Readability 判不出正文（返回 null）时回退整篇 body 转换，带 warning，
// 对齐 webclip 的 vendored readability + turndown 思路（仅参考，不 fork）。

import { Readability } from '@mozilla/readability'

import { DocprocError } from '../bridge/protocol'
import { createTurndown } from './turndown'
import { throwIfCancelled, type Parser } from './types'

export const parseHtml: Parser = async (input) => {
  input.onProgress?.('load', 20)
  const htmlText = new TextDecoder('utf-8', { fatal: false }).decode(input.data)
  throwIfCancelled(input.isCancelled)

  const doc = new DOMParser().parseFromString(htmlText, 'text/html')
  const warnings: string[] = []

  let contentHtml: string
  const article = new Readability(doc).parse()
  const articleContent = article?.content?.trim() ? article.content : null
  if (articleContent !== null) {
    contentHtml = articleContent
  } else {
    // Readability 不适用（短页 / 非文章结构）：整篇 body 兜底，不丢内容。
    contentHtml = doc.body?.innerHTML ?? ''
    if (contentHtml.trim().length === 0) {
      throw new DocprocError('corrupt', 'HTML 文档没有可抽取的正文内容')
    }
    warnings.push('readability-unparsed: 正文识别失败，已按整篇转换')
  }
  input.onProgress?.('extract', 60)
  throwIfCancelled(input.isCancelled)

  const text = createTurndown().turndown(contentHtml)
  input.onProgress?.('extract', 100)
  return { text, format: 'html', warnings }
}
