<script setup lang="ts">
// 角色 × 权限矩阵编辑页 — 仅 superadmin 可保存.
//
// 后端: GET /v1/admin/rbac/matrix 一次拉全, PUT /v1/admin/rbac/roles/{role}/permissions
// 原子替换. 保存后后端 RoleCache.Reload, 所有在线用户立即生效.
//
// 通配支持: '*' / 'users:*' / 'users:read:*' — 单元格判断时直接显式 includes.
// 不解析通配 (UI 层只展示, 后端 HasPermission 才解析). 用户可手工写通配 perm 字符串
// (后续如果需要可以加输入框, 现在通过 seed permissions 列表勾选即可).

import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import { usePermission } from '@/composables/usePermission'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const { isRole } = usePermission()
const canEdit = computed(() => isRole('superadmin'))

const loading = ref(false)
const saving = ref(false)
const roles = ref<api.RBACRole[]>([])
const permissions = ref<api.RBACPermission[]>([])
// 编辑中的矩阵 — Set 加速 toggle, 保存时转回 string[]
const matrix = ref<Record<string, Set<string>>>({})
// 原始矩阵 — 检测脏 + 取消恢复
const original = ref<Record<string, Set<string>>>({})

async function load() {
  loading.value = true
  try {
    const r = await api.getRBACMatrix()
    roles.value = r.roles
    permissions.value = r.permissions
    matrix.value = {}
    original.value = {}
    for (const role of r.roles) {
      const set = new Set(r.matrix[role.name] ?? [])
      matrix.value[role.name] = new Set(set)
      original.value[role.name] = new Set(set)
    }
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

// 按 resource 分组权限,矩阵更紧凑
const groupedPermissions = computed(() => {
  const groups: Record<string, api.RBACPermission[]> = {}
  for (const p of permissions.value) {
    ;(groups[p.resource] ||= []).push(p)
  }
  return Object.entries(groups).sort(([a], [b]) => a.localeCompare(b))
})

function isChecked(role: string, perm: string): boolean {
  return matrix.value[role]?.has(perm) ?? false
}

function toggle(role: string, perm: string) {
  if (!canEdit.value) return
  // superadmin 的 '*' 不允许勾掉
  if (role === 'superadmin' && perm === '*' && isChecked(role, perm)) {
    ElMessage.warning('不能取消 superadmin 的全权限')
    return
  }
  const set = matrix.value[role] ?? new Set()
  if (set.has(perm)) set.delete(perm)
  else set.add(perm)
  matrix.value[role] = new Set(set)
}

// 是否有未保存改动 (任一 role 集合跟 original 不一致)
const dirtyRoles = computed(() => {
  const out: string[] = []
  for (const role of roles.value) {
    const cur = matrix.value[role.name] ?? new Set()
    const orig = original.value[role.name] ?? new Set()
    if (cur.size !== orig.size) {
      out.push(role.name)
      continue
    }
    for (const p of cur) {
      if (!orig.has(p)) {
        out.push(role.name)
        break
      }
    }
  }
  return out
})
const isDirty = computed(() => dirtyRoles.value.length > 0)

function diff(role: string): { added: string[]; removed: string[] } {
  const cur = matrix.value[role] ?? new Set()
  const orig = original.value[role] ?? new Set()
  const added: string[] = []
  const removed: string[] = []
  for (const p of cur) if (!orig.has(p)) added.push(p)
  for (const p of orig) if (!cur.has(p)) removed.push(p)
  return { added, removed }
}

async function save() {
  if (!canEdit.value || !isDirty.value) return
  // 二次确认 — 改权限是高敏操作
  const lines = dirtyRoles.value.map((r) => {
    const d = diff(r)
    return `${r}: +${d.added.length} / -${d.removed.length}`
  })
  try {
    await ElMessageBox.confirm(
      `将保存 ${dirtyRoles.value.length} 个角色的权限改动:\n\n${lines.join('\n')}\n\n保存后所有在线用户的权限立即变化, 是否继续?`,
      '确认改动权限矩阵',
      { type: 'warning', confirmButtonText: '保存', cancelButtonText: '取消' },
    )
  } catch {
    return // 用户取消
  }

  saving.value = true
  let warnings: string[] = []
  try {
    for (const role of dirtyRoles.value) {
      const perms = Array.from(matrix.value[role] ?? new Set())
      const resp = await api.setRolePermissions(role, perms)
      if (resp.reload_warning) {
        warnings.push(`${role}: ${resp.reload_warning}`)
      }
      original.value[role] = new Set(matrix.value[role])
    }
    if (warnings.length === 0) {
      ElMessage.success(`已保存 ${dirtyRoles.value.length} 个角色`)
    } else {
      ElMessage.warning(`保存成功但 reload 有警告: ${warnings.join('; ')}`)
    }
    // 自己的 role 在变更列表里 — 重新拉 me 让左侧菜单刷新
    if (auth.user?.role && dirtyRoles.value.includes(auth.user.role)) {
      await auth.refreshMe()
    }
  } catch (e) {
    ElMessage.error(errorMessage(e))
    // 失败重新 load 一次, 跟服务器对齐
    await load()
  } finally {
    saving.value = false
  }
}

function reset() {
  for (const r of roles.value) {
    matrix.value[r.name] = new Set(original.value[r.name] ?? new Set())
  }
}

onMounted(load)
</script>

<template>
  <div class="rbac">
    <div class="page-header">
      <h1>角色权限矩阵</h1>
      <div class="toolbar">
        <el-tag v-if="!canEdit" type="info" effect="plain">仅查看 (需 superadmin 可改)</el-tag>
        <el-tag v-else-if="isDirty" type="warning" effect="plain">
          {{ dirtyRoles.length }} 个角色待保存
        </el-tag>
        <el-button :disabled="!isDirty || saving" @click="reset">取消</el-button>
        <el-button
          type="primary"
          :loading="saving"
          :disabled="!canEdit || !isDirty"
          @click="save"
        >
          保存
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      show-icon
      :closable="false"
      style="margin-bottom: 12px"
    >
      <template #title>
        修改权限将立即生效 (后端会 reload RoleCache); 内置 system 角色可改但不可删.
        通配权限 (如 <code>users:*</code>) 含义请参考权限说明.
      </template>
    </el-alert>

    <el-card v-loading="loading" shadow="never" class="matrix-card">
      <div class="matrix-scroll">
        <table class="matrix">
          <thead>
            <tr>
              <th class="perm-col">权限</th>
              <th
                v-for="r in roles"
                :key="r.name"
                class="role-col"
                :title="r.description"
              >
                <div class="role-name">{{ r.display_name }}</div>
                <div class="role-key text-mono text-muted">{{ r.name }}</div>
              </th>
            </tr>
          </thead>
          <tbody>
            <template v-for="[resource, perms] in groupedPermissions" :key="resource">
              <tr class="resource-row">
                <td :colspan="roles.length + 1">
                  <span class="resource-tag">{{ resource }}</span>
                </td>
              </tr>
              <tr v-for="p in perms" :key="p.name" class="perm-row">
                <td class="perm-col">
                  <div class="perm-name text-mono">{{ p.name }}</div>
                  <div class="perm-desc text-muted">{{ p.description }}</div>
                </td>
                <td
                  v-for="r in roles"
                  :key="r.name"
                  class="cell"
                  :class="{
                    'cell-changed':
                      matrix[r.name]?.has(p.name) !==
                      original[r.name]?.has(p.name),
                  }"
                  @click="toggle(r.name, p.name)"
                >
                  <el-checkbox
                    :model-value="isChecked(r.name, p.name)"
                    :disabled="!canEdit || (r.name === 'superadmin' && p.name === '*')"
                    @click.stop="toggle(r.name, p.name)"
                  />
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </el-card>
  </div>
</template>

<style scoped lang="scss">
.rbac h1 { margin: 0; font-size: 24px; font-weight: 600; }

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
  flex-wrap: wrap;
  gap: 12px;
}
.toolbar { display: flex; gap: 8px; align-items: center; }

