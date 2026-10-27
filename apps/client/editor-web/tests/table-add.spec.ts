// 表格末尾追加：computeAddStripLayout 钳制 / lastRowCellPos / lastColCellPos /
// appendTableRow / appendTableColumn 单测。schema 与 table-stats.spec 同款最小集。

import { describe, expect, it } from 'vitest'
import { Schema } from '@milkdown/kit/prose/model'
import { EditorState, TextSelection } from '@milkdown/kit/prose/state'
import type { EditorView } from '@milkdown/kit/prose/view'

import {
  ADD_STRIP,
  appendTableColumn,
  appendTableRow,
  clearTableCellContents,
  computeAddStripLayout,
  duplicateTableColumn,
  duplicateTableRow,
  lastColCellPos,
  lastRowCellPos,
} from '../src/plugins/table-add'
import { CellSelection, cellAround } from '@milkdown/kit/prose/tables'

const schema = new Schema({
  nodes: {
    doc: { content: 'block+' },
    paragraph: { group: 'block', content: 'inline*' },
    text: { group: 'inline' },
    table: { group: 'block', content: 'table_row+', tableRole: 'table' },
    table_row: {
      content: '(table_cell | table_header)+',
      tableRole: 'row',
    },
    table_cell: {
      content: 'inline*',
      tableRole: 'cell',
      attrs: {
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      },
    },
    table_header: {
      content: 'inline*',
      tableRole: 'header_cell',
      attrs: {
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      },
    },
  },
})

function tableWithDoc(rows: string[][]) {
  const table = schema.node(
    'table',
    undefined,
    rows.map((cells, r) =>
      schema.node(
        'table_row',
        undefined,
        cells.map((text) =>
          schema.node(
            r === 0 ? 'table_header' : 'table_cell',
            undefined,
            text ? [schema.text(text)] : undefined,
          ),
        ),
      ),
    ),
  )
  const doc = schema.node('doc', undefined, [table])
  return { table, doc }
}

/** 最小 view 桩：append* 只用 state/dispatch，不碰 DOM */
function fakeView(initial: EditorState): { view: EditorView; state: () => EditorState } {
  let state = initial
  const view = {
    get state() {
      return state
    },
    dispatch: (tr: Parameters<EditorState['apply']>[0]) => {
      state = state.apply(tr)
    },
  } as unknown as EditorView
  return { view, state: () => state }
}

function cellPositions(
  doc: ReturnType<typeof schema.node>,
  type: 'table_cell' | 'table_header',
): number[] {
  const positions: number[] = []
  doc.descendants((node, pos) => {
    if (node.type.name === type) positions.push(pos)
    return true
  })
  return positions
}

function cellPosByRowCol(
  doc: ReturnType<typeof schema.node>,
  row: number,
  col: number,
): number {
  const byRow: number[][] = []
  let cur = -1
  doc.descendants((node, pos) => {
    if (node.type.name === 'table_row') {
      cur += 1
      byRow.push([])
      return true
    }
    if (node.type.name === 'table_cell' || node.type.name === 'table_header') {
      byRow[cur].push(pos)
      return false
    }
    return true
  })
  const pos = byRow[row]?.[col]
  if (pos === undefined) throw new Error('cell not found')
  return pos
}

/** 光标放进第 row 行第 col 列的单元格（table 在 doc 起点，无合并格） */
function selectionInCell(
  doc: ReturnType<typeof schema.node>,
  row: number,
  col: number,
) {
  return TextSelection.create(doc, cellPosByRowCol(doc, row, col) + 1)
}

/** 整行 CellSelection（row 首格 → 末格） */
function rowCellSelection(
  doc: ReturnType<typeof schema.node>,
  row: number,
  cols: number,
) {
  const first = cellAround(doc.resolve(cellPosByRowCol(doc, row, 0) + 1))
  const last = cellAround(doc.resolve(cellPosByRowCol(doc, row, cols - 1) + 1))
  if (!first || !last) throw new Error('cellAround failed')
  return new CellSelection(first, last)
}

describe('computeAddStripLayout', () => {
  const viewport = { width: 800, height: 600 }

  it('常规：行条贴表格下缘同宽，列条贴表格外右缘同高', () => {
    const layout = computeAddStripLayout(
      { left: 100, top: 100, right: 500, bottom: 300 },
      viewport,
      0,
    )
    expect(layout.row).toEqual({ left: 100, top: 304, width: 400 })
    expect(layout.col).toEqual({ left: 502, top: 100, height: 200 })
  })

  it('汇总条占位时行条下移', () => {
    const layout = computeAddStripLayout(
      { left: 100, top: 100, right: 500, bottom: 300 },
      viewport,
      30,
    )
    expect(layout.row.top).toBe(334)
  })

  it('右侧贴不下时列条叠进表格内右缘（移动端满宽表格）', () => {
    // outside = 502，502 + 22 = 524 > 800-4? 不，本例用窄视口
    const layout = computeAddStripLayout(
      { left: 100, top: 100, right: 500, bottom: 300 },
      { width: 505, height: 600 },
      0,
    )
    expect(layout.col.left).toBe(500 - ADD_STRIP - 2)
  })

  it('超宽表格：行条宽度与左缘钳进视口', () => {
    const layout = computeAddStripLayout(
      { left: -50, top: 100, right: 900, bottom: 300 },
      viewport,
      0,
    )
    expect(layout.row.width).toBe(800 - 8)
    expect(layout.row.left).toBe(4)
  })

  it('超高表格：列条高度与顶缘钳进视口', () => {
    const layout = computeAddStripLayout(
      { left: 100, top: -100, right: 500, bottom: 900 },
      viewport,
      0,
    )
    expect(layout.col.height).toBe(600 - 8)
    expect(layout.col.top).toBe(4)
  })
})

