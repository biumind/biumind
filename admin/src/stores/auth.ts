// auth 状态管理 — token + user info + permissions.
//
// Storage: sessionStorage (关浏览器即清, admin 操作敏感不持久化).
// 不用 cookie (避免跟同源 client 共享认证状态).

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import * as api from '@/api/admin'
import type { MeResponse, Role } from '@/api/types'
import { errorMessage } from '@/api/http'

const KEY_ACCESS = 'biumind_admin_access_token'
const KEY_REFRESH = 'biumind_admin_refresh_token'
const KEY_USER = 'biumind_admin_user'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string>(sessionStorage.getItem(KEY_ACCESS) ?? '')
  const refreshToken = ref<string>(sessionStorage.getItem(KEY_REFRESH) ?? '')
  const user = ref<MeResponse | null>(loadUser())

  function loadUser(): MeResponse | null {
    const raw = sessionStorage.getItem(KEY_USER)
    if (!raw) return null
    try {
      return JSON.parse(raw) as MeResponse
    } catch {
      return null
    }
  }

  function persistUser(u: MeResponse | null) {
    if (u) sessionStorage.setItem(KEY_USER, JSON.stringify(u))
    else sessionStorage.removeItem(KEY_USER)
  }

  // ── computed ──
  const role = computed<Role>(() => (user.value?.role as Role) ?? 'user')
  const permissions = computed<string[]>(() => user.value?.permissions ?? [])
  const isAdmin = computed(() => ['admin', 'superadmin'].includes(role.value))
  const isSuper = computed(() => role.value === 'superadmin')

  // ── actions ──

  /** 登录 → 拿 token + 校验 admin 权限 → 写入 storage. */
  async function login(email: string, password: string) {
    const res = await api.login(email, password)
    // 角色校验: 必须是后台角色 (非 user) 才能登入
    if (!res.user?.role || res.user.role === 'user') {
      throw new Error('当前账号无后台访问权限,请联系超级管理员')
    }
    setAccessToken(res.access_token)
    sessionStorage.setItem(KEY_REFRESH, res.refresh_token)
    refreshToken.value = res.refresh_token
    user.value = res.user
    persistUser(res.user)
  }

  function setAccessToken(t: string) {
    token.value = t
    sessionStorage.setItem(KEY_ACCESS, t)
  }

  /** 刷新 user info — token refresh 后调一次, 拿最新 role/permissions. */
  async function refreshMe() {
    try {
      const me = await api.fetchMe()
      user.value = me
      persistUser(me)
    } catch (e) {
      console.warn('refresh me failed:', errorMessage(e))
    }
  }

  /** 退出登录 — 清 storage + 撤后端 refresh token (best-effort). */
  function signOut() {
    if (refreshToken.value) {
      // 发后端 logout, 不等结果
      api.logout(refreshToken.value).catch(() => {
        /* 忽略, 反正前端要清 */
      })
    }
    token.value = ''
    refreshToken.value = ''
    user.value = null
    sessionStorage.removeItem(KEY_ACCESS)
    sessionStorage.removeItem(KEY_REFRESH)
    sessionStorage.removeItem(KEY_USER)
  }

  return {
    token,
    refreshToken,
    user,
    role,
    permissions,
    isAdmin,
    isSuper,
    login,
    setAccessToken,
    refreshMe,
    signOut,
  }
})
