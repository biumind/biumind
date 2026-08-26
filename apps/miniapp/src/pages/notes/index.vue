<script setup lang="ts">
// 笔记速记 — 最小闭环: 顶部快速输入 (免选笔记本, 自动标题) + 列表 +
// 点条目弹层编辑. 端点对齐 apps/client 的 notes data 层:
//   GET /v1/notes?limit=50 · POST /v1/notes · PUT /v1/notes/{id} (If-Match 乐观锁)
// 长按条目 → 分享: GET/PUT /v1/notes/{id}/share 拿 token, 拼 ${origin}/s/n/{token}
// 复制到剪贴板 (微信场景主要传播方式就是粘贴链接).

import { ref, onMounted } from 'vue';
import { onShow } from '@dcloudio/uni-app';
import {
  listNotes,
  createNote,
  updateNote,
  ensureShare,
  listShares,
  type NoteItem,
} from '@/data/api/notes';
import type { ApiError } from '@/data/api/client';
import { isLoggedIn } from '@/core/token_manager';
import { copyToClipboard } from '@/core/os_integration';
import { formatChatTime } from '@/lib/time_format';

const notes = ref<NoteItem[]>([]);
const loading = ref(false);
const err = ref('');

// ── 快速输入 ─────────────────────────────────────────────────
const draft = ref('');
const saving = ref(false);

function autoTitle(): string {
  const d = new Date();
  const p = (n: number) => (n < 10 ? '0' + n : '' + n);
  return (
    '速记 ' +
    d.getFullYear() +
    '-' +
    p(d.getMonth() + 1) +
    '-' +
    p(d.getDate()) +
    ' ' +
    p(d.getHours()) +
    ':' +
    p(d.getMinutes())
  );
}

async function onSave() {
  const content = draft.value.trim();
  if (!content) {
    uni.showToast({ title: '先写点内容', icon: 'none' });
    return;
  }
  saving.value = true;
  try {
    await createNote(autoTitle(), content);
    draft.value = '';
    uni.showToast({ title: '已保存', icon: 'success' });
    await reload();
  } catch (e: unknown) {
    uni.showToast({
      title: e instanceof Error ? e.message : '保存失败',
      icon: 'none',
    });
  } finally {
    saving.value = false;
  }
}

// ── 列表 ─────────────────────────────────────────────────────
// 已分享笔记 id 集合 (徽标); 来自 GET /v1/notes/shares, 拉取失败不影响列表.
const sharedIds = ref<Set<string>>(new Set());

async function reload() {
  if (!isLoggedIn()) {
    uni.redirectTo({ url: '/pages/me/login' });
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    notes.value = await listNotes(50);
    try {
      const shares = await listShares();
      sharedIds.value = new Set(
        shares
          .filter((s) => (s.status || 'active') === 'active')
          .map((s) => s.note_id),
      );
    } catch {
      // 分享列表失败只丢徽标, 不阻塞笔记列表
    }
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
    uni.showToast({ title: err.value || '加载失败', icon: 'none' });
  } finally {
    loading.value = false;
  }
}

function displayTitle(n: NoteItem): string {
  if (n.title) return n.title;
  const first = (n.content_md || '').split('\n').find((l) => l.trim());
  return first ? first.trim() : '(无标题)';
}

function excerpt(n: NoteItem): string {
  const lines = (n.content_md || '')
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l && l !== n.title.trim());
  return lines.slice(0, 2).join(' / ');
}

function fmtTime(iso: string): string {
  const ts = Date.parse(iso || '');
  return Number.isNaN(ts) ? '' : formatChatTime(ts);
}

// ── 分享 ─────────────────────────────────────────────────────
// 长按条目 → 动作菜单 (对齐 threads 页交互) → 创建/获取分享链接 → 复制.
// 小程序不做密码/有效期配置 UI (留给 App 端), 只创建默认分享.
const sharing = ref(false);

