// G5 — wikilink 往返保真（§⑤ 最大风险，llm_wiki 因此弃 Milkdown）。
//
// llm_wiki 风险：prosemirror 往返把 [[slug]] 改写成 [label](#slug)，丢原始源。
// biumind 走 remarkWikilink（parse 时 split [[x]] 成 wikilink mdast node，不当
// link reference）+ wikilinkSchema atom + node.ts:64-74 toMarkdown runner（产
// [[slug|alias]] literal text）规避。
//
// 本 spec 覆盖 remark 层往返（高风险段）：
//   ① md → remark-parse + remarkWikilink → mdast wikilink node（[[x]] 不当 link）
//   ④ mdast wikilink node → remark-stringify + handler → [[x]] literal md（不 escape）
// wikilinkHandler 镜像 node.ts:64-74 toMarkdown runner 逻辑，证两端一致。
//
// ProseMirror 段（②mdast→atom / ③atom→text）逻辑直接（node.ts:54-74 data 复制），
// jsdom Crepe 端到端被 paragraph runner 的 editorView 依赖阻塞，完整端到端留
// Playwright 专项（真 browser view 完整）。

import { describe, expect, it } from 'vitest'
import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkStringify from 'remark-stringify'

import { STRINGIFY_OPTIONS } from '../src/markdown/stringify-options'
import { remarkWikilink } from '../src/plugins/wikilink/remark'

interface WikilinkMdastNode {
  type: 'wikilink'
  data: { slug: string; alias: string | null }
}

/// mdast wikilink node → `[[slug|alias]]` literal。镜像 ProseMirror
/// node.ts:64-74 toMarkdown runner（state.addNode('text', undefined, literal)）。
function wikilinkHandler(node: WikilinkMdastNode): string {
  const slug = node.data?.slug ?? ''
  const alias = node.data?.alias
  return alias && alias.length > 0 ? `[[${slug}|${alias}]]` : `[[${slug}]]`
}

/// md → remark-parse(+remarkWikilink) → mdast → remark-stringify(+handler+opts) → md。
function roundtrip(md: string): string {
  return unified()
    .use(remarkParse)
    .use(remarkWikilink)
    .use(remarkStringify, {
      ...STRINGIFY_OPTIONS,
      handlers: { wikilink: wikilinkHandler as never },
    })
    .processSync(md)
    .toString()
}

describe('G5 wikilink 往返保真（remark 层）', () => {
  it('[[slug]] 字面保 — 不被当 link reference，不 escape', () => {
    const out = roundtrip('See [[page]] here.')
    expect(out).toContain('[[page]]')
    expect(out).not.toMatch(/\]\(#/)
  })

  it('[[slug|alias]] 双字段往返 — slug + alias 都保', () => {
    expect(roundtrip('Visit [[my-slug|My Page]] now.')).toContain(
      '[[my-slug|My Page]]',
    )
  })

  it('同段多个 wikilink 互不吞并', () => {
    const out = roundtrip('See [[one]] and [[two|Two!]] end.')
    expect(out).toContain('[[one]]')
    expect(out).toContain('[[two|Two!]]')
  })

  it('code fence ``` 不被改 ~~~（STRINGIFY_OPTIONS fence:"`"）', () => {
    const out = roundtrip('```js\nconst x = 1\n```\n')
    expect(out).toMatch(/```js/)
    expect(out).not.toContain('~~~')
  })

  it('emphasis *x* / strong **x** 标点锁定（不被改 _x_）', () => {
    const out = roundtrip('*em* and **strong**')
    expect(out).toContain('*em*')
    expect(out).toContain('**strong**')
    expect(out).not.toMatch(/_em_/)
  })

  it('wikilink 与标题/列表共存 — 互不破坏结构', () => {
    const out = roundtrip('# 标题\n\n链 [[目标]] 跳转\n\n- 项 [[a|A]]\n- 项 [[b]]\n')
    expect(out).toContain('[[目标]]')
    expect(out).toContain('[[a|A]]')
    expect(out).toContain('[[b]]')
  })

  it('未知 [[ 不误匹配（单个 [ 不成 wikilink）', () => {
    const out = roundtrip('Array [0] and [1] indexed.')
    expect(out).not.toContain('[[')
    expect(out).toContain('[0]')
  })
})
