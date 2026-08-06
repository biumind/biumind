// 路由 + 三层守卫:
//   1. 登录守卫: 没 token 跳 /login (除 public 路由)
//   2. 权限守卫: meta.perm 不通过跳 /403
//   3. 角色守卫: meta.roles 不在列表跳 /403
//
// usePermission 内部跟后端 RBAC 通配规则对齐.

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePermission } from '@/composables/usePermission'
import type { Role } from '@/api/types'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    title?: string
    perm?: string
    roles?: Role[]
    icon?: string
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/auth/LoginView.vue'),
    meta: { public: true, title: '登录' },
  },
  {
    path: '/403',
    name: 'forbidden',
    component: () => import('@/views/ForbiddenView.vue'),
    meta: { public: true, title: '权限不足' },
  },
  {
    path: '/404',
    name: 'not-found',
    component: () => import('@/views/NotFoundView.vue'),
    meta: { public: true, title: '页面未找到' },
  },
  {
    path: '/',
    component: () => import('@/layouts/AdminLayout.vue'),
    children: [
      {
        path: '',
        name: 'dashboard',
        component: () => import('@/views/DashboardView.vue'),
        meta: { title: '仪表盘', icon: 'House' },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('@/views/users/UserListView.vue'),
        meta: { title: '用户管理', icon: 'User', perm: 'users:read' },
      },
      {
        path: 'users/:id',
        name: 'user-detail',
        component: () => import('@/views/users/UserDetailView.vue'),
        meta: { title: '用户详情', perm: 'users:read' },
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('@/views/AuditView.vue'),
        meta: { title: '审计日志', icon: 'Document', perm: 'audit:read' },
      },
      {
        path: 'monitor',
        name: 'monitor',
        component: () => import('@/views/MonitorView.vue'),
        meta: { title: '服务监控', icon: 'Monitor', perm: 'monitor:read' },
      },
      {
        path: 'usage',
        name: 'usage',
        component: () => import('@/views/UsageView.vue'),
        meta: { title: '业务用量', icon: 'TrendCharts', perm: 'monitor:read' },
      },
      {
        path: 'system',
        name: 'system',
        component: () => import('@/views/SystemConfigView.vue'),
        meta: { title: '系统配置', icon: 'Setting', perm: 'system:read' },
      },
      {
        path: 'rbac',
        name: 'rbac',
        component: () => import('@/views/RBACView.vue'),
        meta: { title: '角色权限', icon: 'Lock', perm: 'roles:read' },
      },
      {
        // 公告管理 — 后端 requireAdmin(admin / superadmin), 故按 role 收口.
        path: 'announcements',
        name: 'announcements',
        component: () => import('@/views/AnnouncementsView.vue'),
        meta: { title: '公告管理', icon: 'Bell', roles: ['admin', 'superadmin'] },
      },

      // ─── 模型管理（model_relay admin）────────────────────
      // 所有页面 perm=models:read（写权限在页面内按钮粒度收紧）。
      // 路径前缀 /models/* 与 model-relay 的 /v1/admin/models 对齐。
      {
        path: 'models',
        name: 'mr-models',
        component: () => import('@/views/models/ModelsView.vue'),
        meta: { title: '模型', icon: 'MagicStick', perm: 'models:read' },
      },
      {
        path: 'models/channels',
        name: 'mr-channels',
        component: () => import('@/views/models/ChannelsView.vue'),
        meta: { title: '渠道', icon: 'Connection', perm: 'models:read' },
      },
      {
        path: 'models/credentials',
        name: 'mr-credentials',
        component: () => import('@/views/models/CredentialsView.vue'),
        meta: { title: '凭证', icon: 'Key', perm: 'model_credentials:read' },
      },
      {
        path: 'models/providers',
        name: 'mr-providers',
        component: () => import('@/views/models/ProvidersView.vue'),
        meta: { title: '供应商', icon: 'Discount', perm: 'models:read' },
      },
      {
        path: 'models/fx-rates',
        name: 'mr-fx-rates',
        component: () => import('@/views/models/FxRatesView.vue'),
        meta: { title: '汇率', icon: 'Coin', perm: 'models:read' },
      },
      {
        path: 'profile/sessions',
        name: 'my-sessions',
        component: () => import('@/views/MySessionsView.vue'),
        // 任何登录用户都能管自己的设备 — 不挂 perm
        meta: { title: '已登录设备', icon: 'Monitor' },
      },
    ],
  },
  // catch-all
  { path: '/:pathMatch(.*)*', redirect: '/404' },
]

const router = createRouter({
  history: createWebHistory('/admin/'),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()

  // public 路由直通
  if (to.meta.public) return true

  // 没 token 跳 login (附带 redirect 让登录后回这里)
  if (!auth.token) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }

  // user 信息丢失 (storage 被清但 token 还在) → 拉一次 me
  if (!auth.user) {
    await auth.refreshMe()
    if (!auth.user) {
      auth.signOut()
      return { name: 'login' }
    }
  }

  // 权限/角色 守卫
  const { can, isRole } = usePermission()
  if (to.meta.perm && !can(to.meta.perm)) {
    return { name: 'forbidden', query: { perm: to.meta.perm } }
  }
  if (to.meta.roles && !isRole(...to.meta.roles)) {
    return { name: 'forbidden', query: { roles: to.meta.roles.join(',') } }
  }

  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · BiuMind Admin` : 'BiuMind Admin'
})

export default router
