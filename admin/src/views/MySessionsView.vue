<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Monitor,
  Cellphone,
  Platform,
  Iphone,
  Refresh,
  WarnTriangleFilled,
} from '@element-plus/icons-vue'
import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import type { MySession } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const loading = ref(false)
const sessions = ref<MySession[]>([])

const hasOthers = computed(() => sessions.value.some((s) => !s.is_current))

onMounted(load)

async function load() {
  loading.value = true
  try {
    const list = await api.listMySessions()
    // current first, then by last_used_at / created_at desc
    list.sort((a, b) => {
      if (a.is_current !== b.is_current) return a.is_current ? -1 : 1
      const at = a.last_used_at ?? a.created_at
      const bt = b.last_used_at ?? b.created_at
      return new Date(bt).getTime() - new Date(at).getTime()
    })
    sessions.value = list
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function revoke(s: MySession) {
  const isSelf = s.is_current
  try {
    await ElMessageBox.confirm(
      isSelf
        ? '退出此设备将清除本机登录态。'
        : '撤销该设备的授权？该设备将无法继续使用您的账号，需要重新登录。',
      isSelf ? '退出此设备' : '撤销授权',
      {
        type: 'warning',
        confirmButtonText: isSelf ? '退出' : '撤销',
        cancelButtonText: '取消',
        confirmButtonClass: 'el-button--danger',
      },
    )
  } catch {
    return
  }
  try {
    const wasSelf = await api.revokeMySession(s.id)
    if (wasSelf || s.is_current) {
      // 撤了当前 session — 让 auth store 清 token + 跳登录
      ElMessage.success('已退出此设备')
      auth.signOut()
    } else {
      ElMessage.success('已撤销该设备授权')
      await load()
    }
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

async function kickOthers() {
  try {
    await ElMessageBox.confirm(
      '除当前设备外，立即注销所有授权。被踢出的设备需要重新登录。',
      '踢出所有其他设备',
      {
        type: 'warning',
        confirmButtonText: '确认踢出',
        cancelButtonText: '取消',
        confirmButtonClass: 'el-button--danger',
      },
    )
  } catch {
    return
  }
  try {
    const n = await api.revokeOtherSessions()
    ElMessage.success(`已踢出 ${n} 台设备`)
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

function iconFor(kind: MySession['device_kind']) {
  switch (kind) {
    case 'mobile':
      return Cellphone
    case 'browser':
      return Platform
    case 'desktop':
      return Monitor
    default:
      return Iphone
  }
}

function humanLastUsed(iso?: string): string {
  if (!iso) return '未知'
  const t = new Date(iso).getTime()
  const delta = Date.now() - t
  if (delta < 60_000) return '刚刚'
  if (delta < 3_600_000) return `${Math.floor(delta / 60_000)} 分钟前`
  if (delta < 86_400_000) return `${Math.floor(delta / 3_600_000)} 小时前`
  if (delta < 30 * 86_400_000) return `${Math.floor(delta / 86_400_000)} 天前`
  return `${Math.floor(delta / (30 * 86_400_000))} 个月前`
}

function fmtDate(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
</script>

<template>
  <div class="page">
    <div class="header">
      <div>
        <h1>已登录设备</h1>
        <p class="subtitle">
          查看并管理已登录到您账号的所有设备。撤销授权后该设备需重新登录。
        </p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </div>

    <div v-loading="loading" class="cards">
      <div
        v-for="s in sessions"
        :key="s.id"
        class="card"
        :class="{ 'card-current': s.is_current }"
      >
        <div class="icon">
          <el-icon :size="20"><component :is="iconFor(s.device_kind)" /></el-icon>
        </div>
        <div class="body">
          <div class="title-row">
            <span class="title">{{ s.device_name }}</span>
            <el-tag v-if="s.is_current" type="primary" size="small" effect="dark">
              当前设备
            </el-tag>
          </div>
          <div class="desc">已授权该设备访问您的 BiuMind 账号</div>
          <div class="meta">
            最近活跃：{{ humanLastUsed(s.last_used_at) }}
            <span v-if="s.last_ip"> · {{ s.last_ip }}</span>
          </div>
          <div class="meta">
            生效期：{{ s.ttl_days || '?' }} 天；本次授权将于
            <strong>{{ fmtDate(s.expires_at) }}</strong> 到期
          </div>
        </div>
        <div class="action">
          <el-button type="danger" link @click="revoke(s)">
            {{ s.is_current ? '退出' : '撤销' }}
          </el-button>
        </div>
      </div>

      <div v-if="!loading && sessions.length === 1" class="empty-others">
        <el-icon :size="20"><Cellphone /></el-icon>
        <div>
          <div class="empty-title">还没有其他设备登录</div>
          <div class="empty-sub">
            在手机上下载 BiuMind 客户端，登录同一账号即可同步。
          </div>
        </div>
      </div>

      <div v-if="hasOthers" class="kick-all">
        <el-icon :size="18" color="#dc2626"><WarnTriangleFilled /></el-icon>
        <div class="kick-text">
          <strong>踢出所有其他设备</strong>
          <div>除当前设备外，立即注销所有授权。被踢出的设备需要重新登录。</div>
        </div>
        <el-button type="danger" plain :loading="loading" @click="kickOthers">
          一键踢出
        </el-button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.page {
  padding: 0;
}
.header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 24px;
  h1 {
    margin: 0 0 4px;
    font-size: 24px;
    font-weight: 600;
  }
  .subtitle {
    margin: 0;
    font-size: 13px;
    color: #6b7280;
  }
}
.cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.card {
  display: flex;
  gap: 16px;
  padding: 16px;
  background: #fff;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  align-items: flex-start;
  &.card-current {
    border-color: rgba(99, 102, 241, 0.4);
    background: #fafbff;
  }
  .icon {
    width: 40px;
    height: 40px;
    border-radius: 6px;
    background: #f3f4f6;
    color: #6b7280;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }
  .body {
    flex: 1;
    min-width: 0;
  }
  .title-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 4px;
  }
  .title {
    font-size: 15px;
    font-weight: 600;
    color: #111827;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .desc {
    font-size: 12px;
    color: #6b7280;
    margin-bottom: 6px;
  }
  .meta {
    font-size: 12px;
    color: #9ca3af;
    line-height: 1.6;
  }
  .action {
    flex-shrink: 0;
  }
}
.empty-others {
  display: flex;
  gap: 12px;
  padding: 16px;
  background: #f9fafb;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  color: #9ca3af;
  align-items: center;
  .empty-title {
    font-size: 13px;
    color: #4b5563;
    font-weight: 500;
  }
  .empty-sub {
    font-size: 12px;
    color: #9ca3af;
    margin-top: 2px;
  }
}
.kick-all {
  display: flex;
  gap: 12px;
  padding: 16px;
  background: #fef2f2;
  border: 1px solid rgba(220, 38, 38, 0.2);
  border-radius: 8px;
  align-items: center;
  .kick-text {
    flex: 1;
    font-size: 13px;
    color: #4b5563;
    strong {
      color: #111827;
      display: block;
      margin-bottom: 2px;
    }
  }
}
</style>
