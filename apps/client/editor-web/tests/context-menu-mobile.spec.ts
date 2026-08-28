// view.ts 移动端裁剪（M1，设计 §7）：mobileLayout = true 时子菜单改为
// 同级替换式（点按父项 → 当前级内容替换为子级 + 顶部「‹ 返回」），
// mask 关菜单统一 pointerdown（兼容 touch/mouse，桌面路径回归）。

import { afterEach, describe, expect, it, vi } from 'vitest'

import { ContextMenuController } from '../src/context-menu/index'
import type { MenuContext, MenuEntry, MenuItem } from '../src/context-menu/model'
import type { ToolbarActions } from '../src/toolbar'
import type { BridgeClient } from '../src/bridge/client'
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

function item(id: string, overrides: Partial<MenuItem> = {}): MenuItem {
  return { id, label: id, isActive: () => true, ...overrides }
}

function labels(root: ParentNode = document): string[] {
  return [...root.querySelectorAll('.kc-menu-item .kc-menu-label')].map(
    (el) => el.textContent ?? '',
  )
}

function openMobile(entries: MenuEntry[]): MenuView {
  const view = new MenuView()
  view.mobileLayout = true
  view.open(entries, { x: 10, y: 10 }, { ctx, t: (s) => s, onClose: () => {} })
  return view
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('移动端同级替换式子菜单', () => {
  const entries: MenuEntry[] = [
    item('parent', { children: [item('child-1'), item('child-2')] }),
    item('tail'),
  ]

  it('点按父项 → 当前级替换为子级 + 顶部「‹ 返回」（不产生悬浮子菜单）', () => {
    openMobile(entries)
    document
      .querySelectorAll<HTMLDivElement>('.kc-menu-item')[0]
      .dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(document.querySelectorAll('.kc-context-submenu').length).toBe(0)
    expect(labels()).toEqual(['Back', 'child-1', 'child-2'])
    expect(document.querySelector('.kc-menu-back')).not.toBeNull()
  })

  it('「‹ 返回」点按回上级，菜单保持打开', () => {
    const view = openMobile(entries)
    document
      .querySelectorAll<HTMLDivElement>('.kc-menu-item')[0]
      .dispatchEvent(new MouseEvent('click', { bubbles: true }))
    document
      .querySelector<HTMLDivElement>('.kc-menu-back')!
      .dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(labels()).toEqual(['parent', 'tail'])
    expect(view.isOpen).toBe(true)
  })

  it('键盘 → 替换、← 回退', () => {
    openMobile(entries)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }))
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' }))
    expect(labels()).toEqual(['Back', 'child-1', 'child-2'])
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft' }))
    expect(labels()).toEqual(['parent', 'tail'])
  })

  it('子级动作执行仍关菜单', () => {
    const run = vi.fn()
    const view = openMobile([
      item('parent', { children: [item('child', { run })] }),
    ])
    document
      .querySelectorAll<HTMLDivElement>('.kc-menu-item')[0]
      .dispatchEvent(new MouseEvent('click', { bubbles: true }))
    const items = document.querySelectorAll<HTMLDivElement>('.kc-menu-item')
    items[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(run).toHaveBeenCalledOnce()
    expect(view.isOpen).toBe(false)
  })

  it('移动端 hover 不展开子菜单', () => {
    openMobile(entries)
    document
      .querySelectorAll<HTMLDivElement>('.kc-menu-item')[0]
      .dispatchEvent(new MouseEvent('mouseenter', { bubbles: true }))
    expect(document.querySelectorAll('.kc-context-submenu').length).toBe(0)
    expect(labels()).toEqual(['parent', 'tail'])
  })
})

describe('mask 关菜单统一 pointerdown（桌面回归）', () => {
  it('mask pointerdown 关闭菜单（mouse/touch 同一事件路径）', () => {
    const view = new MenuView()
    const onClose = vi.fn()
    view.open([item('a')], { x: 10, y: 10 }, { ctx, t: (s) => s, onClose })
    document
      .querySelector('.kc-menu-mask')!
      // jsdom 无 PointerEvent 构造器 —— 事件 type 才是分发依据
      .dispatchEvent(new Event('pointerdown', { bubbles: true, cancelable: true }))
    expect(view.isOpen).toBe(false)
    expect(onClose).toHaveBeenCalledOnce()
  })
})

