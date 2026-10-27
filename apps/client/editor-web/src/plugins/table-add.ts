// 表格行列结构操作 + 末尾追加条。
// 结构操作（右键菜单调）：appendTableRow/Column（末尾追加）、
// duplicateTableRow/Column（复制行/列）、clearTableCellContents（清空内容）、
// findTable / columnAtSelection（table-sort 复用的定位帮手）。
// 追加条 UI：下缘「+ 添加行」细条、右缘「+」细条。
// 背景：crepe table-block 行列手柄是 pointermove hover 驱动，触屏点按不
// 触发（无 hover），移动端没有任何增删行列路径；桌面端手柄之外的第二
// 通道是右键菜单（context-menu 表格组）。这里补双端通用、触屏优先的
// 常驻入口：选区在表格内即显示，点按 = 末尾追加（覆盖约八成场景，中间
// 插入走右键菜单六项）。
// 显隐与定位由 main.ts 的 updated / selectionUpdated 监听驱动 sync(ctx)，
// 与 table-stats 汇总条同款自管 fixed 浮层（pointerdown preventDefault
// 保选区）。行条让位汇总条：belowTableOffset 由 main 注入，sync 顺序
// 必须先 tableStats 后 tableAdd。

import './table-add.css'

import { editorStateCtx } from '@milkdown/kit/core'
import type { Ctx } from '@milkdown/kit/ctx'
import type { Node as PMNode, ResolvedPos } from '@milkdown/kit/prose/model'
import { Selection } from '@milkdown/kit/prose/state'
import {
  CellSelection,
  TableMap,
  addColumnAfter,
  addRowAfter,
} from '@milkdown/kit/prose/tables'
import type { EditorView } from '@milkdown/kit/prose/view'
import type { AnchorRect } from '../context-menu/view'

/** 细条尺寸（行条高 / 列条宽，与 table-add.css 保持一致） */
export const ADD_STRIP = 22
const GAP = 4

export interface AddStripLayout {
  row: { left: number; top: number; width: number }
  col: { left: number; top: number; height: number }
}

/** 两条细条的视口定位（纯函数，钳制逻辑单测）。
 *  belowTable = 汇总条占用的高度（含间距），行条让位。 */
export function computeAddStripLayout(
  table: AnchorRect,
  viewport: { width: number; height: number },
  belowTable: number,
): AddStripLayout {
  const tableWidth = table.right - table.left
  const tableHeight = table.bottom - table.top
  const rowWidth = Math.min(tableWidth, viewport.width - 2 * GAP)
  const rowLeft = Math.min(
    Math.max(GAP, table.left),
    Math.max(GAP, viewport.width - rowWidth - GAP),
  )
  const colHeight = Math.min(tableHeight, viewport.height - 2 * GAP)
  const colTop = Math.min(
    Math.max(GAP, table.top),
    Math.max(GAP, viewport.height - colHeight - GAP),
  )
  // 列条优先贴表格外右缘；贴不下（移动端表格常满宽）时叠进表格内右缘
  const outside = table.right + 2
  const colLeft =
    outside + ADD_STRIP <= viewport.width - GAP
      ? outside
      : table.right - ADD_STRIP - 2
  return {
    row: {
      left: rowLeft,
      top: table.bottom + GAP + belowTable,
      width: rowWidth,
    },
    col: { left: colLeft, top: colTop, height: colHeight },
  }
}

/** 末行首格的文档位置（cell 节点前）。TableMap.map[i] 是相对表格内容起点
 *  的偏移：doc 位置 = tablePos + 1 + map.map[i]（prosemirror-tables 内部
 *  tableStart = $cell.start(-1) 同款口径）。 */
export function lastRowCellPos(table: PMNode, tablePos: number): number | null {
  const map = TableMap.get(table)
  if (map.width === 0 || map.height === 0) return null
  return tablePos + 1 + map.map[(map.height - 1) * map.width]
}

/** 首行末格的文档位置（加列选区落点：末列任意格即可，取首行）。 */
export function lastColCellPos(table: PMNode, tablePos: number): number | null {
  const map = TableMap.get(table)
  if (map.width === 0 || map.height === 0) return null
  return tablePos + 1 + map.map[map.width - 1]
}

