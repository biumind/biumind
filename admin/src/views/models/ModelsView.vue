<script setup lang="ts">
// ModelsView — P4 段 4 / F2.4 单源模型管理.
//
// 之前: chat 走 model_relay, 其他 mode (image/video/digital_human/hotparse)
//       走 /v1/admin/aigc/* compat 层 (AigcModelsTable + AigcModelDetailDrawer).
//       双视图、字段不一, 维护成本翻倍.
//
// 现在: 一个 ModelsTable + 一个 ModelDetailDrawer 处理 10 mode + "全部";
//       mode 通过 dropdown 切换, 列按 mode 自适应 (F2.2), 详情按 mode 自适应 (F2.3).
//
// v0.3 M2.5: 加「新建模型」按钮 — 上游同步源没收录的模型 (例如阿里云百炼
// qwen3-rerank, dashscope cosyvoice-v3-plus 等) 用户可手动添加.

import { ref, reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import type { Model, ModelInput, ModelMode } from '@/api/modelRelay.types'
import * as api from '@/api/modelRelay'
import { errorMessage } from '@/api/http'
import ModelsTable from './ModelsTable.vue'
import ModelDetailDrawer from './ModelDetailDrawer.vue'

type ModeOption = ModelMode | 'all'

const mode = ref<ModeOption>('chat')
const modeOptions: Array<{ value: ModeOption; label: string; group?: string }> = [
  { value: 'all', label: '全部模型' },
  { value: 'chat', label: '对话 (LLM)' },
  { value: 'embedding', label: '向量 (Embedding)' },
  { value: 'rerank', label: '重排 (Rerank)' },
  { value: 'image_generation', label: '图片生成' },
  { value: 'video_generation', label: '视频生成' },
  { value: 'digital_human', label: '数字人' },
  { value: 'audio_speech', label: '语音合成 (TTS)' },
  { value: 'audio_transcription', label: '语音识别 (ASR)' },
  { value: 'hotparse', label: '爆款解析' },
  { value: 'responses', label: 'Responses (Agent)' },
]

const drawerVisible = ref(false)
const detailModelId = ref('')

function onOpenDetail(row: Model) {
  detailModelId.value = row.id
  drawerVisible.value = true
}

const tableRef = ref<InstanceType<typeof ModelsTable> | null>(null)
function onDrawerSaved() {
  drawerVisible.value = false
  tableRef.value?.load?.()
}

// 各 mode 在 model-relay 对应的 HTTP 端点 — 用户选 mode 时直接看到
// 客户端会打到哪个 URL.
const MODE_ENDPOINT: Record<string, string> = {
  all: '',
  chat: 'POST /v1/messages 或 /v1/chat/completions',
  embedding: 'POST /v1/embeddings',
  rerank: 'POST /v1/rerank',
  image_generation: 'POST /v1/images/generations',
  video_generation: 'POST /v1/jobs (异步)',
  digital_human: 'POST /v1/jobs (异步)',
  audio_speech: 'POST /v1/audio/speech',
  audio_transcription: 'POST /v1/audio/transcriptions (M4)',
  hotparse: 'POST /v1/jobs (异步)',
  responses: 'POST /v1/responses',
}

const HINT: Record<string, string> = {
  all: '全部模态;mode 列在表内显示',
  chat: '客户端能选择的 LLM 模型 (绑 channel 后才能用)',
  embedding: '向量模型 (rag / search) — 按 token 计费',
  rerank: '重排模型 (RAG 第二阶段) — 按 search_unit 计费',
  image_generation: '图片生成模型 — 按张计费, 可用 pricing rule 加分辨率系数',
  video_generation: '视频生成模型 — 按秒计费, 通常多维乘数 (时长×分辨率)',
  digital_human: '数字人 — 按音频秒或字符计费, dispatch_mode=async',
  audio_speech: 'TTS 语音合成 — 按音频秒计费',
  audio_transcription: 'ASR 语音识别 — 按音频秒计费',
  hotparse: '爆款解析 — 按调用次数计费 (cost_per_image)',
  responses: 'OpenAI Responses API (Agent 任务态)',
}

// ─── 新建模型对话框 ──────────────────────────────────────────────────
//
// 上游同步源 (basellm.github.io 等) 不收录的模型走这里. 比如:
//   - qwen3-rerank          (阿里云百炼有, 上游索引缺)
//   - cosyvoice-v3-plus     (阿里云百炼 TTS)
//   - 自部署 TEI / Ollama 私有模型
//
// 创建后 manual_override=true (后端默认), 下次 sync-upstream 不会覆盖.

const createDialog = ref(false)
const creating = ref(false)
const form = reactive<ModelInput>({
  code: '',
  display_name: '',
  mode: 'chat',
  family: '',
  context_window: 0,
  max_output: 0,
  min_plan: 'free',
  status: 'active',
})

function openCreate() {
  // 用当前选中的 mode 作为默认值, 用户切到 rerank 选项再点新建会预填 rerank
  const m = mode.value === 'all' ? 'chat' : (mode.value as ModelMode)
  form.code = ''
  form.display_name = ''
  form.mode = m
  form.family = ''
  form.context_window = 0
  form.max_output = 0
  form.min_plan = 'free'
  form.status = 'active'
  createDialog.value = true
}

async function onSubmitCreate() {
  if (!form.code || !form.display_name) {
    ElMessage.warning('代码和显示名必填')
    return
  }
  creating.value = true
  try {
    const created = await api.createModel({ ...form, manual_override: true })
    ElMessage.success(`已创建: ${created.code}`)
    createDialog.value = false
    tableRef.value?.load?.()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <div>
    <div class="page-header">
      <h1>模型管理</h1>
      <span class="hint">{{ HINT[mode] ?? '' }}</span>
      <div class="header-spacer" />
      <el-button type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        新建模型
      </el-button>
      <el-select v-model="mode" style="width: 200px" placeholder="选择模态">
        <el-option
          v-for="opt in modeOptions"
          :key="opt.value"
          :label="opt.label"
          :value="opt.value"
        />
      </el-select>
    </div>

    <el-card shadow="never">
      <ModelsTable
        ref="tableRef"
        :mode="mode"
        :key="mode"
        @open-detail="onOpenDetail"
      />
    </el-card>

    <ModelDetailDrawer
      v-model:visible="drawerVisible"
      :model-id="detailModelId"
      @saved="onDrawerSaved"
    />

    <!-- 新建模型对话框 — 走 POST /v1/admin/models, manual_override=true -->
    <el-dialog
      v-model="createDialog"
      title="新建模型"
      width="540px"
      :close-on-click-modal="false"
    >
      <el-form label-width="100px">
        <el-form-item label="代码" required>
          <el-input
            v-model="form.code"
            placeholder="如 qwen3-rerank / cosyvoice-v3-plus / bge-m3"
            maxlength="128"
          />
          <div class="form-hint">业务唯一标识, 客户端用此 code 调用 API</div>
        </el-form-item>
        <el-form-item label="显示名" required>
          <el-input
            v-model="form.display_name"
            placeholder="如 通义千问3 Rerank / CosyVoice v3 Plus"
            maxlength="200"
          />
        </el-form-item>
        <el-form-item label="模态" required>
          <el-select v-model="form.mode" style="width: 100%">
            <el-option
              v-for="opt in modeOptions.filter(o => o.value !== 'all')"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            >
              <span>{{ opt.label }}</span>
              <span class="mode-endpoint">{{ MODE_ENDPOINT[opt.value] ?? '' }}</span>
            </el-option>
          </el-select>
          <div class="form-hint">
            <div>决定路由分发到哪条链路:</div>
            <div class="endpoint-line">
              <code>{{ MODE_ENDPOINT[form.mode ?? 'chat'] ?? '—' }}</code>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="Family">
          <el-input
            v-model="form.family"
            placeholder="如 qwen / claude / cosyvoice (用于聚合统计)"
            maxlength="64"
          />
        </el-form-item>
        <el-form-item label="最低套餐">
          <el-radio-group v-model="form.min_plan">
            <el-radio label="free">Free</el-radio>
            <el-radio label="pro">Pro</el-radio>
            <el-radio label="team">Team</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="状态">
          <el-radio-group v-model="form.status">
            <el-radio label="active">启用</el-radio>
            <el-radio label="disabled">禁用</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.mode === 'chat'" label="上下文窗口">
          <el-input-number v-model="form.context_window" :min="0" :step="1024" />
          <div class="form-hint">tokens, 0 表示未知 / 不限</div>
        </el-form-item>
        <el-form-item v-if="form.mode === 'chat'" label="最大输出">
          <el-input-number v-model="form.max_output" :min="0" :step="1024" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="onSubmitCreate">
          创建
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.page-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  h1 { margin: 0; font-size: 22px; font-weight: 600; }
  .hint { color: #6b7280; font-size: 13px; }
  .header-spacer { flex: 1; }
}
.form-hint {
  color: #6b7280;
  font-size: 12px;
  margin-top: 2px;
}
.form-hint .endpoint-line {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  margin-top: 4px;
}
.form-hint .endpoint-line code {
  background: #f3f4f6;
  padding: 2px 6px;
  border-radius: 4px;
  color: #374151;
}
.mode-endpoint {
  margin-left: 12px;
  color: #9ca3af;
  font-size: 12px;
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
}
</style>
