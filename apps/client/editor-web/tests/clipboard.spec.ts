// 剪贴板双实现：native mock bridge（callHandler 通路），web mock
// navigator.clipboard 成功/拒绝两路径。

import { describe, expect, it, vi } from 'vitest'

import type { BridgeClient } from '../src/bridge/client'
import {
  createNativeClipboard,
  createWebClipboard,
} from '../src/context-menu/clipboard'

function mockBridge(reply: { text?: string | null }) {
  const sendClipboardWrite = vi.fn()
  const requestClipboardRead = vi.fn(async () => reply)
  const bridge = { sendClipboardWrite, requestClipboardRead }
  return {
    bridge: bridge as unknown as BridgeClient,
    sendClipboardWrite,
    requestClipboardRead,
  }
}

describe('native clipboard（bridge 实现）', () => {
  it('write → sendClipboardWrite', async () => {
    const { bridge, sendClipboardWrite } = mockBridge({ text: null })
    await createNativeClipboard(bridge).write({ text: 'md **bold**' })
    expect(sendClipboardWrite).toHaveBeenCalledWith({ text: 'md **bold**' })
  })

  it('read → clipboardRead.reply 有文本', async () => {
    const { bridge, requestClipboardRead } = mockBridge({ text: 'pasted' })
    await expect(createNativeClipboard(bridge).read()).resolves.toEqual({
      text: 'pasted',
    })
    expect(requestClipboardRead).toHaveBeenCalled()
  })

  it('read → text null（剪贴板空）返回 null', async () => {
    const { bridge } = mockBridge({ text: null })
    await expect(createNativeClipboard(bridge).read()).resolves.toBeNull()
  })

  it('read → 空对象（老 host 5s 超时回空）返回 null', async () => {
    const { bridge } = mockBridge({})
    await expect(createNativeClipboard(bridge).read()).resolves.toBeNull()
  })
})

describe('web clipboard（navigator.clipboard 实现）', () => {
  function mockNavigatorClipboard(impl: {
    writeText?: (text: string) => Promise<void>
    readText?: () => Promise<string>
  }) {
    Object.defineProperty(navigator, 'clipboard', {
      value: impl,
      configurable: true,
    })
  }

  it('write 成功', async () => {
    const writeText = vi.fn(async (_text: string) => {})
    mockNavigatorClipboard({ writeText })
    await createWebClipboard(vi.fn()).write({ text: 'hello' })
    expect(writeText).toHaveBeenCalledWith('hello')
  })

  it('read 成功返回文本', async () => {
    mockNavigatorClipboard({ readText: async () => 'from-clipboard' })
    await expect(createWebClipboard(vi.fn()).read()).resolves.toEqual({
      text: 'from-clipboard',
    })
  })

  it('read 空串返回 null', async () => {
    mockNavigatorClipboard({ readText: async () => '' })
    await expect(createWebClipboard(vi.fn()).read()).resolves.toBeNull()
  })

  it('read 被拒绝 → 返回 null 并 sendLog 降级（不崩）', async () => {
    mockNavigatorClipboard({
      readText: async () => {
        throw new DOMException('denied', 'NotAllowedError')
      },
    })
    const log = vi.fn()
    await expect(createWebClipboard(log).read()).resolves.toBeNull()
    expect(log).toHaveBeenCalledOnce()
    expect(log.mock.calls[0][0]).toContain('denied')
  })
})
