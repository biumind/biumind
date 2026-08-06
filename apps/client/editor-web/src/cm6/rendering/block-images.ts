// 独占行图片 → 源码行下方挂 block <img> widget（M3）。
// 行为约定（定为）：源码行始终保留（光标离开也不隐藏，行内格式字符照常
// 由 format-marks 隐藏），图挂在源码行下方 block 位 —— 编辑 URL 不用先
// 点掉图。高度走模块级 LRU 缓存（key=src+width），渲染期间先撑 minHeight
// 防滚动跳动；加载失败显示轻量错误占位。实现全部自写。

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
import type { SyntaxNodeRef } from '@lezer/common'

const HEIGHT_CACHE_LIMIT = 500

// 模块级 LRU：key = `${src}|${width}`，value = 上次渲染高度
const heightCache = new Map<string, number>()
// estimatedHeight 没有 view 上下文拿不到 width，另存 src → 最近高度兜底
const lastHeightBySrc = new Map<string, number>()

function cacheGet(key: string): number | undefined {
  const value = heightCache.get(key)
  if (value !== undefined) {
    // 命中提到最新位
    heightCache.delete(key)
    heightCache.set(key, value)
  }
  return value
}

function cacheSet(key: string, src: string, value: number): void {
  heightCache.delete(key)
  heightCache.set(key, value)
  lastHeightBySrc.set(src, value)
  if (heightCache.size > HEIGHT_CACHE_LIMIT) {
    const oldest = heightCache.keys().next().value
    if (oldest !== undefined) heightCache.delete(oldest)
  }
}

/** Image 节点信息：src 与 alt；非 http(s) src 返回 null */
function imageInfo(
  node: SyntaxNodeRef,
  state: EditorState,
): { src: string; alt: string } | null {
  let src = ''
  for (let child = node.node.firstChild; child; child = child.nextSibling) {
    if (child.name === 'URL') src = state.sliceDoc(child.from, child.to)
  }
  if (!/^https?:\/\//.test(src)) return null
  // alt = 标签文本（![alt] 中括号之间）
  const raw = state.sliceDoc(node.from, node.to)
  const altMatch = /^!\[([^\]]*)\]/.exec(raw)
  return { src, alt: altMatch?.[1] ?? '' }
}

/** 独占行判定：行内除该节点外无其他非空白字符 */
function isStandaloneLine(node: SyntaxNodeRef, state: EditorState): boolean {
  const line = state.doc.lineAt(node.from)
  if (line.to !== state.doc.lineAt(node.to).to) return false
  const before = state.sliceDoc(line.from, node.from)
  const after = state.sliceDoc(node.to, line.to)
  return before.trim() === '' && after.trim() === ''
}

class BlockImageWidget extends WidgetType {
  constructor(
    readonly src: string,
    readonly alt: string,
  ) {
    super()
  }

  override eq(other: BlockImageWidget): boolean {
    return other.src === this.src && other.alt === this.alt
  }

  override get estimatedHeight(): number {
    return lastHeightBySrc.get(this.src) ?? -1
  }

  private cacheKey(width: number): string {
    return `${this.src}|${width}`
  }

  override toDOM(view: EditorView): HTMLElement {
    const wrap = document.createElement('div')
    wrap.className = 'cm-md-image'
    const width = view.contentDOM.clientWidth
    const cached = cacheGet(this.cacheKey(width))
    if (cached !== undefined && cached > 0) {
      // 命中高度缓存：加载期间先撑住，防滚动跳动
      wrap.style.minHeight = `${cached}px`
    }
    const img = document.createElement('img')
    img.src = this.src
    img.alt = this.alt
    img.addEventListener('load', () => {
      if (img.offsetHeight > 0) cacheSet(this.cacheKey(width), this.src, img.offsetHeight)
      wrap.style.minHeight = ''
    })
    img.addEventListener('error', () => {
      wrap.style.minHeight = ''
      wrap.textContent = ''
      const err = document.createElement('div')
      err.className = 'cm-md-image-error'
      err.textContent = this.alt
        ? `图片加载失败：${this.alt}`
        : '图片加载失败'
      wrap.appendChild(err)
    })
    // 点击图片 = 光标落到该图源码行行首
    wrap.addEventListener('mousedown', (event) => {
      event.preventDefault()
      const pos = view.posAtDOM(wrap)
      const line = view.state.doc.lineAt(pos)
      view.dispatch({ selection: { anchor: line.from } })
      view.focus()
    })
    wrap.appendChild(img)
    return wrap
  }

  // 事件交给 widget 自身（mousedown 我们自己处理并 preventDefault）
  override ignoreEvent(event: Event): boolean {
    return event.type === 'mousedown' || event.type === 'click'
  }
}

// block widget 不能经 ViewPlugin 提供（@codemirror/view 对动态装饰抛
// "Block decorations may not be specified via plugins"）——走 StateField +
// EditorView.decorations。遍历整篇文档（图片稀少，笔记体量下廉价），
// 光标不影响显隐：仅文档或语法树推进时重建。
const blockImagesField = StateField.define<DecorationSet>({
  create: (state) => buildImageDeco(state),
  update: (value, tr) => {
    if (tr.docChanged || syntaxTree(tr.state) !== syntaxTree(tr.startState)) {
      return buildImageDeco(tr.state)
    }
    return value
  },
  provide: (field) => EditorView.decorations.from(field),
})

function buildImageDeco(state: EditorState): DecorationSet {
  const ranges: Range<Decoration>[] = []
  syntaxTree(state).iterate({
    enter: (node) => {
      if (node.name !== 'Image') return
      if (!isStandaloneLine(node, state)) return
      const info = imageInfo(node, state)
      if (!info) return
      const line = state.doc.lineAt(node.from)
      ranges.push(
        Decoration.widget({
          widget: new BlockImageWidget(info.src, info.alt),
          block: true,
          side: 1,
        }).range(line.to),
      )
    },
  })
  return Decoration.set(ranges, true)
}

export function blockImagesExtension(): Extension {
  return blockImagesField
}
