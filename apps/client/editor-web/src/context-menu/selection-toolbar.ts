// 场景 A：选区浮动工具条（移动端 M1，设计 §3）。
// 驱动 = PM selectionUpdated（选择动作由系统手势完成，选区落定自然触发，
// 不依赖长按）；显示条件 = 选区非空 + 非源码模式，**不以 view.hasFocus()
// 为准**（Android 坑：selection 在、focus 丢，以 focus 为准工具条永不出现）。
// 定位 position:fixed，锚点 = 选区矩形（coordsAtPos from/to），默认选区上方、
// 不足翻下方，clamp 用 window.visualViewport（键盘弹起时 innerHeight 会算
// 出被键盘遮挡的位置）。
// 保选区：工具条及菜单所有可点元素 pointerdown preventDefault（blur 先于
// click 到达；桌面 mousedown preventDefault 的 touch 等价路径）。
// 只在 data-platform = ios/android 时由 main.ts 创建 —— 桌面端绝不出现
// （桌面有右键菜单 + crepe 自带选区工具栏，三重 UI 不可接受）。

import './mobile.css'

import type { EditorView } from 'prosemirror-view'

import type { ClipboardBackend } from './clipboard'
import { filterMenuEntries, type MenuContext, type MenuDeps, type MenuEntry } from './model'
import {
  MenuView,
  computeAvoidingY,
  currentViewport,
  type AnchorRect,
  type Point,
  type Size,
  type ViewportBox,
} from './view'

export type { AnchorRect, ViewportBox } from './view'

// ── 纯函数（单测直接注入） ───────────────────────────────

export type SelectionToolbarItemId =
  | 'cut'
  | 'copy'
  | 'paste'
  | 'select-all'
  | 'more'

export interface SelectionToolbarItemSpec {
  id: SelectionToolbarItemId
  /** i18n msgid（英文原文），渲染时过 translator */
  label: string
  disabled: boolean
}

/** 项集纯函数（AppFlowy ContextMenuButtonItem 同款思路）：
 *  可编辑 = 剪切/复制/粘贴（剪贴板空置灰）/全选/更多▸；readOnly = 复制/全选。 */
export function buildSelectionToolbarItems(opts: {
  readOnly: boolean
  canPaste: boolean
}): SelectionToolbarItemSpec[] {
  if (opts.readOnly) {
    return [
      { id: 'copy', label: 'Copy', disabled: false },
      { id: 'select-all', label: 'Select All', disabled: false },
    ]
  }
  return [
    { id: 'cut', label: 'Cut', disabled: false },
    { id: 'copy', label: 'Copy', disabled: false },
    { id: 'paste', label: 'Paste', disabled: !opts.canPaste },
    { id: 'select-all', label: 'Select All', disabled: false },
    { id: 'more', label: 'More', disabled: false },
  ]
}

/** 显示条件纯函数：选区非空 + 非源码模式（故意不收 hasFocus 参数）。 */
export function shouldShowSelectionToolbar(opts: {
  selectionEmpty: boolean
  sourceMode: boolean
}): boolean {
  return !opts.selectionEmpty && !opts.sourceMode
}

/** 定位纯函数：默认选区上方居中，上方不足翻下方，水平/垂直钳制进视口。
 *  纵向避让逻辑与「更多」菜单共用 computeAvoidingY：上方优先、不足翻下、
 *  两侧都不足（键盘压缩 visualViewport）选空闲更大的一侧 —— 极端情况
 *  允许与选区部分重叠（钳制保证工具条留在可见视口内，不会被推回完全
 *  压住选区）。 */
export function computeSelectionToolbarPosition(
  anchor: AnchorRect,
  size: Size,
  vp: ViewportBox,
  gap = 8,
): Point {
  const margin = 4
  const maxX = Math.max(
    vp.offsetLeft + margin,
    vp.offsetLeft + vp.width - size.width - margin,
  )
  const x = Math.min(
    Math.max((anchor.left + anchor.right) / 2 - size.width / 2, vp.offsetLeft + margin),
    maxX,
  )
  const y = computeAvoidingY(anchor, size.height, vp, gap)
  const maxY = Math.max(
    vp.offsetTop + margin,
    vp.offsetTop + vp.height - size.height - margin,
  )
  return { x, y: Math.min(Math.max(y, vp.offsetTop + margin), maxY) }
}

// ── 控制器 ───────────────────────────────────────────────

export interface SelectionToolbarDeps {
  getView: () => EditorView | null
  isSourceMode: () => boolean
  getReadOnly: () => boolean
  clipboard: ClipboardBackend
  /** 复用桌面菜单动作（复制走双格式链路不变） */
  menuDeps: MenuDeps
  /** 「更多」竖向菜单：桌面注册表（text 有选区组经 isActive 过滤） */
  registry: MenuEntry[]
  menuView: MenuView
  /** 当前 PM 选区的 text 上下文快照（more 菜单 / 剪贴板动作用） */
  buildTextContext: () => MenuContext | null
  translator: () => (s: string) => string
}

export class SelectionToolbar {
  private readonly deps: SelectionToolbarDeps
  private bar: HTMLDivElement | null = null

  constructor(deps: SelectionToolbarDeps) {
    this.deps = deps
  }

  get isVisible(): boolean {
    return this.bar !== null
  }

  /** main.ts 的 selectionUpdated 监听处调用（已挂 crepe config，无需自注册） */
  onSelectionUpdated(): void {
    const view = this.deps.getView()
    if (!view) {
      this.hide()
      return
    }
    const show = shouldShowSelectionToolbar({
      selectionEmpty: view.state.selection.empty,
      sourceMode: this.deps.isSourceMode(),
    })
    if (!show) {
      this.hide()
      return
    }
    if (this.isVisible) {
      // 拖手柄调整选区：只跟随重定位，不重复探测剪贴板（每次 selectionUpdated
      // 都发 bridge 往返太贵）
      this.reposition()
      return
    }
    void this.show(view)
  }

