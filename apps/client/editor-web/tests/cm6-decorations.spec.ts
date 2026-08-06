import { EditorView } from '@codemirror/view'
import { afterEach, describe, expect, it } from 'vitest'

import { decorationsExtension } from '../src/cm6/decorations'
import { makeCm6View } from './cm6-test-utils'

let view: EditorView | null = null

const DOC = [
  '# H1',
  '',
  '## H2',
  '',
  '> quote',
  '',
  '```',
  'code',
  '```',
  '',
  '`ic` ~~st~~',
  '',
  '---',
  '',
  '- bullet',
  '1. ordered',
].join('\n')

afterEach(() => {
  view?.destroy()
  view = null
  document.body.innerHTML = ''
})

describe('cm6 decorations 类名映射', () => {
  it('行 class：标题/引用/代码块/列表', () => {
    view = makeCm6View(DOC, 0, 0, [decorationsExtension()])
    const dom = view.contentDOM
    expect(dom.querySelector('.cm-line.cm-h1.cm-headerLine')).not.toBeNull()
    expect(dom.querySelector('.cm-line.cm-h2')).not.toBeNull()
    expect(dom.querySelector('.cm-line.cm-blockQuote')).not.toBeNull()
    expect(dom.querySelector('.cm-line.cm-unorderedList')).not.toBeNull()
    expect(dom.querySelector('.cm-line.cm-orderedList')).not.toBeNull()
    expect(dom.querySelector('.cm-line.cm-listItem')).not.toBeNull()
  })

  it('代码块行 class 与首末行圆角后缀', () => {
    view = makeCm6View(DOC, 0, 0, [decorationsExtension()])
    const dom = view.contentDOM
    expect(dom.querySelectorAll('.cm-line.cm-codeBlock').length).toBe(3)
    expect(dom.querySelector('.cm-codeBlock-first')).not.toBeNull()
    expect(dom.querySelector('.cm-codeBlock-last')).not.toBeNull()
  })

  it('mark class：行内 code / 删除线 / 分割线', () => {
    view = makeCm6View(DOC, 0, 0, [decorationsExtension()])
    const dom = view.contentDOM
    expect(dom.querySelector('.cm-inlineCode')?.textContent).toBe('`ic`')
    expect(dom.querySelector('.cm-strike')?.textContent).toBe('~~st~~')
    expect(dom.querySelector('.cm-hr')?.textContent).toBe('---')
  })

  it('裸 URL 挂 cm-url', () => {
    view = makeCm6View('go http://y.com now', 0, 0, [decorationsExtension()])
    expect(view.contentDOM.querySelector('.cm-url')?.textContent).toBe('http://y.com')
  })
})
