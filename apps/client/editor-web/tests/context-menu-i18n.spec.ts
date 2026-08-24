// 右键菜单 i18n 守护：从 model.ts 注册表收集全部 label（含子菜单），
// 断言每条都在 zh-Hans 字典中，防止新增菜单项漏翻译（同 i18n-coverage
// 的守护思路，msgid = 英文原文）。

import { describe, expect, it, vi } from 'vitest'

import {
  buildMenuRegistry,
  collectLabels,
  type MenuDeps,
} from '../src/context-menu/model'
import { createTranslator } from '../src/i18n'
import { zhHans } from '../src/i18n/locales/zh-hans'

// i18n 收集只关心 label，deps 全部空实现
function makeDeps(): MenuDeps {
  const noop = vi.fn()
  const asyncNoop = vi.fn(async () => {})
  return {
    commands: {
      undo: noop,
      redo: noop,
      toggleStrong: noop,
      toggleEmphasis: noop,
      toggleStrikeThrough: noop,
      toggleInlineCode: noop,
      wrapInHeading: noop,
      wrapInBlockquote: noop,
      insertHr: noop,
      wrapInBulletList: noop,
      wrapInOrderedList: noop,
      toggleTaskList: noop,
      createCodeBlock: noop,
      insertTable: noop,
      insertTimestamp: noop,
      toggleSourceMode: noop,
    },
    clipboard: { write: asyncNoop, read: vi.fn(async () => null) },
    copySelection: asyncNoop,
    pasteMarkdown: asyncNoop,
    pastePlainText: asyncNoop,
    selectAll: noop,
    removeLink: noop,
    openLink: noop,
    deleteTable: noop,
    copyCodeBlock: asyncNoop,
    editImageCaption: noop,
    deleteNode: noop,
    aiAction: noop,
    sourceCut: asyncNoop,
    sourceCopy: asyncNoop,
    sourcePaste: asyncNoop,
    sourceSelectAll: noop,
  }
}

describe('context-menu i18n 守护', () => {
  it('注册表全部 label 都有 zh-Hans 翻译', () => {
    const labels = collectLabels(buildMenuRegistry(makeDeps()))
    expect(labels.length).toBeGreaterThan(0)
    const missing = labels.filter((label) => !(label in zhHans))
    expect(missing).toEqual([])
  })

  it('翻译后的菜单文案不是英文原文', () => {
    const t = createTranslator('zh-Hans')
    for (const label of collectLabels(buildMenuRegistry(makeDeps()))) {
      expect(t(label)).not.toBe(label)
    }
  })
})
