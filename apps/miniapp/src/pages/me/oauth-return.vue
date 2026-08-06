<script setup lang="ts">
// oauth-return — H5 OAuth callback 落地页.
//
// 后端 callback 完成后 302 redirect 到 /pages/me/oauth-return#access_token=...&refresh_token=...&expires_in=...&return=...
// (失败时 #error=&return=)
//
// 这里在 onMounted 立刻读 fragment, setTokens, 然后 redirectTo return path.
// 同时清理 URL fragment, 防止刷新页面重复消费 (虽然 fragment 不进 history,
// 但用户回退/复制链接可能保留).

import { onMounted, ref } from 'vue';
import { acceptOAuthFragment } from '@/data/api/auth';

const status = ref<'pending' | 'ok' | 'error'>('pending');
const errCode = ref('');

onMounted(() => {
  // #ifndef H5
  status.value = 'error';
  errCode.value = 'h5_only';
  return;
  // #endif

  // #ifdef H5
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const w = globalThis as any;
  const hash: string = w.location?.hash || '';
  const result = acceptOAuthFragment(hash);

  // 清空 fragment, 不让 token 在浏览器 URL 留痕
  if (typeof w.history?.replaceState === 'function') {
    w.history.replaceState(null, '', w.location.pathname + w.location.search);
  }

  if (!result.ok) {
    status.value = 'error';
    errCode.value = result.error || 'unknown';
    return;
  }
  status.value = 'ok';
  // 跳回 return path; SPA 用 redirectTo 而不是 navigateTo 避免压栈
  setTimeout(() => {
    uni.redirectTo({
      url: result.returnPath,
      fail: () => {
        // returnPath 可能是 '/' 或非法 — fallback 到 chat
        uni.redirectTo({ url: '/pages/chat/index' });
      },
    });
  }, 200);
  // #endif
});

function goLogin() {
  uni.redirectTo({ url: '/pages/me/login' });
}
</script>

<template>
  <view class="page">
    <view v-if="status === 'pending'" class="status">
      <text class="title">登录中...</text>
    </view>
    <view v-else-if="status === 'ok'" class="status">
      <text class="title">登录成功</text>
      <text class="hint">正在跳转...</text>
    </view>
    <view v-else class="status">
      <text class="title">登录失败</text>
      <text class="hint">原因: {{ errCode }}</text>
      <button class="btn-primary" @tap="goLogin">返回登录</button>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.page {
  padding: 96rpx 48rpx;
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.status {
  margin-top: 192rpx;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.title {
  font-size: 40rpx;
  font-weight: 600;
  color: #1f2937;
}
.hint {
  margin-top: 24rpx;
  color: #6b7280;
  font-size: 28rpx;
}
.btn-primary {
  margin-top: 64rpx;
  width: 320rpx;
  height: 80rpx;
  background: #3b82f6;
  color: #fff;
  border-radius: 12rpx;
  font-size: 28rpx;
}
</style>
