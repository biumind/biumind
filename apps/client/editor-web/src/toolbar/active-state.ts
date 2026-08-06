// 工具栏按钮选中态：根据 ProseMirror 选区计算当前格式上下文（加粗、标题、列表等）。
// 纯函数，不依赖 Crepe，方便单测。

import type { EditorState } from '@milkdown/kit/prose/state'
import { type ToolbarActiveState } from './types'

// 类型与零值上移到 ./types（与 CM6 版共用），此处 re-export 保持既有引用不变。
export { INACTIVE_STATE, type ToolbarActiveState } from './types'

export function computeActiveState(state: EditorState): ToolbarActiveState {
  const { selection, schema, storedMarks, doc } = state
  const { $from, from, to, empty } = selection

  const markActive = (name: string): boolean => {
    const mark = schema.marks[name]
    if (!mark) return false
    // 光标态看 storedMarks / 当前位置 marks；选区态看范围内是否带 mark
    if (empty) return !!mark.isInSet(storedMarks ?? $from.marks())
    return doc.rangeHasMark(from, to, mark)
  }

  const hasAncestor = (
    name: string,
    pred?: (attrs: Record<string, unknown>) => boolean,
  ): boolean => {
    for (let depth = $from.depth; depth > 0; depth -= 1) {
      const node = $from.node(depth)
      if (node.type.name === name && (!pred || pred(node.attrs))) return true
    }
    return false
  }

  let headingLevel = 0
  for (let depth = $from.depth; depth > 0; depth -= 1) {
    const node = $from.node(depth)
    if (node.type.name === 'heading') {
      headingLevel = node.attrs.level as number
      break
    }
  }

  return {
    strong: markActive('strong'),
    emphasis: markActive('emphasis'),
    strikeThrough: markActive('strike_through'),
    inlineCode: markActive('inlineCode'),
    h1: headingLevel === 1,
    h2: headingLevel === 2,
    h3: headingLevel === 3,
    blockquote: hasAncestor('blockquote'),
    bulletList: hasAncestor('bullet_list'),
    orderedList: hasAncestor('ordered_list'),
    // gfm 任务项是带 checked 属性的 list_item
    taskList: hasAncestor(
      'list_item',
      (attrs) => attrs.checked !== null && attrs.checked !== undefined,
    ),
    codeBlock: hasAncestor('code_block'),
  }
}
