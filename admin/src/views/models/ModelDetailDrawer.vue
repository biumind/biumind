<script setup lang="ts">
// Detail drawer for one model. 三块:
//   1. 基础信息 + 能力 + min_plan + manual_override
//   2. 计价（双币种）
//   3. 下挂的 channels 列表（含一键测试 + 跳到渠道页编辑）
//
// 详情抽屉是单 model "尽量在一处办完"的入口，避免三页跳转。

import { ref, watch, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { errorMessage } from '@/api/http'
import * as api from '@/api/modelRelay'
import { usePermission } from '@/composables/usePermission'
import { useModelRelayStore } from '@/stores/modelRelay'
import { useRouter } from 'vue-router'
import type {
  Model,
  ModelGroup,
  ModelMode,
  Pricing,
  PricingInput,
  PricingRule,
  PricingStrategy,
  DispatchMode,
  Channel,
  CredentialSafe,
  Currency,
  Capabilities,
  Plan,
  EntityStatus,
} from '@/api/modelRelay.types'

const props = defineProps<{ visible: boolean; modelId: string }>()
const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void
  (e: 'saved'): void
}>()

const router = useRouter()
const store = useModelRelayStore()
const { can } = usePermission()
const canWrite = can('models:write')
const canPricing = can('pricing:write')

const loading = ref(false)
const saving = ref(false)
const model = ref<Model | null>(null)
const groups = ref<ModelGroup[]>([])
const pricing = ref<Pricing | null>(null)
const pricingHistory = ref<Pricing[]>([])
const showHistory = ref(false)
const channels = ref<Channel[]>([])
const credentials = ref<CredentialSafe[]>([])

// Editable copies
const editModel = ref({
  display_name: '',
  family: '',
  context_window: 0,
  max_output: 0,
  capabilities: {} as Capabilities,
  min_plan: 'free' as Plan,
  status: 'disabled' as EntityStatus,
  sort_order: 0,
  manual_override: false,
  routing_strategy: 'weighted' as 'weighted' | 'lowest_latency' | 'least_busy' | 'lowest_tpm_rpm' | 'cost_aware',
  // mode 是 schema CHECK 8 选 1 (chat / embedding / image_generation / ...).
  // 后端 modelRequest.Mode JSON field 接收, 空字符串时仓库层会兜底为 chat.
  mode: 'chat' as ModelMode,
})
const editPricing = ref<PricingInput>({
  currency: 'USD',
  input_per_mtok: 0,
  output_per_mtok: 0,
  cache_write_per_mtok: 0,
  cache_read_per_mtok: 0,
  cost_per_image: null,
  cost_per_video_second: null,
  cost_per_audio_second: null,
  cost_per_character: null,
  cost_per_search_unit: 0,
})

// F2.3 — parameter strategy 模型的多维乘数表
const pricingRules = ref<PricingRule[]>([])
const ruleEditJson = ref('')
const ruleSaving = ref(false)

// 计价模式相关 computed (mode-aware UI)
const isTokenPricing = computed(() => {
  if (!model.value) return true
  return model.value.mode === 'chat' || model.value.mode === 'embedding'
})
const showImagePrice = computed(() =>
  model.value?.mode === 'image_generation' || model.value?.mode === 'hotparse',
)
const showVideoPrice = computed(() => model.value?.mode === 'video_generation')
const showAudioPrice = computed(() =>
  model.value?.mode === 'audio_speech' ||
  model.value?.mode === 'audio_transcription' ||
  model.value?.mode === 'digital_human',
)
const showCharPrice = computed(() => model.value?.mode === 'digital_human')
const isParameterStrategy = computed(
  () => model.value?.pricing_strategy === 'parameter',
)

const MODE_LABEL: Record<ModelMode, string> = {
  chat: '对话',
  embedding: '向量',
  rerank: '重排',
  image_generation: '图片',
  video_generation: '视频',
  digital_human: '数字人',
  audio_speech: 'TTS',
  audio_transcription: 'ASR',
  hotparse: '爆款解析',
  responses: 'Responses',
}
const STRATEGY_LABEL: Record<PricingStrategy, string> = {
  token: 'token (input/output)',
  parameter: 'parameter (多维乘数)',
  fixed: 'fixed (固定单价)',
}
const DISPATCH_LABEL: Record<DispatchMode, string> = {
  sync: '同步',
  streaming: '流式',
  async: '异步',
}

