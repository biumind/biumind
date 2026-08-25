// M2 图片长按对象菜单（设计 §10/§10.6）：手势状态机全转换（500ms 触发 /
// >10px 位移取消 / touchend / touchcancel / 第二指取消 / 未命中不启动）、
// 触发序列 §10.3 调用顺序与一次性标记消费、按压守卫 class 加/卸。
// 归一化注入：状态机只吃 {x, y, target} 平面点（handleTouchStart/Move/End），
// jsdom 不需要 TouchEvent 构造器。

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { MenuContext } from '../src/context-menu/model'
import {
  LongPressController,
  type LongPressDeps,
  type LongPressPoint,
} from '../src/context-menu/long-press'
import { InputModeController } from '../src/context-menu/input-mode'

const IMAGE_POS = 42

interface Harness {
  deps: LongPressDeps
  controller: LongPressController
  calls: string[]
  opened: { ctx: MenuContext; point: { x: number; y: number } }[]
  imageEl: HTMLElement
  editorDom: HTMLElement
  inputMode: InputModeController
}

function makeHarness(opts: { readOnly?: boolean; nonEmptySelection?: boolean } = {}): Harness {
  document.body.innerHTML = `
    <div class="milkdown">
      <div class="ProseMirror" contenteditable="true"></div>
      <div class="milkdown-image-block"><img src="x" /></div>
    </div>`
  const root = document.querySelector<HTMLElement>('.milkdown')!
  const editorDom = document.querySelector<HTMLElement>('.ProseMirror')!
  const imageEl = document.querySelector<HTMLElement>('.milkdown-image-block')!
  const calls: string[] = []
  const opened: Harness['opened'] = []

  const view = { state: { selection: { empty: true } } }
  const inputMode = new InputModeController({
    getView: () => view as never,
    getReadOnly: () => false,
    isSourceMode: () => false,
  })
  inputMode.attach()

  const deps: LongPressDeps = {
    root,
    getReadOnly: () => opts.readOnly ?? false,
    aiActions: false,
    imageUpload: true,
    inputMode: {
      suppressNextEditIntent: () => {
        calls.push('suppress')
        inputMode.suppressNextEditIntent()
      },
    } as unknown as InputModeController,
    selectionToolbar: {
      hide: () => calls.push('st-hide'),
    } as unknown as LongPressDeps['selectionToolbar'],
    openAtPoint: (ctx, point) => {
      calls.push('openAtPoint')
      opened.push({ ctx, point })
    },
    resolveImagePos: () => IMAGE_POS,
    getEditorDom: () => editorDom,
    hasNonEmptySelection: () => opts.nonEmptySelection ?? false,
    collapseSelection: () => calls.push('collapse'),
    vibrate: () => calls.push('vibrate'),
  }
  const controller = new LongPressController(deps)
  return { deps, controller, calls, opened, imageEl, editorDom, inputMode }
}

function pt(x: number, y: number, target: EventTarget | null): LongPressPoint {
  return { x, y, target }
}

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  document.body.innerHTML = ''
})

