// 菜单视图：全屏透明 mask + 绝对定位菜单容器。
// 定位：先渲染到屏外实测 getBoundingClientRect（不估常量），右/下空间不足
// 向反方向翻转，四边钳制到视口内 4px；子菜单同算法（锚点 = 父项右缘）。
// 关闭五件套：外部 mousedown（mask）/ Escape / 编辑器滚动（capture）/
// 窗口 resize / 打开期间 keydown 拦截（字符键与 Ctrl/Cmd 组合吞掉并关闭）。
// 菜单容器 mousedown preventDefault —— 点击菜单项不夺焦点、不丢 PM 选区。
// P1 键盘导航降级为只支持 Escape（设计 §4.4）。

import './context-menu.css'

import type { MenuEntry, MenuContext, MenuItem } from './model'

export interface Point {
  x: number
  y: number
}

export interface Size {
  width: number
  height: number
}

export interface PositionOptions {
  /** 视口内边距（钳制用），默认 4 */
  margin?: number
  /** 触发翻转所需的最小剩余空间，默认 8 */
  gap?: number
  /** 水平翻转后的 x（子菜单 = 父菜单左缘 - 子菜单宽；缺省 anchor.x - 宽） */
  flipX?: number
  /** 垂直翻转后的 y（缺省 anchor.y - 高） */
  flipY?: number
}

/** 定位纯函数（测试直接注入尺寸）。 */
export function computeMenuPosition(
  anchor: Point,
  menu: Size,
  viewport: Size,
  options: PositionOptions = {},
): Point {
  const margin = options.margin ?? 4
  const gap = options.gap ?? 8
  let { x, y } = anchor
  if (x + menu.width + gap > viewport.width) {
    x = options.flipX ?? anchor.x - menu.width
  }
  if (y + menu.height + gap > viewport.height) {
    y = options.flipY ?? anchor.y - menu.height
  }
  x = Math.min(Math.max(x, margin), Math.max(margin, viewport.width - menu.width - margin))
  y = Math.min(Math.max(y, margin), Math.max(margin, viewport.height - menu.height - margin))
  return { x, y }
}

interface MenuViewDeps {
  ctx: MenuContext
  /** label 翻译（msgid → 当前 locale） */
  t: (s: string) => string
  onClose: () => void
}

const SUBMENU_CLOSE_DELAY_MS = 200

export class MenuView {
  private mask: HTMLDivElement | null = null
  private container: HTMLDivElement | null = null
  private submenu: HTMLDivElement | null = null
  private submenuCloseTimer: ReturnType<typeof setTimeout> | null = null
  private deps: MenuViewDeps | null = null

  get isOpen(): boolean {
    return this.container !== null
  }

  open(entries: MenuEntry[], anchor: Point, deps: MenuViewDeps): void {
    this.close()
    this.deps = deps

    const mask = document.createElement('div')
    mask.className = 'kc-menu-mask'
    mask.addEventListener('mousedown', (event) => {
      event.preventDefault()
      this.close()
    })
    // mask 上的右键同样关菜单（否则会二次触发 contextmenu 冒泡）
    mask.addEventListener('contextmenu', (event) => {
      event.preventDefault()
      event.stopPropagation()
      this.close()
    })

    const container = document.createElement('div')
    container.className = 'kc-context-menu'
    container.setAttribute('role', 'menu')
    // 点击菜单不夺焦点，保住编辑器选区
    container.addEventListener('mousedown', (event) => event.preventDefault())
    this.renderEntries(container, entries)

    // 先挂到屏外实测尺寸，再计算落点
    container.style.left = '-9999px'
    container.style.top = '-9999px'
    document.body.appendChild(mask)
    document.body.appendChild(container)
    const rect = container.getBoundingClientRect()
    const pos = computeMenuPosition(anchor, rect, {
      width: window.innerWidth,
      height: window.innerHeight,
    })
    container.style.left = `${pos.x}px`
    container.style.top = `${pos.y}px`

    this.mask = mask
    this.container = container

    document.addEventListener('keydown', this.onKeydown, true)
    document.addEventListener('scroll', this.onScroll, true)
    window.addEventListener('resize', this.onResize)
  }

  close(): void {
    this.clearSubmenuTimer()
    this.submenu?.remove()
    this.submenu = null
    this.container?.remove()
    this.container = null
    this.mask?.remove()
    this.mask = null
    if (this.deps) {
      document.removeEventListener('keydown', this.onKeydown, true)
      document.removeEventListener('scroll', this.onScroll, true)
      window.removeEventListener('resize', this.onResize)
      this.deps.onClose()
      this.deps = null
    }
  }

