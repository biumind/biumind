<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { get } from '@/data/api/client';
import { isLoggedIn } from '@/core/token_manager';
import { requestSubscribeMessage } from '@/core/os_integration';

interface NotifyItem {
  id: string;
  title: string;
  body: string;
  created_at: string;
  read: boolean;
}

interface SubscriptionRow {
  id: string;
  platform: string;
  template_id: string;
  times_remaining: number;
}

const items = ref<NotifyItem[]>([]);
const subs = ref<SubscriptionRow[]>([]);
const loading = ref(false);
const err = ref('');

async function reload() {
  if (!isLoggedIn()) {
    uni.redirectTo({ url: '/pages/me/login' });
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    const [n, s] = await Promise.all([
      get<{ items: NotifyItem[] }>('/v1/notify/me/inbox?limit=50'),
      get<{ subscriptions: SubscriptionRow[] }>('/v1/notify/me/subscriptions'),
    ]);
    items.value = n.items || [];
    subs.value = s.subscriptions || [];
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

// 引导用户授权"任务完成"模板. template_id 由各平台后台申请; 这里硬编码占位.
async function onEnableNotifications() {
  try {
    const TEMPLATE_TASK_DONE = 'PLACEHOLDER_TASK_DONE_TPL';
    await requestSubscribeMessage([TEMPLATE_TASK_DONE]);
    uni.showToast({ title: '已开启通知', icon: 'success' });
    await reload();
  } catch (e: unknown) {
    uni.showToast({
      title: e instanceof Error ? e.message : '授权失败',
      icon: 'none',
    });
  }
}

onMounted(reload);
</script>

<template>
  <view class="page">
    <view class="card subs">
      <text class="card-title">推送授权</text>
      <text class="hint">已授权 {{ subs.length }} 条模板; 剩余总次数 {{
        subs.reduce((acc, s) => acc + s.times_remaining, 0)
      }} 次</text>
      <button class="btn-primary" @tap="onEnableNotifications">开启通知</button>
    </view>

    <view class="card">
      <text class="card-title">收件箱</text>
      <view v-if="loading" class="hint">加载中...</view>
      <view v-else-if="err" class="error">{{ err }}</view>
      <view v-else-if="items.length === 0" class="hint">暂无通知</view>
      <view v-else>
        <view v-for="it in items" :key="it.id" class="item">
          <text class="item-title">{{ it.title }}</text>
          <text class="item-body">{{ it.body }}</text>
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
.subs {
  display: flex;
  flex-direction: column;
  align-items: stretch;
}
.card-title {
  display: block;
  font-size: 28rpx;
  color: #6b7280;
  margin-bottom: 16rpx;
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
.item {
  padding: 20rpx 0;
  border-bottom: 1px solid #f3f4f6;
}
.item:last-child {
  border-bottom: none;
}
.item-title {
  display: block;
  font-size: 30rpx;
  font-weight: 500;
  color: #1f2937;
}
.item-body {
  margin-top: 8rpx;
  font-size: 26rpx;
  color: #6b7280;
  line-height: 1.5;
}
.hint {
  color: #9ca3af;
  font-size: 26rpx;
}
.error {
  color: #ef4444;
  font-size: 26rpx;
}
</style>
