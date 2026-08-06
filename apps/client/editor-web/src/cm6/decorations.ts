// class 装饰（始终生效，无 reveal）：遍历可见范围语法树，给行/内联区间挂
// class，样式全部在 theme.ts。多行块（FencedCode）首末行加 -first/-last
// 后缀用于圆角闭合。
// 坑（照记）：喂 RangeSetBuilder 前必须先按 pos、再按 length 排序。

import { syntaxTree } from '@codemirror/language'
import { RangeSetBuilder, type Extension } from '@codemirror/state'
import {
  Decoration,
  EditorView,
  ViewPlugin,
  type DecorationSet,
  type ViewUpdate,
} from '@codemirror/view'
import type { SyntaxNodeRef } from '@lezer/common'

interface DecoItem {
  from: number
  to: number
  deco: Decoration
}

const HEADING_RE = /^ATXHeading([1-6])$/

export function decorationsExtension(): Extension {
  const build = (view: EditorView): DecorationSet => {
    const items: DecoItem[] = []

    // 给节点覆盖的每一行挂 line class；firstLast 时首末行加 -first/-last
    const lineDeco = (
      node: SyntaxNodeRef,
      cls: string,
      firstLast = false,
      extra = '',
    ): void => {
      const fromLine = view.state.doc.lineAt(node.from)
      const toLine = view.state.doc.lineAt(node.to)
      for (let n = fromLine.number; n <= toLine.number; n += 1) {
        let clsName = cls
        if (firstLast) {
          if (n === fromLine.number) clsName += ` ${cls}-first`
          if (n === toLine.number) clsName += ` ${cls}-last`
        }
        if (extra) clsName += ` ${extra}`
        const pos = view.state.doc.line(n).from
        items.push({ from: pos, to: pos, deco: Decoration.line({ class: clsName }) })
      }
    }

    const markDeco = (node: SyntaxNodeRef, cls: string): void => {
      items.push({
        from: node.from,
        to: node.to,
        deco: Decoration.mark({ class: cls }),
      })
    }

    for (const { from, to } of view.visibleRanges) {
      syntaxTree(view.state).iterate({
        from,
        to,
        enter: (node) => {
          const heading = HEADING_RE.exec(node.name)
          if (heading) {
            lineDeco(node, `cm-h${heading[1]}`, false, 'cm-headerLine')
            return
          }
          switch (node.name) {
            case 'FencedCode':
              lineDeco(node, 'cm-codeBlock', true)
              break
            case 'Blockquote':
              lineDeco(node, 'cm-blockQuote')
              break
            case 'BulletList':
              lineDeco(node, 'cm-unorderedList')
              break
            case 'OrderedList':
              lineDeco(node, 'cm-orderedList')
              break
            case 'ListItem':
              lineDeco(node, 'cm-listItem')
              break
            case 'InlineCode':
              markDeco(node, 'cm-inlineCode')
              break
            case 'URL':
              markDeco(node, 'cm-url')
              break
            case 'Strikethrough':
              markDeco(node, 'cm-strike')
              break
            case 'HorizontalRule':
              markDeco(node, 'cm-hr')
              break
            default:
              break
          }
        },
      })
    }

    // 坑：先按 pos 再按 length 排序，再喂 RangeSetBuilder
    items.sort((a, b) => a.from - b.from || a.to - a.from - (b.to - b.from))
    const builder = new RangeSetBuilder<Decoration>()
    for (const item of items) builder.add(item.from, item.to, item.deco)
    return builder.finish()
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
