<script setup lang="ts">
import { onLaunch, onShow } from '@dcloudio/uni-app';
import { detectPlatform } from '@/core/platform/detect';
import PrivacyConsent from '@/components/PrivacyConsent.vue';

onLaunch(() => {
  const platform = detectPlatform();
  console.log('[BiuMind MiniApp] launched on platform:', platform);
});

onShow(() => {
  // App resumed — TODO: re-arm RealtimeHub topic subscriptions
});
</script>

<template>
  <!-- 隐私协议首启 gate — 全局 overlay, 未同意前不可关闭 -->
  <PrivacyConsent />
</template>

<style lang="scss">
/* 全局样式 — 各页面用 scoped 样式覆盖 */
page {
  background-color: #f6f7fb;
  color: #1f2937;
  font-family: -apple-system, BlinkMacSystemFont, 'PingFang SC',
    'Microsoft YaHei', sans-serif;
}
</style>
