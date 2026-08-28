// Image presign — biu-file://<uuid> 渲染时解析。
//
// 正文从插入、编辑、落库到同步全程只存 biu-file:// 规范 URI；presigned
// URL 只作为 DOM 渲染耗材存在。Crepe 的 image block / inline 组件支持
// `proxyDomURL`：文档节点保持原始 src，写进 <img> 前经这里换 URL。
//   * 缓存命中 → 同步返回（nodeView 每次 update 都会调 proxyDomURL，
//     无缓存会每张图每次重排都发一个 bridge 往返；同步返回也避免闪烁）。
//   * 未命中 / 过期 → 返回 Promise（组件原生支持 async proxyDomURL）。
//   * 15 分钟 TTL 过期导致 <img> 403 → onImageLoadError 强制重换并重设
//     src（同 fileId 30 秒内最多重试一次，防断网时错误事件死循环）。
//
// presignGet 由调用方注入（main.ts 里接 bridge.requestPresignGet），
// 插件本身不依赖 BridgeClient，方便 vitest 直接测。

export type PresignGet = (fileId: string) => Promise<string>

export interface ImagePresignConfig {
  proxyDomURL: (url: string) => Promise<string> | string
  onImageLoadError: (event: Event) => void | Promise<void>
  /**
   * Promise 化的 URL 解析（复制链路用）：biu-file:// → presigned URL
   * （走同一缓存），其他 URL 原样返回（blob:/https: 透传，blob 在同
   * 会话内可 fetch）。不要把 resolve 展开进 Crepe feature config —
   * 只挑 proxyDomURL / onImageLoadError 两个字段。
   */
  resolve: (url: string) => Promise<string>
}

const BIU_FILE_RE = /^biu-file:\/\/([0-9a-fA-F-]{36})$/

/** brain presignGetTTL = 15min，留 1 分钟余量防边界过期。 */
const CACHE_TTL_MS = 14 * 60 * 1000

/** 同一 fileId 两次 onImageLoadError 强刷的最小间隔（断网时错误事件会连发）。 */
const RETRY_INTERVAL_MS = 30 * 1000

export function createImagePresignConfig(
  presignGet: PresignGet,
): ImagePresignConfig {
  const cache = new Map<string, { url: string; expiresAt: number }>()
  const inFlight = new Map<string, Promise<string>>()
  const lastRetryAt = new Map<string, number>()

  function fetchAndCache(fileId: string): Promise<string> {
    const pending = inFlight.get(fileId)
    if (pending) return pending
    const p = presignGet(fileId)
      .then((url) => {
        if (!url) throw new Error(`presignGet ${fileId}: empty url`)
        cache.set(fileId, { url, expiresAt: Date.now() + CACHE_TTL_MS })
        return url
      })
      .finally(() => inFlight.delete(fileId))
    inFlight.set(fileId, p)
    return p
  }

  return {
    proxyDomURL: (url: string) => {
      const m = BIU_FILE_RE.exec(url)
      if (!m) return url
      const hit = cache.get(m[1])
      if (hit && hit.expiresAt > Date.now()) return hit.url
      return fetchAndCache(m[1])
    },

    onImageLoadError: (event: Event) => {
      const img = event.target as HTMLImageElement | null
      if (!img || !img.src) return
      // 当前 src 是上次换出的临时 URL → 反查 fileId，强制重换一次。
      for (const [fileId, entry] of cache) {
        if (entry.url !== img.src) continue
        const now = Date.now()
        if (now - (lastRetryAt.get(fileId) ?? 0) < RETRY_INTERVAL_MS) return
        lastRetryAt.set(fileId, now)
        cache.delete(fileId)
        return fetchAndCache(fileId)
          .then((url) => {
            img.src = url
          })
          .catch(() => {})
      }
    },

    resolve: (url: string) => {
      const m = BIU_FILE_RE.exec(url)
      if (!m) return Promise.resolve(url)
      const hit = cache.get(m[1])
      if (hit && hit.expiresAt > Date.now()) return Promise.resolve(hit.url)
      return fetchAndCache(m[1])
    },
  }
}