describe('手势状态机', () => {
  it('500ms 到点触发：序列严格按 §10.3 顺序（suppress → hide → collapse → blur → vibrate → openAtPoint）', () => {
    const h = makeHarness({ nonEmptySelection: true })
    h.controller.attach()
    const blurSpy = vi.spyOn(h.editorDom, 'blur').mockImplementation(() => {
      h.calls.push('blur')
    })
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    vi.advanceTimersByTime(500)

    expect(h.calls).toEqual([
      'suppress',
      'st-hide',
      'collapse',
      'blur',
      'vibrate',
      'openAtPoint',
    ])
    expect(h.opened).toHaveLength(1)
    // ctx：image + nodePos；锚点 = 触点上方偏移 12px
    expect(h.opened[0].ctx.itemType).toBe('image')
    expect(h.opened[0].ctx.nodePos).toBe(IMAGE_POS)
    expect(h.opened[0].point).toEqual({ x: 100, y: 200 - 12 })
    blurSpy.mockRestore()
  })

  it('无选区场景不 collapse', () => {
    const h = makeHarness({ nonEmptySelection: false })
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    vi.advanceTimersByTime(500)
    expect(h.calls).not.toContain('collapse')
  })

  it('readOnly 透传进 ctx（注册表过滤后只剩复制图片）', () => {
    const h = makeHarness({ readOnly: true })
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    vi.advanceTimersByTime(500)
    expect(h.opened[0].ctx.readOnly).toBe(true)
  })

  it('500ms 内松手（touchend）不触发', () => {
    const h = makeHarness()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    vi.advanceTimersByTime(300)
    h.controller.handleTouchEnd()
    vi.advanceTimersByTime(500)
    expect(h.opened).toHaveLength(0)
  })

  it('位移 >10px 取消，页面正常滚动路径不受干扰', () => {
    const h = makeHarness()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    h.controller.handleTouchMove(pt(100, 215, h.imageEl)) // dy=15
    vi.advanceTimersByTime(600)
    expect(h.opened).toHaveLength(0)
  })

  it('位移 ≤10px 不取消', () => {
    const h = makeHarness()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    h.controller.handleTouchMove(pt(107, 205, h.imageEl)) // ~8.6px
    vi.advanceTimersByTime(500)
    expect(h.opened).toHaveLength(1)
  })

  it('touchcancel 取消', () => {
    const h = makeHarness()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    h.controller.handleTouchEnd()
    vi.advanceTimersByTime(600)
    expect(h.opened).toHaveLength(0)
  })

  it('pressing 期间第二次 touchstart（双指缩放）取消', () => {
    const h = makeHarness()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    h.controller.handleTouchStart(pt(120, 220, h.imageEl)) // 第二指
    vi.advanceTimersByTime(600)
    expect(h.opened).toHaveLength(0)
  })

  it('未命中图片（文本区域）不启动计时', () => {
    const h = makeHarness()
    const text = document.createElement('p')
    document.querySelector('.milkdown')!.appendChild(text)
    h.controller.handleTouchStart(pt(100, 200, text))
    vi.advanceTimersByTime(600)
    expect(h.opened).toHaveLength(0)
  })

  it('按压窗口防文本选择 class：touchstart 即加，triggered 移除', () => {
    const h = makeHarness()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    expect(h.editorDom.classList.contains('kc-lp-pressing')).toBe(true)
    vi.advanceTimersByTime(500)
    expect(h.editorDom.classList.contains('kc-lp-pressing')).toBe(false)
  })

  it('cancelled 同样移除按压守卫 class', () => {
    const h = makeHarness()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    expect(h.editorDom.classList.contains('kc-lp-pressing')).toBe(true)
    h.controller.handleTouchEnd()
    expect(h.editorDom.classList.contains('kc-lp-pressing')).toBe(false)
  })

  it('一次性标记被消费：触发后下一次 applyIntent 跳过（不唤键盘）', () => {
    const h = makeHarness()
    const prosemirror = document.querySelector<HTMLElement>('.ProseMirror')!
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    vi.advanceTimersByTime(500)
    // 触发序列已 suppress → 手指抬起后 InputModeController 判到折叠
    // 光标（empty selection）也跳过这一次编辑意图
    h.inputMode.applyIntent()
    expect(prosemirror.getAttribute('inputmode')).toBe('none')
    // 标记已消费：再一次 applyIntent 走正常编辑意图（移除 inputmode）
    h.inputMode.applyIntent()
    expect(prosemirror.getAttribute('inputmode')).toBeNull()
    h.inputMode.destroy()
  })

  it('destroy 清监听与计时（teardown 安全）', () => {
    const h = makeHarness()
    h.controller.attach()
    h.controller.handleTouchStart(pt(100, 200, h.imageEl))
    h.controller.destroy()
    vi.advanceTimersByTime(600)
    expect(h.opened).toHaveLength(0)
    expect(h.editorDom.classList.contains('kc-lp-pressing')).toBe(false)
  })

  // 实机 bug：image-block 的 node view 显式 draggable="true"，移动端长按
  // ≈ 触发 HTML5 拖拽 → 半透明放大的 drag ghost 跟手（截图"重影"）。
  it('图片 dragstart 被 preventDefault（防拖拽影子）；destroy 后不再拦', () => {
    const h = makeHarness()
    h.controller.attach()
    const img = h.imageEl.querySelector('img')!
    const onImg = new Event('dragstart', { bubbles: true, cancelable: true })
    img.dispatchEvent(onImg)
    expect(onImg.defaultPrevented).toBe(true)
    const onBlock = new Event('dragstart', { bubbles: true, cancelable: true })
    h.imageEl.dispatchEvent(onBlock)
    expect(onBlock.defaultPrevented).toBe(true)

    h.controller.destroy()
    const afterDestroy = new Event('dragstart', { bubbles: true, cancelable: true })
    img.dispatchEvent(afterDestroy)
    expect(afterDestroy.defaultPrevented).toBe(false)
  })

  it('非图片元素（文本）的 dragstart 不拦', () => {
    const h = makeHarness()
    h.controller.attach()
    const text = document.createElement('p')
    document.querySelector('.milkdown')!.appendChild(text)
    const ev = new Event('dragstart', { bubbles: true, cancelable: true })
    text.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
    h.controller.destroy()
  })
})