describe('lastRowCellPos / lastColCellPos', () => {
  it('3×2 表：末行首格 / 首行末格', () => {
    const { table, doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
      ['c', 'd'],
    ])
    const cells = cellPositions(doc, 'table_cell')
    const headers = cellPositions(doc, 'table_header')
    // table 是 doc 第一个节点，pos = 0
    expect(lastRowCellPos(table, 0)).toBe(cells[2]) // 第三行首格 'c'
    expect(lastColCellPos(table, 0)).toBe(headers[1]) // 表头末格 'h2'
  })
})

describe('appendTableRow / appendTableColumn', () => {
  it('末尾追加一行：新行空单元格，行数 +1', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: TextSelection.create(doc, 3), // 光标在表格内
    })
    const { view, state } = fakeView(initial)
    expect(appendTableRow(view)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.childCount).toBe(3) // 2 行 → 3 行
    const lastRow = table?.child(2)
    expect(lastRow?.childCount).toBe(2)
    expect(lastRow?.textContent).toBe('')
  })

  it('末尾追加一列：表头行补 header 格，数据行补 cell 格', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: TextSelection.create(doc, 3),
    })
    const { view, state } = fakeView(initial)
    expect(appendTableColumn(view)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.child(0).childCount).toBe(3)
    expect(table?.child(0).child(2).type.name).toBe('table_header')
    expect(table?.child(1).childCount).toBe(3)
    expect(table?.child(1).child(2).type.name).toBe('table_cell')
  })

  it('选区不在表格内返回 false，文档不变', () => {
    const para = schema.node('paragraph', undefined, [schema.text('x')])
    const doc = schema.node('doc', undefined, [para])
    const initial = EditorState.create({
      schema,
      doc,
      selection: TextSelection.create(doc, 1),
    })
    const { view, state } = fakeView(initial)
    expect(appendTableRow(view)).toBe(false)
    expect(appendTableColumn(view)).toBe(false)
    expect(state().doc.eq(doc)).toBe(true)
  })
})

describe('duplicateTableRow / duplicateTableColumn', () => {
  it('复制行：含内容克隆插到本行后', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
      ['c', 'd'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 1, 0),
    })
    const { view, state } = fakeView(initial)
    expect(duplicateTableRow(view)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.childCount).toBe(4)
    expect(table?.child(1).textContent).toBe('ab')
    expect(table?.child(2).textContent).toBe('ab') // 副本紧跟本行
    expect(table?.child(3).textContent).toBe('cd')
  })

  it('复制列：表头行克隆 header 格，数据行克隆 cell 格', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 1, 0), // 第 1 列
    })
    const { view, state } = fakeView(initial)
    expect(duplicateTableColumn(view)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.child(0).childCount).toBe(3)
    expect(table?.child(0).child(1).type.name).toBe('table_header')
    expect(table?.child(0).child(1).textContent).toBe('h1') // 副本紧随本列
    expect(table?.child(1).child(1).type.name).toBe('table_cell')
    expect(table?.child(1).child(1).textContent).toBe('a')
    expect(table?.child(1).child(2).textContent).toBe('b')
  })

  it('选区不在表格内返回 false', () => {
    const para = schema.node('paragraph', undefined, [schema.text('x')])
    const doc = schema.node('doc', undefined, [para])
    const initial = EditorState.create({
      schema,
      doc,
      selection: TextSelection.create(doc, 1),
    })
    const { view } = fakeView(initial)
    expect(duplicateTableRow(view)).toBe(false)
    expect(duplicateTableColumn(view)).toBe(false)
  })
})

describe('clearTableCellContents', () => {
  it('文本光标：只清当前单元格，结构不动', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 1, 0),
    })
    const { view, state } = fakeView(initial)
    expect(clearTableCellContents(view)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.childCount).toBe(2)
    expect(table?.child(1).childCount).toBe(2)
    expect(table?.child(1).child(0).textContent).toBe('')
    expect(table?.child(1).child(1).textContent).toBe('b')
    expect(table?.child(0).textContent).toBe('h1h2')
  })

  it('CellSelection 整行：清该行全部单元格', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
      ['c', 'd'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: rowCellSelection(doc, 1, 2),
    })
    const { view, state } = fakeView(initial)
    expect(clearTableCellContents(view)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.child(1).textContent).toBe('')
    expect(table?.child(2).textContent).toBe('cd')
  })

  it('目标已空返回 false（不产生无意义 undo 步）', () => {
    const { doc } = tableWithDoc([
      ['h1'],
      [''],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 1, 0),
    })
    const { view, state } = fakeView(initial)
    expect(clearTableCellContents(view)).toBe(false)
    expect(state().doc.eq(doc)).toBe(true)
  })

  it('选区不在表格内返回 false', () => {
    const para = schema.node('paragraph', undefined, [schema.text('x')])
    const doc = schema.node('doc', undefined, [para])
    const initial = EditorState.create({
      schema,
      doc,
      selection: TextSelection.create(doc, 1),
    })
    const { view } = fakeView(initial)
    expect(clearTableCellContents(view)).toBe(false)
  })
})
