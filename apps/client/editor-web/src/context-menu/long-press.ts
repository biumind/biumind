// 场景 B：图片长按对象菜单（M2，设计 §10）。
// 手势状态机：
//   idle ──touchstart(命中图片)──> pressing（t0 起 500ms 计时）
//   pressing ──计时到点──> triggered（执行触发序列 §10.3）
//   pressing ──位移>10px / touchend / touchcancel / 第二次 touchstart
//             （双指缩放）──> cancelled → idle
// 设计原则：长按只赋给「无文本选择语义」的 atom 对象 —— 图片上没有
// 选词语义，500ms 按压无歧义；文本流内对象（链接/表格）不做。
// touchstart 不 preventDefault（会杀掉滚动与系统手势正常路径；系统
// callout 已由 M1 CSS 关、Android contextmenu 已 swallow，无需再拦）。
// 事件委托挂 .milkdown 容器（一处监听）；DOM TouchEvent 归一化成
// {x, y, target} 平面结构 —— 状态机只吃归一化点，jsdom 直接注入测试。
// 只在 mobileCustom 时由 main.ts 创建：桌面/源码模式零监听零变化。

import { IMAGE_BLOCK_SELECTOR } from './context'
import type { InputModeController } from './input-mode'
import type { MenuContext } from './model'
import type { SelectionToolbar } from './selection-toolbar'

/** iOS 系统手感时长 */
const LONG_PRESS_MS = 500
/** 位移取消阈值（nowen-note 值；Joplin 零阈值太敏感已否） */
const MOVE_TOLERANCE_PX = 10
/** 菜单锚点：触点上方偏移（手指遮挡下方） */
const ANCHOR_OFFSET_Y = 12
/** 按压窗口防文本选择的 class（mobile.css：user-select: none） */
const PRESS_GUARD_CLASS = 'kc-lp-pressing'

/** 归一化触摸点（DOM TouchEvent → 平面结构，便于 jsdom 注入测试） */
export interface LongPressPoint {
  x: number
  y: number
  target: EventTarget | null
}

export function normalizeTouch(event: TouchEvent): LongPressPoint | null {
  const t = event.touches[0] ?? event.changedTouches[0]
  if (!t) return null
  return { x: t.clientX, y: t.clientY, target: event.target }
}

export interface LongPressDeps {
  /** 事件委托挂载容器（.milkdown） */
  root: HTMLElement
  getReadOnly: () => boolean
  aiActions: boolean
  imageUpload: boolean
  inputMode: InputModeController | null
  selectionToolbar: SelectionToolbar | null
  /** ContextMenuController.openAtPoint（构建 ctx 之后的公共段落） */
  openAtPoint: (
    ctx: MenuContext,
    point: { x: number; y: number },
  ) => void | Promise<void>
  /** 触点 → 图片节点 pos（生产 = context.ts imagePosFromDom 包装） */
  resolveImagePos: (point: { x: number; y: number }) => number | null
  /** .ProseMirror（按压守卫 class + 触发时 blur） */
  getEditorDom: () => HTMLElement | null
  hasNonEmptySelection: () => boolean
  /** 非空选区 collapse 到 nodePos 附近（经 PM dispatch，不移焦） */
  collapseSelection: (nearPos: number) => void
  /** 触觉反馈（默认 navigator.vibrate；iOS web 无 API 静默跳过） */
  vibrate?: (ms: number) => void
}

export class LongPressController {
  private readonly deps: LongPressDeps
  private pressing = false
  private timer: ReturnType<typeof setTimeout> | null = null
  private start: { x: number; y: number } | null = null
  private nodePos: number | null = null
  private attached = false

  constructor(deps: LongPressDeps) {
    this.deps = deps
  }

  attach(): void {
    if (this.attached) return
    this.attached = true
    const { root } = this.deps
    // 全部 passive —— 不拦任何默认行为（滚动/系统手势正常路径）
    root.addEventListener('touchstart', this.onTouchStart, { passive: true })
    root.addEventListener('touchmove', this.onTouchMove, { passive: true })
    root.addEventListener('touchend', this.onTouchEnd, { passive: true })
    root.addEventListener('touchcancel', this.onTouchEnd, { passive: true })
  }

