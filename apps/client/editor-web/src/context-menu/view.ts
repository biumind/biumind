// 菜单视图：全屏透明 mask + 绝对定位菜单容器。
// 定位：先渲染到屏外实测 getBoundingClientRect（不估常量），右/下空间不足
// 向反方向翻转，四边钳制到视口内 4px；子菜单同算法（锚点 = 父项右缘）。
// 关闭五件套：外部 mousedown（mask）/ Escape / 编辑器滚动（capture）/
// 窗口 resize / 打开期间 keydown 拦截（字符键与 Ctrl/Cmd 组合吞掉并关闭）。
// 菜单容器 mousedown preventDefault —— 点击菜单项不夺焦点、不丢 PM 选区。
// 键盘导航（P2）：roving 高亮（↑/↓ 循环、跳过分隔线与禁用项）、Enter 执行、
// → 展开子菜单、← 收起子菜单、Escape 关闭；焦点始终留在编辑器（keydown 在
// document capture 拦截），高亮只是视觉态 + aria-activedescendant 标注。

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

interface RenderedItem {
  el: HTMLDivElement
  item: MenuItem
  disabled: boolean
}

/** 一级菜单（主菜单或一个子菜单）的渲染状态 */
interface MenuLevel {
  root: HTMLDivElement
  items: RenderedItem[]
  /** 高亮索引（-1 = 无） */
  index: number
}

const SUBMENU_CLOSE_DELAY_MS = 200
const ACTIVE_CLASS = 'kc-menu-active'

export class MenuView {
  private mask: HTMLDivElement | null = null
  private levels: MenuLevel[] = []
  private submenuCloseTimer: ReturnType<typeof setTimeout> | null = null
  private deps: MenuViewDeps | null = null
  private idSeq = 0

  get isOpen(): boolean {
    return this.levels.length > 0
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

    const container = this.buildMenuRoot()
    const level = this.renderLevel(container, entries)

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
    this.levels = [level]

    document.addEventListener('keydown', this.onKeydown, true)
    document.addEventListener('scroll', this.onScroll, true)
    window.addEventListener('resize', this.onResize)
  }

  close(): void {
    this.clearSubmenuTimer()
    for (const level of this.levels) level.root.remove()
    this.levels = []
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

  // ── 渲染 ────────────────────────────────────────────────

  private buildMenuRoot(): HTMLDivElement {
    const root = document.createElement('div')
    root.className = 'kc-context-menu'
    root.setAttribute('role', 'menu')
    // 点击菜单不夺焦点，保住编辑器选区
    root.addEventListener('mousedown', (event) => event.preventDefault())
    return root
  }

  private renderLevel(root: HTMLDivElement, entries: MenuEntry[]): MenuLevel {
    const deps = this.deps!
    const level: MenuLevel = { root, items: [], index: -1 }
    for (const entry of entries) {
      if (entry === 'separator') {
        const sep = document.createElement('div')
        sep.className = 'kc-menu-sep'
        root.appendChild(sep)
        continue
      }
      const rendered = this.renderItem(level, entry, deps)
      level.items.push(rendered)
      root.appendChild(rendered.el)
    }
    return level
  }

  private renderItem(
    level: MenuLevel,
    item: MenuItem,
    deps: MenuViewDeps,
  ): RenderedItem {
    const el = document.createElement('div')
    el.className = 'kc-menu-item'
    el.setAttribute('role', 'menuitem')
    el.id = `kc-menu-item-${this.idSeq++}`
    if (item.danger) el.classList.add('kc-menu-danger')
    const disabled = item.disabled?.(deps.ctx) ?? false
    if (disabled) {
      el.classList.add('kc-menu-disabled')
      el.setAttribute('aria-disabled', 'true')
    }

    // 图标列固定宽：无图标项也留占位，label 对齐
    const icon = document.createElement('span')
    icon.className = 'kc-menu-icon'
    if (item.icon) icon.innerHTML = item.icon
    el.appendChild(icon)

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
      el.setAttribute('aria-haspopup', 'menu')
    }

    const rendered: RenderedItem = { el, item, disabled }
    el.addEventListener('mouseenter', () => {
      this.clearSubmenuTimer()
      this.setHighlight(level, level.items.indexOf(rendered))
      if (item.children) {
        this.openSubmenu(level, rendered)
      } else {
        this.scheduleSubmenuClose()
      }
    })
    el.addEventListener('mouseleave', () => {
      if (item.children) this.scheduleSubmenuClose()
    })
    if (!disabled && (item.run || item.children)) {
      el.addEventListener('click', () => this.activate(level, rendered))
    }
    return rendered
  }

  // ── 键盘导航（roving 高亮） ─────────────────────────────

  private currentLevel(): MenuLevel {
    return this.levels[this.levels.length - 1]
  }

  private setHighlight(level: MenuLevel, index: number): void {
    if (level.index >= 0 && level.items[level.index]) {
      level.items[level.index].el.classList.remove(ACTIVE_CLASS)
    }
    level.index = index
    if (index >= 0 && level.items[index]) {
      const { el } = level.items[index]
      el.classList.add(ACTIVE_CLASS)
      level.root.setAttribute('aria-activedescendant', el.id)
      // jsdom 未实现 scrollIntoView —— 可选调用，测试环境跳过
      el.scrollIntoView?.({ block: 'nearest' })
    } else {
      level.root.removeAttribute('aria-activedescendant')
    }
  }

