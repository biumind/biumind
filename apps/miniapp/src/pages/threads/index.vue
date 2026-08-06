<script setup lang="ts">
import { ref, computed } from 'vue';
import { onShow } from '@dcloudio/uni-app';
import {
  deleteThread,
  listThreads,
  patchThread,
  type Thread,
} from '@/data/api/chat';
import { isLoggedIn } from '@/core/token_manager';
import { loadPinned, pin, unpin } from '@/lib/pinned_threads';

const PENDING_THREAD_KEY = 'biumind.pending_thread_id';

const threads = ref<Thread[]>([]);
const loading = ref(false);
const err = ref('');
const query = ref('');
const pinnedIds = ref<Set<string>>(new Set());

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase();
  let list = threads.value;
  if (q) {
    list = list.filter((t) => (t.title || '').toLowerCase().includes(q));
  }
  // 置顶在前; 组内按 updated_at desc (后端已排好, 直接稳定排序)
  return [...list].sort((a, b) => {
    const pa = pinnedIds.value.has(a.id) ? 1 : 0;
    const pb = pinnedIds.value.has(b.id) ? 1 : 0;
    return pb - pa;
  });
});

async function reload() {
  if (!isLoggedIn()) {
    uni.redirectTo({ url: '/pages/me/login' });
    return;
  }
  loading.value = true;
  err.value = '';
  pinnedIds.value = loadPinned();
  try {
    threads.value = await listThreads();
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

function openThread(t: Thread) {
  try {
    uni.setStorageSync(PENDING_THREAD_KEY, t.id);
  } catch {
    /* noop */
  }
  uni.switchTab({ url: '/pages/chat/index' });
}

function onLongPress(t: Thread) {
  const isPinned = pinnedIds.value.has(t.id);
  const items: { label: string; key: string }[] = [
    { label: isPinned ? '取消置顶' : '置顶', key: 'pin' },
    { label: '重命名', key: 'rename' },
    { label: '复制标题', key: 'copy' },
    { label: '删除', key: 'delete' },
  ];
  uni.showActionSheet({
    itemList: items.map((x) => x.label),
    // 把"删除"标红 — itemColor 是整体, 用 ActionSheet 自定义有限,
    // 改通过最后一项 "#ef4444" 实现 (微信支持 itemColor 整体色, 这里
    // 用 modal 二次确认替代视觉警告)
    success: (res) => {
      const action = items[res.tapIndex];
      if (!action) return;
      switch (action.key) {
        case 'pin':
          pinnedIds.value = isPinned ? unpin(t.id) : pin(t.id);
          uni.showToast({
            title: isPinned ? '已取消置顶' : '已置顶',
            icon: 'none',
          });
          break;
        case 'rename':
          doRename(t);
          break;
        case 'copy':
          uni.setClipboardData({
            data: t.title || '',
            success: () => uni.showToast({ title: '已复制', icon: 'none' }),
          });
          break;
        case 'delete':
          doDelete(t);
          break;
      }
    },
  });
}

// ── 重命名 / 删除 (与 Flutter threads_controller.dart 同 contract) ──

function doRename(t: Thread) {
  // showModal editable: true 弹输入框, 微信基础库 ≥ 2.17.1 支持
  uni.showModal({
    title: '重命名会话',
    editable: true,
    placeholderText: '输入新标题',
    content: t.title || '',
    confirmText: '保存',
    cancelText: '取消',
    success: async (res) => {
      if (!res.confirm) return;
      const newTitle = (res.content || '').trim();
      if (!newTitle || newTitle === t.title) return;

      // 乐观更新本地, 失败回滚
      const oldTitle = t.title;
      const idx = threads.value.findIndex((x) => x.id === t.id);
      if (idx >= 0) {
        threads.value.splice(idx, 1, { ...t, title: newTitle });
      }
      try {
        const updated = await patchThread(t.id, { title: newTitle });
        if (idx >= 0) {
          threads.value.splice(idx, 1, updated);
        }
        uni.showToast({ title: '已重命名', icon: 'success' });
      } catch (e: unknown) {
        // 回滚
        if (idx >= 0) {
          threads.value.splice(idx, 1, { ...t, title: oldTitle });
        }
        const msg = e instanceof Error ? e.message : String(e);
        uni.showToast({ title: '重命名失败: ' + msg, icon: 'none' });
      }
    },
  });
}

function doDelete(t: Thread) {
  uni.showModal({
    title: '删除会话',
    content: '确定删除 "' + (t.title || '(未命名)') + '" 吗? 此操作无法撤销.',
    confirmText: '删除',
    confirmColor: '#ef4444',
    cancelText: '取消',
    success: async (res) => {
      if (!res.confirm) return;

      // 乐观: 先从本地移除, 失败回滚
      const idx = threads.value.findIndex((x) => x.id === t.id);
      const removed = idx >= 0 ? threads.value.splice(idx, 1)[0] : null;
      // 同步清掉本地 pin 状态
      if (pinnedIds.value.has(t.id)) {
        pinnedIds.value = unpin(t.id);
      }
      try {
        await deleteThread(t.id);
        uni.showToast({ title: '已删除', icon: 'success' });
      } catch (e: unknown) {
        // 回滚
        if (removed && idx >= 0) {
          threads.value.splice(idx, 0, removed);
        }
        const msg = e instanceof Error ? e.message : String(e);
        uni.showToast({ title: '删除失败: ' + msg, icon: 'none' });
      }
    },
  });
}

function clearQuery() {
  query.value = '';
}

function fmt(d: string): string {
  if (!d) return '';
  return d.slice(0, 16).replace('T', ' ');
}

onShow(reload);
</script>

<template>
  <view class="page">
    <view class="searchbar">
      <input
        v-model="query"
        class="search-input"
        placeholder="搜索会话..."
        confirm-type="search"
      />
      <text v-if="query" class="search-clear" @tap="clearQuery">×</text>
    </view>

    <view v-if="loading" class="hint">加载中...</view>
    <view v-else-if="err" class="error">{{ err }}</view>
    <view v-else-if="threads.length === 0" class="hint">
      暂无会话, 去对话页发起一条
    </view>
    <view v-else-if="filtered.length === 0" class="hint">
      没有匹配 "{{ query }}" 的会话
    </view>
    <view v-else>
      <view
        v-for="t in filtered"
        :key="t.id"
        class="row"
        @tap="openThread(t)"
        @longpress="onLongPress(t)"
      >
        <view class="row-main">
          <view class="row-title-line">
            <text v-if="pinnedIds.has(t.id)" class="pin-mark">📌</text>
            <text class="row-title">{{ t.title || '(未命名)' }}</text>
          </view>
          <text class="row-sub">
            {{ t.message_count }} 条 · {{ fmt(t.updated_at) }}
          </text>
        </view>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.page {
  padding: 16rpx;
}
.searchbar {
  position: relative;
  display: flex;
  align-items: center;
  background: #fff;
  border-radius: 12rpx;
  padding: 0 24rpx;
  margin-bottom: 16rpx;
  height: 72rpx;
}
.search-input {
  flex: 1;
  height: 72rpx;
  font-size: 28rpx;
  color: #1f2937;
}
.search-clear {
  position: absolute;
  right: 24rpx;
  top: 50%;
  transform: translateY(-50%);
  font-size: 40rpx;
  color: #9ca3af;
  width: 48rpx;
  height: 48rpx;
  text-align: center;
  line-height: 48rpx;
}
.row {
  background: #fff;
  border-radius: 12rpx;
  padding: 24rpx;
  margin-bottom: 16rpx;
}
.row-main {
  display: flex;
  flex-direction: column;
}
.row-title-line {
  display: flex;
  align-items: center;
  gap: 8rpx;
}
.pin-mark {
  font-size: 24rpx;
}
.row-title {
  font-size: 30rpx;
  color: #1f2937;
  font-weight: 500;
}
.row-sub {
  margin-top: 8rpx;
  font-size: 24rpx;
  color: #9ca3af;
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
</style>
