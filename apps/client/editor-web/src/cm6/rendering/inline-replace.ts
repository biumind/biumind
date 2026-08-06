// 通用「隐藏/替换 + reveal 策略」ViewPlugin —— Rich Markdown 复刻核心
// （机制参考 Joplin ReplacementExtension，实现全部自写；Joplin 为 AGPL，
// 不复制其代码）。
//
// 契约：给出语法树节点名集合 + create/range/reveal 三钩子，插件负责在
// docChanged || viewportChanged || selectionSet 时遍历可见范围语法树，
// 重建 DecorationSet。reveal 语义：
//   'line'   — 光标与节点同行、或选区碰到替换区间 → 不替换（露源码）
//   'active' — 选区碰到替换区间、或碰到节点的父节点 → 露源码
// 跨行替换区间一律跳过（防御）；结果 Decoration.set(ranges, true)。

import { syntaxTree } from '@codemirror/language'
import type { EditorState, Extension, Range } from '@codemirror/state'
import {
  Decoration,
  EditorView,
  ViewPlugin,
  WidgetType,
  type DecorationSet,
  type ViewUpdate,
} from '@codemirror/view'
import type { SyntaxNodeRef } from '@lezer/common'

export type RevealMode = 'line' | 'active'

export interface InlineReplacement {
  /** 命中的语法树节点名集合 */
  nodeNames: string[]
  /** 返回替换装饰或 widget；返回 null 表示该节点不替换 */
  create(node: SyntaxNodeRef, state: EditorState): Decoration | WidgetType | null
  /** 自定义替换区间；默认 [node.from, node.to]，返回 null 跳过 */
  range?(node: SyntaxNodeRef, state: EditorState): [number, number] | null
  /** 露源码策略，默认 'active' */
  reveal?: RevealMode
}

/** 选区（含光标）是否碰到 [from, to] 区间 */
function selectionTouches(state: EditorState, from: number, to: number): boolean {
  for (const range of state.selection.ranges) {
    if (range.from <= to && range.to >= from) return true
  }
  return false
}

function shouldReveal(
  mode: RevealMode,
  node: SyntaxNodeRef,
  state: EditorState,
  from: number,
  to: number,
): boolean {
  if (mode === 'line') {
    if (selectionTouches(state, from, to)) return true
    for (const range of state.selection.ranges) {
      const line = state.doc.lineAt(range.head)
      if (line.from <= from && to <= line.to) return true
    }
    return false
  }
  // active：碰替换区间或父节点区间
  if (selectionTouches(state, from, to)) return true
  const parent = node.node.parent
  return !!parent && selectionTouches(state, parent.from, parent.to)
}

export function inlineReplacement(spec: InlineReplacement): Extension {
  const nameSet = new Set(spec.nodeNames)
  const revealMode = spec.reveal ?? 'active'

  const build = (view: EditorView): DecorationSet => {
    const ranges: Range<Decoration>[] = []
    for (const { from: vFrom, to: vTo } of view.visibleRanges) {
      syntaxTree(view.state).iterate({
        from: vFrom,
        to: vTo,
        enter: (node) => {
          if (!nameSet.has(node.name)) return
          const created = spec.create(node, view.state)
          if (!created) return
          const r = spec.range
            ? spec.range(node, view.state)
            : ([node.from, node.to] as [number, number])
          if (!r) return
          const [from, to] = r
          if (from >= to) return
          // 跨行替换区间跳过（防御）
          if (view.state.doc.lineAt(from).number !== view.state.doc.lineAt(to).number) return
          if (shouldReveal(revealMode, node, view.state, from, to)) return
          const decoration =
            created instanceof WidgetType
              ? Decoration.replace({ widget: created })
              : created
          ranges.push(decoration.range(from, to))
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
        if (update.docChanged || update.viewportChanged || update.selectionSet) {
          this.decorations = build(update.view)
        }
      }
    },
    { decorations: (v) => v.decorations },
  )
}