  destroy(): void {
    this.close()
  }

  private renderEntries(root: HTMLDivElement, entries: MenuEntry[]): void {
    const deps = this.deps
    if (!deps) return
    for (const entry of entries) {
      if (entry === 'separator') {
        const sep = document.createElement('div')
        sep.className = 'kc-menu-sep'
        root.appendChild(sep)
        continue
      }
      root.appendChild(this.renderItem(entry))
    }
  }

  private renderItem(item: MenuItem): HTMLDivElement {
    const deps = this.deps!
    const el = document.createElement('div')
    el.className = 'kc-menu-item'
    el.setAttribute('role', 'menuitem')
    if (item.danger) el.classList.add('kc-menu-danger')
    const disabled = item.disabled?.(deps.ctx) ?? false
    if (disabled) el.classList.add('kc-menu-disabled')

    const label = document.createElement('span')
    label.className = 'kc-menu-label'
    label.textContent = deps.t(item.label)
    el.appendChild(label)

    if (item.shortcut) {
      const shortcut = document.createElement('span')
      shortcut.className = 'kc-menu-shortcut'
      shortcut.textContent = item.shortcut
      el.appendChild(shortcut)
    }
    if (item.children) {
      const arrow = document.createElement('span')
      arrow.className = 'kc-menu-arrow'
      arrow.textContent = '▸'
      el.appendChild(arrow)
      el.addEventListener('mouseenter', () => {
        this.clearSubmenuTimer()
        this.openSubmenu(item, el)
      })
      el.addEventListener('mouseleave', () => this.scheduleSubmenuClose())
    } else {
      el.addEventListener('mouseenter', () => this.scheduleSubmenuClose())
      if (!disabled && item.run) {
        el.addEventListener('click', () => {
          const run = item.run
          this.close()
          void run?.(deps.ctx)
        })
      }
    }
    return el
  }

  private openSubmenu(item: MenuItem, anchorEl: HTMLDivElement): void {
    const deps = this.deps
    if (!deps || !item.children) return
    this.submenu?.remove()
    this.submenu = null

    const sub = document.createElement('div')
    sub.className = 'kc-context-menu kc-context-submenu'
    sub.setAttribute('role', 'menu')
    sub.addEventListener('mousedown', (event) => event.preventDefault())
    sub.addEventListener('mouseenter', () => this.clearSubmenuTimer())
    sub.addEventListener('mouseleave', () => this.scheduleSubmenuClose())
    for (const child of item.children) {
      if (!child.isActive(deps.ctx)) continue
      sub.appendChild(this.renderItem(child))
    }
    sub.style.left = '-9999px'
    sub.style.top = '-9999px'
    document.body.appendChild(sub)

    const parentRect = anchorEl.getBoundingClientRect()
    const rect = sub.getBoundingClientRect()
    const pos = computeMenuPosition(
      { x: parentRect.right, y: parentRect.top },
      rect,
      { width: window.innerWidth, height: window.innerHeight },
      { flipX: parentRect.left - rect.width },
    )
    sub.style.left = `${pos.x}px`
    sub.style.top = `${pos.y}px`
    this.submenu = sub
  }

  private scheduleSubmenuClose(): void {
    this.clearSubmenuTimer()
    this.submenuCloseTimer = setTimeout(() => {
      this.submenu?.remove()
      this.submenu = null
    }, SUBMENU_CLOSE_DELAY_MS)
  }

  private clearSubmenuTimer(): void {
    if (this.submenuCloseTimer) {
      clearTimeout(this.submenuCloseTimer)
      this.submenuCloseTimer = null
    }
  }

  /** 打开期间拦截键盘：Escape 关；字符键 / Ctrl/Cmd 组合吞掉并关（防热键
   *  穿透到编辑器造成选区/内容变化）；方向键放行但关菜单。 */
  private onKeydown = (event: KeyboardEvent): void => {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      this.close()
      return
    }
    const isChar = event.key.length === 1
    const isCombo = event.ctrlKey || event.metaKey
    if (isChar || isCombo) {
      event.preventDefault()
      event.stopPropagation()
    }
    this.close()
  }

  private onScroll = (): void => {
    this.close()
  }

  private onResize = (): void => {
    this.close()
  }
}
