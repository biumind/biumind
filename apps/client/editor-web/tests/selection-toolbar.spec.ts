// 选区浮动工具条（移动端 M1，设计 §3/§8）：
// 项集生成纯函数（可编辑/readOnly 裁剪/粘贴置灰）、显示条件（非空选区 /
// 源码模式不显示 / 不依赖 hasFocus）、定位翻转（visualViewport 注入 mock）。

import { describe, expect, it } from 'vitest'

import {
  buildSelectionToolbarItems,
  computeSelectionToolbarPosition,
  shouldShowSelectionToolbar,
  type ViewportBox,
} from '../src/context-menu/selection-toolbar'

describe('buildSelectionToolbarItems（项集纯函数）', () => {
  it('可编辑：剪切/复制/粘贴/全选/更多', () => {
    const items = buildSelectionToolbarItems({ readOnly: false, canPaste: true })
    expect(items.map((i) => i.id)).toEqual([
      'cut',
      'copy',
      'paste',
      'select-all',
      'more',
    ])
    expect(items.every((i) => !i.disabled)).toBe(true)
  })

  it('剪贴板为空：粘贴置灰，其余不变', () => {
    const items = buildSelectionToolbarItems({ readOnly: false, canPaste: false })
    const paste = items.find((i) => i.id === 'paste')
    expect(paste?.disabled).toBe(true)
    expect(items.find((i) => i.id === 'copy')?.disabled).toBe(false)
  })

  it('readOnly：只剩 复制/全选', () => {
    const items = buildSelectionToolbarItems({ readOnly: true, canPaste: true })
    expect(items.map((i) => i.id)).toEqual(['copy', 'select-all'])
  })
})

describe('shouldShowSelectionToolbar（显示条件）', () => {
  it('选区非空 + 非源码模式 → 显示', () => {
    expect(
      shouldShowSelectionToolbar({ selectionEmpty: false, sourceMode: false }),
    ).toBe(true)
  })

  it('选区塌缩 → 不显示', () => {
    expect(
      shouldShowSelectionToolbar({ selectionEmpty: true, sourceMode: false }),
    ).toBe(false)
  })

  it('源码模式 → 不显示', () => {
    expect(
      shouldShowSelectionToolbar({ selectionEmpty: false, sourceMode: true }),
    ).toBe(false)
  })

  it('签名不含 hasFocus（Android 失焦坑：以选区非空为准）', () => {
    // shouldShowSelectionToolbar 只有两个入参 —— 若未来有人加回
    // hasFocus 门控，这里的调用方式会直接编译报错
    expect(shouldShowSelectionToolbar.length).toBe(1)
  })
})

describe('computeSelectionToolbarPosition（定位翻转）', () => {
  const BAR = { width: 240, height: 44 }
  const VP: ViewportBox = { width: 390, height: 800, offsetLeft: 0, offsetTop: 0 }
  const ANCHOR = { left: 100, top: 300, right: 260, bottom: 320 }

  it('默认选区上方居中', () => {
    const pos = computeSelectionToolbarPosition(ANCHOR, BAR, VP)
    // x = 180 - 120 = 60；y = 300 - 44 - 8 = 248
    expect(pos).toEqual({ x: 60, y: 248 })
  })

  it('上方不足 → 翻选区下方', () => {
    const near = { left: 100, top: 20, right: 260, bottom: 40 }
    const pos = computeSelectionToolbarPosition(near, BAR, VP)
    expect(pos.y).toBe(40 + 8)
  })

  it('水平出界 → 钳制进视口 4px', () => {
    const left = { left: 0, top: 300, right: 40, bottom: 320 }
    expect(computeSelectionToolbarPosition(left, BAR, VP).x).toBe(4)
    const right = { left: 350, top: 300, right: 390, bottom: 320 }
    expect(computeSelectionToolbarPosition(right, BAR, VP).x).toBe(
      390 - 240 - 4,
    )
  })

  it('键盘弹起（visualViewport 缩小 + offsetTop）：按可见区域钳制', () => {
    const keyboard: ViewportBox = {
      width: 390,
      height: 350,
      offsetLeft: 0,
      offsetTop: 450,
    }
    // 长选区：上方不足 → 翻下方；翻下后仍出可见底 → 钳回可见区内
    const pos = computeSelectionToolbarPosition(
      { left: 100, top: 451, right: 260, bottom: 790 },
      BAR,
      keyboard,
    )
    // 上方 451-44-8=399 < 454 → 翻下 790+8=798 → 钳到 450+350-44-4=752
    expect(pos.y).toBe(752)
    expect(pos.y + BAR.height).toBeLessThanOrEqual(450 + 350 - 4)
  })

  it('贴顶选区 + 键盘：翻下方后仍在可见区内', () => {
    const keyboard: ViewportBox = {
      width: 390,
      height: 350,
      offsetLeft: 0,
      offsetTop: 450,
    }
    const pos = computeSelectionToolbarPosition(
      { left: 100, top: 455, right: 260, bottom: 475 },
      BAR,
      keyboard,
    )
    // 上方 455-44-8=403 < 454 → 翻下 475+8=483
    expect(pos.y).toBe(483)
  })
})
