<script setup lang="ts">
// pages/me/about — 关于 BiuMind. 静态页, 显示版本/ICP/开源协议/官网/联系我们.
//
// 版本号从 manifest.json 取 (vite-plugin-uni 注入到 process.env, 但小程序
// 端拿不到). 简化: 从 wx.getAccountInfoSync 拿 envVersion + 包发布版本.

import { ref, onMounted } from 'vue';

const versionName = ref('0.1.0'); // manifest 同步, 发版时一起改
const envVersion = ref('');

onMounted(() => {
  // #ifdef MP-WEIXIN
  try {
    const wxAny = (typeof wx !== 'undefined' ? wx : null) as
      | { getAccountInfoSync?: () => { miniProgram: { envVersion: string; version?: string } } }
      | null;
    if (wxAny?.getAccountInfoSync) {
      const info = wxAny.getAccountInfoSync();
      envVersion.value = info.miniProgram.envVersion; // 'release' | 'trial' | 'develop'
      if (info.miniProgram.version) versionName.value = info.miniProgram.version;
    }
  } catch {
    /* noop */
  }
  // #endif
});

function envLabel(env: string): string {
  if (env === 'release') return '正式版';
  if (env === 'trial') return '体验版';
  if (env === 'develop') return '开发版';
  return env || '-';
}

function copyText(text: string) {
  uni.setClipboardData({
    data: text,
    success: () => uni.showToast({ title: '已复制', icon: 'none' }),
  });
}

function openLink(path: string) {
  uni.navigateTo({ url: path });
}
</script>

<template>
  <view class="about">
    <!-- ── Logo + 名称 ── -->
    <view class="hero">
      <view class="logo">
        <text class="logo-letter">B</text>
      </view>
      <text class="title">BiuMind</text>
      <text class="subtitle">你的 AI 第二大脑</text>
      <view class="version-chip">
        <text class="version-text">v{{ versionName }}</text>
        <text v-if="envVersion" class="env-text">· {{ envLabel(envVersion) }}</text>
      </view>
    </view>

    <!-- ── 信息行 ── -->
    <view class="section">
      <view class="row" @tap="openLink('/pages/legal/terms')">
        <text class="row-label">用户协议</text>
        <text class="row-arrow">›</text>
      </view>
      <view class="row" @tap="openLink('/pages/legal/privacy')">
        <text class="row-label">隐私政策</text>
        <text class="row-arrow">›</text>
      </view>
      <view class="row" @tap="copyText('your-biumind.example.com')">
        <text class="row-label">官方网站</text>
        <text class="row-value">your-biumind.example.com</text>
      </view>
      <view class="row" @tap="copyText('hi@your-biumind.example.com')">
        <text class="row-label">联系我们</text>
        <text class="row-value">hi@your-biumind.example.com</text>
      </view>
    </view>

    <!-- ── 版权 ── -->
    <view class="footer">
      <text class="footer-text">© 2026 BiuMind. All rights reserved.</text>
      <text class="footer-text">Made with ❤️ in 北京</text>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.about {
  padding: 32rpx;
}
.hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 64rpx 0 48rpx;
}
.logo {
  width: 128rpx;
  height: 128rpx;
  border-radius: 28rpx;
  background: #3b82f6;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 24rpx;
  box-shadow: 0 8rpx 24rpx rgba(59, 130, 246, 0.3);
}
.logo-letter {
  color: #fff;
  font-size: 72rpx;
  font-weight: 700;
}
.title {
  font-size: 44rpx;
  font-weight: 700;
  color: #0f172a;
  letter-spacing: -1rpx;
}
.subtitle {
  margin-top: 8rpx;
  font-size: 26rpx;
  color: #64748b;
}
.version-chip {
  margin-top: 24rpx;
  padding: 6rpx 20rpx;
  background: #f1f5f9;
  border-radius: 999rpx;
  display: flex;
  align-items: center;
  gap: 8rpx;
}
.version-text {
  font-size: 22rpx;
  color: #475569;
  font-weight: 500;
}
.env-text {
  font-size: 22rpx;
  color: #94a3b8;
}

.section {
  background: #fff;
  border-radius: 16rpx;
  margin-bottom: 32rpx;
}
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 28rpx 32rpx;
  border-bottom: 1rpx solid #f1f5f9;
}
.row:last-child {
  border-bottom: none;
}
.row-label {
  font-size: 28rpx;
  color: #1e293b;
}
.row-value {
  font-size: 26rpx;
  color: #94a3b8;
}
.row-arrow {
  font-size: 28rpx;
  color: #cbd5e1;
}

.footer {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 32rpx 0;
  gap: 8rpx;
}
.footer-text {
  font-size: 22rpx;
  color: #94a3b8;
}
</style>
