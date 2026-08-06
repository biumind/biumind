// lib/cache_clear.ts — 清除本地缓存.
//
// 列出所有 biumind.* / 我们已知的 storage key, 一键删除.
// 不删 access_token / refresh_token (token_manager 自己管, 退出登录走那条路径).

const CLEARABLE_KEYS = [
  // 草稿
  // (实际 key 是 'biumind.draft.<threadId>' 动态, 用 prefix 删)
  'biumind.preferred_model',
  'biumind.font_scale',
  'biumind.pinned_threads',
  'biumind.privacy_consent',
  'biumind.pending_thread_id',
];

const PREFIXED_KEYS = ['biumind.draft.'];

export interface ClearResult {
  cleared: number;
  bytes?: number;
}

export async function clearLocalCache(): Promise<ClearResult> {
  let cleared = 0;
  let bytes = 0;

  // 已知固定 key
  for (const k of CLEARABLE_KEYS) {
    try {
      const v = uni.getStorageSync(k);
      if (v !== '' && v !== undefined && v !== null) {
        bytes += stringSize(v);
        uni.removeStorageSync(k);
        cleared++;
      }
    } catch {
      /* noop */
    }
  }

  // 前缀匹配 (drafts) — 用 getStorageInfo 列出所有 key 再过滤
  try {
    const info = await new Promise<UniApp.GetStorageInfoSuccess>(
      (resolve, reject) => {
        uni.getStorageInfo({
          success: resolve,
          fail: reject,
        });
      },
    );
    const keys = info.keys || [];
    for (const k of keys) {
      if (PREFIXED_KEYS.some((p) => k.startsWith(p))) {
        try {
          const v = uni.getStorageSync(k);
          bytes += stringSize(v);
          uni.removeStorageSync(k);
          cleared++;
        } catch {
          /* noop */
        }
      }
    }
  } catch {
    /* getStorageInfo 失败 — 忽略, 至少 CLEARABLE_KEYS 已清 */
  }

  return { cleared, bytes };
}

function stringSize(v: unknown): number {
  if (typeof v === 'string') return v.length * 2; // UTF-16 估算
  try {
    return JSON.stringify(v).length * 2;
  } catch {
    return 0;
  }
}

export function formatBytes(b: number): string {
  if (b < 1024) return b + ' B';
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
  return (b / 1024 / 1024).toFixed(2) + ' MB';
}
