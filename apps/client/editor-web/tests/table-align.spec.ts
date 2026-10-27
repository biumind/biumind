// 表格列对齐落盘链路守护：
//   1. PM 机制层 —— 整列 CellSelection + setCellAttr 必须覆盖含表头行的
//      全列（右键对齐三项的修复前提：setCellAttr 在文本光标下只改单格，
//      而序列化只读表头行 alignment attr，单格修改不落盘）
//   2. remark 层 —— GFM 对齐冒号语法（`:-`/`:-:`/`-:`）经我们的
//      STRINGIFY_OPTIONS 往返不丢

import { describe, expect, it } from 'vitest'
import { Schema } from '@milkdown/kit/prose/model'
import { EditorState, TextSelection } from '@milkdown/kit/prose/state'
import {
  CellSelection,
  cellAround,
  setCellAttr,
} from '@milkdown/kit/prose/tables'
import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkGfm from 'remark-gfm'
import remarkStringify from 'remark-stringify'

import { STRINGIFY_OPTIONS } from '../src/markdown/stringify-options'

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
        // 与 milkdown gfm table 同名的对齐 attr（序列化从表头行读它）
        alignment: { default: 'left' },
      },
    },
    table_header: {
      content: 'inline*',
      tableRole: 'header_cell',
      attrs: {
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
        alignment: { default: 'left' },
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

/** 整列 CellSelection（首行格 → 末行格），等价 selectColCommand 的产物 */
function colCellSelection(
  doc: ReturnType<typeof schema.node>,
  col: number,
  rows: number,
) {
  const first = cellAround(doc.resolve(cellPosByRowCol(doc, 0, col) + 1))
  const last = cellAround(doc.resolve(cellPosByRowCol(doc, rows - 1, col) + 1))
  if (!first || !last) throw new Error('cellAround failed')
  return new CellSelection(first, last)
}

describe('列对齐 PM 机制（右键对齐的修复前提）', () => {
  it('整列 CellSelection + setCellAttr：全列含表头 alignment 生效', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
      ['c', 'd'],
    ])
    const state = EditorState.create({
      schema,
      doc,
      selection: colCellSelection(doc, 1, 3),
    })
    let next = state
    const applied = setCellAttr('alignment', 'center')(state, (tr) => {
      next = state.apply(tr)
    })
    expect(applied).toBe(true)
    const table = next.doc.firstChild
    // 表头格（序列化读取源）与数据格全列居中
    expect(table?.child(0).child(1).attrs.alignment).toBe('center')
    expect(table?.child(1).child(1).attrs.alignment).toBe('center')
    expect(table?.child(2).child(1).attrs.alignment).toBe('center')
    // 其他列不受影响
    expect(table?.child(0).child(0).attrs.alignment).toBe('left')
  })

  it('反例守护：文本光标下 setCellAttr 只改单格（这就是要先 selectCol 的原因）', () => {
    const { doc } = tableWithDoc([
      ['h1', 'h2'],
      ['a', 'b'],
    ])
    const state = EditorState.create({
      schema,
      doc,
      selection: TextSelection.create(doc, cellPosByRowCol(doc, 1, 1) + 1),
    })
    let next = state
    setCellAttr('alignment', 'right')(state, (tr) => {
      next = state.apply(tr)
    })
    const table = next.doc.firstChild
    // 只改了光标所在数据格；表头格不动 → 序列化读表头 = 不落盘
    expect(table?.child(1).child(1).attrs.alignment).toBe('right')
    expect(table?.child(0).child(1).attrs.alignment).toBe('left')
  })
})

describe('列对齐 remark 往返（STRINGIFY_OPTIONS 下不丢冒号语法）', () => {
  it('左/中/右对齐标记解析后再序列化仍在', () => {
    const md = '| a | b | c |\n| :- | :-: | -: |\n| 1 | 2 | 3 |\n'
    const out = unified()
      .use(remarkParse)
      .use(remarkGfm)
      .use(remarkStringify, STRINGIFY_OPTIONS)
      .processSync(md)
      .toString()
    expect(out).toContain(':-')
    expect(out).toContain(':-:')
    expect(out).toContain('-:')
  })
})
