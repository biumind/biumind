// 表格尺寸选择器：网格渲染、hover 高亮、点击回调、开关与定位行为。
// jsdom 无布局引擎：offsetWidth/Height = 0、getBoundingClientRect 全 0，
// 定位断言只看坐标被写入 style（不溢出逻辑依赖真实尺寸，此处不覆盖）。

import { describe, expect, it, vi } from 'vitest'

import { createTableSizePicker } from '../src/toolbar/table-size-picker'

// jsdom 无 PointerEvent 构造器 —— MouseEvent 子类补齐 pointerType，
// picker 的 hover 分流（mouse 预览 / touch 不预览）依赖该字段
class FakePointerEvent extends MouseEvent {
  readonly pointerType: string
  constructor(type: string, init: PointerEventInit = {}) {
    super(type, init)
    this.pointerType = init.pointerType ?? 'mouse'
  }
}
;(globalThis as Record<string, unknown>).PointerEvent = FakePointerEvent

function makePicker() {
  const onPick = vi.fn()
  const picker = createTableSizePicker({ onPick, t: (s) => s })
  document.body.appendChild(picker.element)
  return { picker, onPick }
}

const cell = (el: HTMLElement, row: number, col: number): HTMLButtonElement => {
  const c = el.querySelector<HTMLButtonElement>(
    `button[data-row="${row}"][data-col="${col}"]`,
  )
  if (!c) throw new Error(`cell ${row},${col} not found`)
  return c
}

describe('table-size-picker', () => {
  it('渲染 8×10 网格', () => {
    const { picker } = makePicker()
    expect(picker.element.querySelectorAll('.kc-tsp-cell').length).toBe(80)
  })

  it('mouse hover 高亮矩形区并更新标签', () => {
    const { picker } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    const target = cell(picker.element, 2, 3)
    target.dispatchEvent(
      new PointerEvent('pointerover', { pointerType: 'mouse', bubbles: true }),
    )
    expect(target.classList.contains('kc-tsp-on')).toBe(true)
    expect(
      cell(picker.element, 2, 0).classList.contains('kc-tsp-on'),
    ).toBe(true)
    expect(
      cell(picker.element, 3, 3).classList.contains('kc-tsp-on'),
    ).toBe(false)
    const label = picker.element.querySelector('.kc-tsp-label')
    expect(label?.textContent).toBe('4 × 3 Table')
  })

  it('触屏不产生 hover 预览', () => {
    const { picker } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    const target = cell(picker.element, 2, 3)
    target.dispatchEvent(
      new PointerEvent('pointerover', { pointerType: 'touch', bubbles: true }),
    )
    expect(target.classList.contains('kc-tsp-on')).toBe(false)
  })

  it('点击落格回调 1 基行列并关闭', () => {
    const { picker, onPick } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    cell(picker.element, 4, 6).click()
    expect(onPick).toHaveBeenCalledWith(5, 7)
    expect(picker.isOpen).toBe(false)
    expect(picker.element.style.display).toBe('none')
  })

  it('再次 open 时高亮重置', () => {
    const { picker } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    cell(picker.element, 2, 3).dispatchEvent(
      new PointerEvent('pointerover', { pointerType: 'mouse', bubbles: true }),
    )
    picker.close()
    picker.openAtPoint({ x: 10, y: 10 })
    expect(
      picker.element.querySelectorAll('.kc-tsp-on').length,
    ).toBe(0)
  })

  it('文档 pointerdown 外部点击关闭', () => {
    const { picker } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    expect(picker.isOpen).toBe(true)
    document.body.dispatchEvent(
      new PointerEvent('pointerdown', { bubbles: true }),
    )
    expect(picker.isOpen).toBe(false)
  })

  it('Escape 关闭', () => {
    const { picker } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    document.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
    )
    expect(picker.isOpen).toBe(false)
  })

  it('重复 open 同一点等于关闭（切换语义）', () => {
    const { picker } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    picker.openAtPoint({ x: 10, y: 10 })
    expect(picker.isOpen).toBe(false)
  })

  it('destroy 后元素移除且再 close 不抛', () => {
    const { picker } = makePicker()
    picker.openAtPoint({ x: 10, y: 10 })
    picker.destroy()
    expect(picker.element.isConnected).toBe(false)
    picker.close()
  })
})
