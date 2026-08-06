// 块级格式命令：行级正则改写，全部自写实现（M1 范围）。
//   * 标题：行首 `#` 数量 toggle（空行也可处理）
//   * 引用：行首 `> ` toggle
//   * 列表：bullet / ordered / task 三种 marker 识别，同类型去除、
//     异类型替换，有序列表改写后对受影响段落重编号
//   * 分割线 / 代码围栏 / 3×3 表格 / 时间戳

import { EditorSelection } from '@codemirror/state'
import type { Command, EditorView } from '@codemirror/view'
import { formatTimestamp } from '../../timestamp'

type ListKind = 'bullet' | 'ordered' | 'task'

const TASK_RE = /^(\s*)[-*+] \[[ xX]\]\s/
const BULLET_RE = /^(\s*)[-*+]\s/
const ORDERED_RE = /^(\s*)\d+[.)]\s/
const HEADING_RE = /^#{1,6}\s/
const QUOTE_RE = /^> ?/

function listMatch(text: string): { kind: ListKind; marker: string } | null {
  const task = text.match(TASK_RE)
  if (task) return { kind: 'task', marker: task[0].slice(task[1].length) }
  const bullet = text.match(BULLET_RE)
  if (bullet) return { kind: 'bullet', marker: bullet[0].slice(bullet[1].length) }
  const ordered = text.match(ORDERED_RE)
  if (ordered) return { kind: 'ordered', marker: ordered[0].slice(ordered[1].length) }
  return null
}

function newMarker(kind: ListKind): string {
  if (kind === 'bullet') return '- '
  if (kind === 'task') return '- [ ] '
  return '1. '
}

/** 收集选区覆盖的行号（含空选区所在行） */
function selectedLineNumbers(view: EditorView): number[] {
  const { state } = view
  const lines = new Set<number>()
  for (const range of state.selection.ranges) {
    const from = state.doc.lineAt(range.from)
    // 选区末端刚好落在下一行行首时不把下一行算进来
    const toPos = range.to > range.from && range.to === state.doc.lineAt(range.to).from
      ? range.to - 1
      : range.to
    const to = state.doc.lineAt(toPos)
    for (let n = from.number; n <= to.number; n += 1) lines.add(n)
  }
  return [...lines].sort((a, b) => a - b)
}

/** 按「新行文本表」整行替换；选区由 dispatch 自动映射 */
function replaceLines(view: EditorView, newTexts: Map<number, string>): void {
  const { state } = view
  const changes = []
  for (const [n, text] of newTexts) {
    const line = state.doc.line(n)
    if (line.text === text) continue
    changes.push({ from: line.from, to: line.to, insert: text })
  }
  if (changes.length > 0) view.dispatch({ changes })
}

export function toggleHeaderLevel(level: 1 | 2 | 3): Command {
  return (view) => {
    const hashes = '#'.repeat(level) + ' '
    const newTexts = new Map<number, string>()
    for (const n of selectedLineNumbers(view)) {
      const text = view.state.doc.line(n).text
      const m = text.match(HEADING_RE)
      if (m) {
        const current = m[0].trim().length
        // 同级 → 去除；异级 → 替换
        newTexts.set(n, current === level ? text.slice(m[0].length) : hashes + text.slice(m[0].length))
      } else if (text.trim() === '') {
        newTexts.set(n, hashes)
      } else {
        newTexts.set(n, hashes + text)
      }
    }
    replaceLines(view, newTexts)
    return true
  }
}

export const toggleBlockquote: Command = (view) => {
  const newTexts = new Map<number, string>()
  for (const n of selectedLineNumbers(view)) {
    const text = view.state.doc.line(n).text
    const m = text.match(QUOTE_RE)
    newTexts.set(n, m ? text.slice(m[0].length) : `> ${text}`)
  }
  replaceLines(view, newTexts)
  return true
}

