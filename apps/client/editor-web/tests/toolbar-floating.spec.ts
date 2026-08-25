// F2 移动端浮动格式条：显隐决策（focused/readOnly/sourceMode → show?）
// 与定位（visualViewport 各状态 → bottom 值）纯函数单测。

import { describe, expect, it } from 'vitest'

import {
  computeFloatingBottom,
  shouldShowFloatingToolbar,
} from '../src/toolbar/floating-controller'

describe('shouldShowFloatingToolbar（显隐决策）', () => {
  it('聚焦（光标或选区）→ 显示', () => {
    expect(
      shouldShowFloatingToolbar({
        focused: true,
        readOnly: false,
        sourceMode: false,
      }),
    ).toBe(true)
  })

  it('失焦 → 隐藏', () => {
    expect(
      shouldShowFloatingToolbar({
        focused: false,
        readOnly: false,
        sourceMode: false,
      }),
    ).toBe(false)
  })

  it('readOnly → 恒不显示（即使聚焦/源码模式）', () => {
    expect(
      shouldShowFloatingToolbar({
        focused: true,
        readOnly: true,
        sourceMode: false,
      }),
    ).toBe(false)
    expect(
      shouldShowFloatingToolbar({
        focused: false,
        readOnly: true,
        sourceMode: true,
      }),
    ).toBe(false)
  })

  it('源码模式 → 恒可见（失焦也在，源码切换按钮可用）', () => {
    expect(
      shouldShowFloatingToolbar({
        focused: false,
        readOnly: false,
        sourceMode: true,
      }),
    ).toBe(true)
  })
})

describe('computeFloatingBottom（贴键盘上沿定位）', () => {
  it('无键盘（vv 占满视口）→ bottom 0', () => {
    expect(
      computeFloatingBottom({
        innerHeight: 800,
        vvHeight: 800,
        vvOffsetTop: 0,
      }),
    ).toBe(0)
  })

  it('键盘弹起（vv 缩小）→ bottom = 键盘高度', () => {
    expect(
      computeFloatingBottom({
        innerHeight: 800,
        vvHeight: 450,
        vvOffsetTop: 0,
      }),
    ).toBe(350)
  })

  it('iOS 不 resize 路径（vv offsetTop + 缩小）→ 按可见区底部算', () => {
    // 可见区 [100, 550]，innerHeight 800 → 底部遮挡 250
    expect(
      computeFloatingBottom({
        innerHeight: 800,
        vvHeight: 450,
        vvOffsetTop: 100,
      }),
    ).toBe(250)
  })

  it('异常态（vv 超出视口）→ 钳到 0 不出负值', () => {
    expect(
      computeFloatingBottom({
        innerHeight: 800,
        vvHeight: 900,
        vvOffsetTop: 0,
      }),
    ).toBe(0)
  })
})
