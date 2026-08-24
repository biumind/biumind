// 键盘导航（P2）：↑/↓ roving 高亮（循环 + 跳过 disabled / separator）、
// Enter 执行、→ 展开子菜单、← 收起子菜单、Escape 关闭。
// jsdom 直接 dispatch keydown 到 document（监听器挂在 document capture）。

import { afterEach, describe, expect, it, vi } from 'vitest'

import type { MenuContext, MenuEntry, MenuItem } from '../src/context-menu/model'
import { MenuView } from '../src/context-menu/view'

const ctx: MenuContext = {
  itemType: 'text',
  hasSelection: true,
  readOnly: false,
  canPaste: true,
  aiActions: false,
  imageUpload: false,
  from: 0,
  to: 1,
}

function item(
  id: string,
  overrides: Partial<MenuItem> = {},
): MenuItem {
  return {
    id,
    label: id,
    isActive: () => true,
    ...overrides,
  }
}

function key(k: string, init: KeyboardEventInit = {}): void {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: k, ...init }))
}

function activeId(): string | null {
  // 子菜单打开时父子两级都有高亮态，取文档序最后（最深一级）的
  const all = document.querySelectorAll('.kc-menu-item.kc-menu-active')
  const el = all[all.length - 1]
  if (!el) return null
  return el.querySelector('.kc-menu-label')?.textContent ?? null
}

function openMenu(entries: MenuEntry[]): { view: MenuView; onClose: ReturnType<typeof vi.fn> } {
  const view = new MenuView()
  const onClose = vi.fn()
  view.open(entries, { x: 10, y: 10 }, { ctx, t: (s) => s, onClose })
  return { view, onClose }
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('菜单键盘导航', () => {
  it('↓ 从第一个可用项开始高亮，循环回绕', () => {
    openMenu([item('a'), item('b'), item('c')])
    key('ArrowDown')
    expect(activeId()).toBe('a')
    key('ArrowDown')
    expect(activeId()).toBe('b')
    key('ArrowDown')
    key('ArrowDown') // 回绕到 a
    expect(activeId()).toBe('a')
    key('ArrowUp') // 反向回绕到 c
    expect(activeId()).toBe('c')
  })

  it('跳过 disabled 项与分隔线', () => {
    openMenu([
      item('a'),
      'separator',
      item('b', { disabled: () => true }),
      item('c'),
    ])
    key('ArrowDown')
    expect(activeId()).toBe('a')
    key('ArrowDown') // 跳过 disabled 的 b
    expect(activeId()).toBe('c')
  })

  it('Enter 执行高亮项并关闭菜单', () => {
    const run = vi.fn()
    const { view, onClose } = openMenu([item('a'), item('b', { run })])
    key('ArrowDown')
    key('ArrowDown') // 高亮 b
    key('Enter')
    expect(run).toHaveBeenCalledOnce()
    expect(view.isOpen).toBe(false)
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('Enter 对 disabled 项不执行', () => {
    const run = vi.fn()
    openMenu([item('a', { disabled: () => true, run }), item('b')])
    key('ArrowDown') // a disabled → 高亮 b
    expect(activeId()).toBe('b')
    key('ArrowUp') // 回绕仍跳过 a → 停在 b
    expect(activeId()).toBe('b')
    expect(run).not.toHaveBeenCalled()
  })

  it('→ 展开子菜单并高亮首项，← 收起回父级', () => {
    const childRun = vi.fn()
    const { view } = openMenu([
      item('parent', { children: [item('child-1', { run: childRun }), item('child-2')] }),
      item('tail'),
    ])
    key('ArrowDown') // 高亮 parent
    expect(activeId()).toBe('parent')
    key('ArrowRight') // 展开子菜单
    expect(view.isOpen).toBe(true)
    expect(document.querySelectorAll('.kc-context-submenu').length).toBe(1)
    expect(activeId()).toBe('child-1')
    key('ArrowDown') // 子菜单内移动
    expect(activeId()).toBe('child-2')
    key('ArrowLeft') // 收起子菜单
    expect(document.querySelectorAll('.kc-context-submenu').length).toBe(0)
    key('ArrowDown') // 回主菜单移动：parent → tail
    expect(activeId()).toBe('tail')
  })

  it('Enter 对子菜单父项 = 展开子菜单；子项 Enter 执行', () => {
    const childRun = vi.fn()
    const { view } = openMenu([
      item('parent', { children: [item('child', { run: childRun })] }),
    ])
    key('ArrowDown')
    key('Enter') // 展开
    expect(view.isOpen).toBe(true)
    expect(activeId()).toBe('parent') // 父项保持高亮，子菜单待 ↓ 进入
    key('ArrowDown')
    expect(activeId()).toBe('child')
    key('Enter')
    expect(childRun).toHaveBeenCalledOnce()
    expect(view.isOpen).toBe(false)
  })

  it('Escape 关闭；字符键吞掉并关闭（防热键穿透）', () => {
    const { view, onClose } = openMenu([item('a')])
    key('Escape')
    expect(view.isOpen).toBe(false)
    expect(onClose).toHaveBeenCalledOnce()

    const second = openMenu([item('a')])
    key('c', { metaKey: true }) // ⌘C 穿透拦截
    expect(second.view.isOpen).toBe(false)
  })
})
