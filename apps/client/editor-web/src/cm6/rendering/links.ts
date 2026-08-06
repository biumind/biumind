// 链接渲染：Link 节点内 URL 与包裹它的 [、]、(、) LinkMark 全部隐藏，
// 只留 label 文本（链接色 + 下划线由 HighlightStyle tags.link 承担）。
// Image 节点不处理（M3 block-images 独立挂 block widget）；裸 URL
// （Autolink）是可见文本本身，不能隐藏 —— 仅当父节点是 Link 时才替换。
// reveal='active'：光标/选区进入 Link 节点即露出完整源码。
//
// 另附外链点击导航：Ctrl/Cmd+点击直接打开；无修饰键时「二次点击」
// （光标/选区已在 Link 区间内、链接已 reveal）打开 → onNavigate(url)
// （main.ts 桥接为 navigate:{kind:'external', url}，交给 Flutter 开浏览器）。

import { syntaxTree } from '@codemirror/language'
import type { EditorState, Extension } from '@codemirror/state'
import { Decoration, EditorView } from '@codemirror/view'
import type { SyntaxNode } from '@lezer/common'
import { inlineReplacement } from './inline-replace'

export function linksExtension(): ReturnType<typeof inlineReplacement> {
  return inlineReplacement({
    nodeNames: ['LinkMark', 'URL'],
    create: (node) =>
      node.node.parent?.name === 'Link' ? Decoration.replace({}) : null,
    reveal: 'active',
  })
}

/** pos 落在 Link 节点内时返回节点区间与 URL 子节点文本；否则 null（裸 Autolink 不算） */
function linkAt(
  state: EditorState,
  pos: number,
): { from: number; to: number; url: string } | null {
  let node: SyntaxNode | null = syntaxTree(state).resolveInner(pos, 0)
  while (node) {
    if (node.name === 'Link') {
      for (let child = node.firstChild; child; child = child.nextSibling) {
        if (child.name === 'URL') {
          return {
            from: node.from,
            to: node.to,
            url: state.sliceDoc(child.from, child.to),
          }
        }
      }
      return null
    }
    node = node.parent
  }
  return null
}

/** pos 落在 Link 节点内时取其 URL 子节点文本；否则 null（裸 Autolink 不算） */
export function linkUrlAt(state: EditorState, pos: number): string | null {
  return linkAt(state, pos)?.url ?? null
}

/**
 * 点击链接是否应打开（返回 url）：
 *   * Ctrl/Cmd+点击 → 直接打开（桌面第一次点击即可开）
 *   * 无修饰键 → 「二次点击」：当前光标/选区已位于该 Link 区间内
 *     （链接已 reveal）才打开；否则放行，第一次点击 = 落光标露源码。
 * 判定用事件派发前的 state（CM 默认 mousedown 会移动选区）。
 */
export function shouldOpenLink(
  state: EditorState,
  pos: number,
  modKey: boolean,
): string | null {
  const link = linkAt(state, pos)
  if (!link || !/^https?:\/\//.test(link.url)) return null
  if (modKey) return link.url
  for (const range of state.selection.ranges) {
    if (range.from <= link.to && range.to >= link.from) return link.url
  }
  return null
}

/** 点击链接导航：Ctrl/Cmd+点击直接打开；触屏二次点击打开。 */
export function linkNavigationExtension(
  onNavigate: (url: string) => void,
): Extension {
  return EditorView.domEventHandlers({
    mousedown(event, view) {
      const pos = view.posAtCoords({ x: event.clientX, y: event.clientY })
      if (pos === null) return false
      const url = shouldOpenLink(
        view.state,
        pos,
        event.metaKey || event.ctrlKey,
      )
      if (!url) return false
      event.preventDefault()
      onNavigate(url)
      return true
    },
  })
}
