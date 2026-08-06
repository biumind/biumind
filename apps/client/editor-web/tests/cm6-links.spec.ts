import { EditorView } from '@codemirror/view'
import { afterEach, describe, expect, it } from 'vitest'

import { linksExtension } from '../src/cm6/rendering/links'
import { makeCm6View } from './cm6-test-utils'

let view: EditorView | null = null

afterEach(() => {
  view?.destroy()
  view = null
  document.body.innerHTML = ''
})

describe('cm6 links 渲染', () => {
  it('光标在链接外：URL 与 LinkMark 隐藏，只留 label', () => {
    const doc = 'see [label](http://x) end\nplain'
    view = makeCm6View(doc, doc.length, doc.length, [linksExtension()])
    const text = view.contentDOM.textContent
    expect(text).toBe('see label endplain')
    expect(text).not.toContain('http://x')
    expect(text).not.toContain('](')
  })

  it("reveal='active'：光标进入 label 即露出完整源码", () => {
    const doc = 'see [label](http://x) end\nplain'
    view = makeCm6View(doc, doc.length, doc.length, [linksExtension()])
    view.dispatch({ selection: { anchor: 6 } }) // 'label' 内
    expect(view.contentDOM.textContent).toContain('[label](http://x)')
  })

  it('裸 URL（Autolink）不隐藏 —— 它本身就是可见文本', () => {
    const doc = 'go http://y.com now\nplain'
    view = makeCm6View(doc, doc.length, doc.length, [linksExtension()])
    expect(view.contentDOM.textContent).toContain('http://y.com')
  })

  it('Image 节点本期不处理（M3 范围）', () => {
    const doc = 'pic ![alt](http://img) end\nplain'
    view = makeCm6View(doc, doc.length, doc.length, [linksExtension()])
    expect(view.contentDOM.textContent).toContain('[alt](http://img)')
  })
})