function onLongPress(n: NoteItem) {
  uni.showActionSheet({
    itemList: ['分享'],
    success: (res) => {
      if (res.tapIndex === 0) shareNote(n);
    },
  });
}

async function shareNote(n: NoteItem) {
  if (sharing.value) return;
  sharing.value = true;
  try {
    const share = await ensureShare(n.id);
    // 单 origin 寻址: API baseURL 的 origin 即站点 origin, /s/n/ 由 nginx 反代
    const origin = ((import.meta.env.VITE_BIU_API_BASE as string) || '').replace(
      /\/$/,
      '',
    );
    await copyToClipboard(origin + '/s/n/' + share.token);
    uni.showToast({ title: '分享链接已复制', icon: 'none' });
    sharedIds.value = new Set(sharedIds.value).add(n.id);
  } catch (e: unknown) {
    uni.showToast({
      title: e instanceof Error ? e.message : '分享失败',
      icon: 'none',
    });
  } finally {
    sharing.value = false;
  }
}

// ── 编辑弹层 ─────────────────────────────────────────────────
const editing = ref(false);
const editId = ref('');
const editVersion = ref(0);
const editTitle = ref('');
const editContent = ref('');
const editSaving = ref(false);

function openEdit(n: NoteItem) {
  editId.value = n.id;
  editVersion.value = n.version;
  editTitle.value = n.title;
  editContent.value = n.content_md;
  editing.value = true;
}

function cancelEdit() {
  editing.value = false;
}

async function saveEdit() {
  const content = editContent.value.trim();
  if (!content) {
    uni.showToast({ title: '内容不能为空', icon: 'none' });
    return;
  }
  editSaving.value = true;
  try {
    await updateNote(editId.value, editVersion.value, {
      title: editTitle.value.trim(),
      content_md: content,
    });
    editing.value = false;
    uni.showToast({ title: '已保存', icon: 'success' });
    await reload();
  } catch (e: unknown) {
    const ae = e as ApiError;
    if (ae && ae.status === 409) {
      // 乐观锁冲突 — 其他端改过, 刷新列表让用户基于最新内容再改
      editing.value = false;
      uni.showToast({ title: '内容已被其他端修改, 已刷新', icon: 'none' });
      await reload();
    } else {
      uni.showToast({
        title: e instanceof Error ? e.message : '保存失败',
        icon: 'none',
      });
    }
  } finally {
    editSaving.value = false;
  }
}

onMounted(reload);
// 从 tabbar 以外入口返回时刷新 (编辑在其他端发生时也能尽快看到)
onShow(() => {
  if (isLoggedIn() && notes.value.length > 0) reload();
});
</script>

