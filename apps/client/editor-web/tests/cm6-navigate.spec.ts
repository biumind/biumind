import { ensureSyntaxTree } from '@codemirror/language'
import { markdown } from '@codemirror/lang-markdown'
import { EditorSelection, EditorState } from '@codemirror/state'
import { GFM } from '@lezer/markdown'
import { describe, expect, it } from 'vitest'

import { linkUrlAt, shouldOpenLink } from '../src/cm6/rendering/links'

function stateAt(doc: string, pos: number): EditorState {
  const state = EditorState.create({
    doc,
    selection: EditorSelection.cursor(pos),
    extensions: [markdown({ extensions: [GFM] })],
  })
  ensureSyntaxTree(state, doc.length, 1000)
  return state
}

describe('cm6 linkUrlAt（Mod+click navigate 取 URL）', () => {
  const doc = 'see [label](https://x.com/a) end'

  it('pos 在 label 内 → 返回 url', () => {
    expect(linkUrlAt(stateAt(doc, 6), 6)).toBe('https://x.com/a')
  })

  it('pos 在 url 内 → 返回 url', () => {
    expect(linkUrlAt(stateAt(doc, 18), 18)).toBe('https://x.com/a')
  })

  it('pos 在链接外 → null', () => {
    expect(linkUrlAt(stateAt(doc, 1), 1)).toBeNull()
    expect(linkUrlAt(stateAt(doc, doc.length), doc.length)).toBeNull()
  })

  it('裸 Autolink 不算（不在 Link 节点内）', () => {
    const bare = 'go https://y.com now'
    expect(linkUrlAt(stateAt(bare, 8), 8)).toBeNull()
  })

  it('图片不算（父节点是 Image 不是 Link）', () => {
    const img = '![alt](https://x.com/i.png)'
    expect(linkUrlAt(stateAt(img, 3), 3)).toBeNull()
  })
})

describe('cm6 shouldOpenLink（触屏二次点击 + 修饰键）', () => {
  const doc = 'see [label](https://x.com/a) end'
  const URL = 'https://x.com/a'
  // Link 区间 [4,26]：'see ' 之后到 ' end' 之前
  const POS_IN_LINK = 6

  it('光标在链接外 + 无修饰键点击 → 不打开（第一次点击放行）', () => {
    const state = stateAt(doc, 1)
    expect(shouldOpenLink(state, POS_IN_LINK, false)).toBeNull()
  })

  it('光标已在链接内 + 无修饰键再点 → 打开（二次点击）', () => {
    const state = stateAt(doc, 6)
    expect(shouldOpenLink(state, POS_IN_LINK, false)).toBe(URL)
  })

  it('选区与链接相交也算「已位于区间内」', () => {
    const doc2 = 'see [label](https://x.com/a) end'
    const state = EditorState.create({
      doc: doc2,
      selection: EditorSelection.range(2, 8),
      extensions: [markdown({ extensions: [GFM] })],
    })
    ensureSyntaxTree(state, doc2.length, 1000)
    expect(shouldOpenLink(state, POS_IN_LINK, false)).toBe(URL)
  })

  it('光标在链接外 + Ctrl/Cmd 点击 → 直接打开（桌面第一次点击）', () => {
    const state = stateAt(doc, 1)
    expect(shouldOpenLink(state, POS_IN_LINK, true)).toBe(URL)
  })

  it('非 http(s) 链接任何点击都不打开', () => {
    const doc3 = 'see [f](biu-file://uuid) end'
    const state = stateAt(doc3, 6)
    expect(shouldOpenLink(state, 6, true)).toBeNull()
    expect(shouldOpenLink(state, 6, false)).toBeNull()
  })

  it('点击位置不在链接上 → null', () => {
    const state = stateAt(doc, 1)
    expect(shouldOpenLink(state, 1, true)).toBeNull()
    expect(shouldOpenLink(state, 1, false)).toBeNull()
  })
})
