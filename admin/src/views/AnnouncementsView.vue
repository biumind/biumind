<script setup lang="ts">
// 公告管理 — 后台发布产品公告 / 通知, 客户端 NotificationBell 拉取展示.
//
// scope:
//   - 列表展示全部公告(含草稿), 按创建倒序
//   - 新建 / 编辑 / 删除
//   - 行内快捷发布 / 下架(published switch)
//
// published=true 时后端经 Realtime SSE 推送让在线客户端即时刷新; 草稿(published=false)
// 不下发. min/max_app_version 控制对哪些客户端可见(灰度 / 兼容). expires_at 过期后不再返回.
//
// 后端 requireAdmin(admin / superadmin), 故路由 + 菜单按 role 收口, 写操作所有进得来的人都可做.

import { ref, onMounted, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { errorMessage } from '@/api/http'
import * as api from '@/api/announcements'
import type { Announcement, AnnouncementInput, AnnouncementLevel } from '@/api/announcements'

const list = ref<Announcement[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    list.value = await api.listAnnouncements()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

// ─── dialog state ──────────────────────────────────────────────────
type DialogMode = 'create' | 'edit'
const dialogVisible = ref(false)
const dialogMode = ref<DialogMode>('create')
const editingId = ref<string>('')

function emptyForm(): AnnouncementInput {
  return {
    level: 'info',
    title: '',
    body: '',
    body_zh: '',
    url: '',
    min_app_version: '',
    max_app_version: '',
    published: false,
    expires_at: null,
  }
}

const form = reactive<AnnouncementInput>(emptyForm())

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = ''
  Object.assign(form, emptyForm())
  dialogVisible.value = true
}

function openEdit(row: Announcement) {
  dialogMode.value = 'edit'
  editingId.value = row.id
  Object.assign(form, {
    level: row.level,
    title: row.title,
    body: row.body,
    body_zh: row.body_zh,
    url: row.url,
    min_app_version: row.min_app_version,
    max_app_version: row.max_app_version,
    published: row.published,
    expires_at: row.expires_at,
  })
  dialogVisible.value = true
}

async function onSave() {
  if (!form.title.trim()) {
    ElMessage.warning('请填写标题')
    return
  }
  const body: AnnouncementInput = { ...form, expires_at: form.expires_at || null }
  try {
    if (dialogMode.value === 'create') {
      await api.createAnnouncement(body)
      ElMessage.success(body.published ? '公告已发布' : '草稿已保存')
    } else {
      await api.updateAnnouncement(editingId.value, body)
      ElMessage.success('公告已更新')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  }
}

// 行内快捷发布 / 下架. 复用 update — 需带全字段.
async function togglePublish(row: Announcement, next: boolean) {
  try {
    await api.updateAnnouncement(row.id, {
      level: row.level,
      title: row.title,
      body: row.body,
      body_zh: row.body_zh,
      url: row.url,
      min_app_version: row.min_app_version,
      max_app_version: row.max_app_version,
      published: next,
      expires_at: row.expires_at,
    })
    row.published = next
    ElMessage.success(next ? '已发布' : '已下架')
  } catch (e) {
    ElMessage.error(errorMessage(e))
    await load() // 回滚 switch 视图状态
  }
}

async function onDelete(row: Announcement) {
  await ElMessageBox.confirm(`确认删除公告 "${row.title}"？已读记录会一并清除。`, '删除公告', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  })
    .then(async () => {
      try {
        await api.deleteAnnouncement(row.id)
        ElMessage.success('已删除')
        await load()
      } catch (e) {
        ElMessage.error(errorMessage(e))
      }
    })
    .catch(() => {})
}

const LEVEL_LABELS: Record<AnnouncementLevel, string> = {
  info: '通知',
  warning: '警告',
  error: '严重',
}
const LEVEL_TAG: Record<AnnouncementLevel, '' | 'warning' | 'danger'> = {
  info: '',
  warning: 'warning',
  error: 'danger',
}

function fmtTime(iso: string | null) {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

function versionRange(row: Announcement) {
  const lo = row.min_app_version
  const hi = row.max_app_version
  if (!lo && !hi) return '全部'
  return `${lo || '*'} ~ ${hi || '*'}`
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1>公告管理</h1>
      <span class="hint">发布产品公告 / 通知, 客户端铃铛拉取展示。草稿不下发, 发布即经 Realtime 实时推送。</span>
      <div class="header-spacer" />
      <el-button type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>
        新建公告
      </el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="list" v-loading="loading" stripe>
        <el-table-column label="级别" width="90">
          <template #default="{ row }: { row: Announcement }">
            <el-tag size="small" :type="LEVEL_TAG[row.level]" effect="plain">
              {{ LEVEL_LABELS[row.level] ?? row.level }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="标题" prop="title" min-width="200" show-overflow-tooltip />
        <el-table-column label="版本范围" width="160">
          <template #default="{ row }: { row: Announcement }">
            <span class="muted">{{ versionRange(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="过期时间" width="180">
          <template #default="{ row }: { row: Announcement }">
            <span class="muted">{{ fmtTime(row.expires_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="180">
          <template #default="{ row }: { row: Announcement }">
            <span class="muted">{{ fmtTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="发布" width="90" align="center">
          <template #default="{ row }: { row: Announcement }">
            <el-switch
              :model-value="row.published"
              @change="(v: boolean) => togglePublish(row, v)"
            />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }: { row: Announcement }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="onDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty
        v-if="!loading && list.length === 0"
        description="尚无公告，点击右上角「新建公告」开始"
      >
        <el-button type="primary" @click="openCreate">新建公告</el-button>
      </el-empty>
    </el-card>

    <!-- Create / Edit dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'create' ? '新建公告' : '编辑公告'"
      width="640px"
      :close-on-click-modal="false"
    >
      <el-form label-width="100px">
        <el-form-item label="级别">
          <el-radio-group v-model="form.level">
            <el-radio label="info">通知</el-radio>
            <el-radio label="warning">警告</el-radio>
            <el-radio label="error">严重</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="标题" required>
          <el-input v-model="form.title" placeholder="如 v2.3 已发布" maxlength="200" />
        </el-form-item>
        <el-form-item label="正文(中文)">
          <el-input
            v-model="form.body_zh"
            type="textarea"
            :rows="3"
            placeholder="中文客户端显示的正文(支持 markdown)"
          />
        </el-form-item>
        <el-form-item label="正文(英文)">
          <el-input
            v-model="form.body"
            type="textarea"
            :rows="3"
            placeholder="英文 / 默认 fallback 正文(支持 markdown)"
          />
        </el-form-item>
        <el-form-item label="跳转链接">
          <el-input v-model="form.url" placeholder="（可选）点击公告跳转的 URL" />
        </el-form-item>
        <el-form-item label="版本下限">
          <el-input v-model="form.min_app_version" placeholder="（可选）如 2.1.0, 低于此版本不展示" />
        </el-form-item>
        <el-form-item label="版本上限">
          <el-input v-model="form.max_app_version" placeholder="（可选）如 3.0.0, 高于此版本不展示" />
        </el-form-item>
        <el-form-item label="过期时间">
          <el-date-picker
            v-model="form.expires_at"
            type="datetime"
            placeholder="（可选）过期后不再下发"
            value-format="YYYY-MM-DDTHH:mm:ssZ"
          />
        </el-form-item>
        <el-form-item label="立即发布">
          <el-switch v-model="form.published" />
          <span class="form-hint">关闭则保存为草稿，不下发给客户端</span>
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
.muted { color: #6b7280; font-size: 13px; }
.form-hint {
  margin-left: 12px;
  color: #6b7280;
  font-size: 12px;
}
</style>
