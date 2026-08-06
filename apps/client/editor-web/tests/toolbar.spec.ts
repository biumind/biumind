import { describe, expect, it } from 'vitest'

import { createToolbar, type ToolbarActions } from '../src/toolbar'
import { INACTIVE_STATE } from '../src/toolbar/active-state'

function makeActions(): ToolbarActions & { calls: string[] } {
  const calls: string[] = []
  const record =
    (name: string) =>
    () => {
      calls.push(name)
    }
  return {
    calls,
    undo: record('undo'),
    redo: record('redo'),
    toggleStrong: record('strong'),
    toggleEmphasis: record('emphasis'),
    toggleStrikeThrough: record('strikeThrough'),
    toggleInlineCode: record('inlineCode'),
    wrapInHeading: (level) => {
      calls.push(`h${level}`)
    },
    wrapInBlockquote: record('blockquote'),
    insertHr: record('hr'),
    wrapInBulletList: record('bulletList'),
    wrapInOrderedList: record('orderedList'),
    toggleTaskList: record('taskList'),
    createCodeBlock: record('codeBlock'),
    insertTable: record('table'),
    insertTimestamp: record('timestamp'),
    toggleSourceMode: record('source'),
  }
}

const btn = (el: HTMLElement, key: string): HTMLButtonElement => {
  const b = el.querySelector<HTMLButtonElement>(`button[data-key="${key}"]`)
  if (!b) throw new Error(`button ${key} not found`)
  return b
}

describe('toolbar', () => {
  it('渲染全部按钮与中文 title', () => {
    const toolbar = createToolbar(makeActions())
    const keys = [
      'undo', 'redo',
      'strong', 'emphasis', 'strikeThrough', 'inlineCode',
      'h1', 'h2', 'h3',
      'blockquote', 'hr',
      'bulletList', 'orderedList', 'taskList',
      'codeBlock', 'table',
      'timestamp', 'source',
    ]
    for (const key of keys) {
      const b = btn(toolbar.element, key)
      expect(b.title).toBeTruthy()
    }
    expect(btn(toolbar.element, 'strong').title).toBe('加粗')
    expect(btn(toolbar.element, 'source').title).toBe('源码模式')
  })

  it('点击按钮触发对应动作', () => {
    const actions = makeActions()
    const toolbar = createToolbar(actions)
    btn(toolbar.element, 'undo').click()
    btn(toolbar.element, 'strong').click()
    btn(toolbar.element, 'h2').click()
    btn(toolbar.element, 'source').click()
    expect(actions.calls).toEqual(['undo', 'strong', 'h2', 'source'])
  })

  it('mousedown 阻止默认行为（避免编辑器失焦）', () => {
    const toolbar = createToolbar(makeActions())
    const event = new MouseEvent('mousedown', { cancelable: true })
    btn(toolbar.element, 'strong').dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
  })

  it('setDisabled 禁用所有按钮', () => {
    const actions = makeActions()
    const toolbar = createToolbar(actions)
    toolbar.setDisabled(true)
    const all = toolbar.element.querySelectorAll('button')
    expect(all.length).toBeGreaterThan(0)
    all.forEach((b) => expect(b.disabled).toBe(true))
    toolbar.setDisabled(false)
    all.forEach((b) => expect(b.disabled).toBe(false))
  })

  it('源码模式下除切换按钮外全部禁用，切换按钮高亮', () => {
    const toolbar = createToolbar(makeActions())
    toolbar.setSourceMode(true)
    expect(btn(toolbar.element, 'source').disabled).toBe(false)
    expect(btn(toolbar.element, 'source').classList.contains('kc-tb-active')).toBe(true)
    expect(btn(toolbar.element, 'strong').disabled).toBe(true)
    expect(btn(toolbar.element, 'undo').disabled).toBe(true)
    toolbar.setSourceMode(false)
    expect(btn(toolbar.element, 'strong').disabled).toBe(false)
  })

  it('setActive 切换选中态高亮', () => {
    const toolbar = createToolbar(makeActions())
    toolbar.setActive({ ...INACTIVE_STATE, strong: true, h2: true })
    expect(btn(toolbar.element, 'strong').classList.contains('kc-tb-active')).toBe(true)
    expect(btn(toolbar.element, 'h2').classList.contains('kc-tb-active')).toBe(true)
    expect(btn(toolbar.element, 'h1').classList.contains('kc-tb-active')).toBe(false)
    toolbar.setActive({ ...INACTIVE_STATE })
    expect(btn(toolbar.element, 'strong').classList.contains('kc-tb-active')).toBe(false)
  })

  it('readOnly 与源码模式叠加时仍保持禁用', () => {
    const toolbar = createToolbar(makeActions())
    toolbar.setDisabled(true)
    toolbar.setSourceMode(true)
    expect(btn(toolbar.element, 'source').disabled).toBe(true)
    toolbar.setDisabled(false)
    expect(btn(toolbar.element, 'source').disabled).toBe(false)
  })
})
