<script setup lang="ts">
// ModelsTable — P4 段 4 / F2.2 统一模型表格组件.
//
// 单一模型字典源 (model_relay.models), 通过 props.mode 切换 mode-aware 列:
//
//   chat / embedding              → 上下文 + 计价 (input/output per Mtok)
//   image_generation              → 计价 (cost_per_image), pricing_strategy 标签
//   video_generation              → 计价 (cost_per_video_second), dispatch_mode 标签
//   audio_speech / audio_transcription → 计价 (cost_per_audio_second)
//   digital_human                 → 计价 (cost_per_audio_second 或 cost_per_character)
//   hotparse                      → 计价 (cost_per_image, 按图片定价)
//
// 替代:
//   - ModelsView.vue 内嵌 chat 表 (旧)
//   - AigcModelsTable.vue (旧, 走 /v1/admin/aigc/* compat)
//
// 一律走 /v1/admin/models?mode=...&include_pricing=true (F2.1 后端).
// pricings 由后端一次 SQL 批量返回, 客户端不再 N+1.

import { ref, watch, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Search, Lock } from '@element-plus/icons-vue'
import { errorMessage } from '@/api/http'
import * as api from '@/api/modelRelay'
import type {
  Model,
  ModelMode,
  Pricing,
  Capabilities,
  Currency,
  PricingStrategy,
  DispatchMode,
} from '@/api/modelRelay.types'
import { useModelRelayStore } from '@/stores/modelRelay'
import { usePermission } from '@/composables/usePermission'

const props = defineProps<{
  /** 当前 mode 过滤; 单 mode (chat/image/...) 或 'all' 显示全部. */
  mode: ModelMode | 'all'
}>()

const emit = defineEmits<{
  (e: 'open-detail', model: Model): void
}>()

const store = useModelRelayStore()
const { can } = usePermission()
const canWrite = can('models:write')

const models = ref<Model[]>([])
const pricingByModelId = ref<Map<string, Pricing>>(new Map())
const channelStatsByModel = ref<Map<string, { active: number; total: number; auto_disabled: number }>>(new Map())
const loading = ref(false)
const syncing = ref(false)

const search = ref('')
const filterStatus = ref('')
const filterFamily = ref('')
const filterMinPlan = ref('')

