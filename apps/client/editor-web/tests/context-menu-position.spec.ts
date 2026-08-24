// 定位纯函数边界值：四向翻转 + 视口 4px 钳制（尺寸全部注入，不起 DOM）。

import { describe, expect, it } from 'vitest'

import { computeMenuPosition } from '../src/context-menu/view'

const VIEWPORT = { width: 800, height: 600 }
const MENU = { width: 200, height: 150 }

describe('computeMenuPosition', () => {
  it('空间充足：锚点即落点', () => {
    expect(
      computeMenuPosition({ x: 100, y: 100 }, MENU, VIEWPORT),
    ).toEqual({ x: 100, y: 100 })
  })

  it('右方不足 menuWidth + 8 → 向左翻（x - menuWidth）', () => {
    // 剩余 800-700=100 < 200+8 → 翻到 700-200=500
    expect(
      computeMenuPosition({ x: 700, y: 100 }, MENU, VIEWPORT),
    ).toEqual({ x: 500, y: 100 })
  })

  it('下方不足 menuHeight + 8 → 向上翻（y - menuHeight）', () => {
    // 剩余 600-500=100 < 150+8 → 翻到 500-150=350
    expect(
      computeMenuPosition({ x: 100, y: 500 }, MENU, VIEWPORT),
    ).toEqual({ x: 100, y: 350 })
  })

  it('右下都不足 → 双向翻', () => {
    expect(
      computeMenuPosition({ x: 700, y: 500 }, MENU, VIEWPORT),
    ).toEqual({ x: 500, y: 350 })
  })

  it('翻转后仍出界 → 钳制到视口内 4px', () => {
    // 窄视口：向左翻出负 → 钳到 4
    expect(
      computeMenuPosition({ x: 150, y: 100 }, MENU, { width: 300, height: 600 }),
    ).toEqual({ x: 4, y: 100 })
    // 菜单比视口还高 → 钳到 4（不溢出）
    expect(
      computeMenuPosition({ x: 100, y: 100 }, { width: 200, height: 700 }, VIEWPORT),
    ).toEqual({ x: 100, y: 4 })
    // 菜单比视口还宽 → 钳到 4
    expect(
      computeMenuPosition({ x: 100, y: 100 }, { width: 900, height: 150 }, VIEWPORT),
    ).toEqual({ x: 4, y: 100 })
  })

  it('恰好贴边（剩余 = 尺寸 + 8）不翻转', () => {
    // 800-592=208 = 200+8 → 不翻
    expect(
      computeMenuPosition({ x: 592, y: 442 }, MENU, VIEWPORT),
    ).toEqual({ x: 592, y: 442 })
  })

  it('子菜单：右方不足用 flipX 翻到父菜单左缘', () => {
    // 父项右缘 700，子菜单宽 200 → 翻到父左缘（500）之外 = 500-200=300
    expect(
      computeMenuPosition({ x: 700, y: 100 }, MENU, VIEWPORT, {
        flipX: 500 - 200,
      }),
    ).toEqual({ x: 300, y: 100 })
  })

  it('自定义 margin/gap', () => {
    expect(
      computeMenuPosition({ x: 690, y: 100 }, MENU, VIEWPORT, { gap: 120 }),
    ).toEqual({ x: 490, y: 100 })
    expect(
      computeMenuPosition({ x: 1, y: 1 }, MENU, VIEWPORT, { margin: 10 }),
    ).toEqual({ x: 10, y: 10 })
  })
})
