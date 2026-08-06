import { EditorView } from '@codemirror/view'
import { afterEach, describe, expect, it } from 'vitest'

import {
  insertCodeBlock,
  insertDateTime,
  insertHorizontalRule,
  insertTable,
  toggleBlockquote,
  toggleHeaderLevel,
  toggleList,
} from '../src/cm6/commands/block-format'
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

describe('cm6 block-format / 标题', () => {
  it('普通行 → H1 → 去除', () => {
    const v = makeView('title', 2)
    toggleHeaderLevel(1)(v)
    expect(v.state.doc.toString()).toBe('# title')
    toggleHeaderLevel(1)(v)
    expect(v.state.doc.toString()).toBe('title')
  })

  it('异级标题替换 # 数量', () => {
    const v = makeView('# title', 4)
    toggleHeaderLevel(3)(v)
    expect(v.state.doc.toString()).toBe('### title')
  })

  it('空行也可处理', () => {
    const v = makeView('', 0)
    toggleHeaderLevel(2)(v)
    expect(v.state.doc.toString()).toBe('## ')
  })

  it('多行选区逐行处理', () => {
    const v = makeView('a\nb', 0, 3)
    toggleHeaderLevel(1)(v)
    expect(v.state.doc.toString()).toBe('# a\n# b')
  })
})

describe('cm6 block-format / 引用', () => {
  it('行首 > toggle', () => {
    const v = makeView('text', 2)
    toggleBlockquote(v)
    expect(v.state.doc.toString()).toBe('> text')
    toggleBlockquote(v)
    expect(v.state.doc.toString()).toBe('text')
  })
})

describe('cm6 block-format / 列表互转与重编号', () => {
  it('普通行加无序标记，再次 toggle 去除', () => {
    const v = makeView('a\nb', 0, 3)
    toggleList('bullet')(v)
    expect(v.state.doc.toString()).toBe('- a\n- b')
    toggleList('bullet')(v)
    expect(v.state.doc.toString()).toBe('a\nb')
  })

  it('无序 → 有序：替换标记并重编号', () => {
    const v = makeView('- a\n- b\n- c', 0, 11)
    toggleList('ordered')(v)
    expect(v.state.doc.toString()).toBe('1. a\n2. b\n3. c')
  })

  it('有序 → 任务：整段替换为 checkbox 标记', () => {
    const v = makeView('1. a\n2. b', 0, 9)
    toggleList('task')(v)
    expect(v.state.doc.toString()).toBe('- [ ] a\n- [ ] b')
  })

  it('任务列表同类型 toggle 去除标记', () => {
    const v = makeView('- [x] done', 5)
    toggleList('task')(v)
    expect(v.state.doc.toString()).toBe('done')
  })

  it('有序段中间插入行后重编号（含前序行）', () => {
    const v = makeView('1. a\n2. b\ntext', 12)
    toggleList('ordered')(v)
    expect(v.state.doc.toString()).toBe('1. a\n2. b\n3. text')
  })

  it('有序 → 无序：替换为 -', () => {
    const v = makeView('3. item', 4)
    toggleList('bullet')(v)
    expect(v.state.doc.toString()).toBe('- item')
  })
})

describe('cm6 block-format / 围栏与插入', () => {
  it('选区行包 ``` 围栏', () => {
    const doc = 'line1\nline2'
    const v = makeView(doc, 0, doc.length)
    insertCodeBlock(v)
    expect(v.state.doc.toString()).toBe('```\nline1\nline2\n```')
  })

  it('空选区插一对围栏，光标落中间', () => {
    const v = makeView('', 0)
    insertCodeBlock(v)
    expect(v.state.doc.toString()).toBe('```\n\n```')
    expect(v.state.selection.main.head).toBe(4)
  })

  it('分割线：非空行尾另起一行插 ---', () => {
    const v = makeView('text', 4)
    insertHorizontalRule(v)
    expect(v.state.doc.toString()).toBe('text\n---\n')
  })

  it('分割线：空行直接插 ---', () => {
    const v = makeView('', 0)
    insertHorizontalRule(v)
    expect(v.state.doc.toString()).toBe('---\n')
  })

  it('插入 3×3 表格模板', () => {
    const v = makeView('', 0)
    insertTable(v)
    const text = v.state.doc.toString()
    expect(text).toContain('| --- | --- | --- |')
    expect(text.split('\n').filter((l) => l.startsWith('|')).length).toBe(5)
  })

  it('插入时间戳（YYYY-MM-DD HH:mm）', () => {
    const v = makeView('', 0)
    insertDateTime(v)
    expect(v.state.doc.toString()).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  })
})
