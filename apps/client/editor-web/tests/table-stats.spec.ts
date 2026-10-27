// 表格统计：parseCellNumber / computeTableStats / selectedColumnIndex /
// formatStat 纯函数单测。schema 与 milkdown gfm 同名最小集。

import { describe, expect, it } from 'vitest'
import { Schema } from '@milkdown/kit/prose/model'
import { TextSelection } from '@milkdown/kit/prose/state'
import { CellSelection, cellAround } from '@milkdown/kit/prose/tables'

import {
  computeTableStats,
  defaultMetric,
  formatStat,
  metricMsgId,
  metricValue,
  parseCellNumber,
  selectedColumnIndex,
  type ColumnStats,
} from '../src/plugins/table-stats'

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
      // prosemirror-tables TableMap 读这三个 attrs —— 缺了 map 算 NaN
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

function makeTable(rows: string[][]): ReturnType<typeof schema.node> {
  const rowNodes = rows.map((cells, r) =>
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
  )
  return schema.node('table', undefined, rowNodes)
}

function tableWithDoc(rows: string[][]) {
  const table = makeTable(rows)
  const doc = schema.node('doc', undefined, [table])
  return { table, doc }
}

describe('parseCellNumber', () => {
  it('常规数字', () => {
    expect(parseCellNumber('123')).toBe(123)
    expect(parseCellNumber('-12.5')).toBe(-12.5)
    expect(parseCellNumber('.5')).toBe(0.5)
    expect(parseCellNumber('+7')).toBe(7)
  })

  it('千分位 / 货币 / 百分号 / 空白', () => {
    expect(parseCellNumber('1,234,567')).toBe(1234567)
    expect(parseCellNumber('1，234')).toBe(1234)
    expect(parseCellNumber('¥1,200')).toBe(1200)
    expect(parseCellNumber('$ 50')).toBe(50)
    expect(parseCellNumber('50%')).toBe(50)
    expect(parseCellNumber(' 42 ')).toBe(42)
  })

  it('全角数字 / 全角小数点', () => {
    expect(parseCellNumber('１２３')).toBe(123)
    expect(parseCellNumber('３．５')).toBe(3.5)
  })

  it('括号负数（会计记法）', () => {
    expect(parseCellNumber('(123)')).toBe(-123)
    expect(parseCellNumber('(12.5)')).toBe(-12.5)
  })

  it('非数值回 null', () => {
    expect(parseCellNumber('')).toBeNull()
    expect(parseCellNumber('abc')).toBeNull()
    expect(parseCellNumber('1a2')).toBeNull()
    expect(parseCellNumber('1.2.3')).toBeNull()
    expect(parseCellNumber('--5')).toBeNull()
  })
})

describe('computeTableStats', () => {
  it('按列统计：首行表头不参与，空单元格跳过', () => {
    const { table } = tableWithDoc([
      ['名称', '数量', '备注'],
      ['甲', '10', 'x'],
      ['乙', '20', ''],
      ['丙', '', 'y'],
    ])
    const data = computeTableStats(table)
    expect(data.headers).toEqual(['名称', '数量', '备注'])
    // 名称列：非数值
    expect(data.columns[0].count).toBe(3)
    expect(data.columns[0].numericCount).toBe(0)
    // 数量列：2 个数值
    expect(data.columns[1].count).toBe(2)
    expect(data.columns[1].numericCount).toBe(2)
    expect(data.columns[1].sum).toBe(30)
    expect(data.columns[1].min).toBe(10)
    expect(data.columns[1].max).toBe(20)
    // 备注列
    expect(data.columns[2].count).toBe(2)
    expect(data.columns[2].numericCount).toBe(0)
  })

  it('只有表头的表格不炸', () => {
    const { table } = tableWithDoc([['空表']])
    const data = computeTableStats(table)
    expect(data.columns.length).toBe(1)
    expect(data.columns[0].count).toBe(0)
    expect(data.columns[0].numericCount).toBe(0)
  })

  it('混合数值/文本的列只统计解析成功的', () => {
    const { table } = tableWithDoc([
      ['x'],
      ['10'],
      ['abc'],
      ['30'],
    ])
    const data = computeTableStats(table)
    expect(data.columns[0].count).toBe(3)
    expect(data.columns[0].numericCount).toBe(2)
    expect(data.columns[0].sum).toBe(40)
  })
})

