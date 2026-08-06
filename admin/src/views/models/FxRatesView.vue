<script setup lang="ts">
// FxRates — 双向汇率手填 + "距上次更新 N 天" banner。
// MVP 用 manual source；P2 接入定时拉取（cron source）。
import { ref, onMounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { errorMessage } from '@/api/http'
import * as api from '@/api/modelRelay'
import type { FxRate, Currency, FxRateUpsert } from '@/api/modelRelay.types'
import { useModelRelayStore } from '@/stores/modelRelay'
import { usePermission } from '@/composables/usePermission'

const store = useModelRelayStore()
const { can } = usePermission()
const canEdit = can('fx_rates:write')

const rates = ref<FxRate[]>([])
const stalest = ref<FxRate | undefined>()
const stalestAge = ref<number>(0)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const env = await api.listFxRates()
    rates.value = env.items
    stalest.value = env.stalest
    stalestAge.value = env.stalest_age_seconds ?? 0
    // 同步进 store（保持其它页面读到最新值）
    await store.refreshFxRates(true)
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

const editRow = ref<FxRate | null>(null)
const editRate = ref(0)
const editVisible = computed({
  get: () => editRow.value !== null,
  set: (v: boolean) => { if (!v) editRow.value = null },
})

function startEdit(row: FxRate) {
  if (!canEdit) return
  if (row.from_currency === row.to_currency) {
    ElMessage.info('自反汇率（USD→USD / CNY→CNY）固定为 1.0')
    return
  }
  editRow.value = row
  editRate.value = row.rate
}

async function saveEdit() {
  if (!editRow.value) return
  if (editRate.value <= 0) {
    ElMessage.warning('汇率必须大于 0')
    return
  }
  try {
    await api.setFxRate({
      from_currency: editRow.value.from_currency,
      to_currency: editRow.value.to_currency,
      rate: editRate.value,
      source: 'manual',
    } satisfies FxRateUpsert)
    ElMessage.success('保存成功')
    editRow.value = null
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

function cancelEdit() {
  editRow.value = null
}

const ageDays = computed(() => Math.floor(stalestAge.value / 86_400))
const isStale = computed(() => ageDays.value >= 14)

function fmtTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

function fmtRate(n: number) {
  return n.toFixed(6)
}

async function recomputeReverse(row: FxRate) {
  // Convenience: when admin updates USD→CNY, offer to set CNY→USD = 1/rate.
  const reverse = rates.value.find(
    (r) => r.from_currency === row.to_currency && r.to_currency === row.from_currency,
  )
  if (!reverse || row.from_currency === row.to_currency) return
  const proposed = 1 / row.rate
  await ElMessageBox.confirm(
    `是否同步设置 ${row.to_currency} → ${row.from_currency} = ${fmtRate(proposed)}？`,
    '同步反向汇率',
    { confirmButtonText: '同步', cancelButtonText: '不同步', type: 'info' },
  )
    .then(async () => {
      try {
        await api.setFxRate({
          from_currency: reverse.from_currency,
          to_currency: reverse.to_currency,
          rate: proposed,
          source: 'manual',
        })
        ElMessage.success('反向汇率已同步')
        await load()
      } catch (e) {
        ElMessage.error(errorMessage(e))
      }
    })
    .catch(() => {})
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1>汇率管理</h1>
      <span class="hint">用于 USD ↔ CNY 双币种结算与显示，模型定价按原币种存储。</span>
    </div>

    <el-alert
      v-if="isStale"
      type="warning"
      show-icon
      :closable="false"
      :title="`汇率已 ${ageDays} 天未更新（最旧记录: ${stalest?.from_currency}→${stalest?.to_currency}），建议核对央行/Stripe 当前牌价`"
      style="margin-bottom: 16px"
    />

    <el-card shadow="never">
      <el-table :data="rates" v-loading="loading" stripe>
        <el-table-column label="源币种" width="120">
          <template #default="{ row }: { row: FxRate }">
            <el-tag size="small">{{ row.from_currency }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="目标币种" width="120">
          <template #default="{ row }: { row: FxRate }">
            <el-tag size="small" effect="plain">{{ row.to_currency }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="汇率" min-width="180">
          <template #default="{ row }: { row: FxRate }">
            <code class="rate">{{ fmtRate(row.rate) }}</code>
            <span v-if="row.from_currency === row.to_currency" class="muted"> （自反固定 1.0）</span>
          </template>
        </el-table-column>
        <el-table-column label="来源" width="120">
          <template #default="{ row }: { row: FxRate }">
            <el-tag size="small" :type="row.source === 'cron' ? 'success' : 'info'">
              {{ row.source === 'cron' ? '定时拉取' : '人工录入' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="更新时间" width="180">
          <template #default="{ row }: { row: FxRate }">
            <span class="muted">{{ fmtTime(row.updated_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120" v-if="canEdit">
          <template #default="{ row }: { row: FxRate }">
            <el-button
              v-if="row.from_currency !== row.to_currency"
              link
              type="primary"
              @click="startEdit(row)"
            >编辑</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Edit dialog -->
    <el-dialog
      v-model="editVisible"
      :title="editRow ? `编辑 ${editRow.from_currency} → ${editRow.to_currency}` : ''"
      width="420px"
      :close-on-click-modal="false"
      @close="cancelEdit"
    >
      <el-form label-width="80px" v-if="editRow">
        <el-form-item label="汇率">
          <el-input-number
            v-model="editRate"
            :min="0.000001"
            :precision="6"
            :step="0.001"
            controls-position="right"
            style="width: 240px"
          />
          <div class="muted" style="margin-top: 6px">
            含义：1 {{ editRow.from_currency }} = {{ editRate }} {{ editRow.to_currency }}
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cancelEdit">取消</el-button>
        <el-button type="primary" @click="async () => { const r = editRow; await saveEdit(); if (r) await recomputeReverse({ ...r, rate: editRate }) }">
          保存
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.page-header {
  display: flex;
  align-items: baseline;
  gap: 16px;
  margin-bottom: 16px;
  h1 { margin: 0; font-size: 22px; font-weight: 600; }
  .hint { color: #6b7280; font-size: 13px; }
}
.rate {
  font-variant-numeric: tabular-nums;
  background: #f3f4f6;
  padding: 2px 8px;
  border-radius: 4px;
  font-weight: 500;
}
.muted { color: #6b7280; font-size: 13px; }
</style>