export function toggleList(kind: ListKind): Command {
  return (view) => {
    const { state } = view
    const selected = new Set(selectedLineNumbers(view))
    // 先在新文本表上改写选中行
    const texts: string[] = []
    for (let n = 1; n <= state.doc.lines; n += 1) texts.push(state.doc.line(n).text)
    for (const n of selected) {
      const text = texts[n - 1]
      const m = listMatch(text)
      const indent = text.match(/^\s*/)?.[0] ?? ''
      if (m?.kind === kind) {
        // 同类型 → 去除列表标记
        texts[n - 1] = indent + text.slice(indent.length + m.marker.length)
      } else if (m) {
        // 异类型 → 替换标记
        texts[n - 1] = indent + newMarker(kind) + text.slice(indent.length + m.marker.length)
      } else {
        // 非列表行 → 加标记
        texts[n - 1] = indent + newMarker(kind) + text.slice(indent.length)
      }
    }
    // 有序列表：受影响行所在的连续有序段重编号（1,2,3…）
    if (kind === 'ordered') {
      for (const n of selected) {
        if (!ORDERED_RE.test(texts[n - 1])) continue
        let start = n
        while (start > 1 && ORDERED_RE.test(texts[start - 2])) start -= 1
        let counter = 1
        for (let i = start; i <= texts.length && ORDERED_RE.test(texts[i - 1]); i += 1) {
          texts[i - 1] = texts[i - 1].replace(ORDERED_RE, `$1${counter}. `)
          counter += 1
        }
      }
    }
    const newTexts = new Map<number, string>()
    for (const n of selected) newTexts.set(n, texts[n - 1])
    // 重编号可能改动选区外的行（同段后续行），一并下发
    if (kind === 'ordered') {
      for (let n = 1; n <= texts.length; n += 1) {
        if (texts[n - 1] !== state.doc.line(n).text) newTexts.set(n, texts[n - 1])
      }
    }
    replaceLines(view, newTexts)
    return true
  }
}

export const insertHorizontalRule: Command = (view) => {
  const { state } = view
  const range = state.selection.main
  const line = state.doc.lineAt(range.from)
  const needLeadingBreak = line.text.trim() !== '' && range.from !== line.to
  const atLineEnd = line.text.trim() !== '' && range.from === line.to
  const insert = needLeadingBreak || atLineEnd ? '\n---\n' : '---\n'
  const from = needLeadingBreak ? range.from : atLineEnd ? line.to : line.from
  view.dispatch({
    changes: { from, to: range.from === from ? range.to : from, insert },
    selection: EditorSelection.cursor(from + insert.length),
  })
  return true
}

export const insertCodeBlock: Command = (view) => {
  const { state } = view
  const range = state.selection.main
  if (range.empty) {
    // 空选区：插一对围栏，光标落中间空行
    const insert = '```\n\n```'
    view.dispatch({
      changes: { from: range.from, insert },
      selection: EditorSelection.cursor(range.from + 4),
    })
    return true
  }
  const fromLine = state.doc.lineAt(range.from)
  const toLine = state.doc.lineAt(range.to)
  view.dispatch({
    changes: [
      { from: fromLine.from, insert: '```\n' },
      { from: toLine.to, insert: '\n```' },
    ],
  })
  return true
}

// 对齐 Crepe insertTableCommand {row: 3, col: 3}：表头 + 3 行 3 列
const TABLE_TEMPLATE = '|   |   |   |\n| --- | --- | --- |\n|   |   |   |\n|   |   |   |\n|   |   |   |'

export const insertTable: Command = (view) => {
  const { state } = view
  const range = state.selection.main
  const line = state.doc.lineAt(range.from)
  // 非空行则落到行尾另起，避免把表格插进文本中间
  const from = line.text.trim() !== '' ? line.to : range.from
  const insert = (line.text.trim() !== '' ? '\n' : '') + TABLE_TEMPLATE + '\n'
  view.dispatch({
    changes: { from, to: from === range.from ? range.to : from, insert },
    selection: EditorSelection.cursor(from + insert.length),
  })
  return true
}

export const insertDateTime: Command = (view) => {
  view.dispatch(view.state.replaceSelection(formatTimestamp(new Date())))
  return true
}