// ─── load ─────────────────────────────────────────────────────────
async function load() {
  loading.value = true
  try {
    const modeParam = props.mode === 'all' ? undefined : props.mode
    const [m, channels] = await Promise.all([
      api.listModels({
        status: filterStatus.value || undefined,
        family: filterFamily.value || undefined,
        min_plan: filterMinPlan.value || undefined,
        q: search.value || undefined,
        mode: modeParam,
        include_pricing: true,
      }),
      api.listChannels(),
    ])
    models.value = m.items

    // pricings: backend 用 model.code 作 key (F2.1 设计)
    const pmap = new Map<string, Pricing>()
    if (m.pricings) {
      for (const mdl of m.items) {
        const p = m.pricings[mdl.code]
        if (p && p.id) pmap.set(mdl.id, p)
      }
    }
    pricingByModelId.value = pmap

    // Channel stats
    const stats = new Map<string, { active: number; total: number; auto_disabled: number }>()
    for (const c of channels.items) {
      const cur = stats.get(c.model_id) ?? { active: 0, total: 0, auto_disabled: 0 }
      cur.total++
      if (c.status === 'active') cur.active++
      if (c.status === 'auto_disabled') cur.auto_disabled++
      stats.set(c.model_id, cur)
    }
    channelStatsByModel.value = stats

    await store.refreshFxRates()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

let searchTimer: ReturnType<typeof setTimeout> | null = null
function onSearch() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(load, 300)
}

watch(() => props.mode, load)

// ─── sync upstream (chat only) ────────────────────────────────────
async function onSync() {
  if (syncing.value) return
  syncing.value = true
  try {
    const res = await api.syncUpstream()
    if (res.not_modified) {
      ElMessage.info('上游元数据未变化（ETag 命中）')
    } else {
      ElMessage.success(
        `同步完成：新增 ${res.added}、更新 ${res.updated}、跳过 ${res.skipped}（共 ${res.total}）`,
      )
      await load()
    }
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    syncing.value = false
  }
}

// ─── currency display ─────────────────────────────────────────────
function toggleCurrency() {
  store.setDisplayCurrency(store.displayCurrency === 'CNY' ? 'USD' : 'CNY')
}

function fmtMoney(amount: number, fromCurrency: Currency): string {
  const display = store.displayCurrency
  const v = store.convertCurrency(amount, fromCurrency, display)
  const sym = display === 'USD' ? '$' : '¥'
  return `${sym}${v.toFixed(v < 0.01 ? 4 : v < 1 ? 3 : 2)}`
}

// 不同 mode 的计价单元格
function fmtPriceCell(modelId: string, mode: string): string {
  const p = pricingByModelId.value.get(modelId)
  if (!p) return '—'
  switch (mode) {
    case 'chat':
    case 'embedding':
      return `${fmtMoney(p.input_per_mtok, p.currency)} / ${fmtMoney(p.output_per_mtok, p.currency)}`
    case 'image_generation':
    case 'hotparse': {
      if (p.cost_per_image == null) return '—'
      return `${fmtMoney(p.cost_per_image, p.currency)} / 张`
    }
    case 'video_generation': {
      if (p.cost_per_video_second == null) return '—'
      return `${fmtMoney(p.cost_per_video_second, p.currency)} / 秒`
    }
    case 'audio_speech':
    case 'audio_transcription': {
      if (p.cost_per_audio_second == null) return '—'
      return `${fmtMoney(p.cost_per_audio_second, p.currency)} / 秒`
    }
    case 'digital_human': {
      if (p.cost_per_audio_second != null) {
        return `${fmtMoney(p.cost_per_audio_second, p.currency)} / 秒`
      }
      if (p.cost_per_character != null) {
        return `${fmtMoney(p.cost_per_character, p.currency)} / 字`
      }
      return '—'
    }
    default:
      return '—'
  }
}

function priceTooltip(modelId: string, mode: string): string {
  const p = pricingByModelId.value.get(modelId)
  if (!p) return ''
  const which =
    mode === 'chat' || mode === 'embedding' ? '输入 / 输出（每百万 token）' :
    mode === 'image_generation' || mode === 'hotparse' ? '每张图' :
    mode === 'video_generation' ? '每视频秒' :
    mode === 'audio_speech' || mode === 'audio_transcription' ? '每音频秒' :
    mode === 'digital_human' ? '每秒 / 每字' :
    ''
  return `${which} · 原币种 ${p.currency}`
}

// ─── status / capability rendering ────────────────────────────────
const STATUS_LABEL: Record<string, string> = {
  active: '启用',
  disabled: '禁用',
  deprecated: '弃用',
  invalid: '无效',
  auto_disabled: '自动禁用',
  archived: '归档',
}
const STATUS_TYPE: Record<string, 'success' | 'info' | 'warning' | 'danger'> = {
  active: 'success',
  disabled: 'info',
  deprecated: 'warning',
  invalid: 'danger',
  auto_disabled: 'warning',
  archived: 'info',
}
const PLAN_LABEL: Record<string, string> = {
  free: '免费',
  pro: 'Pro',
  team: 'Team',
}

const MODE_LABEL: Record<string, string> = {
  chat: '对话',
  embedding: '向量',
  image_generation: '图片',
  video_generation: '视频',
  digital_human: '数字人',
  audio_speech: 'TTS',
  audio_transcription: 'ASR',
  hotparse: '爆款解析',
}

const STRATEGY_LABEL: Record<PricingStrategy, string> = {
  token: 'token',
  parameter: '多维乘数',
  fixed: '固定单价',
}
const DISPATCH_LABEL: Record<DispatchMode, string> = {
  sync: '同步',
  streaming: '流式',
  async: '异步',
}

const CAP_ICONS: Array<{ key: keyof Capabilities; label: string; emoji: string }> = [
  { key: 'vision', label: '图片', emoji: '📷' },
  { key: 'tools', label: '工具', emoji: '🛠' },
  { key: 'thinking', label: '推理', emoji: '🧠' },
  { key: 'cache', label: '缓存', emoji: '💾' },
  { key: 'audio', label: '音频', emoji: '🎙' },
]

function fmtTokens(n: number): string {
  if (!n) return '—'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
  if (n >= 1_000) return `${Math.round(n / 1_000)}K`
  return n.toString()
}

const FAMILY_OPTIONS = [
  'claude', 'openai', 'gemini', 'deepseek', 'qwen', 'kimi', 'zhipu',
  'grok', 'mistral', 'llama', 'minimax', 'sensetime', 'volcengine', 'other',
]

// 渠道数列样式：active>=1 绿，0 但 total>0 红，全 0 灰
function channelHealthClass(modelId: string): string {
  const s = channelStatsByModel.value.get(modelId)
  if (!s || s.total === 0) return 'zero'
  if (s.active === 0) return 'fail'
  if (s.auto_disabled > 0) return 'warn'
  return 'ok'
}

// ─── column visibility ────────────────────────────────────────────
const showContextCol = computed(() =>
  props.mode === 'chat' || props.mode === 'embedding' || props.mode === 'all',
)
const showStrategyCol = computed(() =>
  props.mode === 'image_generation' ||
  props.mode === 'video_generation' ||
  props.mode === 'audio_speech' ||
  props.mode === 'audio_transcription' ||
  props.mode === 'digital_human' ||
  props.mode === 'all',
)
const showDispatchCol = computed(() =>
  props.mode === 'video_generation' ||
  props.mode === 'digital_human' ||
  props.mode === 'all',
)
const showCapsCol = computed(() =>
  props.mode === 'chat' || props.mode === 'all',
)
const showSyncBtn = computed(() => props.mode === 'chat' || props.mode === 'all')

const priceColLabel = computed(() => {
  switch (props.mode) {
    case 'image_generation':
    case 'hotparse':
      return '计价（每张）'
    case 'video_generation':
      return '计价（每秒）'
    case 'audio_speech':
    case 'audio_transcription':
      return '计价（每秒）'
    case 'digital_human':
      return '计价（每秒/每字）'
    case 'embedding':
      return '计价（每百万 token）'
    case 'chat':
    case 'all':
    default:
      return '计价（输入/输出）'
  }
})

function openDetail(row: Model) {
  emit('open-detail', row)
}

defineExpose({ load })
onMounted(load)
</script>

<template>
  <div>
    <div class="filters">
      <el-input
        v-model="search"
        clearable
        placeholder="搜索 code 或显示名…"
        style="width: 240px"
        @input="onSearch"
        @clear="onSearch"
      >
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <el-select
        v-model="filterFamily"
        clearable
        placeholder="全部 family"
        style="width: 150px"
        @change="load"
        @clear="load"
      >
        <el-option v-for="f in FAMILY_OPTIONS" :key="f" :label="f" :value="f" />
      </el-select>
      <el-select
        v-model="filterMinPlan"
        clearable
        placeholder="全部档位"
        style="width: 130px"
        @change="load"
        @clear="load"
      >
        <el-option label="免费" value="free" />
        <el-option label="Pro" value="pro" />
        <el-option label="Team" value="team" />
      </el-select>
      <el-select
        v-model="filterStatus"
        clearable
        placeholder="全部状态"
        style="width: 130px"
        @change="load"
        @clear="load"
      >
        <el-option label="启用" value="active" />
        <el-option label="禁用" value="disabled" />
        <el-option label="弃用" value="deprecated" />
        <el-option label="自动禁用" value="auto_disabled" />
      </el-select>
      <span class="muted small">共 {{ models.length }} 条</span>
      <div class="header-spacer" />
      <el-button-group>
        <el-button @click="toggleCurrency">显示: {{ store.displayCurrency }}</el-button>
      </el-button-group>
      <el-button
        v-if="canWrite && showSyncBtn"
        type="primary"
        :loading="syncing"
        @click="onSync"
      >
        <el-icon><Refresh /></el-icon>
        同步上游
      </el-button>
    </div>

    <el-table
      :data="models"
      v-loading="loading"
      stripe
      style="margin-top: 12px; cursor: pointer"
      @row-click="openDetail"
    >
      <el-table-column label="模型" min-width="220">
        <template #default="{ row }: { row: Model }">
          <div class="model-cell">
            <div class="code">{{ row.code }}</div>
            <div class="meta">
              <span v-if="row.display_name && row.display_name !== row.code" class="display-name">
                {{ row.display_name }}
              </span>
              <el-tag v-if="props.mode === 'all'" size="small" effect="plain" class="mode-tag">
                {{ MODE_LABEL[row.mode] ?? row.mode }}
              </el-tag>
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="Family" width="110">
        <template #default="{ row }: { row: Model }">
          <el-tag size="small" effect="plain">{{ row.family || 'other' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column v-if="showContextCol" label="上下文" width="100">
        <template #default="{ row }: { row: Model }">
          <span class="muted">{{ fmtTokens(row.context_window) }}</span>
        </template>
      </el-table-column>
      <el-table-column v-if="showCapsCol" label="能力" width="140">
        <template #default="{ row }: { row: Model }">
          <span
            v-for="c in CAP_ICONS"
            :key="c.key"
            class="cap-icon"
            :class="{ on: row.capabilities?.[c.key] }"
            :title="c.label"
          >{{ c.emoji }}</span>
        </template>
      </el-table-column>
      <el-table-column v-if="showStrategyCol" label="计价模式" width="110">
        <template #default="{ row }: { row: Model }">
          <el-tag size="small" effect="plain">
            {{ STRATEGY_LABEL[row.pricing_strategy as PricingStrategy] ?? row.pricing_strategy }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column v-if="showDispatchCol" label="分发" width="90">
        <template #default="{ row }: { row: Model }">
          <el-tag
            size="small"
            effect="plain"
            :type="row.dispatch_mode === 'async' ? 'warning' : 'info'"
          >
            {{ DISPATCH_LABEL[row.dispatch_mode as DispatchMode] ?? row.dispatch_mode }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="渠道数" width="100" align="center">
        <template #default="{ row }: { row: Model }">
          <span class="channel-stat" :class="channelHealthClass(row.id)">
            {{ channelStatsByModel.get(row.id)?.active ?? 0 }}
            <span class="slash">/</span>
            {{ channelStatsByModel.get(row.id)?.total ?? 0 }}
          </span>
        </template>
      </el-table-column>
      <el-table-column :label="priceColLabel" min-width="170">
        <template #default="{ row }: { row: Model }">
          <span class="price" :title="priceTooltip(row.id, row.mode)">
            {{ fmtPriceCell(row.id, row.mode) }}
          </span>
        </template>
      </el-table-column>
      <el-table-column label="档位" width="90">
        <template #default="{ row }: { row: Model }">
          <el-tag size="small" effect="plain">{{ PLAN_LABEL[row.min_plan] }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }: { row: Model }">
          <el-tag size="small" :type="STATUS_TYPE[row.status] ?? 'info'">
            {{ STATUS_LABEL[row.status] ?? row.status }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="人工锁定" width="90" align="center">
        <template #default="{ row }: { row: Model }">
          <el-icon
            v-if="row.manual_override"
            :size="16"
            color="#3b82f6"
            title="manual_override=true，下次同步不覆盖"
          >
            <Lock />
          </el-icon>
          <span v-else class="muted">—</span>
        </template>
      </el-table-column>
    </el-table>

    <el-empty
      v-if="!loading && models.length === 0"
      :description="props.mode === 'chat'
        ? '尚无模型，点击右上角「同步上游」从 basellm.github.io 拉取主流模型'
        : `尚无 ${MODE_LABEL[props.mode] ?? props.mode} 模型 — 通过模型详情手动新建`"
    />
  </div>
</template>

<style scoped lang="scss">
.filters {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.header-spacer { flex: 1; }
.muted { color: #6b7280; font-size: 13px; }
.muted.small { font-size: 12px; }

.model-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  .code {
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 13px;
    color: #111827;
  }
  .meta {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .display-name {
    font-size: 12px;
    color: #6b7280;
  }
  .mode-tag {
    transform: scale(0.85);
    transform-origin: left center;
  }
}
.cap-icon {
  display: inline-block;
  margin-right: 4px;
  font-size: 14px;
  filter: grayscale(100%);
  opacity: 0.3;
  transition: filter 0.1s, opacity 0.1s;
  &.on {
    filter: none;
    opacity: 1;
  }
}
.channel-stat {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  &.zero { color: #9ca3af; }
  &.fail { color: #dc2626; }
  &.warn { color: #d97706; }
  &.ok   { color: #16a34a; }
  .slash { color: #d1d5db; margin: 0 2px; }
}
.price {
  font-variant-numeric: tabular-nums;
  font-size: 13px;
  color: #1f2937;
}
</style>
