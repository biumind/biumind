// token_manager.ts — 跨页面共享的 access_token / refresh_token 容器 + 自动 refresh.
//
// 设计要点:
//   - access_token 存 uni.setStorageSync, 启动从 storage 恢复
//   - refresh_token 同位置, 401 时调 /v1/auth/refresh 静默换 access
//   - 内存里持有 inflight refresh promise 防止并发刷新风暴
//   - refresh 失败 → clearTokens + 抛 SessionExpired error, client.ts
//     拦截 → 跳登录页

const STORAGE_KEY = 'biumind.tokens';

export interface StoredTokens {
  accessToken: string;
  refreshToken: string;
  expiresAt: number; // ms epoch
}

let cache: StoredTokens | null = null;

function load(): StoredTokens | null {
  if (cache !== null) return cache;
  try {
    const raw = uni.getStorageSync(STORAGE_KEY);
    if (raw && typeof raw === 'object') {
      cache = raw as StoredTokens;
      return cache;
    }
  } catch {
    /* storage unavailable — 视为未登录 */
  }
  return null;
}

// getAccessToken — 当前还有效的 access. 留 30s buffer, 接近过期返 null
// 让调用方主动 refresh 而非等服务端 401 (减一次 round-trip).
export function getAccessToken(): string | null {
  const t = load();
  if (!t) return null;
  if (Date.now() + 30_000 >= t.expiresAt) return null;
  return t.accessToken;
}

export function getRefreshToken(): string | null {
  const t = load();
  return t ? t.refreshToken : null;
}

export function setTokens(t: StoredTokens): void {
  cache = t;
  try {
    uni.setStorageSync(STORAGE_KEY, t);
  } catch {
    /* 存储满 / 拒绝 — 内存 cache 仍可用至 app 退出 */
  }
}

export function clearTokens(): void {
  cache = null;
  try {
    uni.removeStorageSync(STORAGE_KEY);
  } catch {
    /* noop */
  }
}

// isLoggedIn — load() 非空就视为登录, 不严格校验是否过期 (过期会
// 在请求时由 refresh 路径处理). 仅用于"未登录跳登录页"的早期 guard.
export function isLoggedIn(): boolean {
  return load() !== null;
}

// SessionExpired — refresh 失败的 sentinel error. client.ts 捕获 → 跳登录.
export class SessionExpired extends Error {
  constructor(message = 'session expired') {
    super(message);
    this.name = 'SessionExpired';
  }
}

// inflight refresh promise — 多个并发请求遇到 401 时只发一个 refresh,
// 其他请求 await 同一个 promise.
let inflightRefresh: Promise<string> | null = null;

interface RefreshResp {
  access_token: string;
  expires_in_seconds: number;
}

const BASE_URL =
  (import.meta.env.VITE_BIU_API_BASE as string) || '';

// refreshAccessToken — 用 refresh_token 调 /v1/auth/refresh 换新 access.
// 成功: 更新 storage, 返回新 access_token
// 失败: clearTokens + 抛 SessionExpired
export async function refreshAccessToken(): Promise<string> {
  if (inflightRefresh) return inflightRefresh;

  const refreshToken = getRefreshToken();
  if (!refreshToken) {
    throw new SessionExpired('no refresh token');
  }

  inflightRefresh = new Promise<string>((resolve, reject) => {
    uni.request({
      url: BASE_URL + '/v1/auth/refresh',
      method: 'POST',
      header: { 'Content-Type': 'application/json' },
      data: { refresh_token: refreshToken },
      success: (res) => {
        const status = res.statusCode || 0;
        if (status >= 200 && status < 300) {
          const r = res.data as RefreshResp;
          if (!r.access_token || !r.expires_in_seconds) {
            clearTokens();
            reject(new SessionExpired('bad refresh response'));
            return;
          }
          const cur = load();
          // 后端 /v1/auth/refresh 当前只下发新 access, 不轮换 refresh —
          // 复用旧 refresh 直到它过期. 跟 identity handleRefresh 实现一致.
          setTokens({
            accessToken: r.access_token,
            refreshToken: cur ? cur.refreshToken : refreshToken,
            expiresAt: Date.now() + r.expires_in_seconds * 1000,
          });
          resolve(r.access_token);
        } else {
          // 401 / 403 → refresh_token 也失效了, 清掉跳登录
          clearTokens();
          reject(new SessionExpired('refresh denied (status ' + status + ')'));
        }
      },
      fail: () => {
        // 网络失败不清 token (下次有网就好), 但报错让上层处理
        reject(new Error('refresh network failed'));
      },
      complete: () => {
        inflightRefresh = null;
      },
    });
  });
  return inflightRefresh;
}
