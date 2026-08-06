<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import type { AuditEvent } from '@/api/types'

const route = useRoute()
const events = ref<AuditEvent[]>([])
const loading = ref(false)

// 过滤. 支持从 ?action=xxx 进来 (dashboard 卡片点击下钻).
const filterAction = ref(typeof route.query.action === 'string' ? route.query.action : '')
const filterActor = ref('')
const filterStatus = ref<'all' | 'success' | 'failed'>('all')
const limit = ref<number>(200)

async function load() {
  loading.value = true
  try {
    const r = await api.listAudit(limit.value)
    events.value = r.events ?? []
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

function fmtTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

const filtered = computed(() => {
  let out = events.value
  if (filterStatus.value === 'success') out = out.filter((e) => e.success)
  else if (filterStatus.value === 'failed') out = out.filter((e) => !e.success)
  if (filterAction.value) {
    const q = filterAction.value.toLowerCase()
    out = out.filter((e) => e.action.toLowerCase().includes(q))
  }
  if (filterActor.value) {
    const q = filterActor.value.toLowerCase()
    out = out.filter(
      (e) =>
        (e.actor_email || '').toLowerCase().includes(q) ||
        (e.actor_id || '').toLowerCase().includes(q) ||
        (e.actor_ip || '').includes(q),
    )
  }
  return out
})

const failureCount = computed(() => events.value.filter((e) => !e.success).length)

// 行高亮: 失败 / 高危动作
function rowClass({ row }: { row: AuditEvent }) {
  if (!row.success) return 'row-failed'
  if (row.action.endsWith('.brute_force')) return 'row-failed'
  if (row.action.startsWith('user.role.change') && !row.action.endsWith('.noop')) return 'row-warn'
  if (row.action === 'user.sessions.revoke') return 'row-warn'
  return ''
}

// 动作 → 颜色
function actionTagType(action: string): 'success' | 'warning' | 'danger' | 'info' | 'primary' {
  if (action.startsWith('auth.login.failed') || action.endsWith('.brute_force')) return 'danger'
  if (action.startsWith('auth.login')) return 'success'
  if (action.startsWith('user.role')) return 'warning'
  if (action.startsWith('user.sessions')) return 'warning'
  if (action.startsWith('system.config')) return 'primary'
  if (action.startsWith('alert.')) return 'info'
  return 'info'
}

function copyText(s: string) {
  navigator.clipboard?.writeText(s).then(
    () => ElMessage.success('已复制'),
    () => {},
  )
}

onMounted(load)
</script>

<template>
  <div class="audit">
    <div class="page-header">
      <h1>审计日志</h1>
      <div class="toolbar">
        <el-input
          v-model="filterAction"
          placeholder="按动作过滤 (e.g. login)"
          clearable
          style="width: 200px"
        />
        <el-input
          v-model="filterActor"
          placeholder="按操作人/IP 过滤"
          clearable
          style="width: 220px"
        />
        <el-radio-group v-model="filterStatus" size="default">
          <el-radio-button value="all">全部</el-radio-button>
          <el-radio-button value="success">成功</el-radio-button>
          <el-radio-button value="failed">失败</el-radio-button>
        </el-radio-group>
        <el-select v-model="limit" style="width: 100px" @change="load">
          <el-option :value="100" label="100" />
          <el-option :value="200" label="200" />
          <el-option :value="500" label="500" />
          <el-option :value="1000" label="1000" />
        </el-select>
        <el-button :loading="loading" type="primary" @click="load">
          <el-icon><Refresh /></el-icon>
          刷新
        </el-button>
      </div>
    </div>

    <div class="summary">
      <span>共 {{ events.length }} 条</span>
      <span v-if="failureCount > 0" class="failure-count">
        · <el-tag type="danger" size="small" effect="dark">{{ failureCount }} 失败</el-tag>
      </span>
      <span v-if="filtered.length !== events.length" class="text-muted">
        · 过滤后 {{ filtered.length }} 条
      </span>
    </div>

    <el-card shadow="never">
      <el-table
        :data="filtered"
        v-loading="loading"
        :row-class-name="rowClass"
        size="small"
        stripe
      >
        <el-table-column label="时间" width="170" fixed>
          <template #default="{ row }: { row: AuditEvent }">
            <span class="text-mono text-muted">{{ fmtTime(row.at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="结果" width="64">
          <template #default="{ row }: { row: AuditEvent }">
            <el-tag v-if="row.success" type="success" size="small" effect="plain">✓</el-tag>
            <el-tag v-else type="danger" size="small" effect="dark">✗</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="动作" width="200">
          <template #default="{ row }: { row: AuditEvent }">
            <el-tag :type="actionTagType(row.action)" size="small" effect="plain">
              {{ row.action }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作人" min-width="220">
          <template #default="{ row }: { row: AuditEvent }">
            <div class="actor">
              <span v-if="row.actor_email" class="email">{{ row.actor_email }}</span>
              <span v-else-if="row.actor_id" class="text-mono text-muted" :title="row.actor_id">
                {{ row.actor_id.slice(0, 8) }}…
              </span>
              <span v-else class="text-muted">匿名</span>
              <el-tag
                v-if="row.actor_role"
                size="small"
                effect="plain"
                style="margin-left: 4px"
              >
                {{ row.actor_role }}
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="IP" width="130">
          <template #default="{ row }: { row: AuditEvent }">
            <span
              v-if="row.actor_ip"
              class="text-mono"
              style="cursor: pointer"
              :title="`点击复制 · UA: ${row.actor_ua || '—'}`"
              @click="copyText(row.actor_ip)"
            >
              {{ row.actor_ip }}
            </span>
            <span v-else class="text-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="目标" width="240">
          <template #default="{ row }: { row: AuditEvent }">
            <span v-if="row.target" class="text-mono">
              {{ row.target.length > 20 ? row.target.slice(0, 20) + '…' : row.target }}
              <span v-if="row.target_type" class="text-muted">({{ row.target_type }})</span>
            </span>
            <span v-else class="text-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="详情" min-width="280">
          <template #default="{ row }: { row: AuditEvent }">
            <div v-if="!row.success" class="error-detail">
              <el-tag size="small" type="danger" effect="dark" v-if="row.error_code">
                {{ row.error_code }}
              </el-tag>
              <span v-if="row.error_message" class="error-msg">{{ row.error_message }}</span>
            </div>
            <span v-else-if="row.detail" class="text-muted">{{ row.detail }}</span>
            <span v-else class="text-muted">—</span>
          </template>
        </el-table-column>
      </el-table>
      <p v-if="filtered.length === 0 && !loading" class="text-muted" style="text-align: center; padding: 24px">
        {{ events.length === 0 ? '暂无审计数据' : '过滤无结果' }}
      </p>
    </el-card>
  </div>
</template>

<style scoped lang="scss">
.audit h1 { margin: 0; font-size: 24px; font-weight: 600; }

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
  flex-wrap: wrap;
  gap: 12px;
}
.toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.summary {
  margin-bottom: 8px;
  font-size: 13px;
  color: #6b7280;
  display: flex;
  gap: 6px;
  align-items: center;
}
.failure-count { display: inline-flex; align-items: center; gap: 4px; }

.actor {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  .email { font-size: 13px; }
}
.error-detail {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  .error-msg { color: #f56c6c; font-size: 12px; }
}

.text-mono { font-family: ui-monospace, SFMono-Regular, monospace; font-size: 12px; }
.text-muted { color: #9ca3af; }

:deep(.row-failed) { background-color: rgba(245, 108, 108, 0.06) !important; }
:deep(.row-failed:hover > td) { background-color: rgba(245, 108, 108, 0.12) !important; }
:deep(.row-warn) { background-color: rgba(230, 162, 60, 0.05) !important; }
:deep(.row-warn:hover > td) { background-color: rgba(230, 162, 60, 0.12) !important; }
</style>
