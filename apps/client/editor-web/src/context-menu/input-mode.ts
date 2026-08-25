// 输入法门控（移动端 M1 实机修复，bug：选中时输入法弹出——应只在编辑时弹）。
// Android WebView 长按选词聚焦 contenteditable → IME 弹出盖住工具条。
// 修法：mount 后给 .milkdown .ProseMirror 设 inputmode="none"（聚焦不弹
// 软键盘，iOS 13+ / Android Chrome/WebView 都认；源码模式 textarea 不设，
// 它是纯编辑场景）；pointerup 后下一帧按选区状态判编辑意图：
//   折叠光标 = 编辑意图 → 移除 inputmode + blur()/focus() 唤起键盘；
//   非空选区（长按/双击/拖手柄）→ 保持 inputmode=none，键盘不弹。
// 选区从非空塌缩回光标（用户点了一下）同样走 pointerup → 编辑意图路径。
// 键盘已弹起时用户做选择：v1 不主动收键盘（注释即决策，勿改）。
// PM 重建（setDoc 换笔记 / teardown）后 DOM 换新 —— attach() 挂在 main.ts
// 的 mountCrepeEditor 流程里，每次重建重设。

import type { EditorView } from 'prosemirror-view'

/** 决策输出：show-keyboard = 恢复编辑（移除 inputmode + 重聚焦唤键盘）；
 *  keep-suppressed = 保持 inputmode=none（选择意图）；
 *  none = 无需动作（readOnly/源码模式/状态已一致）。 */
export type InputModeAction = 'show-keyboard' | 'keep-suppressed' | 'none'

/** 编辑意图判定纯函数（单测直接注入）。 */
export function decideInputMode(opts: {
  /** pointerup 后下一帧的 PM selection.empty */
  selectionEmpty: boolean
  /** 当前 contenteditable 是否处于 inputmode=none 抑制态 */
  suppressed: boolean
  readOnly: boolean
  sourceMode: boolean
}): InputModeAction {
  if (opts.readOnly || opts.sourceMode) return 'none'
  if (opts.selectionEmpty) {
    // 折叠光标 = 编辑意图；已在编辑态（键盘已弹）则无需动作
    return opts.suppressed ? 'show-keyboard' : 'none'
  }
  // 非空选区 = 选择意图：保持抑制；键盘已弹起时（suppressed=false）
  // 不主动收键盘（v1 明确不处理）
  return opts.suppressed ? 'keep-suppressed' : 'none'
}

export interface InputModeControllerDeps {
  getView: () => EditorView | null
  getReadOnly: () => boolean
  isSourceMode: () => boolean
}

export class InputModeController {
  private readonly deps: InputModeControllerDeps
  private dom: HTMLElement | null = null
  /** 一次性抑制标记（M2 长按触发序列 §10.3-1）：下一次 applyIntent
   *  消费并跳过 —— 防触发后 pointerup 判到折叠光标走编辑意图唤键盘
   *  盖住已打开的长按菜单。 */
  private suppressNext = false

  constructor(deps: InputModeControllerDeps) {
    this.deps = deps
  }

  /** M2 长按触发序列调用：下一次编辑意图判定直接跳过（消费一次） */
  suppressNextEditIntent(): void {
    this.suppressNext = true
  }

  /** mount 后调用：设 inputmode=none + 挂 pointerup 监听。
   *  每次 PM 重建（mountCrepeEditor）都要重调 —— DOM 是新的。 */
  attach(): void {
    const dom = document.querySelector<HTMLElement>('.milkdown .ProseMirror')
    if (!dom) return
    this.dom = dom
    dom.setAttribute('inputmode', 'none')
    dom.addEventListener('pointerup', this.onPointerUp)
  }

  destroy(): void {
    this.dom?.removeEventListener('pointerup', this.onPointerUp)
    this.dom = null
  }

  private onPointerUp = (): void => {
    // 下一帧再判：pointerup 时 PM 还没处理完本次 selection 更新
    requestAnimationFrame(() => this.applyIntent())
  }

  /** 编辑意图判定（public 供单测直接驱动；内部由 pointerup rAF 调用） */
  applyIntent(): void {
    // 一次性抑制标记：先消费后跳过（M2 长按触发序列的键盘联动防线）
    if (this.suppressNext) {
      this.suppressNext = false
      return
    }
    const dom = this.dom
    const view = this.deps.getView()
    if (!dom || !view) return
    const action = decideInputMode({
      selectionEmpty: view.state.selection.empty,
      suppressed: dom.getAttribute('inputmode') === 'none',
      readOnly: this.deps.getReadOnly(),
      sourceMode: this.deps.isSourceMode(),
    })
    if (action !== 'show-keyboard') return
    // 恢复编辑：移除抑制 + 重聚焦唤起键盘（blur 先于 focus —— 元素已聚焦
    // 时单 focus() 在 Android WebView 不触发 IME）
    dom.removeAttribute('inputmode')
    dom.blur()
    dom.focus()
  }
}
