// HTML → Markdown：readability 抽正文 + turndown 转换。

import { describe, expect, it } from 'vitest'

import { DocprocError } from '../src/bridge/protocol'
import { parseDocument } from '../src/parsers'

const enc = (s: string) => new TextEncoder().encode(s)

const ARTICLE_HTML = `<!doctype html>
<html><head><title>测试文章</title></head>
<body>
  <nav>导航噪音 应该被去掉</nav>
  <article>
    <h1>客户端文档解析</h1>
    <p>这是正文第一段，包含足够多的文字让 Readability 把它识别为文章主体内容，而不是导航或页脚。</p>
    <h2>小节</h2>
    <ul><li>要点一</li><li>要点二</li></ul>
  </article>
  <footer>页脚噪音</footer>
</body></html>`

describe('parseDocument: html', () => {
  it('readability 抽正文 → markdown 保留结构', async () => {
    const out = await parseDocument({
      fileName: 'article.html',
      data: enc(ARTICLE_HTML),
    })
    expect(out.format).toBe('html')
    expect(out.text).toContain('# 客户端文档解析')
    expect(out.text).toContain('## 小节')
    expect(out.text).toMatch(/-\s+要点一/)
    expect(out.text).toContain('正文第一段')
  })

  it('readability 判不出正文时整篇兜底 + warning', async () => {
    // 该文档 Readability.parse() 返回 null（实测），走 body 兜底。
    const out = await parseDocument({
      fileName: 'shell.htm',
      data: enc('<html><body><div>   </div><p></p></body></html>'),
    })
    expect(out.format).toBe('html')
    expect(out.warnings.some((w) => w.startsWith('readability-unparsed'))).toBe(
      true,
    )
  })

  it('空 body 报 corrupt', async () => {
    const err = await parseDocument({
      fileName: 'empty.html',
      data: enc('<html><body></body></html>'),
    }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DocprocError)
    expect((err as DocprocError).code).toBe('corrupt')
  })
})