export interface TableLocation {
  node: PMNode
  /** table 节点在文档中的位置（rectAt 定位、cellPos 推算用） */
  pos: number
}

/** 选区位置向上找 table 节点（table-sort 复用） */
export function findTable($pos: ResolvedPos): TableLocation | null {
  for (let d = $pos.depth; d > 0; d -= 1) {
    if ($pos.node(d).type.name === 'table') {
      return { node: $pos.node(d), pos: $pos.before(d) }
    }
  }
  return null
}

/** 选区落进指定 cell（节点前位置 → 节点内 Selection.near）。 */
function selectCell(view: EditorView, cellPos: number): void {
  const sel = Selection.near(view.state.doc.resolve(cellPos + 1), 1)
  view.dispatch(view.state.tr.setSelection(sel))
}

/** 选区所在列索引（沿 depth 找 table_row，取 cell 在 row 内的 index）。
 *  table-sort 的按列排序同样复用。 */
export function columnAtSelection(view: EditorView): number | null {
  const { $from } = view.state.selection
  for (let d = $from.depth; d > 0; d -= 1) {
    if ($from.node(d).type.name === 'table_row') return $from.index(d)
  }
  return null
}

/** 复制当前行：整行（含内容）克隆插到本行之后，光标落进新行。 */
export function duplicateTableRow(view: EditorView): boolean {
  const { $from } = view.state.selection
  for (let d = $from.depth; d > 0; d -= 1) {
    if ($from.node(d).type.name === 'table_row') {
      const row = $from.node(d)
      const rowPos = $from.before(d)
      const insertAt = rowPos + row.nodeSize
      const tr = view.state.tr.insert(insertAt, row.copy(row.content))
      tr.setSelection(Selection.near(tr.doc.resolve(insertAt + 1), 1))
      view.dispatch(tr.scrollIntoView())
      return true
    }
  }
  return false
}

/** 复制当前列：每行克隆同位置 cell 插到其后（表头行克隆 header 格），
 *  整表 replaceWith 一次 tr、一步撤销。 */
export function duplicateTableColumn(view: EditorView): boolean {
  const found = findTable(view.state.selection.$from)
  if (!found) return false
  const col = columnAtSelection(view)
  if (col === null) return false
  const newRows: PMNode[] = []
  found.node.forEach((row) => {
    if (row.type.name !== 'table_row' || col >= row.childCount) {
      newRows.push(row)
      return
    }
    const cells: PMNode[] = []
    row.forEach((cell) => cells.push(cell))
    cells.splice(col + 1, 0, cells[col].copy(cells[col].content))
    newRows.push(row.type.create(row.attrs, cells, row.marks))
  })
  const newTable = found.node.type.create(
    found.node.attrs,
    newRows,
    found.node.marks,
  )
  const tr = view.state.tr.replaceWith(
    found.pos,
    found.pos + found.node.nodeSize,
    newTable,
  )
  // 光标落进新列同行（原 from 经 mapping 仍指旧列，+1 列宽难精确，
  // 保守落表格内即可）
  const target = Math.min(view.state.selection.from, tr.doc.content.size)
  tr.setSelection(Selection.near(tr.doc.resolve(target), 1))
  view.dispatch(tr.scrollIntoView())
  return true
}

/** 清空内容：CellSelection 清选中全部单元格，文本光标清当前单元格；
 *  只删 cell 内容不动结构（区分于删除行/列）。全部已空返回 false。 */
export function clearTableCellContents(view: EditorView): boolean {
  const { selection } = view.state
  // [from, to) 内容区间（cell 节点内），倒序删除防位置漂移
  const ranges: Array<[number, number]> = []
  if (selection instanceof CellSelection) {
    selection.forEachCell((node, pos) => {
      if (node.content.size > 0) ranges.push([pos + 1, pos + node.nodeSize - 1])
    })
  } else {
    const { $from } = selection
    for (let d = $from.depth; d > 0; d -= 1) {
      const node = $from.node(d)
      if (node.type.name === 'table_cell' || node.type.name === 'table_header') {
        if (node.content.size > 0) {
          const cellPos = $from.before(d)
          ranges.push([cellPos + 1, cellPos + node.nodeSize - 1])
        }
        break
      }
    }
  }
  if (ranges.length === 0) return false
  const tr = view.state.tr
  for (const [from, to] of ranges.sort((a, b) => b[0] - a[0])) {
    tr.delete(from, to)
  }
  view.dispatch(tr)
  return true
}

