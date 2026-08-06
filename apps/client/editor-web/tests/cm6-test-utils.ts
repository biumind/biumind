// CM6 命令测试的共享辅助：jsdom 无布局引擎，Range.getClientRects 未实现，
// EditorView 挂载后 measure 阶段会抛异步异常 —— 打桩为空头目即可（命令
// 测试只关心文档与选区，不做任何几何断言）。

import { markdown } from '@codemirror/lang-markdown'
import { EditorSelection, EditorState, type Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { GFM } from '@lezer/markdown'

if (typeof Range !== 'undefined' && !Range.prototype.getClientRects) {
  Range.prototype.getClientRects = function getClientRects() {
    return Object.assign([], { item: () => null }) as unknown as DOMRectList
  }
}

export function makeCm6View(
  doc: string,
  anchor: number,
  head = anchor,
  extensions: Extension[] = [],
): EditorView {
  const parent = document.createElement('div')
  document.body.appendChild(parent)
  return new EditorView({
    parent,
    state: EditorState.create({
      doc,
      selection: EditorSelection.range(anchor, head),
      extensions: [markdown({ extensions: [GFM] }), ...extensions],
    }),
  })
}
