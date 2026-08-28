// Image upload — 粘贴/拖入图片的 onUpload 链路。
//
// Crepe ImageBlock 的默认 onUpload 是 URL.createObjectURL（blob URL 只在
// 当前 webview 会话内存中有效，落库即坏引用）。这里覆盖成：File 读成
// base64 → 经注入的 request（main.ts 接 bridge.requestImageFileUpload）
// 发给 host → host 走与「插入图片」同一条 presign 直传链路 → 回
// biu-file://<uuid> 规范 URI 作为节点 src。
//
// 失败语义：host 回 null / 超时 / 异常 → 抛错。Crepe uploader 的
// Promise.all 整体 reject，图片节点不插入 —— 绝不回落 blob URL，
// 防止不可持久化的引用再次落库（这是本插件存在的根因）。
//
// request 由调用方注入（main.ts 里接 bridge），插件本身不依赖
// BridgeClient，方便 vitest 直接测。

export interface ImageUploadRequest {
  name: string
  mime: string
  dataBase64: string
}

export type ImageUploadRequestFn = (
  file: ImageUploadRequest,
) => Promise<string | null>

export interface ImageUploadConfig {
  onUpload: (file: File) => Promise<string>
}

/** File → base64（不带 data: URL 前缀）。 */
function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error ?? new Error('FileReader error'))
    reader.onload = () => {
      const result = reader.result
      if (typeof result !== 'string') {
        reject(new Error('FileReader: unexpected result type'))
        return
      }
      // "data:<mime>;base64,<data>" → 只留 data 段
      const comma = result.indexOf(',')
      resolve(comma >= 0 ? result.slice(comma + 1) : result)
    }
    reader.readAsDataURL(file)
  })
}

export function createImageUploadConfig(
  request: ImageUploadRequestFn,
  log: (msg: string) => void,
): ImageUploadConfig {
  return {
    onUpload: async (file: File) => {
      try {
        const dataBase64 = await readFileAsBase64(file)
        const uri = await request({
          name: file.name || 'pasted-image',
          mime: file.type || 'image/png',
          dataBase64,
        })
        if (typeof uri !== 'string' || uri.length === 0) {
          throw new Error('host returned empty uri')
        }
        return uri
      } catch (error) {
        log(`image upload failed: ${String(error)}`)
        throw error
      }
    },
  }
}
