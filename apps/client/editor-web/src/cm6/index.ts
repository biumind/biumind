// CM6 内核挂载入口：main.ts 在 init payload features.engine === 'cm6' 时
// 经 `await import('./cm6')` 动态加载（懒加载，不进 Milkdown 路径主 chunk）。

import type { Theme } from '../bridge/protocol'
import type { ToolbarActiveState } from '../toolbar/types'
import { createEditor, type Cm6Handle } from './editor'

export type { Cm6Handle } from './editor'

export interface MountCm6Deps {
  container: HTMLElement
  markdown: string
  theme: Theme
  readOnly: boolean
  /** features.mermaid：挂 mermaid 代码块预览 */
  mermaid: boolean
  onDocChanged: (markdown: string) => void
  onActiveStateChange: (active: ToolbarActiveState) => void
  /** Ctrl/Cmd+点击外链 → navigate:{kind:'external', url} */
  onNavigate?: (url: string) => void
}

export function mountCm6Editor(deps: MountCm6Deps): Cm6Handle {
  return createEditor({
    parent: deps.container,
    markdown: deps.markdown,
    theme: deps.theme,
    readOnly: deps.readOnly,
    mermaid: deps.mermaid,
    onDocChanged: deps.onDocChanged,
    onActiveStateChange: deps.onActiveStateChange,
    onNavigate: deps.onNavigate,
  })
}