<template>
  <view class="page">
    <!-- 快速输入 -->
    <view class="card quick">
      <textarea
        v-model="draft"
        class="quick-input"
        placeholder="记点什么..."
        :maxlength="-1"
        auto-height
      />
      <button
        class="btn-primary"
        :disabled="saving"
        @tap="onSave"
      >
        {{ saving ? '保存中...' : '保存' }}
      </button>
    </view>

    <!-- 列表 -->
    <view v-if="loading && notes.length === 0" class="hint">加载中...</view>
    <view v-else-if="err" class="error">{{ err }}</view>
    <view v-else-if="notes.length === 0" class="hint">
      还没有笔记, 在上面写下第一条速记吧
    </view>
    <view v-else>
      <view
        v-for="n in notes"
        :key="n.id"
        class="card item"
        hover-class="item-hover"
        @tap="openEdit(n)"
        @longpress="onLongPress(n)"
      >
        <view class="item-head">
          <text class="item-title">{{ displayTitle(n) }}</text>
          <text v-if="sharedIds.has(n.id)" class="share-mark">已分享</text>
          <text class="item-time">{{ fmtTime(n.updated_at) }}</text>
        </view>
        <text v-if="excerpt(n)" class="item-excerpt">{{ excerpt(n) }}</text>
      </view>
    </view>

    <!-- 编辑弹层 -->
    <view v-if="editing" class="overlay" @tap="cancelEdit">
      <view class="dialog" @tap.stop>
        <text class="dialog-title">编辑笔记</text>
        <input
          v-model="editTitle"
          class="dialog-input"
          placeholder="标题 (留空用首行)"
          maxlength="64"
        />
        <textarea
          v-model="editContent"
          class="dialog-textarea"
          placeholder="内容"
          :maxlength="-1"
        />
        <view class="dialog-actions">
          <button class="btn-cancel" @tap="cancelEdit">取消</button>
          <button class="btn-save" :disabled="editSaving" @tap="saveEdit">
            {{ editSaving ? '保存中...' : '保存' }}
          </button>
        </view>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.page {
  padding: 16rpx;
}
.card {
  background: #fff;
  border-radius: 12rpx;
  padding: 24rpx;
  margin-bottom: 16rpx;
}
.quick {
  display: flex;
  flex-direction: column;
}
.quick-input {
  width: 100%;
  min-height: 120rpx;
  font-size: 28rpx;
  color: #1f2937;
  line-height: 1.5;
  box-sizing: border-box;
}
.btn-primary {
  margin-top: 16rpx;
  height: 80rpx;
  line-height: 80rpx;
  background: #3b82f6;
  color: #fff;
  border-radius: 12rpx;
  font-size: 28rpx;
}
.btn-primary[disabled] {
  opacity: 0.6;
}
.item-hover {
  background: #f8fafc;
}
.item-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.item-title {
  font-size: 30rpx;
  font-weight: 500;
  color: #1f2937;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-right: 16rpx;
}
.item-time {
  font-size: 22rpx;
  color: #9ca3af;
  flex-shrink: 0;
}
.share-mark {
  flex-shrink: 0;
  font-size: 20rpx;
  color: #3b82f6;
  background: #eff6ff;
  border: 1px solid #bfdbfe;
  border-radius: 8rpx;
  padding: 2rpx 10rpx;
  margin-right: 12rpx;
}
.item-excerpt {
  display: block;
  margin-top: 8rpx;
  font-size: 26rpx;
  color: #6b7280;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.hint {
  padding: 96rpx 24rpx;
  text-align: center;
  color: #9ca3af;
  font-size: 28rpx;
}
.error {
  padding: 32rpx;
  color: #ef4444;
  font-size: 26rpx;
}

/* 编辑弹层 */
.overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}
.dialog {
  width: 640rpx;
  background: #fff;
  border-radius: 16rpx;
  padding: 40rpx 32rpx;
  display: flex;
  flex-direction: column;
}
.dialog-title {
  font-size: 32rpx;
  font-weight: 600;
  color: #1f2937;
  text-align: center;
  margin-bottom: 24rpx;
}
.dialog-input {
  height: 80rpx;
  padding: 0 24rpx;
  border: 1px solid #d1d5db;
  border-radius: 12rpx;
  font-size: 30rpx;
  margin-bottom: 16rpx;
  box-sizing: border-box;
}
.dialog-textarea {
  width: 100%;
  height: 320rpx;
  padding: 16rpx 24rpx;
  border: 1px solid #d1d5db;
  border-radius: 12rpx;
  font-size: 28rpx;
  line-height: 1.5;
  box-sizing: border-box;
}
.dialog-actions {
  display: flex;
  gap: 24rpx;
  margin-top: 24rpx;
}
.btn-cancel,
.btn-save {
  flex: 1;
  height: 80rpx;
  line-height: 80rpx;
  border-radius: 12rpx;
  font-size: 28rpx;
}
.btn-cancel {
  background: #f3f4f6;
  color: #1f2937;
}
.btn-save {
  background: #3b82f6;
  color: #fff;
}
.btn-save[disabled] {
  opacity: 0.6;
}
</style>
