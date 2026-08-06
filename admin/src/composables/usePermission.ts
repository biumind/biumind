// 权限判断 helper. 跟后端 packages/go-sdk/biu/auth/rbac.go HasPermission
// 通配规则一致: '*' / 'users:*' / 'users:read:*' / 精确字符串.

import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import type { Role } from '@/api/types'

export function usePermission() {
  const auth = useAuthStore()

  const perms = computed(() => auth.permissions)

  /** 单条权限检查, 支持 *, users:*, users:read:* 通配. */
  function can(perm: string): boolean {
    const p = perms.value
    if (p.includes('*')) return true
    if (p.includes(perm)) return true
    const parts = perm.split(':')
    for (let i = parts.length - 1; i >= 1; i--) {
      const wildcard = parts.slice(0, i).join(':') + ':*'
      if (p.includes(wildcard)) return true
    }
    return false
  }

  function canAny(...permList: string[]): boolean {
    return permList.some(can)
  }

  function canAll(...permList: string[]): boolean {
    return permList.every(can)
  }

  function isRole(...roles: Role[]): boolean {
    return roles.includes(auth.role)
  }

  function isSuper(): boolean {
    return auth.role === 'superadmin'
  }

  return { can, canAny, canAll, isRole, isSuper, role: auth.role, perms }
}
