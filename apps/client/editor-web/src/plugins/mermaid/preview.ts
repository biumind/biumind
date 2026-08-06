// Mermaid live preview for code blocks with `language === 'mermaid'`.
//
// Strategy: a ProseMirror plugin that maintains a DecorationSet
// of widget decorations attached *after* each mermaid code block.
// Each widget is a host <div> we render the SVG into asynchronously.
// On every transaction we re-walk the doc, diff against the previous
// (source string -> renderedSvg) cache, and re-render only what
// changed.

import { $prose } from '@milkdown/kit/utils'
import { Plugin, PluginKey } from '@milkdown/kit/prose/state'
import { Decoration, DecorationSet } from '@milkdown/kit/prose/view'
import type { EditorView } from '@milkdown/kit/prose/view'
import type { Node as ProseNode } from '@milkdown/kit/prose/model'

const KEY = new PluginKey<DecorationSet>('kc-mermaid-preview')

type Mermaid = (typeof import('mermaid'))['default']

// mermaid 核心 (~700KB minified) 只在首次真正渲染 mermaid 块时动态加载，
// 避免被静态打进主 chunk。模块级缓存 promise，重复渲染复用同一次加载；
// 加载失败时清空缓存，允许下次重试。
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

interface PreviewWidget {
  source: string
  el: HTMLElement
  renderToken: number
}

const widgets = new Map<HTMLElement, PreviewWidget>()
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
  wrap.dataset.kcMermaid = '1'
  wrap.textContent = '渲染中…'
  const widget: PreviewWidget = { source, el: wrap, renderToken: 0 }
  widgets.set(wrap, widget)
  void renderInto(widget)
  return wrap
}

async function renderInto(widget: PreviewWidget): Promise<void> {
  const myToken = ++nextRenderToken
  widget.renderToken = myToken
  const id = `kc-mermaid-${myToken}`
  try {
    const mermaid = await loadMermaid(
      document.documentElement.classList.contains('dark') ? 'dark' : 'light',
    )
    const { svg } = await mermaid.render(id, widget.source)
    if (widget.renderToken !== myToken) return // newer render started
    widget.el.innerHTML = svg
  } catch (err) {
    if (widget.renderToken !== myToken) return
    widget.el.innerHTML = ''
    const errBox = document.createElement('pre')
    errBox.style.color = 'var(--kc-editor-fg, #b91c1c)'
    errBox.style.whiteSpace = 'pre-wrap'
    errBox.style.margin = '0'
    errBox.textContent = `Mermaid 渲染失败: ${(err as Error).message ?? err}`
    widget.el.appendChild(errBox)
  }
}

function buildDecorations(doc: ProseNode, prevSet: DecorationSet): DecorationSet {
  const decorations: Decoration[] = []
  doc.descendants((node, pos) => {
    if (node.type.name !== 'code_block') return
    const language = (node.attrs as { language?: string }).language ?? ''
    if (language !== 'mermaid') return
    const source = node.textContent
    const after = pos + node.nodeSize
    decorations.push(
      Decoration.widget(after, (_view, _getPos) => {
        // Reuse a previous widget DOM node when possible (preserves
        // any in-flight render and avoids flashes).
        const previousAtPos = prevSet
          .find(after, after)
          .map((d) => d.spec?.['kc-mermaid-el'] as HTMLElement | undefined)
          .filter((el): el is HTMLElement => !!el)[0]
        if (previousAtPos) {
          const w = widgets.get(previousAtPos)
          if (w && w.source !== source) {
            w.source = source
            void renderInto(w)
          }
          return previousAtPos
        }
        return createPreviewElement(source)
      }, {
        side: 1,
        ['kc-mermaid-el' as string]: undefined,
        ignoreSelection: true,
      } as Decoration['spec']),
    )
  })
  return DecorationSet.create(doc, decorations)
}

export const mermaidPreviewPlugin = $prose(() => {
  return new Plugin<DecorationSet>({
    key: KEY,
    state: {
      init: (_config, state) => buildDecorations(state.doc, DecorationSet.empty),
      apply(tr, oldSet, _oldState, newState) {
        if (!tr.docChanged) return oldSet
        return buildDecorations(newState.doc, oldSet)
      },
    },
    props: {
      decorations(state) {
        return KEY.getState(state) ?? null
      },
    },
    view(_view: EditorView) {
      return {
        destroy() {
          widgets.clear()
        },
      }
    },
  })
})
