// 任务列表 checkbox：`- [ ]` 整段（ListMark + TaskMarker）替换为可点击
// checkbox widget；点击改源码（[ ]↔[x] 三字符替换），docChanged 自然回流。
// 已完成项整行挂 cm-md-completed-item（半透明）。实现全部自写。

import { syntaxTree } from '@codemirror/language'
import type { Extension, Range } from '@codemirror/state'
import {
  Decoration,
  EditorView,
  ViewPlugin,
  WidgetType,
  type DecorationSet,
  type ViewUpdate,
} from '@codemirror/view'
import type { SyntaxNodeRef } from '@lezer/common'
import { inlineReplacement } from './inline-replace'

class CheckboxWidget extends WidgetType {
  constructor(readonly checked: boolean) {
    super()
  }

  override eq(other: CheckboxWidget): boolean {
    return other.checked === this.checked
  }

  override toDOM(view: EditorView): HTMLElement {
    const wrap = document.createElement('span')
    wrap.className = 'cm-md-checkbox'
    const box = document.createElement('input')
    box.type = 'checkbox'
    box.checked = this.checked
    box.tabIndex = -1
    box.addEventListener('change', () => toggleTaskAt(view, wrap))
    wrap.appendChild(box)
    return wrap
  }

  // 事件全部放行给 widget 自身（mousedown 不拦、CM 不拿来移动选区）——
  // 否则点击瞬间选区落到该行触发 reveal，widget 在 click 前就被拆回源码
  override ignoreEvent(): boolean {
    return true
  }
}

/** 点击 → 定位所在行 → 三字符替换 [ ]↔[x]（改的是源码） */
function toggleTaskAt(view: EditorView, dom: HTMLElement): void {
  const pos = view.posAtDOM(dom)
  const line = view.state.doc.lineAt(pos)
  const m = /\[( |x|X)\]/.exec(line.text)
  if (!m || m.index === undefined) return
  const from = line.from + m.index
  view.dispatch({
    changes: { from, to: from + 3, insert: m[1] === ' ' ? '[x]' : '[ ]' },
  })
}

/** TaskMarker 的前置 ListMark（语法树上 Task 与 ListMark 是 ListItem 下的兄弟） */
function listMarkBefore(node: SyntaxNodeRef): { from: number; to: number } | null {
  const task = node.node.parent
  const listMark = task?.prevSibling
  if (!listMark || listMark.name !== 'ListMark') return null
  return listMark
}

export function checkboxExtension(): Extension {
  return inlineReplacement({
    nodeNames: ['TaskMarker'],
    create: (node, state) =>
      new CheckboxWidget(state.sliceDoc(node.from, node.to).toLowerCase() === '[x]'),
    range: (node) => {
      // 替换范围含行首 ListMark（`- [ ]` 整段换一个框）；无 ListMark 不渲染
      const listMark = listMarkBefore(node)
      if (!listMark) return null
      return [listMark.from, node.to]
    },
    reveal: 'line',
  })
}

const completedLine = Decoration.line({ class: 'cm-md-completed-item' })

/** 已完成任务项整行半透明 */
export function completedTaskLineExtension(): Extension {
  const build = (view: EditorView): DecorationSet => {
    const ranges: Range<Decoration>[] = []
    for (const { from, to } of view.visibleRanges) {
      syntaxTree(view.state).iterate({
        from,
        to,
        enter: (node) => {
          if (node.name !== 'TaskMarker') return
          const text = view.state.sliceDoc(node.from, node.to).toLowerCase()
          if (text !== '[x]') return
          const line = view.state.doc.lineAt(node.from)
          ranges.push(completedLine.range(line.from))
        },
      })
    }
    return Decoration.set(ranges, true)
  }

  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet

      constructor(view: EditorView) {
        this.decorations = build(view)
      }

      update(update: ViewUpdate): void {
        if (update.docChanged || update.viewportChanged) {
          this.decorations = build(update.view)
        }
      }
    },
    { decorations: (v) => v.decorations },
  )
}
