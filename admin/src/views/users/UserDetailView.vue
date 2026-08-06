<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import type { AdminUserDetail, Role } from '@/api/types'
import { ROLE_LABELS, ROLE_TAG_TYPES, PLAN_LABELS } from '@/api/types'
import { usePermission } from '@/composables/usePermission'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const { can, isSuper } = usePermission()
const auth = useAuthStore()

const data = ref<AdminUserDetail | null>(null)
const loading = ref(false)
const errMsg = ref('')

// 改套餐 dialog
const planDialogOpen = ref(false)
const newPlan = ref<'free' | 'pro' | 'team'>('pro')
const reason = ref('')
const submittingPlan = ref(false)

// 改角色 dialog
const roleDialogOpen = ref(false)
const newRole = ref<Role>('admin')
const roleReason = ref('')
const submittingRole = ref(false)

const ALL_ROLES: Role[] = [
  'superadmin',
  'admin',
  'support',
  'finance',
  'ops',
  'viewer',
  'user',
]

async function load() {
  loading.value = true
  errMsg.value = ''
  try {
    data.value = await api.getUser(route.params.id as string)
  } catch (e) {
    errMsg.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

function fmtTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

function openPlanDialog() {
  if (!data.value) return
  newPlan.value = data.value.user.plan
  reason.value = ''
  planDialogOpen.value = true
}

async function submitPlan() {
  if (!data.value) return
  if (!reason.value.trim()) {
    ElMessage.warning('请填写变更原因')
    return
  }
  submittingPlan.value = true
  try {
    await api.setUserPlan(data.value.user.id, newPlan.value, reason.value.trim())
    ElMessage.success('套餐已更新')
    planDialogOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    submittingPlan.value = false
  }
}

// ── 充值积分 ──
const creditsDialogOpen = ref(false)
const creditsAmount = ref(10000)
const creditsReason = ref('')
const submittingCredits = ref(false)
const creditsIdempotencyKey = ref('')

function genIdempotencyKey() {
  // 前端生成幂等键: 每次打开 dialog 一个新 key, 双击同一笔确认被后端幂等拦截.
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return 'adm-' + Math.random().toString(36).slice(2) + Date.now().toString(36)
}

function openCreditsDialog() {
  if (!data.value) return
  creditsAmount.value = 10000
  creditsReason.value = ''
  creditsIdempotencyKey.value = genIdempotencyKey()
  creditsDialogOpen.value = true
}

async function submitCredits() {
  if (!data.value) return
  if (!creditsAmount.value || creditsAmount.value <= 0) {
    ElMessage.warning('充值金额必须大于 0')
    return
  }
  if (!creditsReason.value.trim()) {
    ElMessage.warning('请填写充值原因(审计用)')
    return
  }
  // 二次确认 — 充值不可撤销, 防误操作.
  try {
    await ElMessageBox.confirm(
      `确认给 ${data.value.user.email} 充值 ${creditsAmount.value.toLocaleString()} 永久积分?该操作立即生效、不可撤销.`,
      '充值确认',
      { type: 'warning', confirmButtonText: '确认充值', cancelButtonText: '取消' },
    )
  } catch {
    return // 用户取消
  }
  submittingCredits.value = true
  try {
    await api.grantUserCredits(data.value.user.id, {
      amount: creditsAmount.value,
      reason: creditsReason.value.trim(),
      idempotency_key: creditsIdempotencyKey.value,
    })
    ElMessage.success('充值成功')
    creditsDialogOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    submittingCredits.value = false
  }
}

// ── 改角色 ──
const isSelf = () => data.value?.user.id === auth.user?.id

function openRoleDialog() {
  if (!data.value) return
  newRole.value = data.value.user.role
  roleReason.value = ''
  roleDialogOpen.value = true
}

async function submitRole() {
  if (!data.value) return
  if (!roleReason.value.trim()) {
    ElMessage.warning('请填写变更原因')
    return
  }
  if (newRole.value === data.value.user.role) {
    ElMessage.info('角色未变更')
    roleDialogOpen.value = false
    return
  }
  submittingRole.value = true
  try {
    const res = await api.setUserRole(
      data.value.user.id,
      newRole.value,
      roleReason.value.trim(),
    )
    ElMessage.success(
      `角色已改为 ${ROLE_LABELS[newRole.value]} ${
        res.sessions_revoked ? `(撤销 ${res.sessions_revoked} 个 session)` : ''
      }`,
    )
    roleDialogOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    submittingRole.value = false
  }
}

// ── 撤 session ──
async function revokeSessions() {
  if (!data.value) return
  try {
    await ElMessageBox.confirm(
      `确认撤销 ${data.value.user.email} 的所有登录 session?该用户需要重新登录.`,
      '撤销 Session',
      { type: 'warning', confirmButtonText: '确认撤销', cancelButtonText: '取消' },
    )
  } catch {
    return // 用户取消
  }
  try {
    const res = await api.revokeUserSessions(data.value.user.id)
    ElMessage.success(`已撤销 ${res.revoked} 个 session`)
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <el-button link @click="router.push('/users')"> ← 返回列表</el-button>
      <h1>用户详情</h1>
    </div>

    <el-alert v-if="errMsg" :title="errMsg" type="error" show-icon style="margin-bottom: 16px" />

    <el-card v-loading="loading" shadow="never" class="mb-16" v-if="data">
      <template #header>
        <span>基础信息</span>
      </template>
      <el-descriptions :column="2" size="small" border>
        <el-descriptions-item label="ID">
          <span class="text-mono">{{ data.user.id }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="邮箱">{{ data.user.email }}</el-descriptions-item>
        <el-descriptions-item label="套餐">
          <el-tag size="small" effect="plain">
            {{ PLAN_LABELS[data.user.plan] ?? data.user.plan }}
          </el-tag>
          <el-button
            v-if="can('users:write:plan')"
            link
            type="primary"
            size="small"
            style="margin-left: 8px"
            @click="openPlanDialog"
          >
            修改
          </el-button>
        </el-descriptions-item>
        <el-descriptions-item label="积分余额">
          <template v-if="data.balance">
            <el-tag size="small" type="success" effect="plain">
              {{ (data.balance.permanent_balance + data.balance.time_limited_balance).toLocaleString() }}
            </el-tag>
            <span class="text-muted" style="margin-left: 8px; font-size: 12px">
              永久 {{ data.balance.permanent_balance.toLocaleString() }}<template
                v-if="data.balance.time_limited_balance > 0">
                / 时效 {{ data.balance.time_limited_balance.toLocaleString() }}
              </template>
            </span>
          </template>
          <span v-else class="text-muted">—</span>
          <el-button
            v-if="can('users:write:plan')"
            link
            type="primary"
            size="small"
            style="margin-left: 8px"
            @click="openCreditsDialog"
          >
            充值
          </el-button>
        </el-descriptions-item>
        <el-descriptions-item label="角色">
          <el-tag size="small" :type="ROLE_TAG_TYPES[data.user.role] ?? ''">
            {{ ROLE_LABELS[data.user.role] ?? data.user.role }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="注册时间" :span="2">
          <span class="text-mono">{{ fmtTime(data.user.created_at) }}</span>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" v-if="data" class="mb-16">
      <template #header>
        <span>限额</span>
      </template>
      <el-descriptions :column="3" size="small" border>
        <el-descriptions-item label="Hub RPM">{{ data.limits.HubRPM }}</el-descriptions-item>
        <el-descriptions-item label="Hub TPM">{{ data.limits.HubTPM }}</el-descriptions-item>
        <el-descriptions-item label="Sandbox 日次">{{ data.limits.SandboxDaily }}</el-descriptions-item>
        <el-descriptions-item label="Sandbox 并发">{{ data.limits.SandboxConcurrent }}</el-descriptions-item>
        <el-descriptions-item label="Memory 配额">{{ data.limits.MemoryQuota }}</el-descriptions-item>
        <el-descriptions-item label="Brain 项目数">{{ data.limits.BrainProjects }}</el-descriptions-item>
      </el-descriptions>
    </el-card>

    <!-- 危险操作区 — 仅 superadmin 可见; 操作不可逆, UI 用红色边框警示 -->
    <el-card v-if="data && isSuper()" shadow="never" class="danger-zone">
      <template #header>
        <el-icon><Warning /></el-icon>
        <span style="margin-left: 6px">危险操作</span>
      </template>
      <div class="danger-row">
        <div class="danger-info">
          <h4>修改用户角色</h4>
          <p class="text-muted">
            改完后该用户所有登录 session 立即失效, 必须重新登录后新角色才生效.
            不能改自己, 不能降最后一个超级管理员.
          </p>
        </div>
        <el-button :disabled="isSelf()" type="warning" @click="openRoleDialog">
          {{ isSelf() ? '不能改自己角色' : '改角色' }}
        </el-button>
      </div>
      <el-divider />
      <div class="danger-row">
        <div class="danger-info">
          <h4>撤销所有 Session</h4>
          <p class="text-muted">
            强制该用户在所有设备上下线. 适用场景: 密码泄露 / 离职 / 异常活动.
            该操作仅撤 refresh token, 旧 access token 在 ≤15 分钟内自然过期.
          </p>
        </div>
        <el-button type="danger" @click="revokeSessions">撤销 Session</el-button>
      </div>
    </el-card>

    <!-- 改角色 dialog -->
    <el-dialog v-model="roleDialogOpen" title="修改用户角色" width="500px">
      <el-alert
        type="warning"
        show-icon
        :closable="false"
        title="重要"
        description="改完后用户所有 session 立即失效, 需要重新登录新角色才生效."
        style="margin-bottom: 16px"
      />
      <el-form label-width="80px">
        <el-form-item label="当前角色">
          <el-tag v-if="data" size="small" :type="ROLE_TAG_TYPES[data.user.role]">
            {{ ROLE_LABELS[data.user.role] }}
          </el-tag>
        </el-form-item>
        <el-form-item label="新角色">
          <el-select v-model="newRole" style="width: 100%">
            <el-option
              v-for="r in ALL_ROLES"
              :key="r"
              :value="r"
              :label="ROLE_LABELS[r]"
            >
              <el-tag size="small" :type="ROLE_TAG_TYPES[r]">{{ ROLE_LABELS[r] }}</el-tag>
              <span class="text-mono text-muted" style="margin-left: 8px">{{ r }}</span>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="原因">
          <el-input
            v-model="roleReason"
            type="textarea"
            :rows="3"
            placeholder="说明这次变更的原因(必填, 入审计日志)"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="roleDialogOpen = false">取消</el-button>
        <el-button type="warning" :loading="submittingRole" @click="submitRole">
          确认修改
        </el-button>
      </template>
    </el-dialog>

    <!-- 改套餐 dialog -->
    <el-dialog v-model="planDialogOpen" :title="$t('user.changePlanDialog.title')" width="500px">
      <el-form label-width="80px">
        <el-form-item :label="$t('user.changePlanDialog.newPlan')">
          <el-radio-group v-model="newPlan">
            <el-radio value="free">免费</el-radio>
            <el-radio value="pro">专业版</el-radio>
            <el-radio value="team">团队版</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('user.changePlanDialog.reason')">
          <el-input
            v-model="reason"
            type="textarea"
            :rows="3"
            :placeholder="$t('user.changePlanDialog.reasonPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="planDialogOpen = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submittingPlan" @click="submitPlan">
          {{ $t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 充值积分 dialog -->
    <el-dialog v-model="creditsDialogOpen" title="给用户充值积分" width="500px">
      <el-alert
        type="info"
        show-icon
        :closable="false"
        title="永久积分"
        description="本次充值入账永久积分(永不过期)。请核对金额,确认后立即生效、不可撤销。"
        style="margin-bottom: 16px"
      />
      <el-form label-width="80px">
        <el-form-item label="当前余额">
          <span v-if="data?.balance" class="text-muted">
            {{ (data.balance.permanent_balance + data.balance.time_limited_balance).toLocaleString() }}
          </span>
        </el-form-item>
        <el-form-item label="充值金额">
          <el-input-number
            v-model="creditsAmount"
            :min="1"
            :step="10000"
            controls-position="right"
            style="width: 100%"
          />
          <div class="text-muted" style="font-size: 12px; line-height: 1.6; margin-top: 4px">
            积分数(整数)。参考:10 万积分 ≈ pro 套餐约 10 个月用量
          </div>
        </el-form-item>
        <el-form-item label="原因">
          <el-input
            v-model="creditsReason"
            type="textarea"
            :rows="3"
            placeholder="说明充值原因(必填, 入审计日志, 例如:客户赔付 / 活动赠送 / 测试)"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="creditsDialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="submittingCredits" @click="submitCredits">
          确认充值
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.page-header {
  display: flex; align-items: center; gap: 12px;
  margin-bottom: 16px;
  h1 { margin: 0; font-size: 24px; font-weight: 600; }
}
.mb-16 { margin-bottom: 16px; }

.danger-zone {
  border: 1px solid #f56c6c;
  :deep(.el-card__header) { background: #fef0f0; color: #f56c6c; }
}
.danger-row {
  display: flex; justify-content: space-between; align-items: center;
  gap: 24px;
  .danger-info {
    flex: 1;
    h4 { margin: 0 0 4px; font-size: 14px; font-weight: 600; }
    p { margin: 0; font-size: 13px; }
  }
}
</style>