watch(
  () => props.visible,
  (v) => {
    if (v && props.modelId) load()
  },
)

async function load() {
  if (!props.modelId) return
  loading.value = true
  try {
    const [detail, ch, cr] = await Promise.all([
      api.getModel(props.modelId),
      api.listChannels({ model_id: props.modelId }),
      api.listCredentials({ status: 'active' }),
    ])
    model.value = detail.model
    groups.value = detail.groups
    channels.value = ch.items
    credentials.value = cr.items

    Object.assign(editModel.value, {
      display_name: detail.model.display_name,
      family: detail.model.family,
      context_window: detail.model.context_window,
      max_output: detail.model.max_output,
      capabilities: { ...detail.model.capabilities },
      min_plan: detail.model.min_plan,
      status: detail.model.status,
      sort_order: detail.model.sort_order,
      manual_override: detail.model.manual_override,
      routing_strategy: detail.model.routing_strategy ?? 'weighted',
      mode: (detail.model.mode || 'chat') as ModelMode,
    })

    // Pricing — empty placeholder if none set
    try {
      const pr = await api.getPricing(props.modelId)
      if (pr.id) {
        pricing.value = pr
        editPricing.value = {
          currency: pr.currency,
          input_per_mtok: pr.input_per_mtok,
          output_per_mtok: pr.output_per_mtok,
          cache_write_per_mtok: pr.cache_write_per_mtok,
          cache_read_per_mtok: pr.cache_read_per_mtok,
          cost_per_image: pr.cost_per_image ?? null,
          cost_per_video_second: pr.cost_per_video_second ?? null,
          cost_per_audio_second: pr.cost_per_audio_second ?? null,
          cost_per_character: pr.cost_per_character ?? null,
          cost_per_search_unit: pr.cost_per_search_unit ?? 0,
        }
      } else {
        pricing.value = null
        editPricing.value = {
          currency: 'USD',
          input_per_mtok: 0,
          output_per_mtok: 0,
          cache_write_per_mtok: 0,
          cache_read_per_mtok: 0,
          cost_per_image: null,
          cost_per_video_second: null,
          cost_per_audio_second: null,
          cost_per_character: null,
          cost_per_search_unit: 0,
        }
      }
    } catch {
      pricing.value = null
    }

    // F2.3 — pricing_rules (仅 parameter strategy 才有意义, 但拉一下不报错)
    try {
      const rs = await api.listPricingRules(props.modelId)
      pricingRules.value = rs.items
      // 默认编辑窗放最新规则的 JSON; 没有则留模板
      if (rs.items.length > 0) {
        ruleEditJson.value = JSON.stringify(rs.items[0].rule_jsonb, null, 2)
      } else {
        ruleEditJson.value = JSON.stringify(
          {
            by_duration: [
              { max_seconds: 5, multiplier: 1.0 },
              { max_seconds: 10, multiplier: 1.8 },
            ],
            by_resolution: [
              { resolution: '720p', multiplier: 1.0 },
              { resolution: '1080p', multiplier: 1.5 },
            ],
          },
          null,
          2,
        )
      }
    } catch {
      pricingRules.value = []
    }

    // Pricing history (lazy — only loaded when user toggles the section,
    // but we prefetch here so the toggle is instant; cost is one extra
    // small SELECT per drawer open, negligible).
    try {
      const hist = await api.getPricingHistory(props.modelId)
      pricingHistory.value = hist.items
    } catch {
      pricingHistory.value = []
    }
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

const credById = computed(() => {
  const m = new Map<string, CredentialSafe>()
  for (const c of credentials.value) m.set(c.id, c)
  return m
})

async function onSaveModel() {
  if (!model.value) return
  saving.value = true
  try {
    await api.updateModel(model.value.id, {
      code: model.value.code,
      ...editModel.value,
    })
    ElMessage.success('模型已保存')
    emit('saved')
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    saving.value = false
  }
}

async function onSavePricing() {
  if (!model.value) return
  saving.value = true
  try {
    // 只发当前 mode 用得到的字段, 其他保持后端 NULL.
    const body: PricingInput = { currency: editPricing.value.currency }
    if (isTokenPricing.value) {
      body.input_per_mtok = editPricing.value.input_per_mtok
      body.output_per_mtok = editPricing.value.output_per_mtok
      body.cache_write_per_mtok = editPricing.value.cache_write_per_mtok
      body.cache_read_per_mtok = editPricing.value.cache_read_per_mtok
    }
    if (showImagePrice.value && editPricing.value.cost_per_image != null) {
      body.cost_per_image = editPricing.value.cost_per_image
    }
    if (showVideoPrice.value && editPricing.value.cost_per_video_second != null) {
      body.cost_per_video_second = editPricing.value.cost_per_video_second
    }
    if (showAudioPrice.value && editPricing.value.cost_per_audio_second != null) {
      body.cost_per_audio_second = editPricing.value.cost_per_audio_second
    }
    if (showCharPrice.value && editPricing.value.cost_per_character != null) {
      body.cost_per_character = editPricing.value.cost_per_character
    }
    // 通用按单元价格不按 mode 过滤: pseudo-model (wiki-parse-text 等) 的
    // mode 是手工注册时随意选的, 始终透传当前值 (0 = 不适用), 避免调价时丢价.
    body.cost_per_search_unit = editPricing.value.cost_per_search_unit ?? 0
    await api.setPricing(model.value.id, body)
    ElMessage.success('计价已保存（append-only，旧记录保留）')
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    saving.value = false
  }
}

// F2.3 — append 一条新 pricing_rule (parameter strategy 用)
async function onSavePricingRule() {
  if (!model.value) return
  let parsed: Record<string, unknown>
  try {
    parsed = JSON.parse(ruleEditJson.value)
  } catch (e) {
    ElMessage.error('JSON 格式错误: ' + (e as Error).message)
    return
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    ElMessage.error('rule_jsonb 必须是一个 JSON 对象')
    return
  }
  ruleSaving.value = true
  try {
    await api.appendPricingRule(model.value.id, { rule_jsonb: parsed })
    ElMessage.success(
      '已追加 pricing rule (append-only); 后端已自动把 pricing_strategy 设为 parameter',
    )
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    ruleSaving.value = false
  }
}

async function onDelete() {
  if (!model.value) return
  await ElMessageBox.confirm(
    `确认删除模型 "${model.value.code}"？关联的 channels 和 pricing 会一并 cascade 删除。`,
    '删除模型',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
  )
    .then(async () => {
      try {
        await api.deleteModel(model.value!.id)
        ElMessage.success('已删除')
        emit('saved')
      } catch (e) {
        ElMessage.error(errorMessage(e))
      }
    })
    .catch(() => {})
}

// ─── channel tests inside drawer ────────────────────────────────
const testingChannelId = ref<string>('')
async function onTestChannel(c: Channel) {
  testingChannelId.value = c.id
  try {
    const res = await api.testChannel(c.id)
    if (res.ok) {
      ElMessage.success(`✓ 通畅 (${res.latency_ms}ms)`)
    } else {
      ElMessage.error(`✗ ${res.error_code ?? 'error'}: ${res.error ?? ''}`)
    }
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    testingChannelId.value = ''
  }
}

function gotoChannels() {
  router.push({
    path: '/models/channels',
    query: { model_id: props.modelId },
  })
}

// ─── helpers ────────────────────────────────────────────────────
function fmtTime(iso?: string) {
  return iso ? new Date(iso).toLocaleString('zh-CN', { hour12: false }) : '—'
}

// pctDiff returns a signed string "+12.50%" / "-3.20%" / "" for two
// numbers. Empty string when prev is 0 / undefined (first row, or
// missing baseline) — calling code renders nothing in that case.
function pctDiff(curr: number, prev: number | undefined): string {
  if (prev === undefined || prev === 0) return ''
  if (curr === prev) return ''
  const delta = ((curr - prev) / prev) * 100
  const sign = delta > 0 ? '+' : ''
  return `${sign}${delta.toFixed(2)}%`
}

// Returns 'up' / 'down' / '' so template can color-code price moves.
function diffClass(curr: number, prev: number | undefined): string {
  if (prev === undefined || prev === 0 || curr === prev) return ''
  return curr > prev ? 'up' : 'down'
}
const STATUS_TYPE: Record<string, 'success' | 'info' | 'danger' | 'warning'> = {
  active: 'success',
  disabled: 'info',
  auto_disabled: 'warning',
  invalid: 'danger',
}
const STATUS_LABEL: Record<string, string> = {
  active: '启用',
  disabled: '禁用',
  auto_disabled: '自动降级',
  deprecated: '弃用',
  invalid: '失效',
}
</script>

<template>
  <el-drawer
    :model-value="visible"
    @update:model-value="(v: boolean) => emit('update:visible', v)"
    direction="rtl"
    size="780px"
    :close-on-click-modal="false"
  >
    <template #header>
      <div class="drawer-header">
        <span class="title">{{ model?.code ?? '...' }}</span>
        <el-tag v-if="model" size="small" :type="STATUS_TYPE[model.status] ?? 'info'">
          {{ STATUS_LABEL[model.status] ?? model.status }}
        </el-tag>
        <el-tag v-if="model" size="small" effect="plain">
          {{ MODE_LABEL[model.mode as ModelMode] ?? model.mode }}
        </el-tag>
        <el-tag v-if="model?.manual_override" size="small" effect="plain" type="warning" title="manual_override=true">
          人工锁定
        </el-tag>
      </div>
    </template>

    <div v-loading="loading" class="drawer-content">
      <!-- 1) Basic + capabilities -->
      <el-card shadow="never" class="section">
        <template #header><b>基础信息</b></template>
        <el-form label-width="120px" v-if="model" :disabled="!canWrite">
          <el-form-item label="Code">
            <el-input :model-value="model.code" disabled />
          </el-form-item>
          <el-form-item label="显示名">
            <el-input v-model="editModel.display_name" />
          </el-form-item>
          <el-form-item label="Family">
            <el-input v-model="editModel.family" placeholder="claude / openai / qwen ..." />
          </el-form-item>
          <el-form-item label="模态 (mode)">
            <el-select v-model="editModel.mode" style="width: 220px">
              <el-option label="对话 (chat)" value="chat" />
              <el-option label="向量 (embedding)" value="embedding" />
              <el-option label="图像生成 (image_generation)" value="image_generation" />
              <el-option label="视频生成 (video_generation)" value="video_generation" />
              <el-option label="数字人 (digital_human)" value="digital_human" />
              <el-option label="语音合成 (audio_speech / TTS)" value="audio_speech" />
              <el-option label="语音识别 (audio_transcription / ASR)" value="audio_transcription" />
              <el-option label="热点解析 (hotparse)" value="hotparse" />
            </el-select>
            <span class="form-hint">
              同步上游会按 LiteLLM 字典 + 关键字启发式自动判断;
              这里手工修后建议同时打开「人工锁定」, 防止下次同步覆盖.
            </span>
          </el-form-item>
          <el-form-item label="计价 / 分发">
            <div class="mode-tags">
              <el-tag size="small" effect="plain">
                pricing: {{ STRATEGY_LABEL[model.pricing_strategy as PricingStrategy] ?? model.pricing_strategy }}
              </el-tag>
              <el-tag
                size="small"
                effect="plain"
                :type="model.dispatch_mode === 'async' ? 'warning' : 'info'"
              >
                dispatch: {{ DISPATCH_LABEL[model.dispatch_mode as DispatchMode] ?? model.dispatch_mode }}
              </el-tag>
            </div>
            <span class="form-hint">pricing_strategy / dispatch_mode 当前不开放编辑, 由 schema DEFAULT 决定</span>
          </el-form-item>
          <el-form-item v-if="isTokenPricing" label="上下文窗口">
            <el-input-number v-model="editModel.context_window" :min="0" :step="1000" />
            <span class="form-hint">tokens</span>
          </el-form-item>
          <el-form-item v-if="isTokenPricing" label="最大输出">
            <el-input-number v-model="editModel.max_output" :min="0" :step="512" />
            <span class="form-hint">tokens</span>
          </el-form-item>
          <el-form-item label="能力">
            <div class="caps">
              <el-checkbox v-model="editModel.capabilities.vision">📷 图片</el-checkbox>
              <el-checkbox v-model="editModel.capabilities.tools">🛠 工具</el-checkbox>
              <el-checkbox v-model="editModel.capabilities.thinking">🧠 推理</el-checkbox>
              <el-checkbox v-model="editModel.capabilities.cache">💾 缓存</el-checkbox>
              <el-checkbox v-model="editModel.capabilities.audio">🎙 音频</el-checkbox>
              <el-checkbox v-model="editModel.capabilities.json_mode">{ } JSON</el-checkbox>
            </div>
          </el-form-item>
          <el-form-item label="最低档位">
            <el-radio-group v-model="editModel.min_plan">
              <el-radio label="free">免费</el-radio>
              <el-radio label="pro">Pro</el-radio>
              <el-radio label="team">Team</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="状态">
            <el-radio-group v-model="editModel.status">
              <el-radio label="active">启用</el-radio>
              <el-radio label="disabled">禁用</el-radio>
              <el-radio label="deprecated">弃用</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="排序">
            <el-input-number v-model="editModel.sort_order" :min="0" :step="1" />
          </el-form-item>
          <el-form-item label="路由策略">
            <el-radio-group v-model="editModel.routing_strategy">
              <el-radio label="weighted">priority + 权重随机</el-radio>
              <el-radio label="lowest_latency">最低延迟优先</el-radio>
              <el-radio label="least_busy">最少占用</el-radio>
              <el-radio label="lowest_tpm_rpm" disabled>最大配额余量（P3）</el-radio>
              <el-radio label="cost_aware" disabled>成本最低（P3）</el-radio>
            </el-radio-group>
            <div class="form-hint">
              <template v-if="editModel.routing_strategy === 'weighted'">
                同 priority 内按 weight 加权随机；priority 不同时严格降级
              </template>
              <template v-else-if="editModel.routing_strategy === 'lowest_latency'">
                同 priority 内挑 latency_p50_ms 最低的；新通道（未测）优先获得流量以建立统计
              </template>
              <template v-else-if="editModel.routing_strategy === 'least_busy'">
                同 priority 内挑当前 in-flight 请求数最少的；适合上游单 key 并发瓶颈场景
              </template>
            </div>
          </el-form-item>
          <el-form-item label="人工锁定">
            <el-switch v-model="editModel.manual_override" />
            <span class="form-hint">开启后下次"同步上游"会跳过此模型，保护手工修改</span>
          </el-form-item>
        </el-form>

        <!-- Groups (read-only in MVP — 全员 default) -->
        <div class="groups-row" v-if="groups.length">
          <span class="muted">绑定分组：</span>
          <el-tag v-for="g in groups" :key="g.id" size="small" effect="plain">
            {{ g.code }}
          </el-tag>
        </div>

        <div class="section-actions" v-if="canWrite">
          <el-button :loading="saving" type="primary" @click="onSaveModel">保存基础信息</el-button>
          <el-button :loading="saving" type="danger" link @click="onDelete">删除模型</el-button>
        </div>
      </el-card>

      <!-- 2) Pricing -->
      <el-card shadow="never" class="section">
        <template #header>
          <div class="section-header">
            <b>计价</b>
            <span v-if="pricing" class="muted small">
              当前 effective: {{ fmtTime(pricing.effective_at) }}
            </span>
          </div>
        </template>
        <el-form label-width="160px" v-if="canPricing">
          <el-form-item label="原币种">
            <el-radio-group v-model="editPricing.currency">
              <el-radio label="USD">USD（$）</el-radio>
              <el-radio label="CNY">CNY（¥）</el-radio>
            </el-radio-group>
          </el-form-item>

          <!-- token 类 (chat / embedding) -->
          <template v-if="isTokenPricing">
            <el-form-item label="输入（每 1M token）">
              <el-input-number
                v-model="editPricing.input_per_mtok"
                :min="0"
                :precision="6"
                :step="0.1"
                controls-position="right"
                style="width: 200px"
              />
            </el-form-item>
            <el-form-item label="输出（每 1M token）">
              <el-input-number
                v-model="editPricing.output_per_mtok"
                :min="0"
                :precision="6"
                :step="0.1"
                controls-position="right"
                style="width: 200px"
              />
            </el-form-item>
            <el-form-item label="缓存写入（可选）">
              <el-input-number
                v-model="editPricing.cache_write_per_mtok"
                :min="0"
                :precision="6"
                :step="0.1"
                controls-position="right"
                style="width: 200px"
              />
            </el-form-item>
            <el-form-item label="缓存读取（可选）">
              <el-input-number
                v-model="editPricing.cache_read_per_mtok"
                :min="0"
                :precision="6"
                :step="0.1"
                controls-position="right"
                style="width: 200px"
              />
            </el-form-item>
          </template>

          <!-- 图片 / 爆款解析 -->
          <el-form-item v-if="showImagePrice" label="单价（每张）">
            <el-input-number
              v-model="editPricing.cost_per_image"
              :min="0"
              :precision="6"
              :step="0.001"
              controls-position="right"
              style="width: 200px"
            />
            <span class="form-hint">parameter strategy 时此价为基础价, 实际乘 rule 系数</span>
          </el-form-item>

          <!-- 视频 -->
          <el-form-item v-if="showVideoPrice" label="单价（每视频秒）">
            <el-input-number
              v-model="editPricing.cost_per_video_second"
              :min="0"
              :precision="6"
              :step="0.001"
              controls-position="right"
              style="width: 200px"
            />
            <span class="form-hint">parameter strategy 时按 rule 矩阵 (by_duration × by_resolution) 乘</span>
          </el-form-item>

          <!-- 音频 / 数字人 -->
          <el-form-item v-if="showAudioPrice" label="单价（每音频秒）">
            <el-input-number
              v-model="editPricing.cost_per_audio_second"
              :min="0"
              :precision="6"
              :step="0.001"
              controls-position="right"
              style="width: 200px"
            />
          </el-form-item>

          <!-- 数字人 (字符数) -->
          <el-form-item v-if="showCharPrice" label="单价（每字）">
            <el-input-number
              v-model="editPricing.cost_per_character"
              :min="0"
              :precision="8"
              :step="0.0001"
              controls-position="right"
              style="width: 200px"
            />
            <span class="form-hint">数字人按字数计价时填这里 (与每秒二选一)</span>
          </el-form-item>

          <!-- 通用按单元 (rerank 按次 / wiki 解析按页) -->
          <el-form-item label="单价（每搜索单元）">
            <el-input-number
              v-model="editPricing.cost_per_search_unit"
              :min="0"
              :precision="6"
              :step="0.001"
              controls-position="right"
              style="width: 200px"
            />
            <span class="form-hint">
              rerank 按次 (1 unit = 1 query × ≤100 docs)、wiki 解析按页 (pseudo-model
              如 wiki-parse-text / wiki-ocr) 都复用此列；不适用留 0
            </span>
          </el-form-item>

          <div class="section-actions">
            <el-button :loading="saving" type="primary" @click="onSavePricing">
              {{ pricing ? '调价（追加新行）' : '设置初始计价' }}
            </el-button>
            <span class="muted small" style="margin-left: 12px">
              append-only — 旧 effective_at 行保留，可回溯审计
            </span>
          </div>
        </el-form>
        <div v-else class="muted">无 pricing:write 权限，无法编辑计价</div>

        <!-- 调价历史 -->
        <div class="history-section" v-if="pricingHistory.length > 1">
          <div class="history-toggle" @click="showHistory = !showHistory">
            <el-icon :class="{ rotated: showHistory }"><ArrowRight /></el-icon>
            <b>调价历史</b>
            <el-tag size="small" effect="plain" round>{{ pricingHistory.length }}</el-tag>
            <span class="muted small">最新生效在前</span>
          </div>
          <div v-show="showHistory" class="history-table">
            <el-table :data="pricingHistory" size="small" stripe>
              <el-table-column label="生效时间" width="180">
                <template #default="{ row, $index }: { row: Pricing; $index: number }">
                  <div class="time-cell">
                    <span :class="{ current: $index === 0 }">{{ fmtTime(row.effective_at) }}</span>
                    <el-tag v-if="$index === 0" size="small" type="success">现行</el-tag>
                  </div>
                </template>
              </el-table-column>
              <el-table-column label="币种" width="80">
                <template #default="{ row }: { row: Pricing }">
                  <code>{{ row.currency }}</code>
                </template>
              </el-table-column>
              <el-table-column label="输入 / 1Mtok">
                <template #default="{ row, $index }: { row: Pricing; $index: number }">
                  <div class="price-with-diff">
                    <span>{{ row.input_per_mtok.toFixed(4) }}</span>
                    <span
                      v-if="pricingHistory[$index + 1]"
                      class="diff"
                      :class="diffClass(row.input_per_mtok, pricingHistory[$index + 1].input_per_mtok)"
                    >{{ pctDiff(row.input_per_mtok, pricingHistory[$index + 1].input_per_mtok) }}</span>
                  </div>
                </template>
              </el-table-column>
              <el-table-column label="输出 / 1Mtok">
                <template #default="{ row, $index }: { row: Pricing; $index: number }">
                  <div class="price-with-diff">
                    <span>{{ row.output_per_mtok.toFixed(4) }}</span>
                    <span
                      v-if="pricingHistory[$index + 1]"
                      class="diff"
                      :class="diffClass(row.output_per_mtok, pricingHistory[$index + 1].output_per_mtok)"
                    >{{ pctDiff(row.output_per_mtok, pricingHistory[$index + 1].output_per_mtok) }}</span>
                  </div>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </el-card>

      <!-- 2.5) Pricing Rules (parameter strategy / F2.3) -->
      <el-card
        v-if="model && (isParameterStrategy || showVideoPrice || showImagePrice)"
        shadow="never"
        class="section"
      >
        <template #header>
          <div class="section-header">
            <b>多维乘数 (pricing_rules)</b>
            <el-tag v-if="isParameterStrategy" size="small" type="success" effect="plain">
              当前 strategy = parameter
            </el-tag>
            <el-tag v-else size="small" effect="plain">
              当前 strategy = {{ model.pricing_strategy }}
            </el-tag>
          </div>
        </template>

        <div v-if="!isParameterStrategy" class="muted small" style="margin-bottom: 12px">
          此模型当前不是 parameter 策略；保存一条规则后会自动升级为 parameter。
          适合场景：视频按时长×分辨率定价、图片按分辨率定价。
        </div>

        <el-form label-width="160px" v-if="canPricing">
          <el-form-item label="规则 JSON">
            <el-input
              v-model="ruleEditJson"
              type="textarea"
              :rows="10"
              placeholder='{"by_duration": [...], "by_resolution": [...]}'
              :spellcheck="false"
              style="font-family: ui-monospace, monospace; font-size: 12px"
            />
            <div class="form-hint" style="margin-left: 0; margin-top: 6px">
              示例：<code>{ "by_duration": [{"max_seconds": 5, "multiplier": 1.0}],
              "by_resolution": [{"resolution": "720p", "multiplier": 1.0}] }</code>
            </div>
          </el-form-item>
          <div class="section-actions">
            <el-button :loading="ruleSaving" type="primary" @click="onSavePricingRule">
              追加规则（append-only）
            </el-button>
            <span class="muted small" style="margin-left: 12px">
              已有 {{ pricingRules.length }} 条历史规则
            </span>
          </div>
        </el-form>
        <div v-else class="muted">无 pricing:write 权限，无法编辑规则</div>

        <!-- 历史规则列表 (折叠) -->
        <div v-if="pricingRules.length > 0" class="history-section">
          <div class="history-toggle" @click="showHistory = !showHistory">
            <el-icon :class="{ rotated: showHistory }"><ArrowRight /></el-icon>
            <b>规则历史</b>
            <el-tag size="small" effect="plain" round>{{ pricingRules.length }}</el-tag>
            <span class="muted small">最新生效在前</span>
          </div>
          <div v-show="showHistory" class="history-table">
            <el-table :data="pricingRules" size="small" stripe>
              <el-table-column label="生效时间" width="180">
                <template #default="{ row, $index }: { row: PricingRule; $index: number }">
                  <div class="time-cell">
                    <span :class="{ current: $index === 0 }">{{ fmtTime(row.effective_at) }}</span>
                    <el-tag v-if="$index === 0" size="small" type="success">现行</el-tag>
                  </div>
                </template>
              </el-table-column>
              <el-table-column label="rule_jsonb">
                <template #default="{ row }: { row: PricingRule }">
                  <pre class="rule-json">{{ JSON.stringify(row.rule_jsonb, null, 0) }}</pre>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </el-card>

      <!-- 3) Channels -->
      <el-card shadow="never" class="section">
        <template #header>
          <div class="section-header">
            <b>下挂渠道（{{ channels.length }}）</b>
            <el-button link type="primary" @click="gotoChannels">
              到渠道页管理
              <el-icon><ArrowRight /></el-icon>
            </el-button>
          </div>
        </template>
        <el-table :data="channels" v-if="channels.length" size="small">
          <el-table-column label="上游模型" prop="upstream_model" min-width="160" />
          <el-table-column label="凭证" min-width="140">
            <template #default="{ row }: { row: Channel }">
              {{ credById.get(row.credential_id)?.label ?? row.credential_id.slice(0, 8) }}
            </template>
          </el-table-column>
          <el-table-column label="优先级 / 权重" width="120">
            <template #default="{ row }: { row: Channel }">
              {{ row.priority }} / {{ row.weight }}
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }: { row: Channel }">
              <el-tag size="small" :type="STATUS_TYPE[row.status] ?? 'info'">
                {{ STATUS_LABEL[row.status] ?? row.status }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="延迟 P50" width="100">
            <template #default="{ row }: { row: Channel }">
              <span class="muted">{{ row.latency_p50_ms ? `${row.latency_p50_ms}ms` : '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="100">
            <template #default="{ row }: { row: Channel }">
              <el-button
                link
                type="primary"
                :loading="testingChannelId === row.id"
                @click="onTestChannel(row)"
              >测试</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-empty
          v-else
          :image-size="80"
          description="尚未绑定任何渠道。到渠道页新建一条来启用此模型。"
        >
          <el-button type="primary" @click="gotoChannels">前往渠道页</el-button>
        </el-empty>
      </el-card>
    </div>
  </el-drawer>
</template>

<style scoped lang="scss">
.drawer-header {
  display: flex;
  align-items: center;
  gap: 10px;
  .title {
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 16px;
    font-weight: 600;
  }
}
.drawer-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.section {
  &:deep(.el-card__header) { padding: 12px 16px; }
}
.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.section-actions {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid #f3f4f6;
  display: flex;
  align-items: center;
}
.caps {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 16px;
}
.groups-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  border-top: 1px solid #f3f4f6;
}
.form-hint {
  margin-left: 12px;
  color: #6b7280;
  font-size: 12px;
}
.muted { color: #6b7280; font-size: 13px; }
.muted.small { font-size: 12px; }

.history-section {
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid #f3f4f6;
}
.history-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  user-select: none;
  font-size: 13px;
  color: #374151;
  &:hover { color: #1f2937; }
  .el-icon {
    transition: transform 0.15s;
    &.rotated { transform: rotate(90deg); }
  }
}
.history-table {
  margin-top: 12px;
}
.time-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  .current { font-weight: 500; }
}
.price-with-diff {
  display: inline-flex;
  align-items: baseline;
  gap: 8px;
  font-variant-numeric: tabular-nums;
  .diff {
    font-size: 11px;
    color: #9ca3af;
    &.up { color: #dc2626; }
    &.down { color: #16a34a; }
  }
}
.mode-tags {
  display: inline-flex;
  gap: 6px;
  flex-wrap: wrap;
}
.rule-json {
  margin: 0;
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  font-size: 11px;
  color: #374151;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