/** 末尾追加一行：选区先落末行单元格，再走 addRowAfter（合并单元格由
 *  prosemirror-tables 处理）。选区不在表格内返回 false。 */
export function appendTableRow(view: EditorView): boolean {
  const found = findTable(view.state.selection.$from)
  if (!found) return false
  const cellPos = lastRowCellPos(found.node, found.pos)
  if (cellPos === null) return false
  selectCell(view, cellPos)
  return addRowAfter(view.state, view.dispatch)
}

/** 末尾追加一列：选区先落末列单元格，再走 addColumnAfter。 */
export function appendTableColumn(view: EditorView): boolean {
  const found = findTable(view.state.selection.$from)
  if (!found) return false
  const cellPos = lastColCellPos(found.node, found.pos)
  if (cellPos === null) return false
  selectCell(view, cellPos)
  return addColumnAfter(view.state, view.dispatch)
}

export interface TableAddDeps {
  t: (s: string) => string
  /** 节点 pos → 视口矩形（main 注入 view.nodeDOM；测试可桩） */
  rectAt: (ctx: Ctx, pos: number) => AnchorRect | null
  /** 源码模式下隐藏（textarea 场景无 PM 表格语义） */
  isSourceMode?: () => boolean
  /** 只读隐藏（统计条只读可看，追加条是写操作） */
  getReadOnly?: () => boolean
  /** 汇总条占用的下方高度（含间距），行条让位；缺省 0 */
  belowTableOffset?: () => number
  appendRow: () => void
  appendColumn: () => void
}

export interface TableAddController {
  /** 下缘「+ 添加行」细条 */
  rowElement: HTMLElement
  /** 右缘「+」细条 */
  colElement: HTMLElement
  /** 文档/选区变化后重算显隐与定位 */
  sync: (ctx: Ctx) => void
  hide: () => void
  setTranslator: (t: (s: string) => string) => void
  destroy: () => void
}

export function createTableAddController(
  deps: TableAddDeps,
): TableAddController {
  let t = deps.t

  const rowElement = document.createElement('div')
  rowElement.className = 'kc-tadd kc-tadd-row'
  const colElement = document.createElement('div')
  colElement.className = 'kc-tadd kc-tadd-col'

  const renderLabels = (): void => {
    rowElement.textContent = `＋ ${t('Add Row')}`
    colElement.textContent = '＋'
    colElement.title = t('Add Column')
  }
  renderLabels()

  // 点按不抢编辑器焦点（保持 PM 选区），与汇总条/尺寸选择器同款手法
  rowElement.addEventListener('pointerdown', (e) => e.preventDefault())
  colElement.addEventListener('pointerdown', (e) => e.preventDefault())
  rowElement.addEventListener('click', () => deps.appendRow())
  colElement.addEventListener('click', () => deps.appendColumn())

  const hide = (): void => {
    rowElement.style.display = 'none'
    colElement.style.display = 'none'
  }
  hide()

  const sync = (ctx: Ctx): void => {
    if (deps.isSourceMode?.() || deps.getReadOnly?.()) {
      hide()
      return
    }
    const state = ctx.get(editorStateCtx)
    const found = findTable(state.selection.$from)
    if (!found) {
      hide()
      return
    }
    const rect = deps.rectAt(ctx, found.pos)
    if (!rect) {
      hide()
      return
    }
    const layout = computeAddStripLayout(
      rect,
      { width: window.innerWidth, height: window.innerHeight },
      deps.belowTableOffset?.() ?? 0,
    )
    Object.assign(rowElement.style, {
      display: 'flex',
      left: `${layout.row.left}px`,
      top: `${layout.row.top}px`,
      width: `${layout.row.width}px`,
    })
    Object.assign(colElement.style, {
      display: 'flex',
      left: `${layout.col.left}px`,
      top: `${layout.col.top}px`,
      height: `${layout.col.height}px`,
    })
  }

  return {
    rowElement,
    colElement,
    sync,
    hide,
    setTranslator(next) {
      t = next
      renderLabels()
    },
    destroy() {
      hide()
      rowElement.remove()
      colElement.remove()
    },
  }
}
