// 剪贴板双实现：native mock bridge（callHandler 通路），web mock
// navigator.clipboard 成功/拒绝两路径。

import { describe, expect, it, vi } from 'vitest'
import { Fragment, Schema } from 'prosemirror-model'

import type { BridgeClient } from '../src/bridge/client'
import {
  createNativeClipboard,
  createWebClipboard,
} from '../src/context-menu/clipboard'
import { fragmentToHtml } from '../src/context-menu/html'

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

  it('write 带 html → payload 透传双格式（P2）', async () => {
    const { bridge, sendClipboardWrite } = mockBridge({ text: null })
    await createNativeClipboard(bridge).write({
      text: 'md **bold**',
      html: '<p>md <strong>bold</strong></p>',
    })
    expect(sendClipboardWrite).toHaveBeenCalledWith({
      text: 'md **bold**',
      html: '<p>md <strong>bold</strong></p>',
    })
  })

  it('write 带 image → payload 透传图片二进制（单图复制）', async () => {
    const { bridge, sendClipboardWrite } = mockBridge({ text: null })
    await createNativeClipboard(bridge).write({
      text: '![a](biu-file://uuid)',
      html: '<img src="https://signed">',
      image: { base64: 'iVBORw0KGgo=', mime: 'image/png' },
    })
    expect(sendClipboardWrite).toHaveBeenCalledWith({
      text: '![a](biu-file://uuid)',
      html: '<img src="https://signed">',
      imageBase64: 'iVBORw0KGgo=',
      imageMime: 'image/png',
    })
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

  // P2 web 端双格式：ClipboardItem text/plain + text/html 一起写；
  // 不支持（无 ClipboardItem / 无 write）或写入被拒 → 回退 writeText。
  describe('write 双格式（ClipboardItem）', () => {
    function stubClipboardItem() {
      const instances: { types: readonly string[] }[] = []
      class FakeClipboardItem {
        readonly types: readonly string[]
        constructor(items: Record<string, Blob>) {
          this.types = Object.keys(items)
          instances.push(this)
        }
      }
      vi.stubGlobal('ClipboardItem', FakeClipboardItem)
      return instances
    }

    it('有 html 且环境支持 → navigator.clipboard.write 双格式', async () => {
      const instances = stubClipboardItem()
      const write = vi.fn(async (_items: unknown[]) => {})
      const writeText = vi.fn(async (_text: string) => {})
      mockNavigatorClipboard({ write, writeText } as never)
      await createWebClipboard(vi.fn()).write({
        text: 'md **bold**',
        html: '<p>md <strong>bold</strong></p>',
      })
      expect(write).toHaveBeenCalledOnce()
      expect(instances[0].types).toEqual(['text/plain', 'text/html'])
      expect(writeText).not.toHaveBeenCalled()
      vi.unstubAllGlobals()
    })

    it('无 ClipboardItem（老环境）→ 回退 writeText 纯文本', async () => {
      vi.stubGlobal('ClipboardItem', undefined)
      const writeText = vi.fn(async (_text: string) => {})
      mockNavigatorClipboard({ writeText })
      await createWebClipboard(vi.fn()).write({
        text: 'md **bold**',
        html: '<p>md <strong>bold</strong></p>',
      })
      expect(writeText).toHaveBeenCalledWith('md **bold**')
      vi.unstubAllGlobals()
    })

    it('write 被拒（Safari MIME 窄）→ 回退 writeText + sendLog，不崩', async () => {
      stubClipboardItem()
      const write = vi.fn(async (_items: unknown[]) => {
        throw new DOMException('type not supported', 'NotAllowedError')
      })
      const writeText = vi.fn(async (_text: string) => {})
      mockNavigatorClipboard({ write, writeText } as never)
      const log = vi.fn()
      await createWebClipboard(log).write({
        text: 'md **bold**',
        html: '<p>md <strong>bold</strong></p>',
      })
      expect(writeText).toHaveBeenCalledWith('md **bold**')
      expect(log).toHaveBeenCalledOnce()
      vi.unstubAllGlobals()
    })

    it('无 html（纯文本复制）→ 直接 writeText，不走 ClipboardItem', async () => {
      const instances = stubClipboardItem()
      const writeText = vi.fn(async (_text: string) => {})
      mockNavigatorClipboard({ writeText })
      await createWebClipboard(vi.fn()).write({ text: 'plain' })
      expect(writeText).toHaveBeenCalledWith('plain')
      expect(instances).toHaveLength(0)
      vi.unstubAllGlobals()
    })

    it('带 image（单图复制）→ ClipboardItem 含图片 MIME 三格式', async () => {
      const instances = stubClipboardItem()
      const write = vi.fn(async (_items: unknown[]) => {})
      const writeText = vi.fn(async (_text: string) => {})
      mockNavigatorClipboard({ write, writeText } as never)
      await createWebClipboard(vi.fn()).write({
        text: '![a](biu-file://uuid)',
        html: '<img src="https://signed">',
        image: { base64: 'AQID', mime: 'image/png' },
      })
      expect(write).toHaveBeenCalledOnce()
      expect(instances[0].types).toEqual(['text/plain', 'text/html', 'image/png'])
      expect(writeText).not.toHaveBeenCalled()
      vi.unstubAllGlobals()
    })

    it('只带 image 无 html → 同样走 ClipboardItem（text + image）', async () => {
      const instances = stubClipboardItem()
      const write = vi.fn(async (_items: unknown[]) => {})
      const writeText = vi.fn(async (_text: string) => {})
      mockNavigatorClipboard({ write, writeText } as never)
      await createWebClipboard(vi.fn()).write({
        text: '![a](biu-file://uuid)',
        image: { base64: 'AQID', mime: 'image/png' },
      })
      expect(write).toHaveBeenCalledOnce()
      expect(instances[0].types).toEqual(['text/plain', 'image/png'])
      vi.unstubAllGlobals()
    })
  })
})

describe('fragmentToHtml（P2 双格式复制的 HTML 序列化）', () => {
  // 最小 schema：doc > paragraph+，text 带 strong mark
  const schema = new Schema({
    nodes: {
      doc: { content: 'paragraph+' },
      paragraph: {
        content: 'text*',
        toDOM: () => ['p', 0],
      },
      text: {},
    },
    marks: {
      strong: { toDOM: () => ['strong', 0] },
    },
  })

  it('段落 + 加粗 mark 序列化为 HTML', () => {
    const p = schema.nodes.paragraph.create(null, [
      schema.text('plain '),
      schema.text('bold', [schema.marks.strong.create()]),
    ])
    const html = fragmentToHtml(schema, Fragment.from(p))
    expect(html).toBe('<p>plain <strong>bold</strong></p>')
  })

  it('多个节点按顺序拼接', () => {
    const p1 = schema.nodes.paragraph.create(null, schema.text('a'))
    const p2 = schema.nodes.paragraph.create(null, schema.text('b'))
    expect(fragmentToHtml(schema, Fragment.from([p1, p2]))).toBe(
      '<p>a</p><p>b</p>',
    )
  })
})