describe('selectedColumnIndex', () => {
  function cellPositions(doc: ReturnType<typeof schema.node>): number[] {
    const positions: number[] = []
    doc.descendants((node, pos) => {
      if (node.type.name === 'table_cell') positions.push(pos)
      return true
    })
    return positions
  }

  function cellSel(
    doc: ReturnType<typeof schema.node>,
    cellPosA: number,
    cellPosB: number,
  ): CellSelection {
    // CellSelection 要 cellAround 位姿（指向 cell 节点内的位置），
    // 直接传 cell 前序位置会算错 table 内偏移
    const $a = cellAround(doc.resolve(cellPosA + 1))
    const $b = cellAround(doc.resolve(cellPosB + 1))
    if (!$a || !$b) throw new Error('cellAround failed')
    return new CellSelection($a, $b)
  }

  it('整列 CellSelection → 列索引', () => {
    const { doc } = tableWithDoc([
      ['a', 'b'],
      ['1', '2'],
      ['3', '4'],
    ])
    const cells = cellPositions(doc)
    // 第 2 列 = 每行第 2 个 cell（cells 只收 table_cell，不含表头）
    const sel = cellSel(doc, cells[1], cells[3])
    expect(selectedColumnIndex(sel)).toBe(1)
  })

  it('跨列选择 → null（不是单列语义）', () => {
    const { doc } = tableWithDoc([
      ['a', 'b'],
      ['1', '2'],
    ])
    const cells = cellPositions(doc)
    const sel = cellSel(doc, cells[0], cells[1])
    expect(selectedColumnIndex(sel)).toBeNull()
  })

  it('文本选区 → null', () => {
    const { table } = tableWithDoc([['a'], ['1']])
    const doc = schema.node('doc', undefined, [
      table,
      schema.node('paragraph', undefined, [schema.text('x')]),
    ])
    const paraPos = table.nodeSize + 1
    const sel = TextSelection.create(doc, paraPos + 1)
    expect(selectedColumnIndex(sel)).toBeNull()
  })
})

describe('formatStat', () => {
  it('整数千分位，小数去尾零', () => {
    expect(formatStat(1234567)).toBe('1,234,567')
    expect(formatStat(12.5)).toBe('12.5')
    expect(formatStat(12.0)).toBe('12')
    expect(formatStat(0.333333, 2)).toBe('0.33')
  })
})

describe('主口径（方案 B：单口径 + 点击切换）', () => {
  const numeric: ColumnStats = {
    count: 3,
    numericCount: 2,
    sum: 30,
    min: 10,
    max: 20,
  }
  const text: ColumnStats = {
    count: 2,
    numericCount: 0,
    sum: 0,
    min: Infinity,
    max: -Infinity,
  }

  it('默认口径：数值列合计，非数值列计数', () => {
    expect(defaultMetric(numeric)).toBe('sum')
    expect(defaultMetric(text)).toBe('count')
  })

  it('口径取值', () => {
    expect(metricValue(numeric, 'sum')).toBe(30)
    expect(metricValue(numeric, 'average')).toBe(15)
    expect(metricValue(numeric, 'count')).toBe(3)
    expect(metricValue(numeric, 'max')).toBe(20)
    expect(metricValue(numeric, 'min')).toBe(10)
    // 非数值列求均值不炸
    expect(metricValue(text, 'average')).toBe(0)
  })

  it('口径 msgid 走扁平字典', () => {
    expect(metricMsgId('sum')).toBe('Sum')
    expect(metricMsgId('average')).toBe('Average')
    expect(metricMsgId('count')).toBe('Count')
    expect(metricMsgId('max')).toBe('Max')
    expect(metricMsgId('min')).toBe('Min')
  })
})
