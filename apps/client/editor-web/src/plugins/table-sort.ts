// 表格按列排序：以选区所在列为键重排数据行（首行表头恒不动）。
// 语义对齐 Excel / Google Docs：
//   - 数值感知：能解析成数字的按数值排（千分位/货币/全角等同 table-stats
//     的 parseCellNumber 口径），否则按文本 localeCompare（zh-Hans, numeric）
//   - 同列数字与文本混合时数字排在文本前；空单元格无论升降序恒沉底
//   - 稳定排序（相等键保持原行序）
// 写 AST 但 markdown 往返零影响：排序只是重排 table_row，GFM 原样序列化。
// 入口在右键菜单表格组（移动端经 inTable 同场），一次 tr 完成、一步撤销。

import type { Node as PMNode } from '@milkdown/kit/prose/model'
import { Selection } from '@milkdown/kit/prose/state'
import type { EditorView } from '@milkdown/kit/prose/view'
import { findTable } from './table-add'
import { parseCellNumber } from './table-stats'

export type SortKey =
  | { kind: 'number'; value: number }
  | { kind: 'text'; value: string }
  | { kind: 'empty' }

/** 单元格文本 → 排序键（空串 = empty，恒沉底）。 */
export function cellSortKey(text: string): SortKey {
  const trimmed = text.trim()
  if (!trimmed) return { kind: 'empty' }
  const n = parseCellNumber(trimmed)
  if (n !== null) return { kind: 'number', value: n }
  return { kind: 'text', value: trimmed }
}

/** 键比较：empty 恒最后（不受升降序影响），number 在 text 前。 */
export function compareSortKeys(
  a: SortKey,
  b: SortKey,
  ascending: boolean,
): number {
  if (a.kind === 'empty' && b.kind === 'empty') return 0
  if (a.kind === 'empty') return 1
  if (b.kind === 'empty') return -1
  let cmp: number
  if (a.kind === 'number' && b.kind === 'number') {
    cmp = a.value - b.value
  } else if (a.kind === 'number') {
    cmp = -1
  } else if (b.kind === 'number') {
    cmp = 1
  } else {
    cmp = a.value.localeCompare(b.value, 'zh-Hans', { numeric: true })
  }
  return ascending ? cmp : -cmp
}

/** 数据行的排序置换（纯函数单测）：返回排后顺序（元素 = 原行索引）。 */
export function sortRowOrder(
  columnTexts: string[],
  ascending: boolean,
): number[] {
  const keys = columnTexts.map(cellSortKey)
  return columnTexts
    .map((_, i) => i)
    .sort((ia, ib) => compareSortKeys(keys[ia], keys[ib], ascending))
}

/** 选区所在列索引（沿 depth 找 table_row，取 cell 在 row 内的 index）。 */
function columnAtSelection(view: EditorView): number | null {
  const { $from } = view.state.selection
  for (let d = $from.depth; d > 0; d -= 1) {
    if ($from.node(d).type.name === 'table_row') return $from.index(d)
  }
  return null
}

/** 按选区所在列排序数据行。选区不在表格内或数据行不足两行返回 false。 */
export function sortTableByColumn(
  view: EditorView,
  ascending: boolean,
): boolean {
  const found = findTable(view.state.selection.$from)
  if (!found) return false
  const col = columnAtSelection(view)
  if (col === null) return false

  const rows: PMNode[] = []
  found.node.forEach((child) => {
    if (child.type.name === 'table_row') rows.push(child)
  })
  if (rows.length < 3) return false // 表头 + 至少两行数据才有排序意义

  const header = rows[0]
  const dataRows = rows.slice(1)
  const texts = dataRows.map((row) =>
    col < row.childCount ? row.child(col).textContent.trim() : '',
  )
  const order = sortRowOrder(texts, ascending)
  // 顺序没变就不发 tr（避免无意义 undo 步）
  if (order.every((v, i) => v === i)) return false

  const sorted = [header, ...order.map((i) => dataRows[i])]
  const newTable = found.node.type.create(
    found.node.attrs,
    sorted,
    found.node.marks,
  )
  const tr = view.state.tr.replaceWith(
    found.pos,
    found.pos + found.node.nodeSize,
    newTable,
  )
  // 光标落到排序列的首个数据行（视觉反馈：列首即最小/最大值）
  const headerSize = header.nodeSize
  const target = Math.min(
    found.pos + 1 + headerSize + 1,
    tr.doc.content.size,
  )
  tr.setSelection(Selection.near(tr.doc.resolve(target), 1))
  view.dispatch(tr.scrollIntoView())
  return true
}
