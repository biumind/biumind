// CM6 编辑器装配：EditorState/EditorView + Compartment（theme / history /
// readOnly），桥接 docChanged 与激活态回调。M1 为源码编辑器形态，
// 内联渲染（decorations/rendering）属 M2。

import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from '@codemirror/commands'
import {
  deleteMarkupBackward,
  insertNewlineContinueMarkup,
  markdown,
} from '@codemirror/lang-markdown'
import {
  Compartment,
  EditorSelection,
  EditorState,
  type Extension,
} from '@codemirror/state'
import { searchKeymap } from '@codemirror/search'
import {
  drawSelection,
  EditorView,
  highlightActiveLine,
  keymap,
} from '@codemirror/view'
import { GFM } from '@lezer/markdown'
import type { Theme } from '../bridge/protocol'
import type { ToolbarActiveState } from '../toolbar/types'
import { computeCm6ActiveState } from './active-state'
import { cm6Commands, isCm6Command } from './commands'
import { toggleBold, toggleItalic } from './commands/inline-format'
import { decorationsExtension } from './decorations'
import { mermaidExtension } from './mermaid'
import { renderingExtension } from './rendering'
import { linkNavigationExtension } from './rendering/links'
import { createThemeExtensions } from './theme'

export interface Cm6EditorOptions {
  parent: HTMLElement
  markdown: string
  theme: Theme
  readOnly: boolean
  /** features.mermaid：挂 mermaid 代码块预览 */
  mermaid: boolean
  /** 文档变化（原文 markdown）；防回环由调用方标志配合 */
  onDocChanged: (markdown: string) => void
  /** 选区/文档变化后的工具栏激活态 */
  onActiveStateChange: (active: ToolbarActiveState) => void
  /** Ctrl/Cmd+点击外链（桥接 navigate:{kind:'external', url}） */
  onNavigate?: (url: string) => void
}

export interface Cm6Handle {
  getMarkdown(): string
  /** setDoc 全量替换；preserveSelection 时选区折算到新文档长度（不跳光标） */
  applyMarkdown(markdown: string, preserveSelection?: boolean): void
  /** 工具栏/桥接命令路由（见 commands/index.ts 命令表） */
  execCommand(name: string): boolean
  computeActiveState(): ToolbarActiveState
  setTheme(theme: Theme): void
  setReadOnly(readOnly: boolean): void
  /** 桥接 command:insertText —— 光标处插入 markdown 片段（附件链路） */
  insertText(text: string): void
  focus(): void
  destroy(): void
}

export function createEditor(options: Cm6EditorOptions): Cm6Handle {
  const themeCompartment = new Compartment()
  const historyCompartment = new Compartment()
  const readOnlyCompartment = new Compartment()

  const extensions: Extension[] = [
    // Mod-b/i 行内格式；列表续行 / 标记退格用 @codemirror/lang-markdown 官方导出（MIT）
    keymap.of([
      { key: 'Mod-b', run: toggleBold },
      { key: 'Mod-i', run: toggleItalic },
      { key: 'Enter', run: insertNewlineContinueMarkup },
      { key: 'Backspace', run: deleteMarkupBackward },
    ]),
    keymap.of(historyKeymap),
    keymap.of(searchKeymap),
    keymap.of([indentWithTab]),
    keymap.of(defaultKeymap),
    markdown({ extensions: [GFM], addKeymap: false }),
    historyCompartment.of(history()),
    drawSelection(),
    highlightActiveLine(),
    EditorView.lineWrapping,
    themeCompartment.of(createThemeExtensions(options.theme)),
    decorationsExtension(), // 行/内联 class 装饰（M2）
    renderingExtension(), // 格式字符隐藏 / checkbox / 链接 / 图片（M2/M3）
    ...(options.mermaid ? [mermaidExtension()] : []),
    ...(options.onNavigate ? [linkNavigationExtension(options.onNavigate)] : []),
    readOnlyCompartment.of(EditorState.readOnly.of(options.readOnly)),
    EditorView.updateListener.of((update) => {
      if (update.docChanged) {
        options.onDocChanged(update.state.doc.toString())
      }
      if (update.docChanged || update.selectionSet) {
        options.onActiveStateChange(computeCm6ActiveState(update.state))
      }
    }),
  ]

  const view = new EditorView({
    parent: options.parent,
    state: EditorState.create({ doc: options.markdown, extensions }),
  })

  return {
    getMarkdown: () => view.state.doc.toString(),

    applyMarkdown(md, preserveSelection = false) {
      const { state } = view
      const changes = { from: 0, to: state.doc.length, insert: md }
      if (preserveSelection) {
        // 全量替换后选区折算：锚点/光标各自钳到新文档长度内（不跳光标）
        const clamp = (pos: number): number => Math.min(pos, md.length)
        view.dispatch({
          changes,
          selection: EditorSelection.create(
            state.selection.ranges.map((r) =>
              EditorSelection.range(clamp(r.anchor), clamp(r.head)),
            ),
            state.selection.mainIndex,
          ),
        })
      } else {
        view.dispatch({ changes })
      }
    },

    execCommand(name) {
      if (!isCm6Command(name)) return false
      return cm6Commands[name](view)
    },

    computeActiveState: () => computeCm6ActiveState(view.state),

    setTheme(theme) {
      view.dispatch({
        effects: themeCompartment.reconfigure(createThemeExtensions(theme)),
      })
    },

    setReadOnly(readOnly) {
      view.dispatch({
        effects: readOnlyCompartment.reconfigure(EditorState.readOnly.of(readOnly)),
      })
    },

    insertText(text) {
      view.dispatch(view.state.replaceSelection(text))
    },

    focus: () => view.focus(),

    destroy: () => view.destroy(),
  }
}
