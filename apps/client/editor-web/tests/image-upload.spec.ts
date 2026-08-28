// 粘贴/拖入图片上传（onUpload 链路）：File → base64 → 注入的 request
// → biu-file:// 规范 URI。失败必须抛错（Crepe uploader 整体 reject，
// 节点不插入）——绝不回落 blob URL（本插件存在的根因）。

import { describe, expect, it, vi } from 'vitest'

import {
  createImageUploadConfig,
  type ImageUploadRequest,
} from '../src/plugins/image-upload'

function makeFile(
  bytes: number[],
  name = 'pic.png',
  type = 'image/png',
): File {
  return new File([new Uint8Array(bytes)], name, { type })
}

describe('image-upload onUpload（粘贴/拖入上传链路）', () => {
  it('成功：File → base64（无前缀）→ request → 返回规范 URI', async () => {
    const request = vi.fn(
      async (_file: ImageUploadRequest) =>
        'biu-file://3f6b1d2a-0000-4000-8000-000000000000',
    )
    const { onUpload } = createImageUploadConfig(request, vi.fn())
    const uri = await onUpload(makeFile([1, 2, 3]))
    expect(uri).toBe('biu-file://3f6b1d2a-0000-4000-8000-000000000000')
    expect(request).toHaveBeenCalledOnce()
    const arg = request.mock.calls[0]![0]
    expect(arg.name).toBe('pic.png')
    expect(arg.mime).toBe('image/png')
    // [1,2,3] 的 base64，不带 data: URL 前缀
    expect(arg.dataBase64).toBe('AQID')
  })

  it('host 回 null → 抛错 + log（节点不插入，不产生 blob）', async () => {
    const request = vi.fn(async () => null)
    const log = vi.fn()
    const { onUpload } = createImageUploadConfig(request, log)
    await expect(onUpload(makeFile([1]))).rejects.toThrow()
    expect(log).toHaveBeenCalledOnce()
  })

  it('host 回空串 → 抛错 + log', async () => {
    const request = vi.fn(async () => '')
    const log = vi.fn()
    const { onUpload } = createImageUploadConfig(request, log)
    await expect(onUpload(makeFile([1]))).rejects.toThrow()
    expect(log).toHaveBeenCalledOnce()
  })

  it('request 自身异常 → 透传抛错 + log', async () => {
    const request = vi.fn(async (): Promise<string | null> => {
      throw new Error('network down')
    })
    const log = vi.fn()
    const { onUpload } = createImageUploadConfig(request, log)
    await expect(onUpload(makeFile([1]))).rejects.toThrow('network down')
    expect(log).toHaveBeenCalledOnce()
  })

  it('空文件名 / 空 mime → 兜底默认值', async () => {
    const request = vi.fn(async (_file: ImageUploadRequest) => 'biu-file://x')
    const { onUpload } = createImageUploadConfig(request, vi.fn())
    await onUpload(new File([new Uint8Array([1])], '', { type: '' }))
    const arg = request.mock.calls[0]![0]
    expect(arg.name).toBe('pasted-image')
    expect(arg.mime).toBe('image/png')
  })
})