  /** 在当前级内循环移动高亮，跳过禁用项（分隔线不进 items 列表）。 */
  private moveHighlight(delta: number): void {
    const level = this.currentLevel()
    const n = level.items.length
    if (n === 0) return
    let index = level.index
    for (let step = 0; step < n; step += 1) {
      index = (index + delta + n) % n
      if (!level.items[index].disabled) {
        this.setHighlight(level, index)
        return
      }
    }
  }

  /** 执行高亮项：有子菜单展开之，否则关菜单并 run。 */
  private activate(level: MenuLevel, rendered: RenderedItem): void {
    if (rendered.disabled) return
    if (rendered.item.children) {
      this.openSubmenu(level, rendered)
      return
    }
    const { item } = rendered
    const deps = this.deps
    this.close()
    if (deps && item.run) void item.run(deps.ctx)
  }

  // ── 子菜单 ─────────────────────────────────────────────

  private openSubmenu(parentLevel: MenuLevel, parent: RenderedItem): void {
    const deps = this.deps
    if (!deps || !parent.item.children) return
    // 已挂在该父项下的子菜单不重建
    const existing = this.levels[this.levels.indexOf(parentLevel) + 1]
    if (existing?.root.dataset.parentItem === parent.el.id) return
    this.closeLevelsAfter(parentLevel)

    const sub = this.buildMenuRoot()
    sub.classList.add('kc-context-submenu')
    sub.dataset.parentItem = parent.el.id
    const children = parent.item.children.filter((child) =>
      child.isActive(deps.ctx),
    )
    const level = this.renderLevel(sub, children)
    sub.addEventListener('mouseenter', () => this.clearSubmenuTimer())
    sub.addEventListener('mouseleave', () => this.scheduleSubmenuClose())

    sub.style.left = '-9999px'
    sub.style.top = '-9999px'
    document.body.appendChild(sub)

    const parentRect = parent.el.getBoundingClientRect()
    const rect = sub.getBoundingClientRect()
    const pos = computeMenuPosition(
      { x: parentRect.right, y: parentRect.top },
      rect,
      { width: window.innerWidth, height: window.innerHeight },
      { flipX: parentRect.left - rect.width },
    )
    sub.style.left = `${pos.x}px`
    sub.style.top = `${pos.y}px`

    this.levels.push(level)
    // 键盘展开时高亮首个可用项；鼠标 hover 展开保持无高亮（跟随鼠标）
    parent.el.setAttribute('aria-expanded', 'true')
  }

  /** 收起 level 之后的所有子菜单层 */
  private closeLevelsAfter(level: MenuLevel): void {
    const idx = this.levels.indexOf(level)
    if (idx < 0) return
    for (const l of this.levels.splice(idx + 1)) {
      l.root.remove()
    }
  }

  private scheduleSubmenuClose(): void {
    this.clearSubmenuTimer()
    this.submenuCloseTimer = setTimeout(() => {
      // 只收子菜单层，主菜单不动
      for (const l of this.levels.splice(1)) l.root.remove()
    }, SUBMENU_CLOSE_DELAY_MS)
  }

  private clearSubmenuTimer(): void {
    if (this.submenuCloseTimer) {
      clearTimeout(this.submenuCloseTimer)
      this.submenuCloseTimer = null
    }
  }

  // ── 全局监听（打开期间） ────────────────────────────────

  /** 打开期间拦截键盘：导航键驱动 roving 高亮；Escape 关；字符键 /
   *  Ctrl/Cmd 组合吞掉并关（防热键穿透到编辑器造成选区/内容变化）。 */
  private onKeydown = (event: KeyboardEvent): void => {
    switch (event.key) {
      case 'Escape':
        event.preventDefault()
        event.stopPropagation()
        this.close()
        return
      case 'ArrowDown':
        event.preventDefault()
        event.stopPropagation()
        this.moveHighlight(1)
        return
      case 'ArrowUp':
        event.preventDefault()
        event.stopPropagation()
        this.moveHighlight(-1)
        return
      case 'ArrowRight': {
        const level = this.currentLevel()
        const current = level.items[level.index]
        if (current?.item.children) {
          event.preventDefault()
          event.stopPropagation()
          this.openSubmenu(level, current)
          // 键盘展开子菜单：高亮首个可用项
          this.moveHighlight(1)
        }
        return
      }
      case 'ArrowLeft':
        if (this.levels.length > 1) {
          event.preventDefault()
          event.stopPropagation()
          for (const l of this.levels.splice(1)) l.root.remove()
        }
        return
      case 'Enter': {
        const level = this.currentLevel()
        const current = level.items[level.index]
        if (current) {
          event.preventDefault()
          event.stopPropagation()
          this.activate(level, current)
        }
        return
      }
      case 'Tab':
        // 放行会移焦到编辑器外，吞掉但不动菜单
        event.preventDefault()
        event.stopPropagation()
        return
      default: {
        const isChar = event.key.length === 1
        const isCombo = event.ctrlKey || event.metaKey
        if (isChar || isCombo) {
          event.preventDefault()
          event.stopPropagation()
        }
        this.close()
      }
    }
  }

  private onScroll = (): void => {
    this.close()
  }

  private onResize = (): void => {
    this.close()
  }
}
