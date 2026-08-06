<script setup lang="ts">
// Channels — 路由的最小单元。
// "model X 由 credential Y 在上游模型 Z 上提供，priority/weight=..."
//
// 默认从 model 页跳过来时带 query.model_id 自动筛选；运维诊断场景可手动
// 切换模型筛选 / 看全部。
import { ref, onMounted, computed, reactive, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { errorMessage } from '@/api/http'
import * as api from '@/api/modelRelay'
import type {
  Channel,
  ChannelInput,
  CredentialSafe,
  Model,
  ModelMode,
  EntityStatus,
} from '@/api/modelRelay.types'
import { usePermission } from '@/composables/usePermission'

const route = useRoute()
const { can } = usePermission()
const canWrite = can('models:write')

const channels = ref<Channel[]>([])
const models = ref<Model[]>([])
const credentials = ref<CredentialSafe[]>([])
const loading = ref(false)
const filterModelId = ref<string>(typeof route.query.model_id === 'string' ? route.query.model_id : '')
const filterCredentialId = ref<string>('')
const filterStatus = ref<string>('')
// v0.3: mode 筛选 — 路由层一票多模态后, 找特定模态 channel 的 SRE 路径
// (例: rerank/audio_speech 都在 dashscope provider 下, 没 mode 筛选时
// 列表里全混着)
const filterMode = ref<ModelMode | ''>(
  typeof route.query.mode === 'string' ? (route.query.mode as ModelMode) : '',
)

watch(
  () => route.query.model_id,
  (v) => {
    if (typeof v === 'string') {
      filterModelId.value = v
      load()
    }
  },
)

async function load() {
  loading.value = true
  try {
    const [c, m, cr] = await Promise.all([
      api.listChannels({
        model_id: filterModelId.value || undefined,
        credential_id: filterCredentialId.value || undefined,
        status: filterStatus.value || undefined,
      }),
      // page_size=200 是后端 cap. 模型总量可能 3000+, 这里只做"已展示渠道
      // 解析 model_id → 显示 name"以及顶部筛选下拉的兜底种子集. 真正的
      // 模型选择走 modelSearch (remote), 不依赖此列表完整.
      api.listModels({ page_size: 200 }),
      api.listCredentials({ status: 'active' }),
    ])
    channels.value = c.items
    models.value = m.items
    credentials.value = cr.items
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

// "新建/编辑渠道" dialog 的模型下拉走 remote search.
// 原因: listModels 默认只返前 50 条 (后端 cap 200), 当模型总量 3000+ 时
// 直接 v-for 整个 models 数组会丢掉绝大部分 (含本次想配置的 bge-m3 等).
const modelSearch = ref<Model[]>([])
const modelSearchLoading = ref(false)
async function searchModels(query: string) {
  // 空查询时回填当前已选模型 (编辑场景) + 顶层 models 兜底, 避免下拉一打开
  // 全空; 输入超过 1 字时走后端 q= 过滤.
  if (!query) {
    modelSearch.value = models.value.slice(0, 50)
    return
  }
  modelSearchLoading.value = true
  try {
    const r = await api.listModels({ q: query, page_size: 50 })
    modelSearch.value = r.items
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    modelSearchLoading.value = false
  }
}

const modelById = computed(() => {
  const m = new Map<string, Model>()
  for (const x of models.value) m.set(x.id, x)
  return m
})

// v0.3: 客户端 mode 过滤 — 后端 listChannels 不接 mode 参数 (走 model_id),
// 但 channel 里的 model_id 我们已经能 lookup 到 model.mode, 客户端筛掉
// 不属于该 mode 的 channel 即可. 数据量 < 几百行, 性能不是问题.
const filteredChannels = computed(() => {
  if (!filterMode.value) return channels.value
  return channels.value.filter((ch) => {
    const m = modelById.value.get(ch.model_id)
    return m?.mode === filterMode.value
  })
})

// 渠道里 model.mode 显示用的标签 — 与 ModelDetailDrawer.MODE_LABEL 同步.
const MODE_LABEL: Record<ModelMode, string> = {
  chat: '对话',
  embedding: '向量',
  rerank: '重排',
  image_generation: '图片',
  video_generation: '视频',
  digital_human: '数字人',
  audio_speech: 'TTS',
  audio_transcription: 'ASR',
  hotparse: '爆款',
  responses: 'Resp',
}
// 不同 mode 给个底色, 让一眼分辨同 provider 下的多模态 channel
const MODE_TAG_TYPE: Record<ModelMode, 'success' | 'info' | 'warning' | 'danger' | 'primary'> = {
  chat: 'primary',
  embedding: 'info',
  rerank: 'success',
  image_generation: 'warning',
  video_generation: 'warning',
  digital_human: 'warning',
  audio_speech: 'success',
  audio_transcription: 'info',
  hotparse: 'danger',
  responses: 'primary',
}

// upstream_model 字段的 hint — 客户端选模型后给点提示, 帮用户少猜上游名字.
// 这些是常见模式, 不强制.
const UPSTREAM_HINT: Record<ModelMode, string> = {
  chat: '上游模型 ID, 例如 gpt-4o-2024-11-20 / claude-haiku-4-5 / deepseek-chat',
  embedding: '例如 bge-m3 / text-embedding-3-small / BAAI/bge-m3 (按 SiliconFlow 命名)',
  rerank: '例如 gte-rerank-v2 (阿里云百炼) / BAAI/bge-reranker-v2-m3 (SiliconFlow)',
  image_generation: '例如 wanx2.0-t2i-turbo (阿里云) / dall-e-3 (OpenAI)',
  video_generation: '例如 wanx2.1-i2v-turbo / kling-1.6-std',
  digital_human: '例如 sambert-zhuxiang-v1',
  audio_speech: '例如 cosyvoice-v3-flash / cosyvoice-v3-plus / tts-1-hd',
  audio_transcription: '例如 paraformer-v2 / whisper-large-v3',
  hotparse: '上游模型 ID',
  responses: '上游模型 ID',
}
const credById = computed(() => {
  const m = new Map<string, CredentialSafe>()
  for (const c of credentials.value) m.set(c.id, c)
  return m
})

// ─── dialog state ───────────────────────────────────────────────
type DialogMode = 'create' | 'edit'
const dialogVisible = ref(false)
const dialogMode = ref<DialogMode>('create')
const editingId = ref<string>('')

const form = reactive<ChannelInput & { id?: string }>({
  model_id: '',
  credential_id: '',
  upstream_model: '',
  priority: 100,
  weight: 1,
  rpm_limit: 0,
  tpm_limit: 0,
  status: 'active' as EntityStatus,
})

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = ''
  Object.assign(form, {
    model_id: filterModelId.value || '',
    credential_id: credentials.value[0]?.id || '',
    upstream_model: '',
    priority: 100,
    weight: 1,
    rpm_limit: 0,
    tpm_limit: 0,
    status: 'active' as EntityStatus,
  })
  // 种子: 顶层 models (前 200 条) 让用户清空搜索框时仍有候选; 真正的搜索
  // 走 searchModels remote.
  modelSearch.value = models.value.slice(0, 50)
  // 如果是从模型详情跳过来 (filterModelId 已设), 确保选中项在 modelSearch 里.
  if (filterModelId.value) {
    const cur = models.value.find((m) => m.id === filterModelId.value)
    if (cur && !modelSearch.value.some((m) => m.id === cur.id)) {
      modelSearch.value = [cur, ...modelSearch.value]
    }
  }
  dialogVisible.value = true
}

function openEdit(row: Channel) {
  dialogMode.value = 'edit'
  editingId.value = row.id
  Object.assign(form, {
    model_id: row.model_id,
    credential_id: row.credential_id,
    upstream_model: row.upstream_model,
    priority: row.priority,
    weight: row.weight,
    rpm_limit: row.rpm_limit,
    tpm_limit: row.tpm_limit,
    status: row.status,
  })
  // 编辑场景: model select 是 disabled 但仍要显示 label, 必须保证当前选中
  // 项在 modelSearch 里. 优先从已加载的 models (前 200) 找; 找不到就单独
  // 拉一次详情补进去.
  const cur = models.value.find((m) => m.id === row.model_id)
  if (cur) {
    modelSearch.value = [cur]
  } else {
    modelSearch.value = []
    api
      .getModel(row.model_id)
      .then((d) => {
        modelSearch.value = [d.model]
      })
      .catch(() => {
        /* ignore — disabled select 显示 UUID 也不影响保存 */
      })
  }
  dialogVisible.value = true
}

async function onSave() {
  if (!form.upstream_model.trim()) {
    ElMessage.warning('请填写上游模型 ID')
    return
  }
  if (!form.model_id || !form.credential_id) {
    ElMessage.warning('请选择模型和凭证')
    return
  }
  try {
    if (dialogMode.value === 'create') {
      await api.createChannel(form)
      ElMessage.success('渠道已创建')
    } else {
      await api.updateChannel(editingId.value, form)
      ElMessage.success('渠道已保存')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

async function onDelete(row: Channel) {
  await ElMessageBox.confirm(
    `确认删除渠道 "${modelById.value.get(row.model_id)?.code ?? row.model_id} → ${row.upstream_model}"？`,
    '删除渠道',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
  )
    .then(async () => {
      try {
        await api.deleteChannel(row.id)
        ElMessage.success('已删除')
        await load()
      } catch (e) {
        ElMessage.error(errorMessage(e))
      }
    })
    .catch(() => {})
}

// ─── batch ops ──────────────────────────────────────────────────
const selection = ref<Channel[]>([])
function onSelectionChange(rows: Channel[]) {
  selection.value = rows
}

async function batchSetStatus(target: EntityStatus) {
  const ids = selection.value.map((c) => c.id)
  if (!ids.length) {
    ElMessage.info('请先勾选渠道')
    return
  }
  await ElMessageBox.confirm(
    `将选中的 ${ids.length} 条渠道状态改为 "${target}"？`,
    '批量操作',
    { confirmButtonText: '确定', cancelButtonText: '取消' },
  )
    .then(async () => {
      try {
        // Sequential — preserves order; admin batch ops are tiny (< 50 rows)
        for (const id of ids) {
          const ch = channels.value.find((c) => c.id === id)
          if (!ch) continue
          await api.updateChannel(id, {
            model_id: ch.model_id,
            credential_id: ch.credential_id,
            upstream_model: ch.upstream_model,
            priority: ch.priority,
            weight: ch.weight,
            rpm_limit: ch.rpm_limit,
            tpm_limit: ch.tpm_limit,
            status: target,
          })
        }
        ElMessage.success(`已更新 ${ids.length} 条`)
        selection.value = []
        await load()
      } catch (e) {
        ElMessage.error(errorMessage(e))
      }
    })
    .catch(() => {})
}

// ─── inline test ────────────────────────────────────────────────
const testingId = ref<string>('')
async function onTest(row: Channel) {
  testingId.value = row.id
  try {
    const res = await api.testChannel(row.id)
    if (res.ok) {
      ElMessage.success(`✓ ${row.upstream_model} 通畅 (${res.latency_ms}ms)`)
    } else {
      ElMessage.error(`✗ ${res.error_code}: ${res.error}`)
    }
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    testingId.value = ''
  }
}

// ─── helpers ────────────────────────────────────────────────────
const STATUS_LABEL: Record<string, string> = {
  active: '启用',
  disabled: '禁用',
  auto_disabled: '自动降级',
}
const STATUS_TYPE: Record<string, 'success' | 'info' | 'warning'> = {
  active: 'success',
  disabled: 'info',
  auto_disabled: 'warning',
}

function fmtTime(iso?: string) {
  return iso ? new Date(iso).toLocaleString('zh-CN', { hour12: false }) : '—'
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1>渠道管理</h1>
      <span class="hint">每条渠道 = "model × credential × upstream_model"，路由的最小单元</span>
      <div class="header-spacer" />
      <el-button v-if="canWrite" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        新建渠道
      </el-button>
    </div>

    <el-card shadow="never">
      <div class="filters">
        <el-select
          v-model="filterMode"
          clearable
          placeholder="全部模态"
          style="width: 140px"
        >
          <el-option label="对话 (chat)" value="chat" />
          <el-option label="向量 (embedding)" value="embedding" />
          <el-option label="重排 (rerank)" value="rerank" />
          <el-option label="语音合成 (TTS)" value="audio_speech" />
          <el-option label="语音识别 (ASR)" value="audio_transcription" />
          <el-option label="图片生成" value="image_generation" />
          <el-option label="视频生成" value="video_generation" />
          <el-option label="数字人" value="digital_human" />
          <el-option label="爆款解析" value="hotparse" />
          <el-option label="Responses" value="responses" />
        </el-select>
        <el-select
          v-model="filterModelId"
          clearable
          filterable
          placeholder="全部模型"
          style="width: 240px"
          @change="load"
          @clear="load"
        >
          <el-option v-for="m in models" :key="m.id" :label="m.code" :value="m.id" />
        </el-select>
        <el-select
          v-model="filterCredentialId"
          clearable
          filterable
          placeholder="全部凭证"
          style="width: 200px"
          @change="load"
          @clear="load"
        >
          <el-option v-for="c in credentials" :key="c.id" :label="c.label" :value="c.id" />
        </el-select>
        <el-select
          v-model="filterStatus"
          clearable
          placeholder="全部状态"
          style="width: 140px"
          @change="load"
          @clear="load"
        >
          <el-option label="启用" value="active" />
          <el-option label="禁用" value="disabled" />
          <el-option label="自动降级" value="auto_disabled" />
        </el-select>

        <span class="muted small" style="margin-left: auto">
          共 {{ filteredChannels.length }}<span v-if="filterMode"> / {{ channels.length }}</span> 条
        </span>

        <template v-if="canWrite && selection.length">
          <span class="batch-count">已选 {{ selection.length }}</span>
          <el-button size="small" @click="batchSetStatus('active')">批量启用</el-button>
          <el-button size="small" @click="batchSetStatus('disabled')">批量禁用</el-button>
        </template>
      </div>

      <el-table
        :data="filteredChannels"
        v-loading="loading"
        stripe
        style="margin-top: 12px"
        @selection-change="onSelectionChange"
      >
        <el-table-column type="selection" width="40" v-if="canWrite" />
        <el-table-column label="模态" width="92">
          <template #default="{ row }: { row: Channel }">
            <el-tag
              v-if="modelById.get(row.model_id)?.mode"
              size="small"
              :type="MODE_TAG_TYPE[modelById.get(row.model_id)!.mode] ?? 'info'"
              effect="plain"
            >
              {{ MODE_LABEL[modelById.get(row.model_id)!.mode] ?? modelById.get(row.model_id)!.mode }}
            </el-tag>
            <span v-else class="muted small">—</span>
          </template>
        </el-table-column>
        <el-table-column label="模型" min-width="200">
          <template #default="{ row }: { row: Channel }">
            <code class="model-code">
              {{ modelById.get(row.model_id)?.code ?? row.model_id.slice(0, 8) }}
            </code>
          </template>
        </el-table-column>
        <el-table-column label="上游模型" prop="upstream_model" min-width="200">
          <template #default="{ row }: { row: Channel }">
            <code class="upstream">{{ row.upstream_model }}</code>
          </template>
        </el-table-column>
        <el-table-column label="凭证" min-width="160">
          <template #default="{ row }: { row: Channel }">
            {{ credById.get(row.credential_id)?.label ?? row.credential_id.slice(0, 8) }}
          </template>
        </el-table-column>
        <el-table-column label="优先级" width="90" align="center" sortable>
          <template #default="{ row }: { row: Channel }">{{ row.priority }}</template>
        </el-table-column>
        <el-table-column label="权重" width="80" align="center">
          <template #default="{ row }: { row: Channel }">{{ row.weight }}</template>
        </el-table-column>
        <el-table-column label="限流 R/T" width="120" align="center">
          <template #default="{ row }: { row: Channel }">
            <span class="muted small">
              {{ row.rpm_limit || '∞' }} / {{ row.tpm_limit || '∞' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="健康" width="200">
          <template #default="{ row }: { row: Channel }">
            <div class="health-cell">
              <el-tag size="small" :type="STATUS_TYPE[row.status] ?? 'info'">
                {{ STATUS_LABEL[row.status] ?? row.status }}
              </el-tag>
              <span v-if="row.latency_p50_ms" class="muted small">
                {{ row.latency_p50_ms }}ms
              </span>
              <span v-if="row.failure_count" class="fail-count">
                ✗ {{ row.failure_count }}
              </span>
            </div>
            <div v-if="row.last_error" class="muted small last-err" :title="row.last_error">
              {{ row.last_error }}
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }: { row: Channel }">
            <el-button
              link
              type="primary"
              :loading="testingId === row.id"
              @click="onTest(row)"
            >测试</el-button>
            <el-button v-if="canWrite" link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button v-if="canWrite" link type="danger" @click="onDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-if="!loading && channels.length === 0" description="暂无渠道" />
    </el-card>

    <!-- Create / Edit dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'create' ? '新建渠道' : '编辑渠道'"
      width="540px"
      :close-on-click-modal="false"
    >
      <el-form label-width="120px">
        <el-form-item label="模型" required>
          <el-select
            v-model="form.model_id"
            filterable
            remote
            :remote-method="searchModels"
            :loading="modelSearchLoading"
            :disabled="dialogMode === 'edit'"
            placeholder="输入模型名搜索 (如 bge-m3 / gpt-4o)"
            style="width: 100%"
          >
            <el-option v-for="m in modelSearch" :key="m.id" :label="m.code" :value="m.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="凭证" required>
          <el-select v-model="form.credential_id" filterable style="width: 100%">
            <el-option v-for="c in credentials" :key="c.id" :label="c.label" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="上游模型" required>
          <el-input
            v-model="form.upstream_model"
            :placeholder="
              UPSTREAM_HINT[modelById.get(form.model_id)?.mode ?? 'chat']
              ?? '上游模型 ID'
            "
          />
          <span v-if="modelById.get(form.model_id)?.mode" class="form-hint">
            {{ UPSTREAM_HINT[modelById.get(form.model_id)!.mode] }}
          </span>
        </el-form-item>
        <el-form-item label="优先级">
          <el-input-number v-model="form.priority" :min="0" :step="10" controls-position="right" />
          <span class="form-hint">越大越优先；同 priority 内按 weight 加权随机</span>
        </el-form-item>
        <el-form-item label="权重">
          <el-input-number v-model="form.weight" :min="0" :step="1" controls-position="right" />
          <span class="form-hint">同一 priority tier 内的相对权重</span>
        </el-form-item>
        <el-form-item label="RPM 限流">
          <el-input-number v-model="form.rpm_limit" :min="0" :step="10" controls-position="right" />
          <span class="form-hint">0 = 不限</span>
        </el-form-item>
        <el-form-item label="TPM 限流">
          <el-input-number v-model="form.tpm_limit" :min="0" :step="1000" controls-position="right" />
          <span class="form-hint">0 = 不限</span>
        </el-form-item>
        <el-form-item label="状态">
          <el-radio-group v-model="form.status">
            <el-radio label="active">启用</el-radio>
            <el-radio label="disabled">禁用</el-radio>
          </el-radio-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="onSave">保存</el-button>
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
  .header-spacer { flex: 1; }
}
.filters {
  display: flex;
  gap: 12px;
  align-items: center;
  .batch-count {
    color: #1f2937;
    font-size: 13px;
    margin-left: 12px;
  }
}
.muted { color: #6b7280; font-size: 13px; }
.muted.small { font-size: 12px; }
.model-code,
.upstream {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  font-size: 12px;
  background: #f3f4f6;
  padding: 2px 6px;
  border-radius: 4px;
}
.health-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  .fail-count { color: #dc2626; font-size: 12px; }
}
.last-err {
  color: #b45309;
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 220px;
}
.form-hint {
  margin-left: 12px;
  color: #6b7280;
  font-size: 12px;
}
</style>
