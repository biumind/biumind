import { ensureSyntaxTree } from '@codemirror/language'
import { markdown } from '@codemirror/lang-markdown'
import { EditorSelection, EditorState } from '@codemirror/state'
import { GFM } from '@lezer/markdown'
import { describe, expect, it } from 'vitest'

import { computeCm6ActiveState } from '../src/cm6/active-state'
import { INACTIVE_STATE } from '../src/toolbar/types'

// CM6 EditorState 可在 node 下纯构造；语法树手动确保解析到位（编辑器里由 view 驱动）
function stateAt(doc: string, pos: number): EditorState {
  const state = EditorState.create({
    doc,
    selection: EditorSelection.cursor(pos),
    extensions: [markdown({ extensions: [GFM] })],
  })
  ensureSyntaxTree(state, doc.length, 1000)
  return state
}

describe('cm6 computeCm6ActiveState', () => {
  it('普通段落全部不激活', () => {
    const state = stateAt('plain text', 5)
    expect(computeCm6ActiveState(state)).toEqual(INACTIVE_STATE)
  })

  it('光标在加粗/斜体/删除线/行内代码里', () => {
    expect(computeCm6ActiveState(stateAt('a **bold** b', 5)).strong).toBe(true)
    expect(computeCm6ActiveState(stateAt('a *it* b', 4)).emphasis).toBe(true)
    expect(computeCm6ActiveState(stateAt('a ~~s~~ b', 4)).strikeThrough).toBe(true)
    expect(computeCm6ActiveState(stateAt('a `code` b', 4)).inlineCode).toBe(true)
  })

  it('标题级别映射 h1/h2/h3，h4 不映射', () => {
    expect(computeCm6ActiveState(stateAt('# t', 3)).h1).toBe(true)
    expect(computeCm6ActiveState(stateAt('## t', 4)).h2).toBe(true)
    expect(computeCm6ActiveState(stateAt('### t', 5)).h3).toBe(true)
    const h4 = computeCm6ActiveState(stateAt('#### t', 6))
    expect(h4.h1 || h4.h2 || h4.h3).toBe(false)
  })

  it('引用行', () => {
    expect(computeCm6ActiveState(stateAt('> quoted', 5)).blockquote).toBe(true)
  })

  it('无序/有序/任务列表行', () => {
    const bullet = computeCm6ActiveState(stateAt('- item', 4))
    expect(bullet.bulletList).toBe(true)
    expect(bullet.taskList).toBe(false)
    expect(bullet.orderedList).toBe(false)

    const ordered = computeCm6ActiveState(stateAt('1. item', 5))
    expect(ordered.orderedList).toBe(true)
    expect(ordered.bulletList).toBe(false)

    const task = computeCm6ActiveState(stateAt('- [ ] todo', 6))
    expect(task.taskList).toBe(true)
    expect(task.bulletList).toBe(true)
  })

  it('围栏代码块内 codeBlock 激活', () => {
    const doc = '```js\nconst x = 1\n```'
    const active = computeCm6ActiveState(stateAt(doc, 8))
    expect(active.codeBlock).toBe(true)
  })

  it('代码块外不激活', () => {
    const doc = '```\ncode\n```\nafter'
    const active = computeCm6ActiveState(stateAt(doc, doc.length))
    expect(active.codeBlock).toBe(false)
  })
})