describe('菜单内部滚动不触发关闭（实机 bug：scroll capture 误捕菜单自身）', () => {
  it('菜单内部元素 scroll → 保持打开（scrollIntoView/用户滚动菜单）', () => {
    const view = new MenuView()
    view.open([item('a'), item('b')], { x: 10, y: 10 }, { ctx, t: (s) => s, onClose: () => {} })
    const inner = document.querySelector('.kc-menu-item')!
    // scroll 不冒泡，但 capture 监听在 document 上能收到（沿捕获链下行）
    inner.dispatchEvent(new Event('scroll', { bubbles: false }))
    expect(view.isOpen).toBe(true)
  })

  it('菜单容器自身 scroll（max-height 截断后滚动查看）→ 保持打开', () => {
    const view = new MenuView()
    view.open([item('a')], { x: 10, y: 10 }, { ctx, t: (s) => s, onClose: () => {} })
    document
      .querySelector('.kc-context-menu')!
      .dispatchEvent(new Event('scroll', { bubbles: false }))
    expect(view.isOpen).toBe(true)
  })

  it('菜单外部 scroll → 关闭', () => {
    const view = new MenuView()
    const onClose = vi.fn()
    view.open([item('a')], { x: 10, y: 10 }, { ctx, t: (s) => s, onClose })
    const outside = document.createElement('div')
    document.body.appendChild(outside)
    outside.dispatchEvent(new Event('scroll', { bubbles: false }))
    expect(view.isOpen).toBe(false)
    expect(onClose).toHaveBeenCalledOnce()
  })
})

// 实机 bug：Android WebView 长按选词完成时发 contextmenu，编辑器区域照常
// 弹竖向菜单，与 selectionUpdated 驱动的选区浮动工具条同时出现（双菜单）。
// 修复：mobile=true 时编辑器区域 contextmenu 只 swallow 不弹菜单（源码模式
// 例外——选区工具条在源码模式不显示，此菜单是唯一编辑入口）。
describe('移动端 contextmenu 只 swallow 不弹菜单（双菜单实机 bug）', () => {
  function makeController(mobile: boolean) {
    const getCrepe = vi.fn(() => null)
    const controller = new ContextMenuController({
      bridge: { sendLog: vi.fn() } as unknown as BridgeClient,
      commands: {} as unknown as ToolbarActions,
      getCrepe,
      getSourceMode: () => null,
      getReadOnly: () => false,
      aiActions: false,
      imageUpload: false,
      locale: 'zh-Hans',
      resolveImageUrl: async (url: string) => url,
      mobile,
    })
    controller.attach()
    return { controller, getCrepe }
  }

  function fireContextMenuInEditor(): MouseEvent {
    const editor = document.createElement('div')
    editor.className = 'milkdown'
    const target = document.createElement('p')
    editor.appendChild(target)
    document.body.appendChild(editor)
    const ev = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    target.dispatchEvent(ev)
    return ev
  }

  it('mobile=true：事件被 swallow（抑制系统 UI）但不进 openMenu', () => {
    const { controller, getCrepe } = makeController(true)
    const ev = fireContextMenuInEditor()
    expect(ev.defaultPrevented).toBe(true)
    expect(getCrepe).not.toHaveBeenCalled()
    expect(document.querySelector('.kc-context-menu')).toBeNull()
    controller.destroy()
  })

  it('mobile=false（桌面路径回归）：照常进 openMenu', () => {
    const { controller, getCrepe } = makeController(false)
    const ev = fireContextMenuInEditor()
    expect(ev.defaultPrevented).toBe(true)
    expect(getCrepe).toHaveBeenCalled()
    controller.destroy()
  })
})