  hide(): void {
    if (!this.bar) return
    this.bar.remove()
    this.bar = null
    document.removeEventListener('scroll', this.onScroll, true)
    document.removeEventListener('pointerdown', this.onOutsidePointerDown, true)
    window.visualViewport?.removeEventListener('resize', this.onViewportResize)
  }

  destroy(): void {
    this.hide()
  }

  private async show(view: EditorView): Promise<void> {
    // 剪贴板探测只在「隐藏 → 显示」沿做一次（拒绝/空 → 粘贴置灰，不崩）
    const canPaste = (await this.deps.clipboard.read()) !== null
    // 等待期间选区可能已塌缩 —— 复核一次，防竞态残留
    if (view.state.selection.empty || this.deps.isSourceMode()) return
    if (this.bar) this.hide()

    const items = buildSelectionToolbarItems({
      readOnly: this.deps.getReadOnly(),
      canPaste,
    })
    const t = this.deps.translator()
    const bar = document.createElement('div')
    bar.className = 'kc-selection-toolbar'
    bar.setAttribute('role', 'toolbar')
    // 保选区：整条的 pointerdown 都不放行（blur 先于 click）
    bar.addEventListener('pointerdown', (event) => event.preventDefault())

    for (const spec of items) {
      const btn = document.createElement('button')
      btn.type = 'button'
      btn.className = 'kc-st-btn'
      btn.textContent = spec.id === 'more' ? `${t(spec.label)} ▸` : t(spec.label)
      btn.disabled = spec.disabled
      if (spec.disabled) btn.classList.add('kc-st-disabled')
      if (!spec.disabled) {
        btn.addEventListener('click', () => void this.runItem(spec.id, btn))
      }
      bar.appendChild(btn)
    }

    bar.style.left = '-9999px'
    bar.style.top = '-9999px'
    document.body.appendChild(bar)
    this.bar = bar
    this.reposition()

    document.addEventListener('scroll', this.onScroll, true)
    document.addEventListener('pointerdown', this.onOutsidePointerDown, true)
    window.visualViewport?.addEventListener('resize', this.onViewportResize)
  }

  private async runItem(
    id: SelectionToolbarItemId,
    btn: HTMLButtonElement,
  ): Promise<void> {
    const { menuDeps } = this.deps
    if (id === 'more') {
      this.openMoreMenu(btn)
      return
    }
    const ctx = this.deps.buildTextContext()
    if (!ctx) return
    switch (id) {
      case 'cut':
        await menuDeps.copySelection(ctx, true)
        break
      case 'copy':
        await menuDeps.copySelection(ctx, false)
        break
      case 'paste':
        await menuDeps.pasteMarkdown()
        break
      case 'select-all':
        menuDeps.selectAll()
        return // 全选后选区仍在 —— 工具条跟随保留
    }
    // 动作执行后关闭（设计 §3 关闭条件）
    this.hide()
  }

  /** 「更多」→ 竖向菜单：复用桌面注册表 + MenuView（text 有选区组经
   *  isActive 过滤自然就位）。锚点 = 选区矩形（不是更多按钮）——菜单按
   *  「上方优先、不足翻下」避让选区，保证菜单矩形与选区矩形不相交
   *  （按钮锚点向下展开会正好压住选区）。 */
  private async openMoreMenu(btn: HTMLButtonElement): Promise<void> {
    const ctx = this.deps.buildTextContext()
    if (!ctx) return
    ctx.canPaste = (await this.deps.clipboard.read()) !== null
    const entries = filterMenuEntries(this.deps.registry, ctx)
    if (entries.length === 0) return
    // 菜单遮住期间收起工具条；菜单关闭后选区若还在则重新弹出
    this.hide()
    const rect = btn.getBoundingClientRect()
    const avoidRect = this.selectionAnchorRect()
    this.deps.menuView.open(
      entries,
      { x: rect.left, y: rect.bottom + 4 },
      {
        ctx,
        t: this.deps.translator(),
        onClose: () => this.onSelectionUpdated(),
      },
      avoidRect ? { avoidRect } : {},
    )
  }

  /** 选区矩形（coordsAtPos from/to 的并集）；取不到返回 null */
  private selectionAnchorRect(): AnchorRect | null {
    const view = this.deps.getView()
    if (!view) return null
    try {
      const { from, to } = view.state.selection
      const start = view.coordsAtPos(from)
      const end = view.coordsAtPos(to)
      return {
        left: Math.min(start.left, end.left),
        top: Math.min(start.top, end.top),
        right: Math.max(start.right, end.right),
        bottom: Math.max(start.bottom, end.bottom),
      }
    } catch {
      return null
    }
  }

  private reposition(): void {
    const bar = this.bar
    if (!bar) return
    const anchor = this.selectionAnchorRect()
    if (!anchor) return
    const rect = bar.getBoundingClientRect()
    const pos = computeSelectionToolbarPosition(
      anchor,
      { width: rect.width, height: rect.height },
      currentViewport(),
    )
    bar.style.left = `${pos.x}px`
    bar.style.top = `${pos.y}px`
  }

  private onScroll = (): void => {
    this.hide()
  }

  private onViewportResize = (): void => {
    const view = this.deps.getView()
    if (view && this.isVisible) this.reposition()
  }

  private onOutsidePointerDown = (event: PointerEvent): void => {
    const target = event.target as Node | null
    if (this.bar && target && !this.bar.contains(target)) this.hide()
  }
}

// 类型 re-export 方便测试引用
export type { MenuView }
