<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import type { AdminUser } from '@/api/types'
import { ROLE_LABELS, ROLE_TAG_TYPES, PLAN_LABELS } from '@/api/types'

const router = useRouter()

const users = ref<AdminUser[]>([])
const total = ref(0)
const loading = ref(false)
const q = ref('')
const page = ref(1)
const pageSize = 50

let searchTimer: ReturnType<typeof setTimeout> | null = null
function onSearch() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    page.value = 1
    load()
  }, 300)
}

async function load() {
  loading.value = true
  try {
    const r = await api.listUsers({
      q: q.value || undefined,
      limit: pageSize,
      offset: (page.value - 1) * pageSize,
    })
    users.value = r.users ?? []
    total.value = r.total
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

function fmtTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1>用户管理</h1>
      <el-input
        v-model="q"
        clearable
        :placeholder="$t('user.search')"
        style="width: 300px"
        @input="onSearch"
        @clear="onSearch"
      >
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
    </div>

    <el-card shadow="never">
      <el-table
        :data="users"
        v-loading="loading"
        @row-click="(row: AdminUser) => router.push(`/users/${row.id}`)"
        style="cursor: pointer"
      >
        <el-table-column label="邮箱" prop="email" min-width="220" />
        <el-table-column label="套餐" width="120">
          <template #default="{ row }: { row: AdminUser }">
            <el-tag size="small" effect="plain">
              {{ PLAN_LABELS[row.plan] ?? row.plan }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="角色" width="140">
          <template #default="{ row }: { row: AdminUser }">
            <el-tag size="small" :type="ROLE_TAG_TYPES[row.role] ?? ''">
              {{ ROLE_LABELS[row.role] ?? row.role }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="注册时间" width="180">
          <template #default="{ row }: { row: AdminUser }">
            <span class="text-mono text-muted">{{ fmtTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" align="right">
          <template #default="{ row }: { row: AdminUser }">
            <el-button
              size="small"
              link
              type="primary"
              @click.stop="router.push(`/users/${row.id}`)"
            >
              详情
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination">
        <el-pagination
          v-model:current-page="page"
          :page-size="pageSize"
          :total="total"
          layout="total, prev, pager, next, jumper"
          background
          @current-change="load"
        />
      </div>
    </el-card>
  </div>
</template>

<style scoped lang="scss">
.page-header {
  display: flex; justify-content: space-between; align-items: center;
  margin-bottom: 16px;
  h1 { margin: 0; font-size: 24px; font-weight: 600; }
}
.pagination { margin-top: 16px; display: flex; justify-content: flex-end; }
</style>
