// CM6 版工具栏选中态：光标行正则（块级）+ syntaxTree 祖先链（行内/容器），
// 输出 ToolbarActiveState 12 布尔（与 PM 版输出类型一致，类型见 toolbar/types.ts）。
// 纯函数、不依赖 DOM，EditorState 可在 node 下直接构造测试。

import { syntaxTree } from '@codemirror/language'
import type { EditorState } from '@codemirror/state'
import type { SyntaxNode } from '@lezer/common'
import { INACTIVE_STATE, type ToolbarActiveState } from '../toolbar/types'

const HEADING_RE = /^(#{1,6})\s/
const QUOTE_RE = /^> ?/
const TASK_RE = /^\s*[-*+] \[[ xX]\]\s/
const BULLET_RE = /^\s*[-*+]\s/
const ORDERED_RE = /^\s*\d+[.)]\s/

export function computeCm6ActiveState(state: EditorState): ToolbarActiveState {
  const head = state.selection.main.head
  const line = state.doc.lineAt(head)
  const text = line.text

  // syntaxTree 祖先链：行内标记 + 容器块（代码围栏 / 引用 / 标题）
  let strong = false
  let emphasis = false
  let strikeThrough = false
  let inlineCode = false
  let codeBlock = false
  let inBlockquote = false
  let headingLevel = 0
  let node: SyntaxNode | null = syntaxTree(state).resolveInner(head, 0)
  while (node) {
    switch (node.name) {
      case 'StrongEmphasis':
        strong = true
        break
      case 'Emphasis':
        emphasis = true
        break
      case 'Strikethrough':
        strikeThrough = true
        break
      case 'InlineCode':
        inlineCode = true
        break
      case 'FencedCode':
      case 'CodeBlock':
        codeBlock = true
        break
      case 'Blockquote':
        inBlockquote = true
        break
      default:
        break
    }
    const heading = /^ATXHeading([1-6])$/.exec(node.name)
    if (heading) headingLevel = Number(heading[1])
    node = node.parent
  }

  // 光标行正则兜底：语法树未解析到时块级判定仍可用
  if (headingLevel === 0) {
    const m = text.match(HEADING_RE)
    if (m) headingLevel = m[1].length
  }
  const taskList = TASK_RE.test(text)
  const bulletList = taskList || BULLET_RE.test(text)

  return {
    ...INACTIVE_STATE,
    strong,
    emphasis,
    strikeThrough,
    inlineCode,
    h1: headingLevel === 1,
    h2: headingLevel === 2,
    h3: headingLevel === 3,
    blockquote: inBlockquote || QUOTE_RE.test(text),
    bulletList,
    orderedList: ORDERED_RE.test(text),
    taskList,
    codeBlock,
  }
}
