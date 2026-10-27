// 表格统计：选区所在表格的按列只读统计（计数/求和/均值/最大/最小）。
// 不写 AST —— 纯计算展示，markdown 往返零影响（GFM 无公式语法，
// 计算结果落文档会被 remark-stringify 冲掉，故只做查看型）。
// 两种形态共存，均自管 fixed 浮层，不改 crepe table-tooltip：
//   1. 底部汇总条：选区在表格内即显，按列压缩展示
//   2. 列选浮片：CellSelection 整列选中时在该列上方出详情
// 显隐由 main.ts 的 updated / selectionUpdated 监听驱动 sync(ctx)。

import './table-stats.css'

import { editorStateCtx, editorViewCtx } from '@milkdown/kit/core'
import { CellSelection } from '@milkdown/kit/prose/tables'
import type { Ctx } from '@milkdown/kit/ctx'
import type { Node as PMNode, ResolvedPos } from '@milkdown/kit/prose/model'
import type { Selection } from '@milkdown/kit/prose/state'
import type { AnchorRect } from '../context-menu/view'

export interface ColumnStats {
  /** 非空单元格数（不含表头行） */
  count: number
  /** 数值解析成功的单元格数；0 = 非数值列 */
  numericCount: number
  sum: number
  min: number
  max: number
}

export interface TableStatsData {
  /** 表头文案（首行），渲染时空表头回退「列 N」 */
  headers: string[]
  columns: ColumnStats[]
}

const ROW_NODE = 'table_row'
const CELL_NODES = new Set(['table_cell', 'table_header'])

/** 单元格文本 → 数值。容忍全角数字/千分位/货币符号/百分号/括号负数。 */
export function parseCellNumber(raw: string): number | null {
  let s = raw.trim()
  if (!s) return null
  // 全角 → 半角（数字 + 小数点）
  s = s.replace(/[０-９]/g, (ch) =>
    String.fromCharCode(ch.charCodeAt(0) - 0xfee0),
  )
  s = s.replace(/．/g, '.')
  // 千分位 / 货币符号 / 空白
  s = s.replace(/[,，\s]/g, '').replace(/[¥￥$€£]/g, '')
  // 百分号按原数值计（"50%" → 50，不转小数 —— 避免口径惊扰用户）
  s = s.replace(/%$/, '')
  // 会计负数 (123) → -123
  const paren = /^\((-?[\d.]+)\)$/.exec(s)
  if (paren) s = `-${paren[1]}`
  if (!/^[+-]?(\d+\.?\d*|\.\d+)$/.test(s)) return null
  const n = Number(s)
  return Number.isFinite(n) ? n : null
}

/** 从 table 节点算按列统计（首行 = 表头，不参与统计）。 */
export function computeTableStats(table: PMNode): TableStatsData {
  const rows: PMNode[] = []
  table.forEach((child) => {
    if (child.type.name === ROW_NODE) rows.push(child)
  })
  const headerRow = rows[0]
  const dataRows = rows.slice(1)

  const headers: string[] = []
  const colCount = headerRow?.childCount ?? 0
  headerRow?.forEach((cell) => headers.push(cell.textContent.trim()))

  const columns: ColumnStats[] = []
  for (let c = 0; c < colCount; c += 1) {
    const stats: ColumnStats = {
      count: 0,
      numericCount: 0,
      sum: 0,
      min: Infinity,
      max: -Infinity,
    }
    for (const row of dataRows) {
      const cell = row.child(c)
      if (!CELL_NODES.has(cell.type.name)) continue
      const text = cell.textContent.trim()
      if (!text) continue
      stats.count += 1
      const n = parseCellNumber(text)
      if (n === null) continue
      stats.numericCount += 1
      stats.sum += n
      if (n < stats.min) stats.min = n
      if (n > stats.max) stats.max = n
    }
    columns.push(stats)
  }
  return { headers, columns }
}

/** CellSelection 且锚/头同列 → 列索引；否则 null。 */
export function selectedColumnIndex(selection: Selection): number | null {
  if (!(selection instanceof CellSelection)) return null
  const colOf = ($cell: ResolvedPos): number => {
    for (let d = $cell.depth; d > 0; d -= 1) {
      if ($cell.node(d).type.name === ROW_NODE) return $cell.index(d)
    }
    return -1
  }
  const anchor = colOf(selection.$anchorCell)
  return anchor >= 0 && anchor === colOf(selection.$headCell)
    ? anchor
    : null
}

/** 统计数格式化：整数走千分位，小数留 2 位去尾零。 */
export function formatStat(n: number, fractionDigits = 2): string {
  if (Number.isInteger(n)) return n.toLocaleString('zh-Hans')
  return n.toFixed(fractionDigits).replace(/\.?0+$/, '')
}

export interface TableStatsDeps {
  t: (s: string) => string
  /** 节点 pos → 视口矩形（main 注入 view.nodeDOM；测试可桩） */
  rectAt: (ctx: Ctx, pos: number) => AnchorRect | null
  /** 源码模式下隐藏（textarea 场景无 PM 表格语义） */
  isSourceMode?: () => boolean
}

export interface TableStatsController {
  /** 底部汇总条 */
  element: HTMLElement
  /** 列选详情浮片 */
  chipElement: HTMLElement
  /** 文档/选区变化后重算显隐与内容 */
  sync: (ctx: Ctx) => void
  hide: () => void
  setTranslator: (t: (s: string) => string) => void
  destroy: () => void
}

