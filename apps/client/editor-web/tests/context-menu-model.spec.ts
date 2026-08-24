// 菜单模型过滤快照：各 itemType × readOnly × aiActions 组合下 isActive
// 过滤结果 + disabled 谓词（剪贴板空）。注册表是唯一数据源，过滤逻辑一改
// 这里就红，防止菜单项谓词散弹回归。

import { describe, expect, it, vi } from 'vitest'

import type { ToolbarActions } from '../src/toolbar'
import {
  buildMenuRegistry,
  filterMenuEntries,
  type MenuContext,
  type MenuDeps,
  type MenuEntry,
} from '../src/context-menu/model'

function makeDeps(): MenuDeps {
  const commands = {
    undo: vi.fn(),
    redo: vi.fn(),
    toggleStrong: vi.fn(),
    toggleEmphasis: vi.fn(),
    toggleStrikeThrough: vi.fn(),
    toggleInlineCode: vi.fn(),
    wrapInHeading: vi.fn(),
    wrapInBlockquote: vi.fn(),
    insertHr: vi.fn(),
    wrapInBulletList: vi.fn(),
    wrapInOrderedList: vi.fn(),
    toggleTaskList: vi.fn(),
    createCodeBlock: vi.fn(),
    insertTable: vi.fn(),
    insertTimestamp: vi.fn(),
    toggleSourceMode: vi.fn(),
  } satisfies ToolbarActions
  return {
    commands,
    clipboard: {
      write: vi.fn(async () => {}),
      read: vi.fn(async () => ({ text: 'x' })),
    },
    copySelection: vi.fn(async () => {}),
    pasteMarkdown: vi.fn(async () => {}),
    pastePlainText: vi.fn(async () => {}),
    selectAll: vi.fn(),
    removeLink: vi.fn(),
    openLink: vi.fn(),
    deleteTable: vi.fn(),
    copyCodeBlock: vi.fn(async () => {}),
    editImageCaption: vi.fn(),
    deleteNode: vi.fn(),
    aiAction: vi.fn(),
    sourceCut: vi.fn(async () => {}),
    sourceCopy: vi.fn(async () => {}),
    sourcePaste: vi.fn(async () => {}),
    sourceSelectAll: vi.fn(),
  }
}

function ctx(overrides: Partial<MenuContext>): MenuContext {
  return {
    itemType: 'text',
    hasSelection: true,
    readOnly: false,
    canPaste: true,
    aiActions: false,
    from: 1,
    to: 5,
    ...overrides,
  }
}

/** 可见项 id 快照，分隔线记为 | */
function ids(entries: MenuEntry[]): string {
  return entries.map((e) => (e === 'separator' ? '|' : e.id)).join(',')
}

describe('context-menu model 过滤', () => {
  const registry = buildMenuRegistry(makeDeps())

  it('text 有选区（可编辑）', () => {
    expect(ids(filterMenuEntries(registry, ctx({})))).toBe(
      'cut,copy,paste,paste-plain,|,format-strong,format-emphasis,' +
        'format-strikethrough,format-inline-code,convert',
    )
  })

  it('text 光标态（可编辑）', () => {
    expect(
      ids(filterMenuEntries(registry, ctx({ hasSelection: false }))),
    ).toBe('paste,paste-plain,select-all,|,convert,|,insert')
  })

  it('text 有选区 + aiActions（P2 开关打开时 AI 组出现）', () => {
    expect(ids(filterMenuEntries(registry, ctx({ aiActions: true })))).toBe(
      'cut,copy,paste,paste-plain,|,format-strong,format-emphasis,' +
        'format-strikethrough,format-inline-code,convert,|,ai-ask,ai-edit',
    )
  })

  it('link（有选区，可编辑）', () => {
    expect(
      ids(
        filterMenuEntries(
          registry,
          ctx({ itemType: 'link', linkHref: 'https://a.b' }),
        ),
      ),
    ).toBe('cut,copy,paste,paste-plain,|,link-open,link-copy,link-remove')
  })

  it('link（readOnly）：只剩 复制/打开链接/复制链接', () => {
    expect(
      ids(
        filterMenuEntries(
          registry,
          ctx({ itemType: 'link', linkHref: 'https://a.b', readOnly: true }),
        ),
      ),
    ).toBe('copy,|,link-open,link-copy')
  })

  it('image（可编辑）：P1 只有 编辑说明/删除', () => {
    expect(
      ids(filterMenuEntries(registry, ctx({ itemType: 'image', nodePos: 3 }))),
    ).toBe('image-caption,image-delete')
  })

  it('image（readOnly）：无可见项（菜单不弹）', () => {
    expect(
      filterMenuEntries(registry, ctx({ itemType: 'image', readOnly: true })),
    ).toEqual([])
  })

  it('tableCell（可编辑）：crepe 手柄已覆盖行列/对齐，只补删除表格', () => {
    expect(ids(filterMenuEntries(registry, ctx({ itemType: 'tableCell' })))).toBe(
      'table-delete',
    )
  })

  it('codeBlock：复制代码（readOnly 也可用）', () => {
    expect(ids(filterMenuEntries(registry, ctx({ itemType: 'codeBlock' })))).toBe(
      'code-copy',
    )
    expect(
      ids(
        filterMenuEntries(registry, ctx({ itemType: 'codeBlock', readOnly: true })),
      ),
    ).toBe('code-copy')
  })

  it('source 有选区（可编辑）：剪切/复制/粘贴/全选', () => {
    expect(ids(filterMenuEntries(registry, ctx({ itemType: 'source' })))).toBe(
      'cut,copy,paste,select-all',
    )
  })

  it('source 无选区（可编辑）：粘贴/全选', () => {
    expect(
      ids(
        filterMenuEntries(registry, ctx({ itemType: 'source', hasSelection: false })),
      ),
    ).toBe('paste,select-all')
  })

  it('source（readOnly）：只剩 复制', () => {
    expect(
      ids(
        filterMenuEntries(registry, ctx({ itemType: 'source', readOnly: true })),
      ),
    ).toBe('copy')
  })

  it('text（readOnly）：只剩 复制', () => {
    expect(ids(filterMenuEntries(registry, ctx({ readOnly: true })))).toBe('copy')
  })
})

describe('disabled 谓词', () => {
  const registry = buildMenuRegistry(makeDeps())

  it('剪贴板为空时 粘贴/粘贴为纯文本 置灰', () => {
    const c = ctx({ canPaste: false })
    const entries = filterMenuEntries(registry, c)
    const paste = entries.find((e) => e !== 'separator' && e.id === 'paste')
    const pastePlain = entries.find(
      (e) => e !== 'separator' && e.id === 'paste-plain',
    )
    expect(paste).not.toBe('separator')
    expect(pastePlain).not.toBe('separator')
    if (paste !== 'separator' && paste && pastePlain !== 'separator' && pastePlain) {
      expect(paste.disabled?.(c)).toBe(true)
      expect(pastePlain.disabled?.(c)).toBe(true)
    }
  })

  it('剪贴板有内容时粘贴可用', () => {
    const c = ctx({ canPaste: true })
    const entries = filterMenuEntries(registry, c)
    const paste = entries.find((e) => e !== 'separator' && e.id === 'paste')
    if (paste && paste !== 'separator') {
      expect(paste.disabled?.(c)).toBe(false)
    }
  })
})
