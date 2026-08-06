<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import type { AdminUser, AuditEvent } from '@/api/types'
import type { AuditSummary } from '@/api/admin'
import { ROLE_LABELS, ROLE_TAG_TYPES, PLAN_LABELS } from '@/api/types'
import { usePermission } from '@/composables/usePermission'

const router = useRouter()
const { can } = usePermission()

const totalUsers = ref<number>(0)
const recentUsers = ref<AdminUser[]>([])
const recentAudit = ref<AuditEvent[]>([])
const summary = ref<AuditSummary | null>(null)
const loading = ref(false)
const errMsg = ref('')

onMounted(load)

async function load() {
  loading.value = true
  errMsg.value = ''
  try {
    const tasks: Promise<unknown>[] = []
    if (can('users:read')) {
      tasks.push(
        api.listUsers({ limit: 5 }).then((p) => {
          totalUsers.value = p.total
          recentUsers.value = p.users ?? []
        }),
      )
    }
    if (can('audit:read')) {
      tasks.push(
        api.listAudit(5).then((p) => {
          recentAudit.value = p.events ?? []
        }),
        // 卡片数据 — 后端 1 次 SQL 聚合, 失败不阻塞 (audit 卡片缺数据 < dashboard 整页挂)
        api.getAuditSummary('24h').then((s) => {
          summary.value = s
        }).catch(() => {/* leave summary null */}),
      )
    }
    await Promise.all(tasks)
  } catch (e) {
    errMsg.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

// 跳到 audit 页 + 预过滤 action.
function jumpAudit(actionFilter?: string) {
  router.push({
    path: '/audit',
    query: actionFilter ? { action: actionFilter } : undefined,
  })
}

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}
</script>

<template>
  <div class="dashboard">
    <h1>仪表盘</h1>

    <el-alert v-if="errMsg" :title="errMsg" type="error" show-icon style="margin-bottom: 16px" />

    <el-row :gutter="16" class="kpi-row">
      <el-col v-if="can('users:read')" :span="6">
        <el-card class="kpi-card" shadow="never" @click="router.push('/users')">
          <div class="kpi-label">用户总数</div>
          <div class="kpi-value">{{ totalUsers }}</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card class="kpi-card" shadow="never">
          <div class="kpi-label">服务状态</div>
          <div class="kpi-value">
            <el-tag type="success" size="small">运行中</el-tag>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 安全态势 — 24h 滚动窗口 audit 聚合, 点击下钻 -->
    <el-row v-if="summary && can('audit:read')" :gutter="16" class="kpi-row">
      <el-col :span="6">
        <el-card
          class="kpi-card"
          shadow="never"
          :class="{ 'kpi-warn': summary.failed_logins > 0 }"
          @click="jumpAudit('auth.login.failed')"
        >
          <div class="kpi-label">24h 登录失败</div>
          <div class="kpi-value">{{ summary.failed_logins }}</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card
          class="kpi-card"
          shadow="never"
          :class="{ 'kpi-danger': summary.brute_force_hits > 0 }"
          @click="jumpAudit('auth.login.brute_force')"
        >
          <div class="kpi-label">24h 暴力破解</div>
          <div class="kpi-value">{{ summary.brute_force_hits }}</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card
          class="kpi-card"
          shadow="never"
          :class="{ 'kpi-warn': summary.role_changes > 0 }"
          @click="jumpAudit()"
        >
          <div class="kpi-label">24h 角色变更</div>
          <div class="kpi-value">{{ summary.role_changes }}</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card
          class="kpi-card"
          shadow="never"
          :class="{ 'kpi-danger': summary.total_failures > 0 }"
          @click="jumpAudit()"
        >
          <div class="kpi-label">24h 总失败事件</div>
          <div class="kpi-value">{{ summary.total_failures }}</div>
          <div class="kpi-foot">总事件 {{ summary.total_events }}</div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16">
      <el-col v-if="can('users:read')" :span="12">
        <el-card shadow="never">
          <template #header>
            <span>最近注册</span>
            <el-link
              type="primary"
              :underline="false"
              style="float: right"
              @click="router.push('/users')"
            >
              全部 →
            </el-link>
          </template>
          <el-table :data="recentUsers" :show-header="false" v-loading="loading">
            <el-table-column prop="email" />
            <el-table-column width="100">
              <template #default="{ row }: { row: AdminUser }">
                <el-tag size="small" effect="plain">{{ PLAN_LABELS[row.plan] ?? row.plan }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column width="100">
              <template #default="{ row }: { row: AdminUser }">
                <el-tag size="small" :type="ROLE_TAG_TYPES[row.role] ?? ''">
                  {{ ROLE_LABELS[row.role] ?? row.role }}
                </el-tag>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>

      <el-col v-if="can('audit:read')" :span="12">
        <el-card shadow="never">
          <template #header>
            <span>最近审计</span>
            <el-link
              type="primary"
              :underline="false"
              style="float: right"
              @click="router.push('/audit')"
            >
              全部 →
            </el-link>
          </template>
          <el-table :data="recentAudit" :show-header="false" v-loading="loading">
            <el-table-column>
              <template #default="{ row }">
                <span class="text-mono">{{ fmtTime(row.at) }}</span>
                <span style="margin-left: 8px">{{ row.action }}</span>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<style scoped lang="scss">
.dashboard h1 { margin: 0 0 24px; font-size: 24px; font-weight: 600; }
.kpi-row { margin-bottom: 16px; }
.kpi-card {
  cursor: pointer;
  transition: box-shadow .15s ease;
  .kpi-label { color: #6b7280; font-size: 13px; margin-bottom: 8px; }
  .kpi-value { font-size: 28px; font-weight: 600; color: #111827; }
  .kpi-foot  { color: #9ca3af; font-size: 12px; margin-top: 6px; }
  &:hover { box-shadow: 0 1px 3px rgba(0,0,0,0.08); }
  &.kpi-warn   .kpi-value { color: #d97706; }
  &.kpi-danger .kpi-value { color: #dc2626; }
}
</style>
