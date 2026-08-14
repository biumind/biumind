// image-presign 插件单测 —— biu-file:// 渲染时解析。
//
// 锁的语义：
//   * 非 biu-file URL 原样透传（同步返回，不发起 presign）；
//   * biu-file:// 未命中缓存 → 发起 presign（async），命中 → 同步返回；
//   * 缓存 TTL 内不重复请求；同一 fileId 并发只发一个请求（inFlight 去重）；
//   * presign 返回空串视为失败（reject），不写缓存；
//   * onImageLoadError：按当前 src 反查 fileId，强刷重换并重设 img.src；
//     30 秒内的重复错误事件不再重试（防断网死循环）。

import { describe, expect, it, vi } from 'vitest'

import { createImagePresignConfig } from '../src/plugins/image-presign'

const ID_A = '11111111-2222-3333-4444-555555555555'
const ID_B = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'

const BIU_A = `biu-file://${ID_A}`
const BIU_B = `biu-file://${ID_B}`

function fakeUrl(fileId: string, n: number): string {
  return `https://minio.example/bucket/obj?file=${fileId}&v=${n}&X-Amz-Expires=900`
}

function makeImage(src: string): HTMLImageElement {
  const img = document.createElement('img')
  img.src = src
  return img
}

describe('proxyDomURL', () => {
  it('非 biu-file URL 原样同步透传', () => {
    const presignGet = vi.fn(async () => 'x')
    const { proxyDomURL } = createImagePresignConfig(presignGet)
    expect(proxyDomURL('https://example.com/pic.png')).toBe(
      'https://example.com/pic.png',
    )
    expect(presignGet).not.toHaveBeenCalled()
  })

  it('biu-file 首次 async 换取，命中缓存后同步返回', async () => {
    const presignGet = vi.fn(async (id: string) => fakeUrl(id, 1))
    const { proxyDomURL } = createImagePresignConfig(presignGet)

    const first = proxyDomURL(BIU_A)
    expect(first).toBeInstanceOf(Promise)
    expect(await first).toBe(fakeUrl(ID_A, 1))
    expect(presignGet).toHaveBeenCalledTimes(1)

    // 缓存命中 → 同步 string，且不再请求
    expect(proxyDomURL(BIU_A)).toBe(fakeUrl(ID_A, 1))
    expect(presignGet).toHaveBeenCalledTimes(1)
  })

  it('同一 fileId 并发换取只发一个请求', async () => {
    let calls = 0
    const presignGet = vi.fn(async (id: string) => {
      calls++
      await new Promise((r) => setTimeout(r, 20))
      return fakeUrl(id, 1)
    })
    const { proxyDomURL } = createImagePresignConfig(presignGet)
    const [a, b] = await Promise.all([
      proxyDomURL(BIU_A),
      proxyDomURL(BIU_A),
    ])
    expect(a).toBe(fakeUrl(ID_A, 1))
    expect(b).toBe(fakeUrl(ID_A, 1))
    expect(calls).toBe(1)
  })

  it('presign 返回空串视为失败：reject 且不写缓存', async () => {
    const presignGet = vi.fn(async () => '')
    const { proxyDomURL } = createImagePresignConfig(presignGet)
    await expect(proxyDomURL(BIU_A)).rejects.toThrow('empty url')
    expect(presignGet).toHaveBeenCalledTimes(1)
    // 未写缓存 → 下次仍会重新请求
    await expect(proxyDomURL(BIU_A)).rejects.toThrow('empty url')
    expect(presignGet).toHaveBeenCalledTimes(2)
  })

  it('不同 fileId 各自独立换取', async () => {
    const presignGet = vi.fn(async (id: string) => fakeUrl(id, 1))
    const { proxyDomURL } = createImagePresignConfig(presignGet)
    expect(await proxyDomURL(BIU_A)).toBe(fakeUrl(ID_A, 1))
    expect(await proxyDomURL(BIU_B)).toBe(fakeUrl(ID_B, 1))
    expect(presignGet).toHaveBeenCalledTimes(2)
  })
})

describe('onImageLoadError', () => {
  it('按当前 src 反查 fileId，强刷重换并重设 img.src', async () => {
    let n = 0
    const presignGet = vi.fn(async (id: string) => fakeUrl(id, ++n))
    const { proxyDomURL, onImageLoadError } =
      createImagePresignConfig(presignGet)

    const firstUrl = (await proxyDomURL(BIU_A)) as string
    const img = makeImage(firstUrl)

    await onImageLoadError({ target: img } as unknown as Event)
    expect(presignGet).toHaveBeenCalledTimes(2)
    expect(img.src).toBe(fakeUrl(ID_A, 2))
    // 强刷后缓存指向新 URL
    expect(proxyDomURL(BIU_A)).toBe(fakeUrl(ID_A, 2))
  })

  it('30 秒内重复错误事件不再重试', async () => {
    let n = 0
    const presignGet = vi.fn(async (id: string) => fakeUrl(id, ++n))
    const { proxyDomURL, onImageLoadError } =
      createImagePresignConfig(presignGet)

    const firstUrl = (await proxyDomURL(BIU_A)) as string
    const img = makeImage(firstUrl)
    await onImageLoadError({ target: img } as unknown as Event)
    expect(presignGet).toHaveBeenCalledTimes(2)

    // 新 URL 再次 403（断网）→ 30 秒防抖窗口内，不再发请求
    await onImageLoadError({ target: img } as unknown as Event)
    expect(presignGet).toHaveBeenCalledTimes(2)
  })

  it('src 不在缓存里（外链图）不动作', async () => {
    const presignGet = vi.fn(async (id: string) => fakeUrl(id, 1))
    const { onImageLoadError } = createImagePresignConfig(presignGet)
    const img = makeImage('https://example.com/gone.png')
    await onImageLoadError({ target: img } as unknown as Event)
    expect(presignGet).not.toHaveBeenCalled()
    expect(img.src).toBe('https://example.com/gone.png')
  })
})

// 新设计依赖的不变量：biu-file:// 规范 URI 经 Milkdown 解析→序列化
// 逐字节不变（旧的「临时 URL 落库前换回」方案正是死在 presigned URL 的
// & 被序列化成 \& 上）。这里用真实编辑器锁死这个 round-trip。
describe('biu-file markdown round-trip', () => {
  it('insert → getMarkdown 后 biu-file URI 逐字节不变', async () => {
    const { Editor, defaultValueCtx, rootCtx } = await import(
      '@milkdown/kit/core'
    )
    const { commonmark } = await import('@milkdown/kit/preset/commonmark')
    const { listener } = await import('@milkdown/kit/plugin/listener')
    const { insert, getMarkdown } = await import('@milkdown/kit/utils')

    const el = document.createElement('div')
    document.body.appendChild(el)
    const editor = await Editor.make()
      .config((ctx) => {
        ctx.set(rootCtx, el)
        ctx.set(defaultValueCtx, '')
      })
      .use(commonmark)
      .use(listener)
      .create()

    editor.action(insert(`![photo.png](${BIU_A})\n\n[附件](${BIU_B})`))
    await new Promise((r) => setTimeout(r, 50))
    const out = editor.action(getMarkdown())
    expect(out).toContain(`![photo.png](${BIU_A})`)
    expect(out).toContain(`[附件](${BIU_B})`)
  })
})
