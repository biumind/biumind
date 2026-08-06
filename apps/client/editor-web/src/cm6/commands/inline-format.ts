// 行内格式切换：选区包/解 `**` `*` `~~` `` ` `` 标记。
// 行为约定（M1 范围，全部自写实现）：
//   * 选区已包标记（标记在选区外紧贴或含在选区内）→ 去除
//   * 未包 → 包裹
//   * 空选区 → 插一对标记，光标居中
//   * 光标在标记尾部（**foo|**）→ 跳出到标记外
// 多行选区按行内首末处理，不做块级升级。

import { EditorSelection } from '@codemirror/state'
import type { Command } from '@codemirror/view'

/** 光标前（同一行内）是否存在未闭合的开标记：光标紧贴闭标记内侧时用于「跳出」判断 */
function hasOpenMarkerBefore(line: string, marker: string): boolean {
  // 行内光标前的文本里出现奇数个标记 ≈ 存在开标记（M1 简化判定）
  let count = 0
  let idx = line.indexOf(marker)
  while (idx !== -1) {
    count += 1
    idx = line.indexOf(marker, idx + marker.length)
  }
  return count % 2 === 1
}

export function toggleInlineMarkup(marker: string): Command {
  return (view) => {
    const { state } = view
    const m = marker.length
    const changes: { from: number; to?: number; insert?: string }[] = []
    const newRanges: { anchor: number; head: number }[] = []
    // 多选区：标记检查一律读原文坐标，写出的 changes 用 delta 折算到新坐标
    let delta = 0

    for (const range of state.selection.ranges) {
      if (range.empty) {
        const pos = range.head
        if (state.sliceDoc(pos, pos + m) === marker) {
          const line = state.doc.lineAt(pos)
          const beforeInLine = state.sliceDoc(line.from, pos)
          if (hasOpenMarkerBefore(beforeInLine, marker)) {
            // 光标在标记尾部 → 跳出（不动文档）
            newRanges.push({ anchor: pos + m + delta, head: pos + m + delta })
            continue
          }
        }
        // 空选区 → 插一对标记，光标居中
        changes.push({ from: pos + delta, insert: marker + marker })
        newRanges.push({ anchor: pos + m + delta, head: pos + m + delta })
        delta += 2 * m
        continue
      }

      const { from, to } = range
      const outsideOpen = from - m >= 0 && state.sliceDoc(from - m, from) === marker
      const outsideClose = state.sliceDoc(to, to + m) === marker
      const insideOpen = state.sliceDoc(from, from + m) === marker
      const insideClose = to - m >= from && state.sliceDoc(to - m, to) === marker

      if (outsideOpen && outsideClose) {
        // 标记紧贴选区外侧 → 去除（changes 按位置升序）
        changes.push(
          { from: from - m + delta, to: from + delta },
          { from: to + delta, to: to + m + delta },
        )
        newRanges.push({ anchor: from - m + delta, head: to - m + delta })
        delta -= 2 * m
      } else if (insideOpen && insideClose && to - from >= 2 * m) {
        // 选区本身含标记 → 去除
        changes.push(
          { from: from + delta, to: from + m + delta },
          { from: to - m + delta, to: to + delta },
        )
        newRanges.push({ anchor: from + delta, head: to - 2 * m + delta })
        delta -= 2 * m
      } else {
        // 包裹
        changes.push({ from: from + delta, insert: marker }, { from: to + delta, insert: marker })
        newRanges.push({ anchor: from + m + delta, head: to + m + delta })
        delta += 2 * m
      }
    }

    if (newRanges.length === 0) return false
    view.dispatch({
      changes,
      selection: EditorSelection.create(
        newRanges.map((r) => EditorSelection.range(r.anchor, r.head)),
      ),
      scrollIntoView: true,
    })
    return true
  }
}

export const toggleBold = toggleInlineMarkup('**')
export const toggleItalic = toggleInlineMarkup('*')
export const toggleStrikethrough = toggleInlineMarkup('~~')
export const toggleInlineCode = toggleInlineMarkup('`')
