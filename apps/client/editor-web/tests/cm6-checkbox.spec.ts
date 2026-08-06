import { EditorView } from '@codemirror/view'
import { afterEach, describe, expect, it } from 'vitest'

import {
  checkboxExtension,
  completedTaskLineExtension,
} from '../src/cm6/rendering/checkbox'
import { makeCm6View } from './cm6-test-utils'

let view: EditorView | null = null

const DOC = '- [ ] todo\n- [x] done\n\nplain'
const EXTS = [checkboxExtension(), completedTaskLineExtension()]

afterEach(() => {
  view?.destroy()
  view = null
  document.body.innerHTML = ''
})

function checkboxes(): NodeListOf<HTMLInputElement> {
  return view!.contentDOM.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')
}

describe('cm6 checkbox 渲染', () => {
  it('ListMark+TaskMarker 整段替换为 checkbox widget', () => {
    view = makeCm6View(DOC, DOC.length, DOC.length, EXTS)
    expect(checkboxes().length).toBe(2)
    // 替换范围含行首 ListMark：'- [ ]' 不再出现在可见文本里（后续空格保留）
    expect(view.contentDOM.textContent).toBe(' todo doneplain')
    expect(checkboxes()[0].checked).toBe(false)
    expect(checkboxes()[1].checked).toBe(true)
  })

  it('已完成项整行挂 cm-md-completed-item', () => {
    view = makeCm6View(DOC, DOC.length, DOC.length, EXTS)
    const completed = view.contentDOM.querySelectorAll('.cm-md-completed-item')
    expect(completed.length).toBe(1)
    expect(completed[0].textContent).toContain('done')
  })

  it('点击 checkbox → 源码三字符替换 [ ]→[x]，widget 重渲染', () => {
    view = makeCm6View(DOC, DOC.length, DOC.length, EXTS)
    const first = checkboxes()[0]
    first.dispatchEvent(new Event('change'))
    expect(view.state.doc.toString()).toBe('- [x] todo\n- [x] done\n\nplain')
    // docChanged 触发重建：新 widget 反映勾选态，完成行 class 随之更新
    expect(checkboxes()[0].checked).toBe(true)
    expect(view.contentDOM.querySelectorAll('.cm-md-completed-item').length).toBe(2)
  })

  it('再点已勾选项 → [x]→[ ]', () => {
    view = makeCm6View(DOC, DOC.length, DOC.length, EXTS)
    checkboxes()[1].dispatchEvent(new Event('change'))
    expect(view.state.doc.toString()).toBe('- [ ] todo\n- [ ] done\n\nplain')
    expect(view.contentDOM.querySelectorAll('.cm-md-completed-item').length).toBe(0)
  })

  it("reveal='line'：光标进入该行即露出源码、widget 消失", () => {
    view = makeCm6View(DOC, DOC.length, DOC.length, EXTS)
    view.dispatch({ selection: { anchor: 4 } })
    expect(checkboxes().length).toBe(1)
    expect(view.contentDOM.textContent).toContain('- [ ] todo')
  })
})
