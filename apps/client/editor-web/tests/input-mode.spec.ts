// 输入法门控（移动端实机修复）：decideInputMode 纯函数真值表。
// 选中（非空选区）不弹键盘；折叠光标（编辑意图）才恢复键盘。

import { describe, expect, it } from 'vitest'

import { decideInputMode } from '../src/context-menu/input-mode'

describe('decideInputMode（编辑意图判定）', () => {
  it('抑制态 + 折叠光标（点按/选区塌缩）→ 唤起键盘', () => {
    expect(
      decideInputMode({
        selectionEmpty: true,
        suppressed: true,
        readOnly: false,
        sourceMode: false,
      }),
    ).toBe('show-keyboard')
  })

  it('抑制态 + 非空选区（长按/双击/拖手柄）→ 保持抑制不弹键盘', () => {
    expect(
      decideInputMode({
        selectionEmpty: false,
        suppressed: true,
        readOnly: false,
        sourceMode: false,
      }),
    ).toBe('keep-suppressed')
  })

  it('键盘已弹起（非抑制态）+ 折叠光标 → 无需动作', () => {
    expect(
      decideInputMode({
        selectionEmpty: true,
        suppressed: false,
        readOnly: false,
        sourceMode: false,
      }),
    ).toBe('none')
  })

  it('键盘已弹起 + 用户做选择 → 不主动收键盘（v1 明确不处理）', () => {
    expect(
      decideInputMode({
        selectionEmpty: false,
        suppressed: false,
        readOnly: false,
        sourceMode: false,
      }),
    ).toBe('none')
  })

  it('readOnly → 永不唤键盘', () => {
    expect(
      decideInputMode({
        selectionEmpty: true,
        suppressed: true,
        readOnly: true,
        sourceMode: false,
      }),
    ).toBe('none')
  })

  it('源码模式 → 不干预（textarea 是编辑场景，由自身聚焦行为弹键盘）', () => {
    expect(
      decideInputMode({
        selectionEmpty: true,
        suppressed: true,
        readOnly: false,
        sourceMode: true,
      }),
    ).toBe('none')
  })
})
