// axios 实例 + 401 自动 refresh + 错误统一格式.
//
// 跟 Flutter client 的 _http_helpers.dart 同套思路:
//   - 业务请求 401 → 触发 refreshNow (inflight 锁共享)
//   - refresh 成功 → 重试原请求
//   - refresh 失败 → signOut + 跳 /login

import axios, { type AxiosError, type InternalAxiosRequestConfig } from 'axios'
import router from '@/router'
import { useAuthStore } from '@/stores/auth'

export const http = axios.create({
  // baseURL 留空 — 同源, 直接打 /v1/* 走 site nginx 路径分发
  baseURL: '',
  timeout: 30_000,
  headers: { 'Content-Type': 'application/json' },
})

// ── 请求拦截: 自动加 Bearer ─────────────────────
http.interceptors.request.use((cfg) => {
  const auth = useAuthStore()
  if (auth.token) {
    cfg.headers = cfg.headers ?? {}
    cfg.headers.Authorization = `Bearer ${auth.token}`
  }
  return cfg
})

// ── refresh inflight 锁 ─────────────────────────
let refreshing: Promise<boolean> | null = null

async function tryRefresh(): Promise<boolean> {
  if (refreshing) return refreshing
  const auth = useAuthStore()
  refreshing = (async () => {
    if (!auth.refreshToken) return false
    try {
      const r = await axios.post(
        '/v1/auth/refresh',
        { refresh_token: auth.refreshToken },
        { headers: { 'Content-Type': 'application/json' } },
      )
      auth.setAccessToken(r.data.access_token)
      return true
    } catch {
      return false
    }
  })()
  try {
    return await refreshing
  } finally {
    refreshing = null
  }
}

// ── 响应拦截: 401 自动 refresh + retry ───────────
http.interceptors.response.use(
  (r) => r,
  async (err: AxiosError) => {
    const cfg = err.config as InternalAxiosRequestConfig & { _retry?: boolean }
    if (err.response?.status !== 401 || cfg._retry) {
      return Promise.reject(err)
    }
    cfg._retry = true
    const ok = await tryRefresh()
    if (!ok) {
      // refresh 也失败 → 强制登出 + 跳登录
      const auth = useAuthStore()
      auth.signOut()
      router.push({ name: 'login', query: { reason: 'session_expired' } })
      return Promise.reject(err)
    }
    // 用新 token 重试一次
    const auth = useAuthStore()
    cfg.headers = cfg.headers ?? {}
    cfg.headers.Authorization = `Bearer ${auth.token}`
    return http.request(cfg)
  },
)

// ── 把后端错误体转成可读 message ─────────────────
export function errorMessage(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const body = err.response?.data as { error?: { code?: string; message?: string } } | undefined
    return (
      body?.error?.message ||
      body?.error?.code ||
      err.message ||
      `HTTP ${err.response?.status ?? '?'}`
    )
  }
  return err instanceof Error ? err.message : String(err)
}
