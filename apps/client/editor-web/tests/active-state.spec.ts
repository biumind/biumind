import { describe, expect, it } from 'vitest'
import { Schema } from '@milkdown/kit/prose/model'
import { EditorState, TextSelection } from '@milkdown/kit/prose/state'
import type { Node as PMNode } from '@milkdown/kit/prose/model'

import { computeActiveState, INACTIVE_STATE } from '../src/toolbar/active-state'

// 与 milkdown commonmark/gfm 同名的最小 schema（mark/node 名对齐真实编辑器）
const schema = new Schema({
  nodes: {
    doc: { content: 'block+' },
    paragraph: { group: 'block', content: 'inline*' },
    heading: {
      group: 'block',
      content: 'inline*',
      attrs: { level: { default: 1 } },
    },
    blockquote: { group: 'block', content: 'block+' },
    bullet_list: { group: 'block', content: 'list_item+' },
    ordered_list: { group: 'block', content: 'list_item+' },
    list_item: {
      group: 'block',
      content: 'block+',
      attrs: { checked: { default: null } },
    },
    code_block: { group: 'block', content: 'text*', marks: '' },
    text: { group: 'inline' },
  },
  marks: {
    strong: {},
    emphasis: {},
    strike_through: {},
    inlineCode: {},
  },
})

function stateIn(doc: PMNode, pos: number): EditorState {
  const state = EditorState.create({ doc })
  return state.apply(state.tr.setSelection(TextSelection.create(doc, pos)))
}

function cursorIn(node: PMNode): EditorState {
  const doc = schema.nodes.doc.create(null, node)
  // 位置 2 落在第一个 block 的文本内容里
  return stateIn(doc, 2)
}

describe('computeActiveState', () => {
  it('空文档段落里全部不激活', () => {
    const state = cursorIn(schema.nodes.paragraph.create(null, schema.text('plain')))
    expect(computeActiveState(state)).toEqual(INACTIVE_STATE)
  })

  it('光标在加粗文本里 strong 激活', () => {
    const state = cursorIn(
      schema.nodes.paragraph.create(
        null,
        schema.text('bold', [schema.marks.strong.create()]),
      ),
    )
    expect(computeActiveState(state).strong).toBe(true)
    expect(computeActiveState(state).emphasis).toBe(false)
  })

  it('选区覆盖斜体文本时 emphasis 激活', () => {
    const doc = schema.nodes.doc.create(null, [
      schema.nodes.paragraph.create(null, [
        schema.text('a'),
        schema.text('b', [schema.marks.emphasis.create()]),
      ]),
    ])
    const state = stateIn(doc, 1).apply(
      stateIn(doc, 1).tr.setSelection(TextSelection.create(doc, 1, 3)),
    )
    expect(computeActiveState(state).emphasis).toBe(true)
  })

  it('删除线与行内代码 mark', () => {
    const strike = cursorIn(
      schema.nodes.paragraph.create(
        null,
        schema.text('x', [schema.marks.strike_through.create()]),
      ),
    )
    expect(computeActiveState(strike).strikeThrough).toBe(true)
    const code = cursorIn(
      schema.nodes.paragraph.create(
        null,
        schema.text('x', [schema.marks.inlineCode.create()]),
      ),
    )
    expect(computeActiveState(code).inlineCode).toBe(true)
  })

  it('标题级别映射到 h1/h2/h3', () => {
    for (const [level, key] of [[1, 'h1'], [2, 'h2'], [3, 'h3']] as const) {
      const state = cursorIn(
        schema.nodes.heading.create({ level }, schema.text('title')),
      )
      const active = computeActiveState(state)
      expect(active[key]).toBe(true)
    }
    const h4 = cursorIn(
      schema.nodes.heading.create({ level: 4 }, schema.text('title')),
    )
    const active = computeActiveState(h4)
    expect(active.h1 || active.h2 || active.h3).toBe(false)
  })

  it('引用块与代码块祖先节点', () => {
    const quote = cursorIn(
      schema.nodes.blockquote.create(null, [
        schema.nodes.paragraph.create(null, schema.text('quoted')),
      ]),
    )
    expect(computeActiveState(quote).blockquote).toBe(true)
    const codeBlock = cursorIn(schema.nodes.code_block.create(null, schema.text('code')))
    expect(computeActiveState(codeBlock).codeBlock).toBe(true)
  })

  it('无序/有序列表祖先节点', () => {
    const item = schema.nodes.list_item.create(null, [
      schema.nodes.paragraph.create(null, schema.text('item')),
    ])
    // doc > bullet_list > list_item > paragraph，文本从位置 3 开始
    const bullet = stateIn(
      schema.nodes.doc.create(null, [schema.nodes.bullet_list.create(null, item)]),
      4,
    )
    expect(computeActiveState(bullet).bulletList).toBe(true)
    expect(computeActiveState(bullet).taskList).toBe(false)
    const ordered = stateIn(
      schema.nodes.doc.create(null, [schema.nodes.ordered_list.create(null, item)]),
      4,
    )
    expect(computeActiveState(ordered).orderedList).toBe(true)
  })

  it('带 checked 属性的 list_item 识别为任务列表', () => {
    const taskItem = schema.nodes.list_item.create({ checked: false }, [
      schema.nodes.paragraph.create(null, schema.text('task')),
    ])
    const state = stateIn(
      schema.nodes.doc.create(null, [schema.nodes.bullet_list.create(null, taskItem)]),
      4,
    )
    const active = computeActiveState(state)
    expect(active.taskList).toBe(true)
    expect(active.bulletList).toBe(true)
  })
})