.matrix-card { padding: 0; }
.matrix-scroll { overflow: auto; max-width: 100%; }

.matrix {
  border-collapse: collapse;
  width: 100%;
  min-width: 800px;
  font-size: 13px;

  th, td {
    border: 1px solid #e5e7eb;
    padding: 8px;
    text-align: left;
    vertical-align: middle;
  }

  thead th {
    position: sticky;
    top: 0;
    background: #f9fafb;
    z-index: 2;
  }

  .perm-col {
    min-width: 280px;
    background: #fafbfc;
    position: sticky;
    left: 0;
    z-index: 1;
  }

  thead .perm-col { z-index: 3; }

  .role-col {
    min-width: 110px;
    text-align: center;
    .role-name { font-weight: 600; font-size: 13px; }
    .role-key  { font-size: 11px; }
  }

  .resource-row td {
    background: #f3f4f6;
    padding: 4px 8px;
    .resource-tag {
      font-weight: 600;
      color: #4b5563;
      text-transform: uppercase;
      font-size: 11px;
      letter-spacing: 0.5px;
    }
  }

  .perm-name { font-weight: 500; font-size: 12px; }
  .perm-desc { font-size: 11px; margin-top: 2px; }

  .cell {
    text-align: center;
    cursor: pointer;
    width: 110px;
    user-select: none;
    transition: background 0.1s;
    &:hover { background: rgba(96, 165, 250, 0.08); }
  }

  .cell-changed {
    background-color: rgba(230, 162, 60, 0.12) !important;
    &:hover { background-color: rgba(230, 162, 60, 0.2) !important; }
  }
}

.text-mono { font-family: ui-monospace, SFMono-Regular, monospace; }
.text-muted { color: #9ca3af; }
</style>
