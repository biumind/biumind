// data/api/client.ts — uni.request 的 BiuMind 后端封装.
//
// 公共行为:
//   - 自动加 Authorization Bearer header
//   - 401 → 自动调 refreshAccessToken() + 重放本次请求 (一次)
//   - refresh 失败 → SessionExpired 抛出 + uni.redirectTo 跳登录页
//   - 业务 4xx/5xx → ApiError 抛出 (含 status / code / message)

import {
  getAccessToken,
  refreshAccessToken,
  SessionExpired,
  clearTokens,
} from '@/core/token_manager';

const BASE_URL =
  (import.meta.env.VITE_BIU_API_BASE as string) || '';

export interface ApiError extends Error {
  status: number;
  code: string;
}

function buildError(status: number, code: string, message: string): ApiError {
  const e = new Error(message) as ApiError;
  e.status = status;
  e.code = code;
  return e;
}

// goLogin — 401 + refresh 失败时统一跳登录页. 用 reLaunch 清空页面栈,
// 防止登录后回退又落到失效页. 调用时机由 401 拦截器决定.
function goLogin(): void {
  clearTokens();
  // reLaunch 会触发 onLaunch, 不弹"页面栈过深"
  uni.reLaunch({
    url: '/pages/me/login',
    fail: () => {
      // reLaunch 失败 (极少, 例如 url 路径错) — 兜底用 navigateTo
      uni.navigateTo({ url: '/pages/me/login' });
    },
  });
}

interface RequestOpts {
  method: 'GET' | 'POST' | 'DELETE' | 'PUT' | 'PATCH';
  path: string;
  body?: unknown;
  /** 额外 header (如 notes 更新的 If-Match 乐观锁版本) */
  header?: Record<string, string>;
  /** internal: 防止 401 → refresh → retry 又 401 → 死循环, 第二次直接跳登录 */
  retried?: boolean;
}

async function rawRequest<TRes>(opts: RequestOpts): Promise<TRes> {
  const token = getAccessToken();
  const header: Record<string, string> = { ...(opts.header || {}) };
  if (opts.body !== undefined) header['Content-Type'] = 'application/json';
  if (token) header['Authorization'] = 'Bearer ' + token;

  return new Promise<TRes>((resolve, reject) => {
    uni.request({
      url: BASE_URL + opts.path,
      method: opts.method,
      header,
      data: opts.body as Record<string, unknown> | undefined,
      success: (res) => {
        const status = res.statusCode || 0;
        if (status >= 200 && status < 300) {
          resolve(res.data as TRes);
          return;
        }
        const errBody = res.data as
          | { error?: { code?: string; message?: string } }
          | undefined;
        const code = errBody?.error?.code || 'http_' + status;
        const msg = errBody?.error?.message || `HTTP ${status}`;
        reject(buildError(status, code, msg));
      },
      fail: (e) =>
        reject(buildError(0, 'network', e.errMsg || 'network failure')),
    });
  });
}

// withAuth — 401 拦截 + 自动 refresh + 重放一次. 任何 4xx/5xx 业务错
// 仍正常抛出, 调用方按 ApiError.code 处理.
async function withAuth<TRes>(opts: RequestOpts): Promise<TRes> {
  try {
    return await rawRequest<TRes>(opts);
  } catch (e: unknown) {
    const err = e as ApiError;
    if (err.status !== 401 || opts.retried) throw err;

    // 401 — 试一次 refresh
    try {
      await refreshAccessToken();
    } catch (refreshErr) {
      if (refreshErr instanceof SessionExpired) {
        goLogin();
      }
      throw err;
    }
    // refresh 成功, 重放一次 (retried=true 防止再次 401 死循环)
    return rawRequest<TRes>({ ...opts, retried: true });
  }
}

export async function post<TReq, TRes>(
  path: string,
  body: TReq,
): Promise<TRes> {
  return withAuth<TRes>({ method: 'POST', path, body });
}

export async function get<TRes>(path: string): Promise<TRes> {
  return withAuth<TRes>({ method: 'GET', path });
}

export async function del<TRes>(path: string): Promise<TRes> {
  return withAuth<TRes>({ method: 'DELETE', path });
}

export async function put<TReq, TRes>(
  path: string,
  body: TReq,
  header?: Record<string, string>,
): Promise<TRes> {
  return withAuth<TRes>({ method: 'PUT', path, body, header });
}

export async function patch<TReq, TRes>(
  path: string,
  body: TReq,
): Promise<TRes> {
  return withAuth<TRes>({ method: 'PATCH', path, body });
}
