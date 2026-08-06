<script setup lang="ts">
// Providers — 上游 LLM 服务目录。
//
// MVP scope:
//   - 列表展示 + 引用计数（多少凭证用它）
//   - 新建 / 编辑 / 删除（write 权限的人才有按钮）
//
// 删除走后端 RESTRICT — 还有凭证引用时返回 409，前端把错误透传给用户。

import { ref, onMounted, computed, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { errorMessage } from '@/api/http'
import * as api from '@/api/modelRelay'
import type {
  Provider,
  ProviderInput,
  ProviderProtocol,
  CredentialSafe,
  EntityStatus,
} from '@/api/modelRelay.types'
import { usePermission } from '@/composables/usePermission'

const { can } = usePermission()
const canWrite = can('models:write')

const providers = ref<Provider[]>([])
const credentials = ref<CredentialSafe[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const [p, c] = await Promise.all([api.listProviders(), api.listCredentials()])
    providers.value = p.items
    credentials.value = c.items
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

const credentialCountByProvider = computed(() => {
  const m = new Map<string, number>()
  for (const c of credentials.value) {
    m.set(c.provider_id, (m.get(c.provider_id) ?? 0) + 1)
  }
  return m
})

// ─── dialog state ──────────────────────────────────────────────────
type DialogMode = 'create' | 'edit'
const dialogVisible = ref(false)
const dialogMode = ref<DialogMode>('create')
const editingId = ref<string>('')

const form = reactive<ProviderInput>({
  code: '',
  name: '',
  protocol: 'openai_compat' as ProviderProtocol,
  icon: '',
  description: '',
  status: 'active' as EntityStatus,
})

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = ''
  Object.assign(form, {
    code: '',
    name: '',
    protocol: 'openai_compat',
    icon: '',
    description: '',
    status: 'active',
  })
  dialogVisible.value = true
}

function openEdit(row: Provider) {
  dialogMode.value = 'edit'
  editingId.value = row.id
  Object.assign(form, {
    code: row.code,
    name: row.name,
    protocol: row.protocol,
    icon: row.icon,
    description: row.description,
    status: row.status,
  })
  dialogVisible.value = true
}

async function onSave() {
  if (!form.code.trim()) {
    ElMessage.warning('请填写代码')
    return
  }
  if (!form.name.trim()) {
    ElMessage.warning('请填写显示名')
    return
  }
  try {
    if (dialogMode.value === 'create') {
      await api.createProvider(form)
      ElMessage.success('供应商已创建')
    } else {
      await api.updateProvider(editingId.value, form)
      ElMessage.success('供应商已更新')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

async function onDelete(row: Provider) {
  const usage = credentialCountByProvider.value.get(row.id) ?? 0
  if (usage > 0) {
    ElMessage.warning(`该供应商被 ${usage} 条凭证使用中，请先删除凭证`)
    return
  }
  await ElMessageBox.confirm(`确认删除供应商 "${row.name}"？`, '删除供应商', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  })
    .then(async () => {
      try {
        await api.deleteProvider(row.id)
        ElMessage.success('已删除')
        await load()
      } catch (e) {
        ElMessage.error(errorMessage(e))
      }
    })
    .catch(() => {})
}

const PROTOCOL_LABELS: Record<string, string> = {
  openai_compat: 'OpenAI 兼容',
  anthropic: 'Anthropic',
  dashscope: 'DashScope (阿里云百炼)',
}

function fmtTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1>供应商</h1>
      <span class="hint">上游 LLM 服务目录。系统种子提供 9 个常用供应商；可自建。</span>
      <div class="header-spacer" />
      <el-button v-if="canWrite" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        新建供应商
      </el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="providers" v-loading="loading" stripe>
        <el-table-column label="代码" prop="code" min-width="160">
          <template #default="{ row }: { row: Provider }">
            <code class="code">{{ row.code }}</code>
          </template>
        </el-table-column>
        <el-table-column label="显示名" prop="name" min-width="160" />
        <el-table-column label="协议" width="140">
          <template #default="{ row }: { row: Provider }">
            <el-tag size="small" effect="plain">
              {{ PROTOCOL_LABELS[row.protocol] ?? row.protocol }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }: { row: Provider }">
            <el-tag size="small" :type="row.status === 'active' ? 'success' : 'info'">
              {{ row.status === 'active' ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="凭证数" width="100" align="center">
          <template #default="{ row }: { row: Provider }">
            <span class="cred-count" :class="{ zero: !credentialCountByProvider.get(row.id) }">
              {{ credentialCountByProvider.get(row.id) ?? 0 }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="描述" prop="description" min-width="240" show-overflow-tooltip />
        <el-table-column label="更新时间" width="180">
          <template #default="{ row }: { row: Provider }">
            <span class="muted">{{ fmtTime(row.updated_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right" v-if="canWrite">
          <template #default="{ row }: { row: Provider }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="onDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty
        v-if="!loading && providers.length === 0"
        description="尚无供应商，点击右上角「新建供应商」开始"
      >
        <el-button v-if="canWrite" type="primary" @click="openCreate">新建供应商</el-button>
      </el-empty>
    </el-card>

    <!-- Create / Edit dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'create' ? '新建供应商' : '编辑供应商'"
      width="520px"
      :close-on-click-modal="false"
    >
      <el-form label-width="100px">
        <el-form-item label="代码" required>
          <el-input
            v-model="form.code"
            :disabled="dialogMode === 'edit'"
            placeholder="如 openai / anthropic / dashscope / azure-eastus"
            maxlength="64"
          />
          <div class="form-hint">业务唯一标识，创建后不可改</div>
        </el-form-item>
        <el-form-item label="显示名" required>
          <el-input v-model="form.name" placeholder="如 OpenAI" maxlength="128" />
        </el-form-item>
        <el-form-item label="协议" required>
          <el-radio-group v-model="form.protocol">
            <el-radio label="openai_compat">OpenAI 兼容</el-radio>
            <el-radio label="anthropic">Anthropic</el-radio>
            <el-radio label="dashscope">DashScope</el-radio>
            <el-radio label="volcengine">VolcEngine</el-radio>
          </el-radio-group>
          <div class="form-hint">
            <div>OpenAI 兼容: 覆盖 OpenAI / Azure / DeepSeek / Kimi / OpenRouter / 阿里云百炼 chat 模型等</div>
            <div>Anthropic: Claude 系列原生协议</div>
            <div>
              DashScope: 阿里云百炼私有协议 — 仅 cosyvoice (TTS) / paraformer (ASR) /
              wanx (图像/视频) 等 AIGC 模型用. 阿里云上的 chat 模型请用 OpenAI 兼容.
            </div>
            <div>
              VolcEngine: 火山引擎豆包 Ark 私有协议 — Seedream (文生图) /
              Seedance (文生视频). Doubao chat 模型请用 OpenAI 兼容.
            </div>
          </div>
        </el-form-item>
        <el-form-item label="描述">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="2"
            placeholder="（可选）"
          />
        </el-form-item>
        <el-form-item label="图标">
          <el-input v-model="form.icon" placeholder="（可选）公开 URL 或内置 key" />
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
.code {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  font-size: 13px;
  background: #f3f4f6;
  padding: 2px 6px;
  border-radius: 4px;
}
.cred-count {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  &.zero { color: #9ca3af; }
}
.muted { color: #6b7280; font-size: 13px; }
.form-hint {
  margin-top: 4px;
  color: #6b7280;
  font-size: 12px;
}
</style>
