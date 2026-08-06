<script setup lang="ts">
import { computed } from 'vue'
import { useRouter, RouterView } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePermission } from '@/composables/usePermission'
import { ROLE_LABELS, ROLE_TAG_TYPES, type Role } from '@/api/types'

const router = useRouter()
const auth = useAuthStore()
const { can, isRole } = usePermission()

interface MenuItem {
  title: string
  path: string
  icon: string
  perm?: string
  roles?: Role[]
  children?: MenuItem[]
}

const allMenus: MenuItem[] = [
  { title: '仪表盘', path: '/', icon: 'House' },
  { title: '用户管理', path: '/users', icon: 'User', perm: 'users:read' },
  { title: '审计日志', path: '/audit', icon: 'Document', perm: 'audit:read' },
  { title: '服务监控', path: '/monitor', icon: 'Monitor', perm: 'monitor:read' },
  { title: '业务用量', path: '/usage', icon: 'TrendCharts', perm: 'monitor:read' },
  {
    // 模型管理子菜单 — 5 个 model_relay 后台页面收纳到一个 sub-menu，
    // 避免与 7 个主菜单平铺造成侧边栏过长。
    title: '模型管理',
    path: '/models',
    icon: 'Cpu',
    perm: 'models:read',
    children: [
      { title: '模型', path: '/models', icon: 'MagicStick', perm: 'models:read' },
      { title: '渠道', path: '/models/channels', icon: 'Connection', perm: 'models:read' },
      { title: '凭证', path: '/models/credentials', icon: 'Key', perm: 'model_credentials:read' },
      { title: '供应商', path: '/models/providers', icon: 'Discount', perm: 'models:read' },
      { title: '汇率', path: '/models/fx-rates', icon: 'Coin', perm: 'models:read' },
    ],
  },
  { title: '系统配置', path: '/system', icon: 'Setting', perm: 'system:read' },
  { title: '角色权限', path: '/rbac', icon: 'Lock', perm: 'roles:read' },
  { title: '公告管理', path: '/announcements', icon: 'Bell', roles: ['admin', 'superadmin'] },
]

function visible(m: MenuItem): boolean {
  if (m.perm && !can(m.perm)) return false
  if (m.roles && !isRole(...m.roles)) return false
  return true
}

const visibleMenus = computed(() =>
  allMenus
    .filter(visible)
    .map((m) => ({
      ...m,
      children: m.children?.filter(visible),
    }))
    .filter((m) => !m.children || m.children.length > 0),
)

function logout() {
  auth.signOut()
  router.push({ name: 'login' })
}

function onMenuCommand(cmd: string) {
  if (cmd === 'logout') logout()
  else if (cmd === 'sessions') router.push({ name: 'my-sessions' })
}
</script>

<template>
  <el-container class="admin-layout">
    <el-aside width="220px" class="aside">
      <div class="brand">
        <span class="brand-mark">B</span>
        <span class="brand-text">BiuMind Admin</span>
      </div>
      <el-menu
        :default-active="$route.path"
        router
        background-color="#1f2937"
        text-color="#cbd5e1"
        active-text-color="#60a5fa"
      >
        <template v-for="m in visibleMenus" :key="m.path">
          <el-sub-menu v-if="m.children && m.children.length" :index="m.path">
            <template #title>
              <el-icon><component :is="m.icon" /></el-icon>
              <span>{{ m.title }}</span>
            </template>
            <el-menu-item v-for="c in m.children" :key="c.path" :index="c.path">
              <el-icon><component :is="c.icon" /></el-icon>
              <span>{{ c.title }}</span>
            </el-menu-item>
          </el-sub-menu>
          <el-menu-item v-else :index="m.path">
            <el-icon><component :is="m.icon" /></el-icon>
            <span>{{ m.title }}</span>
          </el-menu-item>
        </template>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="header">
        <div class="header-spacer"></div>
        <el-dropdown @command="onMenuCommand">
          <span class="user-info">
            <el-tag size="small" :type="ROLE_TAG_TYPES[auth.role]" effect="plain">
              {{ ROLE_LABELS[auth.role] }}
            </el-tag>
            <span class="email">{{ auth.user?.email ?? '—' }}</span>
            <el-icon><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="sessions">
                <el-icon><Monitor /></el-icon>
                我的设备
              </el-dropdown-item>
              <el-dropdown-item command="logout" divided>
                <el-icon><SwitchButton /></el-icon>
                {{ $t('app.logout') }}
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </el-header>

      <el-main class="main">
        <RouterView v-slot="{ Component }">
          <transition name="fade" mode="out-in">
            <component :is="Component" />
          </transition>
        </RouterView>
      </el-main>
    </el-container>
  </el-container>
</template>

<style scoped lang="scss">
.admin-layout { height: 100vh; }

.aside {
  background: #1f2937;
  color: #fff;
  border-right: 1px solid #111827;
  display: flex;
  flex-direction: column;
}

.brand {
  height: 64px;
  display: flex;
  align-items: center;
  padding: 0 20px;
  gap: 12px;
  border-bottom: 1px solid #111827;
  .brand-mark {
    width: 32px; height: 32px; border-radius: 8px;
    background: linear-gradient(135deg, #3b82f6 0%, #1e40af 100%);
    color: #fff; display: inline-flex; align-items: center; justify-content: center;
    font-weight: 700; font-size: 18px;
  }
  .brand-text { font-weight: 600; font-size: 15px; }
}

:deep(.el-menu) { border-right: none; }
:deep(.el-menu-item.is-active) {
  background-color: rgba(96, 165, 250, 0.1) !important;
}

.header {
  height: 56px;
  background: #fff;
  display: flex;
  align-items: center;
  padding: 0 24px;
  border-bottom: 1px solid #e5e7eb;
}
.header-spacer { flex: 1; }

.user-info {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-size: 13px;
  .email { color: #4b5563; }
}

.main { padding: 24px; background: #f5f7fa; overflow: auto; }
</style>
