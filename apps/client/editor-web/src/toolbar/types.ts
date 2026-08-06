// 工具栏选中态的公共类型：Crepe（ProseMirror）与 CM6 两套 active-state
// 实现共用，放在独立文件避免 CM6 反向依赖 PM 版实现。

export interface ToolbarActiveState {
  strong: boolean
  emphasis: boolean
  strikeThrough: boolean
  inlineCode: boolean
  h1: boolean
  h2: boolean
  h3: boolean
  blockquote: boolean
  bulletList: boolean
  orderedList: boolean
  taskList: boolean
  codeBlock: boolean
}

export const INACTIVE_STATE: ToolbarActiveState = {
  strong: false,
  emphasis: false,
  strikeThrough: false,
  inlineCode: false,
  h1: false,
  h2: false,
  h3: false,
  blockquote: false,
  bulletList: false,
  orderedList: false,
  taskList: false,
  codeBlock: false,
}
