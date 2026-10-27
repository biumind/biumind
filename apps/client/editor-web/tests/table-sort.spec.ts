// 表格按列排序：cellSortKey / compareSortKeys / sortRowOrder 纯函数 +
// sortTableByColumn 集成（fakeView）。schema 与 table-add.spec 同款最小集。

import { describe, expect, it } from 'vitest'
import { Schema } from '@milkdown/kit/prose/model'
import { EditorState, TextSelection } from '@milkdown/kit/prose/state'
import type { EditorView } from '@milkdown/kit/prose/view'

import {
  cellSortKey,
  compareSortKeys,
  sortRowOrder,
  sortTableByColumn,
} from '../src/plugins/table-sort'

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

function fakeView(initial: EditorState): {
  view: EditorView
  state: () => EditorState
} {
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

/** 光标放进第 row 行第 col 列的单元格（table 在 doc 起点，无合并格） */
function selectionInCell(
  doc: ReturnType<typeof schema.node>,
  row: number,
  col: number,
) {
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
  return TextSelection.create(doc, pos + 1)
}

describe('cellSortKey', () => {
  it('空串 / 空白 → empty', () => {
    expect(cellSortKey('')).toEqual({ kind: 'empty' })
    expect(cellSortKey('  ')).toEqual({ kind: 'empty' })
  })

  it('数字文本 → number（复用 parseCellNumber 口径）', () => {
    expect(cellSortKey('1,234')).toEqual({ kind: 'number', value: 1234 })
    expect(cellSortKey('¥50')).toEqual({ kind: 'number', value: 50 })
  })

  it('其余 → text', () => {
    expect(cellSortKey('苹果')).toEqual({ kind: 'text', value: '苹果' })
  })
})

describe('compareSortKeys', () => {
  it('数字按数值比，不按字典序', () => {
    expect(compareSortKeys({ kind: 'number', value: 9 }, { kind: 'number', value: 10 }, true)).toBeLessThan(0)
  })

  it('数字排文本前；降序反转', () => {
    const n = { kind: 'number', value: 1 } as const
    const t = { kind: 'text', value: 'a' } as const
    expect(compareSortKeys(n, t, true)).toBeLessThan(0)
    expect(compareSortKeys(n, t, false)).toBeGreaterThan(0)
  })

  it('空单元格恒沉底（升降序都一样）', () => {
    const e = { kind: 'empty' } as const
    const n = { kind: 'number', value: 1 } as const
    expect(compareSortKeys(e, n, true)).toBeGreaterThan(0)
    expect(compareSortKeys(e, n, false)).toBeGreaterThan(0)
    expect(compareSortKeys(n, e, false)).toBeLessThan(0)
  })
})

describe('sortRowOrder', () => {
  it('数值列升序 / 降序', () => {
    expect(sortRowOrder(['30', '10', '20'], true)).toEqual([1, 2, 0])
    expect(sortRowOrder(['30', '10', '20'], false)).toEqual([0, 2, 1])
  })

  it('混合列：数字在前，空格沉底', () => {
    expect(sortRowOrder(['香蕉', '10', '', '5'], true)).toEqual([3, 1, 0, 2])
  })

  it('稳定：相等键保持原行序', () => {
    expect(sortRowOrder(['a', 'a', 'a'], true)).toEqual([0, 1, 2])
  })
})

describe('sortTableByColumn', () => {
  it('按光标所在列升序重排数据行，表头不动', () => {
    const { doc } = tableWithDoc([
      ['名称', '数量'],
      ['丙', '30'],
      ['甲', '10'],
      ['乙', '20'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 2, 1), // 数量列
    })
    const { view, state } = fakeView(initial)
    expect(sortTableByColumn(view, true)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.child(0).textContent).toBe('名称数量') // 表头原位
    expect(table?.child(1).textContent).toBe('甲10')
    expect(table?.child(2).textContent).toBe('乙20')
    expect(table?.child(3).textContent).toBe('丙30')
  })

  it('文本列按拼音序排（zh-Hans localeCompare）', () => {
    // 拼音序：丙(bǐng) < 甲(jiǎ) < 乙(yǐ)，造真正乱序的输入
    const { doc } = tableWithDoc([
      ['名称', '数量'],
      ['乙', '20'],
      ['丙', '30'],
      ['甲', '10'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 1, 0), // 名称列
    })
    const { view, state } = fakeView(initial)
    expect(sortTableByColumn(view, true)).toBe(true)
    const table = state().doc.firstChild
    expect(table?.child(1).textContent).toBe('丙30')
    expect(table?.child(2).textContent).toBe('甲10')
    expect(table?.child(3).textContent).toBe('乙20')
  })

  it('顺序已正确返回 false（不产生无意义 undo 步）', () => {
    const { doc } = tableWithDoc([
      ['h'],
      ['1'],
      ['2'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 1, 0),
    })
    const { view, state } = fakeView(initial)
    expect(sortTableByColumn(view, true)).toBe(false)
    expect(state().doc.eq(doc)).toBe(true)
  })

  it('只有表头 + 一行数据时不排（无意义）', () => {
    const { doc } = tableWithDoc([
      ['h'],
      ['1'],
    ])
    const initial = EditorState.create({
      schema,
      doc,
      selection: selectionInCell(doc, 1, 0),
    })
    const { view } = fakeView(initial)
    expect(sortTableByColumn(view, true)).toBe(false)
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
    expect(sortTableByColumn(view, true)).toBe(false)
  })
})
