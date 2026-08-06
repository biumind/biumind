<script setup lang="ts">
// Credentials — admin entrance for upstream API keys.
//
// Critical security:
//   - Plaintext is collected only in the create / rotate forms, sent
//     once over HTTPS, never returned by GET. The list table reads
//     `key_preview` ("sk-12...abcd") instead — that's the most we
//     ever surface.
//   - Edit dialog leaves plaintext input EMPTY by default. Filling it
//     in is opt-in rotation; otherwise only metadata (label / base_url
//     / header / status) is patched.
//   - Test button stamps last_test_at + status onto the row but never
//     decrypts on the wire.

import { ref, onMounted, computed, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { errorMessage } from '@/api/http'
import * as api from '@/api/modelRelay'
import type {
  CredentialSafe,
  Provider,
  ProbeResult,
  CredentialCreateRequest,
  CredentialUpdateRequest,
  EntityStatus,
} from '@/api/modelRelay.types'
import { usePermission } from '@/composables/usePermission'

const { can } = usePermission()
const canWrite = can('model_credentials:write')

const credentials = ref<CredentialSafe[]>([])
const providers = ref<Provider[]>([])
const loading = ref(false)
const search = ref('')
const filterProviderId = ref('')
const filterStatus = ref('')

async function load() {
  loading.value = true
  try {
    const [c, p] = await Promise.all([
      api.listCredentials({
        provider_id: filterProviderId.value || undefined,
        status: filterStatus.value || undefined,
        q: search.value || undefined,
      }),
      api.listProviders({ status: 'active' }),
    ])
    credentials.value = c.items
    providers.value = p.items
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

const providerById = computed(() => {
  const m = new Map<string, Provider>()
  for (const p of providers.value) m.set(p.id, p)
  return m
})

let searchTimer: ReturnType<typeof setTimeout> | null = null
function onSearch() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(load, 300)
}

// ─── dialog state ───────────────────────────────────────────────
type DialogMode = 'create' | 'edit'
const dialogVisible = ref(false)
const dialogMode = ref<DialogMode>('create')
const editingId = ref<string>('')

const form = reactive({
  provider_id: '',
  label: '',
  plaintext: '',
  base_url: '',
  header_override_text: '', // user-edited JSON string, parsed on save
  status: 'active' as EntityStatus,
})

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = ''
  Object.assign(form, {
    provider_id: providers.value[0]?.id ?? '',
    label: '',
    plaintext: '',
    base_url: '',
    header_override_text: '',
    status: 'active' as EntityStatus,
  })
  dialogVisible.value = true
}

function openEdit(row: CredentialSafe) {
  dialogMode.value = 'edit'
  editingId.value = row.id
  Object.assign(form, {
    provider_id: row.provider_id,
    label: row.label,
    plaintext: '',
    base_url: row.base_url,
    header_override_text:
      Object.keys(row.header_override ?? {}).length > 0
        ? JSON.stringify(row.header_override, null, 2)
        : '',
    status: row.status,
  })
  dialogVisible.value = true
}

async function onSave() {
  if (!form.label.trim()) {
    ElMessage.warning('请填写标签')
    return
  }
  let header: Record<string, string> | undefined
  if (form.header_override_text.trim()) {
    try {
      header = JSON.parse(form.header_override_text)
    } catch {
      ElMessage.error('header 必须是合法 JSON')
      return
    }
  }
  try {
    if (dialogMode.value === 'create') {
      if (!form.plaintext) {
        ElMessage.warning('请粘贴上游 API Key')
        return
      }
      const body: CredentialCreateRequest = {
        provider_id: form.provider_id,
        label: form.label,
        plaintext: form.plaintext,
        base_url: form.base_url || undefined,
        header_override: header,
        status: form.status,
      }
      await api.createCredential(body)
      ElMessage.success('凭证创建成功')
    } else {
      const body: CredentialUpdateRequest = {
        label: form.label,
        base_url: form.base_url,
        header_override: header,
        status: form.status,
      }
      if (form.plaintext) body.plaintext = form.plaintext
      await api.updateCredential(editingId.value, body)
      ElMessage.success(form.plaintext ? '凭证已轮换并更新' : '凭证已更新')
    }
    dialogVisible.value = false
    // 立刻清掉 plaintext 字段，最小化在内存里的存活时间
    form.plaintext = ''
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

function onDialogClose() {
  // 防止 plaintext 在关闭后仍留在内存
  form.plaintext = ''
}

// ─── delete ─────────────────────────────────────────────────────
async function onDelete(row: CredentialSafe) {
  await ElMessageBox.confirm(
    `确认删除凭证 "${row.label}"？关联渠道存在时会拒绝。`,
    '删除凭证',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
  )
    .then(async () => {
      try {
        await api.deleteCredential(row.id)
        ElMessage.success('已删除')
        await load()
      } catch (e) {
        ElMessage.error(errorMessage(e))
      }
    })
    .catch(() => {})
}

// ─── test ───────────────────────────────────────────────────────
const testingId = ref<string>('')
const TEST_MODEL_KEY = 'biumind.admin.cred_test_model'

// 默认测试模型按 provider.protocol 判断；用户上次指定的会被记住，
// 因为不同 endpoint（XXLab/Azure 等）支持的模型差异很大，每次都问
// 一遍很烦。
function defaultTestModelFor(row: CredentialSafe): string {
  const cached = typeof localStorage !== 'undefined'
    ? localStorage.getItem(`${TEST_MODEL_KEY}.${row.provider_id}`)
    : null
  if (cached) return cached
  const prov = providerById.value.get(row.provider_id)
  return prov?.protocol === 'anthropic' ? 'claude-haiku-4-5' : 'gpt-4o-mini'
}

async function onTest(row: CredentialSafe) {
  // 先让用户确认 / 修改测试模型 — 不同 endpoint 支持的模型差异很大,
  // 默认 gpt-4o-mini 在很多分发型 key 上没开放, 需要指定。
  let testModel: string
  try {
    const result = await ElMessageBox.prompt(
      `将向 ${row.label} 的 endpoint 发送一次 "hello" 探测请求。`,
      '测试凭证',
      {
        confirmButtonText: '测试',
        cancelButtonText: '取消',
        inputValue: defaultTestModelFor(row),
        inputPlaceholder: '上游真实模型名（如 gpt-4o-mini / claude-3-5-haiku-latest）',
        inputValidator: (v) => !!v?.trim() || '必须指定一个上游模型',
      },
    )
    testModel = (result.value ?? '').trim()
  } catch {
    return // 用户取消
  }
  // 记住选择, 同 provider 下次默认填这个
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(`${TEST_MODEL_KEY}.${row.provider_id}`, testModel)
  }

  testingId.value = row.id
  try {
    const res: ProbeResult = await api.testCredential(row.id, testModel)
    if (res.ok) {
      ElMessage.success(
        `✓ ${row.label} 通畅 (${res.latency_ms}ms${res.tokens ? `, ${res.tokens} tokens` : ''})`,
      )
    } else {
      ElMessage.error(`✗ ${row.label}: ${res.error_code ?? 'error'} — ${res.error ?? ''}`)
    }
    await load() // 刷新 last_test_at / last_test_error
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
  invalid: '失效',
}
const STATUS_TYPE: Record<string, 'success' | 'info' | 'danger'> = {
  active: 'success',
  disabled: 'info',
  invalid: 'danger',
}
function fmtTime(iso?: string) {
  return iso ? new Date(iso).toLocaleString('zh-CN', { hour12: false }) : '—'
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1>凭证管理</h1>
      <span class="hint">上游 LLM API Key（envelope 加密落库；明文永不返回）</span>
      <div class="header-spacer" />
      <el-button v-if="canWrite" type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        新建凭证
      </el-button>
    </div>

    <el-card shadow="never">
      <div class="filters">
        <el-input
          v-model="search"
          clearable
          placeholder="搜索标签…"
          style="width: 240px"
          @input="onSearch"
          @clear="onSearch"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-select
          v-model="filterProviderId"
          clearable
          placeholder="全部供应商"
          style="width: 200px"
          @change="load"
          @clear="load"
        >
          <el-option v-for="p in providers" :key="p.id" :label="p.name" :value="p.id" />
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
          <el-option label="失效" value="invalid" />
        </el-select>
      </div>

      <el-table :data="credentials" v-loading="loading" stripe style="margin-top: 12px">
        <el-table-column label="标签" prop="label" min-width="180" />
        <el-table-column label="供应商" min-width="140">
          <template #default="{ row }: { row: CredentialSafe }">
            {{ providerById.get(row.provider_id)?.name ?? row.provider_id.slice(0, 8) }}
          </template>
        </el-table-column>
        <el-table-column label="Key 摘要" min-width="160">
          <template #default="{ row }: { row: CredentialSafe }">
            <code class="key-preview">{{ row.key_preview || '—' }}</code>
          </template>
        </el-table-column>
        <el-table-column label="Endpoint" min-width="220" show-overflow-tooltip>
          <template #default="{ row }: { row: CredentialSafe }">
            <span class="muted">{{ row.base_url || '默认' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }: { row: CredentialSafe }">
            <el-tag size="small" :type="STATUS_TYPE[row.status] ?? 'info'">
              {{ STATUS_LABEL[row.status] ?? row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最后探测" width="220">
          <template #default="{ row }: { row: CredentialSafe }">
            <div class="last-test">
              <span :class="{ ok: !row.last_test_error, fail: !!row.last_test_error }">
                {{ fmtTime(row.last_test_at) }}
              </span>
              <div v-if="row.last_test_error" class="muted small" :title="row.last_test_error">
                {{ row.last_test_error }}
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="240" fixed="right">
          <template #default="{ row }: { row: CredentialSafe }">
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

      <el-empty v-if="!loading && credentials.length === 0" description="暂无凭证" />
    </el-card>

    <!-- Create / Edit dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'create' ? '新建凭证' : '编辑凭证'"
      width="540px"
      :close-on-click-modal="false"
      @close="onDialogClose"
    >
      <el-form label-width="100px" label-position="right">
        <el-form-item label="供应商" required>
          <el-select v-model="form.provider_id" :disabled="dialogMode === 'edit'" style="width: 100%">
            <el-option v-for="p in providers" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="标签" required>
          <el-input v-model="form.label" placeholder="例如：OpenAI 主账号" maxlength="128" />
        </el-form-item>
        <el-form-item :label="dialogMode === 'create' ? 'API Key' : '轮换 Key'" :required="dialogMode === 'create'">
          <el-input
            v-model="form.plaintext"
            type="password"
            show-password
            :placeholder="dialogMode === 'edit' ? '留空表示不替换 Key' : 'sk-...'"
            autocomplete="new-password"
          />
          <div class="form-hint">
            <el-icon><Lock /></el-icon>
            envelope 加密落库；保存后立即从前端内存清除。
          </div>
        </el-form-item>
        <el-form-item label="Base URL">
          <el-input v-model="form.base_url" placeholder="留空使用供应商默认 endpoint" />
        </el-form-item>
        <el-form-item label="自定义请求头">
          <el-input
            v-model="form.header_override_text"
            type="textarea"
            :rows="3"
            placeholder='{"X-Custom": "value"}（JSON）'
          />
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
}
.key-preview {
  font-variant-numeric: tabular-nums;
  background: #f3f4f6;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 13px;
  color: #4b5563;
}
.muted { color: #6b7280; font-size: 13px; }
.muted.small { font-size: 12px; }
.last-test {
  display: flex;
  flex-direction: column;
  gap: 2px;
  .ok { color: #6b7280; }
  .fail { color: #dc2626; }
}
.form-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  color: #6b7280;
  font-size: 12px;
  margin-top: 6px;
}
</style>