  destroy(): void {
    if (this.attached) {
      this.attached = false
      const { root } = this.deps
      root.removeEventListener('touchstart', this.onTouchStart)
      root.removeEventListener('touchmove', this.onTouchMove)
      root.removeEventListener('touchend', this.onTouchEnd)
      root.removeEventListener('touchcancel', this.onTouchEnd)
    }
    this.cancelPress()
  }

  // ── DOM 事件 → 归一化 ─────────────────────────────────

  private onTouchStart = (event: TouchEvent): void => {
    const point = normalizeTouch(event)
    if (point) this.handleTouchStart(point)
  }

  private onTouchMove = (event: TouchEvent): void => {
    const point = normalizeTouch(event)
    if (point) this.handleTouchMove(point)
  }

  private onTouchEnd = (): void => {
    this.handleTouchEnd()
  }

  // ── 状态机（归一化入口，测试直接注入） ──────────────────

  handleTouchStart(point: LongPressPoint): void {
    if (this.pressing) {
      // pressing 期间的第二个 touchstart = 双指缩放 → 取消
      this.cancelPress()
      return
    }
    const target = point.target as HTMLElement | null
    if (!target?.closest?.(IMAGE_BLOCK_SELECTOR)) return
    const nodePos = this.deps.resolveImagePos({ x: point.x, y: point.y })
    if (nodePos === null) return
    this.pressing = true
    this.nodePos = nodePos
    this.start = { x: point.x, y: point.y }
    // 按压窗口防文本选择：早于系统选择开始（triggered/cancelled 移除）
    this.deps.getEditorDom()?.classList.add(PRESS_GUARD_CLASS)
    this.timer = setTimeout(() => this.trigger(), LONG_PRESS_MS)
  }

  handleTouchMove(point: LongPressPoint): void {
    if (!this.pressing || !this.start) return
    const dx = point.x - this.start.x
    const dy = point.y - this.start.y
    if (Math.hypot(dx, dy) > MOVE_TOLERANCE_PX) this.cancelPress()
  }

  handleTouchEnd(): void {
    if (this.pressing) this.cancelPress()
  }

  private cancelPress(): void {
    if (this.timer) {
      clearTimeout(this.timer)
      this.timer = null
    }
    this.pressing = false
    this.nodePos = null
    this.start = null
    this.deps.getEditorDom()?.classList.remove(PRESS_GUARD_CLASS)
  }

  /** 触发序列（§10.3，严格按序 —— 顺序是联动 bug 的防线，勿调） */
  private trigger(): void {
    if (!this.pressing || this.nodePos === null || !this.start) return
    const nodePos = this.nodePos
    const start = this.start
    this.cancelPress() // 状态归位 + 移除按压守卫
    // 1. 抑制下一次键盘意图（一次性标记）：防触发后 pointerup 判到折叠
    //    光标走编辑意图唤键盘盖住菜单
    this.deps.inputMode?.suppressNextEditIntent()
    // 2. 关选区工具条 + 非空选区 collapse（不移焦）
    this.deps.selectionToolbar?.hide()
    if (this.deps.hasNonEmptySelection()) {
      this.deps.collapseSelection(nodePos)
    }
    // 3. 编辑器失焦：防 F2 浮动格式条在菜单下方滑出叠屏；菜单关闭后
    //    不回焦（用户下一步意图不明，回焦可能误唤键盘）
    this.deps.getEditorDom()?.blur()
    // 4. 触觉反馈（Android 有效；iOS web 无 API 静默跳过）
    if (this.deps.vibrate) {
      this.deps.vibrate(10)
    } else {
      navigator.vibrate?.(10)
    }
    // 5. 打开菜单：image 组经 isActive 自动就位（替换/说明/复制/删除；
    //    readOnly 只剩复制图片）；锚点触点上方偏移（手指遮挡下方）
    void this.deps.openAtPoint(
      {
        itemType: 'image',
        hasSelection: false,
        readOnly: this.deps.getReadOnly(),
        canPaste: false,
        aiActions: this.deps.aiActions,
        imageUpload: this.deps.imageUpload,
        from: 0,
        to: 0,
        nodePos,
      },
      { x: start.x, y: start.y - ANCHOR_OFFSET_Y },
    )
  }
}