interface TableLocation {
  node: PMNode
  /** table 节点在文档中的位置（rectAt 定位用） */
  pos: number
}

function findTable($pos: ResolvedPos): TableLocation | null {
  for (let d = $pos.depth; d > 0; d -= 1) {
    if ($pos.node(d).type.name === 'table') {
      return { node: $pos.node(d), pos: $pos.before(d) }
    }
  }
  return null
}

/** 视口内定位：优先放锚点下/上，水平钳制。 */
function place(
  el: HTMLElement,
  rect: AnchorRect,
  where: 'below' | 'above',
): void {
  el.style.visibility = 'hidden'
  el.style.display = 'block'
  const width = el.offsetWidth
  const height = el.offsetHeight
  const left = Math.min(
    Math.max(4, rect.left),
    Math.max(4, window.innerWidth - width - 4),
  )
  let top =
    where === 'below' ? rect.bottom + 4 : rect.top - height - 4
  top = Math.min(Math.max(4, top), Math.max(4, window.innerHeight - height - 4))
  el.style.left = `${left}px`
  el.style.top = `${top}px`
  el.style.visibility = 'visible'
}

export function createTableStatsController(
  deps: TableStatsDeps,
): TableStatsController {
  let t = deps.t

  const element = document.createElement('div')
  element.className = 'kc-tsbar'
  element.style.display = 'none'

  const chipElement = document.createElement('div')
  chipElement.className = 'kc-tschip'
  chipElement.style.display = 'none'

  const hide = (): void => {
    element.style.display = 'none'
    chipElement.style.display = 'none'
  }

  /** 列名：空表头回退「列 N」 */
  const colName = (data: TableStatsData, index: number): string =>
    data.headers[index] || `${t('Column')} ${index + 1}`

  const renderBar = (data: TableStatsData): void => {
    element.textContent = ''
    for (let c = 0; c < data.columns.length; c += 1) {
      const stats = data.columns[c]
      const col = document.createElement('span')
      col.className = 'kc-tsbar-col'
      const name = document.createElement('b')
      name.textContent = colName(data, c)
      col.appendChild(name)
      const meta = document.createElement('span')
      meta.className = 'kc-tsbar-meta'
      if (stats.numericCount > 0) {
        meta.textContent =
          `Σ ${formatStat(stats.sum)} · x̄ ${formatStat(stats.sum / stats.numericCount)} · n ${stats.count}`
        meta.title =
          `${t('Sum')} ${formatStat(stats.sum)} / ${t('Average')} ` +
          `${formatStat(stats.sum / stats.numericCount)} / ${t('Count')} ${stats.count}`
      } else {
        meta.textContent = `n ${stats.count}`
        meta.title = t('Count')
      }
      col.appendChild(meta)
      element.appendChild(col)
    }
  }

  const renderChip = (data: TableStatsData, index: number): void => {
    const stats = data.columns[index]
    chipElement.textContent = ''
    const title = document.createElement('div')
    title.className = 'kc-tschip-title'
    title.textContent = colName(data, index)
    chipElement.appendChild(title)
    const rows: [string, string][] = [
      [t('Count'), String(stats.count)],
      ...(stats.numericCount > 0
        ? ([
            [t('Sum'), formatStat(stats.sum)],
            [t('Average'), formatStat(stats.sum / stats.numericCount)],
            [t('Max'), formatStat(stats.max)],
            [t('Min'), formatStat(stats.min)],
          ] as [string, string][])
        : []),
    ]
    for (const [label, value] of rows) {
      const row = document.createElement('div')
      row.className = 'kc-tschip-row'
      const l = document.createElement('span')
      l.textContent = label
      const v = document.createElement('b')
      v.textContent = value
      row.appendChild(l)
      row.appendChild(v)
      chipElement.appendChild(row)
    }
  }

  const sync = (ctx: Ctx): void => {
    if (deps.isSourceMode?.()) {
      hide()
      return
    }
    const state = ctx.get(editorStateCtx)
    const found = findTable(state.selection.$from)
    if (!found) {
      hide()
      return
    }
    const data = computeTableStats(found.node)
    renderBar(data)
    const rect = deps.rectAt(ctx, found.pos)
    if (!rect) {
      hide()
      return
    }
    place(element, rect, 'below')

    const col = selectedColumnIndex(state.selection)
    if (col !== null && col < data.columns.length) {
      const cellPos = (state.selection as CellSelection).$anchorCell.pos
      const cellRect = deps.rectAt(ctx, cellPos)
      if (cellRect) {
        renderChip(data, col)
        place(chipElement, cellRect, 'above')
        return
      }
    }
    chipElement.style.display = 'none'
  }

  return {
    element,
    chipElement,
    sync,
    hide,
    setTranslator(next) {
      t = next
    },
    destroy() {
      hide()
      element.remove()
      chipElement.remove()
    },
  }
}

/** main.ts 注入的默认 rectAt：PM view.nodeDOM → getBoundingClientRect。 */
export function domRectAt(ctx: Ctx, pos: number): AnchorRect | null {
  const view = ctx.get(editorViewCtx)
  const dom = view.nodeDOM(pos)
  return dom instanceof HTMLElement ? dom.getBoundingClientRect() : null
}
