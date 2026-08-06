// Mermaid 代码块预览（CM6 版）：语言为 mermaid 的 FencedCode 块尾挂 block
// widget 容器，异步渲染 SVG。行为与样式对齐 Milkdown 版
// plugins/mermaid/preview.ts：mermaid 动态 import + 模块级 promise 缓存 +
// 失败显示错误文本。光标进入代码块行范围时隐藏预览（方便编辑），离开恢复。

import { syntaxTree } from '@codemirror/language'
import {
  StateField,
  type EditorState,
  type Extension,
  type Range,
} from '@codemirror/state'
import {
  Decoration,
  EditorView,
  WidgetType,
  type DecorationSet,
} from '@codemirror/view'
import type { SyntaxNode, SyntaxNodeRef } from '@lezer/common'

type Mermaid = (typeof import('mermaid'))['default']

// mermaid 核心只在首次真正渲染 mermaid 块时动态加载；模块级缓存 promise，
// 重复渲染复用同一次加载；加载失败时清空缓存，允许下次重试。
let mermaidModulePromise: Promise<Mermaid> | null = null
let mermaidInitialized = false

function loadMermaid(theme: 'light' | 'dark'): Promise<Mermaid> {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid').then(
      (mod) => mod.default,
      (err: unknown) => {
        mermaidModulePromise = null
        throw err
      },
    )
  }
  return mermaidModulePromise.then((mermaid) => {
    if (!mermaidInitialized) {
      mermaidInitialized = true
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: theme === 'dark' ? 'dark' : 'default',
      })
    }
    return mermaid
  })
}

let nextRenderToken = 0

function createPreviewElement(source: string): HTMLElement {
  const wrap = document.createElement('div')
  wrap.className = 'kc-mermaid-preview'
  wrap.contentEditable = 'false'
  wrap.style.margin = '8px 0 16px'
  wrap.style.padding = '12px'
  wrap.style.background = 'var(--kc-editor-code-bg, #f6f8fa)'
  wrap.style.border = '1px dashed var(--kc-editor-border, #d0d7de)'
  wrap.style.borderRadius = '8px'
  wrap.style.overflowX = 'auto'
  wrap.style.fontSize = '14px'
  wrap.textContent = '渲染中…'
  void renderInto(wrap, source)
  return wrap
}

async function renderInto(el: HTMLElement, source: string): Promise<void> {
  const id = `kc-mermaid-cm6-${++nextRenderToken}`
  const token = nextRenderToken
  try {
    const mermaid = await loadMermaid(
      document.documentElement.classList.contains('dark') ? 'dark' : 'light',
    )
    const { svg } = await mermaid.render(id, source)
    if (token !== nextRenderToken || !el.isConnected) return
    el.innerHTML = svg
  } catch (err) {
    if (!el.isConnected) return
    el.innerHTML = ''
    const errBox = document.createElement('pre')
    errBox.style.color = 'var(--kc-editor-fg, #b91c1c)'
    errBox.style.whiteSpace = 'pre-wrap'
    errBox.style.margin = '0'
    errBox.textContent = `Mermaid 渲染失败: ${(err as Error).message ?? err}`
    el.appendChild(errBox)
  }
}

class MermaidWidget extends WidgetType {
  constructor(readonly source: string) {
    super()
  }

  override eq(other: MermaidWidget): boolean {
    return other.source === this.source
  }

  override toDOM(): HTMLElement {
    return createPreviewElement(this.source)
  }
}

/** FencedCode 的语言标记（CodeInfo）是否为 mermaid；是则返回代码文本（不含围栏行） */
function mermaidSource(node: SyntaxNodeRef, state: EditorState): string | null {
  let info = ''
  let codeFrom = -1
  let codeTo = -1
  for (let child: SyntaxNode | null = node.node.firstChild; child; child = child.nextSibling) {
    if (child.name === 'CodeInfo') info = state.sliceDoc(child.from, child.to).trim()
    if (child.name === 'CodeText') {
      if (codeFrom === -1) codeFrom = child.from
      codeTo = child.to
    }
  }
  if (info !== 'mermaid') return null
  return codeFrom === -1 ? '' : state.sliceDoc(codeFrom, codeTo)
}

// block widget 不能经 ViewPlugin 提供（@codemirror/view 对动态装饰抛
// "Block decorations may not be specified via plugins"）——走 StateField +
// EditorView.decorations。光标/选区进入代码块时不挂预览（方便编辑），
// 文档 / 选区 / 语法树任一变化即重建（遍历整篇，笔记体量下廉价）。
const mermaidField = StateField.define<DecorationSet>({
  create: (state) => buildMermaidDeco(state),
  update: (value, tr) => {
    if (
      tr.docChanged ||
      tr.selection ||
      syntaxTree(tr.state) !== syntaxTree(tr.startState)
    ) {
      return buildMermaidDeco(tr.state)
    }
    return value
  },
  provide: (field) => EditorView.decorations.from(field),
})

function buildMermaidDeco(state: EditorState): DecorationSet {
  const ranges: Range<Decoration>[] = []
  syntaxTree(state).iterate({
    enter: (node) => {
      if (node.name !== 'FencedCode') return
      const source = mermaidSource(node, state)
      if (source === null) return
      // 光标/选区在代码块内 → 不挂预览（显示纯源码方便编辑）
      let touched = false
      for (const range of state.selection.ranges) {
        if (range.from <= node.to && range.to >= node.from) {
          touched = true
          break
        }
      }
      if (touched) return
      const line = state.doc.lineAt(node.to)
      ranges.push(
        Decoration.widget({
          widget: new MermaidWidget(source),
          block: true,
          side: 1,
        }).range(line.to),
      )
    },
  })
  return Decoration.set(ranges, true)
}

export function mermaidExtension(): Extension {
  return mermaidField
}
