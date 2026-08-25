// 移动端浮动格式工具条控制器（F2，设计 BiuMind-Notes-Mobile-Fullscreen-Design.md
// §6 F2 段）：顶部常驻条 → position:fixed 贴可见视口底部（键盘上沿）的
// 浮动条。按钮集与桌面完全一致（createToolbar floating 模式），本控制器
// 只管三件事：显隐（焦点驱动）、bottom 定位（visualViewport）、编辑器
// 底部留白（防最后一行被盖）。
// 只在 mobileCustom（platform ∈ {ios, android} 且 contextMenu ≠ 'native'）
// 时由 main.ts 创建 —— 桌面 kc-toolbar 保持顶部常驻，零变化。

import type { Toolbar } from './index'

/** 极端小视口阈值（实机场景：横屏 + 软键盘——视口高仅 360–414、键盘占
 *  250–300，可见区不足 100px，44px 的条会吃掉仅剩画布的大半）。
 *  可见区低于此值恒隐（Joplin 容器高 <140px 收起工具条的同款策略）。 */
export const MIN_VIEWPORT_HEIGHT_FOR_TOOLBAR = 200

/** 显隐决策纯函数：readOnly 恒隐；极端小视口恒隐；源码模式恒可见（沿用
 *  setSourceMode 禁用逻辑，只有源码切换按钮可用，用户随时能切回）；其余
 *  跟焦点 —— 光标或选区都算（inputmode=none 的选区态长按选词也已聚焦，
 *  条在方便直接点加粗；业界共识 5：焦点驱动显隐）。 */
export function shouldShowFloatingToolbar(opts: {
  focused: boolean
  readOnly: boolean
  sourceMode: boolean
  /** 可见视口高度（visualViewport.height，无 vv 时传 innerHeight） */
  viewportHeight: number
}): boolean {
  if (opts.readOnly) return false
  if (opts.viewportHeight < MIN_VIEWPORT_HEIGHT_FOR_TOOLBAR) return false
  return opts.focused || opts.sourceMode
}

/** 底部偏移纯函数：webview 随键盘 resize 时 bottom:0 即键盘上沿；
 *  防 iOS 某些路径不 resize，用 visualViewport 计算 ——
 *  bottom = max(0, innerHeight - (vv.height + vv.offsetTop))。 */
export function computeFloatingBottom(opts: {
  innerHeight: number
  vvHeight: number
  vvOffsetTop: number
}): number {
  return Math.max(0, opts.innerHeight - (opts.vvHeight + opts.vvOffsetTop))
}

export interface FloatingToolbarDeps {
  toolbar: Toolbar
  /** ProseMirror DOM（focus/blur 监听目标；PM 重建后需重新 attach） */
  getEditorDom: () => HTMLElement | null
  getReadOnly: () => boolean
  isSourceMode: () => boolean
}

export class FloatingToolbarController {
  private readonly deps: FloatingToolbarDeps
  private focused = false
  private visible = false
  private attachedDom: HTMLElement | null = null

  constructor(deps: FloatingToolbarDeps) {
    this.deps = deps
  }

  attach(): void {
    const el = this.deps.toolbar.element
    el.style.bottom = `${this.bottom()}px`
    // focus/blur 不冒泡，直接挂在 contenteditable DOM 上（focusin/focusout
    // 需 document 级过滤，focus/blur 直挂更稳）。点浮动条/选区工具条/菜单
    // 都是 pointerdown preventDefault，不会误触发 blur 收起。
    const dom = this.deps.getEditorDom()
    if (dom) {
      dom.addEventListener('focus', this.onFocus)
      dom.addEventListener('blur', this.onBlur)
      this.attachedDom = dom
    }
    window.visualViewport?.addEventListener('resize', this.onViewportResize)
    this.refresh()
  }

  /** 外部状态变化后重判显隐：源码模式切换 / readOnly 变更 / 视口变化 */
  refresh(): void {
    const show = shouldShowFloatingToolbar({
      focused: this.focused,
      readOnly: this.deps.getReadOnly(),
      sourceMode: this.deps.isSourceMode(),
      viewportHeight: window.visualViewport?.height ?? window.innerHeight,
    })
    if (show === this.visible) return
    this.visible = show
    const el = this.deps.toolbar.element
    el.classList.toggle('kc-tb-visible', show)
    this.syncEditorPadding(show ? el : null)
  }

  destroy(): void {
    this.attachedDom?.removeEventListener('focus', this.onFocus)
    this.attachedDom?.removeEventListener('blur', this.onBlur)
    this.attachedDom = null
    window.visualViewport?.removeEventListener('resize', this.onViewportResize)
    this.syncEditorPadding(null)
    this.visible = false
  }

  private bottom(): number {
    const vv = window.visualViewport
    return computeFloatingBottom({
      innerHeight: window.innerHeight,
      vvHeight: vv?.height ?? window.innerHeight,
      vvOffsetTop: vv?.offsetTop ?? 0,
    })
  }

  private onFocus = (): void => {
    this.focused = true
    this.refresh()
  }

  private onBlur = (): void => {
    this.focused = false
    this.refresh()
  }

  private onViewportResize = (): void => {
    this.deps.toolbar.element.style.bottom = `${this.bottom()}px`
    // 视口变化可能跨过小视口阈值（横屏+键盘）—— 重判显隐，不只重定位
    this.refresh()
  }

  /** 编辑器底部留白：条显示时防最后一行被盖（AppFlowy 预留
   *  toolbarHeight 的做法；light.css 的 padding 8px 12px 24px 是默认值，
   *  隐藏时清空 inline style 回落）。 */
  private syncEditorPadding(el: HTMLElement | null): void {
    const pm = document.querySelector<HTMLElement>('.milkdown .ProseMirror')
    if (!pm) return
    if (!el) {
      pm.style.paddingBottom = ''
      return
    }
    const h = el.getBoundingClientRect().height
    if (h > 0) pm.style.paddingBottom = `${h + 16}px`
  }
}
