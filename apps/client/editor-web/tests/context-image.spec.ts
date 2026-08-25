// imagePosFromDom 对叶子 atom 节点（image-block）的解析回归。
// 实机 bug（M2 图片长按不触发 + 桌面右键图片菜单失效的共同根因）：
// findNodeOnChain 只走祖先链，而叶子 atom 永不是相邻位置的祖先 ——
// 解析恒返回 null。修复 = imageNodeNear 查 nodeAt/nodeAfter/nodeBefore。

import { afterEach, describe, expect, it } from 'vitest'
import { Schema } from 'prosemirror-model'
import { EditorState } from 'prosemirror-state'
import { EditorView } from 'prosemirror-view'

import { imagePosFromDom } from '../src/context-menu/context'

const schema = new Schema({
  nodes: {
    doc: { content: 'block+' },
    paragraph: { group: 'block', content: 'text*', toDOM: () => ['p', 0] },
    'image-block': {
      group: 'block',
      atom: true,
      attrs: { src: { default: '' } },
      toDOM: (n) => [
        'div',
        { class: 'milkdown-image-block' },
        ['img', { src: n.attrs.src as string }],
      ],
    },
    text: {},
  },
})

function makeView(): { view: EditorView; blockEl: HTMLElement } {
  const doc = schema.node('doc', null, [
    schema.node('paragraph', null, schema.text('hello')),
    schema.node('image-block', { src: 'biu-file://x' }),
    schema.node('paragraph', null, schema.text('world')),
  ])
  const host = document.createElement('div')
  document.body.appendChild(host)
  const view = new EditorView(host, { state: EditorState.create({ schema, doc }) })
  const blockEl = host.querySelector('.milkdown-image-block') as HTMLElement
  return { view, blockEl }
}

/** jsdom 无布局也无 elementFromPoint —— 直接补桩。 */
function stubElementFromPoint(el: Element | null): void {
  ;(document as unknown as Record<string, unknown>).elementFromPoint = () => el
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('imagePosFromDom：atom 图片节点解析（实机 bug 回归）', () => {
  it('触点落在 image-block 容器上 → 返回节点 pos', () => {
    const { view, blockEl } = makeView()
    stubElementFromPoint(blockEl)
    const pos = imagePosFromDom(view, { x: 10, y: 10 })
    // image-block 是第二个子节点：paragraph("hello") 占 7，atom 起点 = 7
    expect(pos).toBe(7)
    view.destroy()
  })

  it('触点落在容器内的 img 元素上 → closest 命中，同样解析', () => {
    const { view, blockEl } = makeView()
    stubElementFromPoint(blockEl.querySelector('img'))
    expect(imagePosFromDom(view, { x: 10, y: 10 })).toBe(7)
    view.destroy()
  })

  it('触点不在任何图片上 → null（不误判）', () => {
    const { view } = makeView()
    stubElementFromPoint(null)
    expect(imagePosFromDom(view, { x: 10, y: 10 })).toBeNull()
    view.destroy()
  })
})
