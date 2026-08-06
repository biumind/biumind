import { EditorView } from '@codemirror/view'
import { Decoration } from '@codemirror/view'
import { afterEach, describe, expect, it } from 'vitest'

import { inlineReplacement } from '../src/cm6/rendering/inline-replace'
import { makeCm6View } from './cm6-test-utils'

let view: EditorView | null = null

afterEach(() => {
  view?.destroy()
  view = null
  document.body.innerHTML = ''
})

const hidden = (): Decoration => Decoration.replace({})

describe('cm6 inline-replace 机制', () => {
  it("reveal='active'：光标在父节点外隐藏，进入即露源码", () => {
    // doc: 'a **bold** b\nplain'，光标落在第二行 plain 上
    view = makeCm6View('a **bold** b\nplain', 13, 13, [
      inlineReplacement({ nodeNames: ['EmphasisMark'], create: hidden, reveal: 'active' }),
    ])
    expect(view.contentDOM.textContent).toBe('a bold bplain')
    // 光标进入加粗文本 → 标记露出
    view.dispatch({ selection: { anchor: 5 } })
    expect(view.contentDOM.textContent).toBe('a **bold** bplain')
    // 光标移出 → 再次隐藏
    view.dispatch({ selection: { anchor: 15 } })
    expect(view.contentDOM.textContent).toBe('a bold bplain')
  })

  it("reveal='line'：光标同行即露（含自定义 range 连后随空格）", () => {
    view = makeCm6View('# Title\nbody', 9, 9, [
      inlineReplacement({
        nodeNames: ['HeaderMark'],
        create: hidden,
        range: (node, state) => {
          let to = node.to
          if (state.sliceDoc(to, to + 1) === ' ') to += 1
          return [node.from, to]
        },
        reveal: 'line',
      }),
    ])
    // 光标在 body 行：'# ' 隐藏
    expect(view.contentDOM.textContent).toBe('Titlebody')
    // 光标回到标题行：源码露出
    view.dispatch({ selection: { anchor: 3 } })
    expect(view.contentDOM.textContent).toBe('# Titlebody')
  })

  it('跨行替换区间被防御性跳过', () => {
    // StrongEmphasis 节点 [2,9]，故意给跨行 range [2,12] → 不替换
    view = makeCm6View('x **bold**\ny', 10, 10, [
      inlineReplacement({
        nodeNames: ['StrongEmphasis'],
        create: hidden,
        range: (node) => [node.from, node.to + 3],
        reveal: 'line',
      }),
    ])
    expect(view.contentDOM.textContent).toContain('**bold**')
  })

  it('选区碰到节点即露（active 语义的选区分支）', () => {
    view = makeCm6View('a **bold** b\nplain', 13, 13, [
      inlineReplacement({ nodeNames: ['EmphasisMark'], create: hidden, reveal: 'active' }),
    ])
    expect(view.contentDOM.textContent).toBe('a bold bplain')
    // 拖选覆盖加粗区 → 露出
    view.dispatch({ selection: { anchor: 2, head: 9 } })
    expect(view.contentDOM.textContent).toBe('a **bold** bplain')
  })
})
