import { EditorView } from '@codemirror/view'
import { afterEach, describe, expect, it } from 'vitest'

import {
  toggleBold,
  toggleInlineCode,
  toggleItalic,
  toggleStrikethrough,
} from '../src/cm6/commands/inline-format'
import { makeCm6View } from './cm6-test-utils'

let view: EditorView | null = null

function makeView(doc: string, anchor: number, head = anchor): EditorView {
  view = makeCm6View(doc, anchor, head)
  return view
}

afterEach(() => {
  view?.destroy()
  view = null
  document.body.innerHTML = ''
})

describe('cm6 inline-format', () => {
  it('选区未包标记 → 包裹', () => {
    const v = makeView('hello world', 0, 5)
    toggleBold(v)
    expect(v.state.doc.toString()).toBe('**hello** world')
    expect([v.state.selection.main.from, v.state.selection.main.to]).toEqual([2, 7])
  })

  it('选区含标记 → 去除', () => {
    const v = makeView('**hello** world', 0, 9)
    toggleBold(v)
    expect(v.state.doc.toString()).toBe('hello world')
    expect([v.state.selection.main.from, v.state.selection.main.to]).toEqual([0, 5])
  })

  it('标记紧贴选区外侧 → 去除', () => {
    const v = makeView('**hello** world', 2, 7)
    toggleBold(v)
    expect(v.state.doc.toString()).toBe('hello world')
    expect([v.state.selection.main.from, v.state.selection.main.to]).toEqual([0, 5])
  })

  it('空选区 → 插一对标记，光标居中', () => {
    const v = makeView('ab', 1)
    toggleBold(v)
    expect(v.state.doc.toString()).toBe('a****b')
    expect(v.state.selection.main.head).toBe(3)
  })

  it('光标在标记尾部 → 跳出（文档不变）', () => {
    const v = makeView('**foo** bar', 5)
    toggleBold(v)
    expect(v.state.doc.toString()).toBe('**foo** bar')
    expect(v.state.selection.main.head).toBe(7)
  })

  it('斜体 / 删除线 / 行内代码标记', () => {
    const italic = makeView('text', 0, 4)
    toggleItalic(italic)
    expect(italic.state.doc.toString()).toBe('*text*')

    const strike = makeView('text', 0, 4)
    toggleStrikethrough(strike)
    expect(strike.state.doc.toString()).toBe('~~text~~')
    toggleStrikethrough(strike)
    expect(strike.state.doc.toString()).toBe('text')

    const code = makeView('text', 0, 4)
    toggleInlineCode(code)
    expect(code.state.doc.toString()).toBe('`text`')
    toggleInlineCode(code)
    expect(code.state.doc.toString()).toBe('text')
  })

  it('多行选区按行内首末处理（不升级块级）', () => {
    const v = makeView('foo\nbar', 0, 7)
    toggleBold(v)
    expect(v.state.doc.toString()).toBe('**foo\nbar**')
  })
})
