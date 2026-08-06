// 命令路由表：命令名 → CM6 Command。供 Cm6Handle.execCommand、工具栏闭包
// 与桥接 command:undo/redo 共用。

import { redo, undo } from '@codemirror/commands'
import type { Command } from '@codemirror/view'
import {
  insertCodeBlock,
  insertDateTime,
  insertHorizontalRule,
  insertTable,
  toggleBlockquote,
  toggleHeaderLevel,
  toggleList,
} from './block-format'
import {
  toggleBold,
  toggleInlineCode,
  toggleItalic,
  toggleStrikethrough,
} from './inline-format'

export type Cm6CommandName =
  | 'undo'
  | 'redo'
  | 'toggleStrong'
  | 'toggleEmphasis'
  | 'toggleStrikeThrough'
  | 'toggleInlineCode'
  | 'h1'
  | 'h2'
  | 'h3'
  | 'blockquote'
  | 'hr'
  | 'bulletList'
  | 'orderedList'
  | 'taskList'
  | 'codeBlock'
  | 'table'
  | 'timestamp'

export const cm6Commands: Record<Cm6CommandName, Command> = {
  undo,
  redo,
  toggleStrong: toggleBold,
  toggleEmphasis: toggleItalic,
  toggleStrikeThrough: toggleStrikethrough,
  toggleInlineCode,
  h1: toggleHeaderLevel(1),
  h2: toggleHeaderLevel(2),
  h3: toggleHeaderLevel(3),
  blockquote: toggleBlockquote,
  hr: insertHorizontalRule,
  bulletList: toggleList('bullet'),
  orderedList: toggleList('ordered'),
  taskList: toggleList('task'),
  codeBlock: insertCodeBlock,
  table: insertTable,
  timestamp: insertDateTime,
}

export function isCm6Command(name: string): name is Cm6CommandName {
  return name in cm6Commands
}
