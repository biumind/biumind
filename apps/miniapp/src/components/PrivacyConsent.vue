<script setup lang="ts">
// PrivacyConsent — 首启隐私协议同意弹窗.
//
// 微信《小程序隐私保护指引》新规 (2023+, 2026 严格执行):
//   - 首次进入小程序必须弹窗向用户展示隐私协议
//   - 用户同意才能使用涉及个人信息的接口 (登录 / 录音 / 相册等)
//   - 已同意状态可缓存, 协议版本变更需重新弹
//
// 用 storage 记录: { version, agreedAt }. version 升级时重弹.
//
// 这里是全局 overlay, App.vue 引用; 未同意时所有页面不可交互.

import { ref, onMounted, computed } from 'vue';

// 改协议时升 version, 用户重新弹窗同意
const PRIVACY_VERSION = '1.0.0';
const STORAGE_KEY = 'biumind.privacy_consent';

interface ConsentRecord {
  version: string;
  agreedAt: number;
}

const visible = ref(false);

const agreedVersion = computed<string | null>(() => {
  try {
    const raw = uni.getStorageSync(STORAGE_KEY);
    if (!raw) return null;
    const rec: ConsentRecord =
      typeof raw === 'string' ? JSON.parse(raw) : (raw as ConsentRecord);
    return rec?.version || null;
  } catch {
    return null;
  }
});

onMounted(() => {
  if (agreedVersion.value !== PRIVACY_VERSION) {
    visible.value = true;
  }
});

function agree() {
  const rec: ConsentRecord = {
    version: PRIVACY_VERSION,
    agreedAt: Date.now(),
  };
  try {
    uni.setStorageSync(STORAGE_KEY, JSON.stringify(rec));
  } catch {
    /* storage 不可用 — 这次会话不弹,下次再问 */
  }
  visible.value = false;
}

function disagree() {
  // 不同意则退出小程序 — 微信规则要求
  uni.showModal({
    title: '需要您的同意',
    content: '不同意《隐私政策》将无法使用 BiuMind, 是否退出?',
    confirmText: '退出',
    cancelText: '再看看',
    success: (r) => {
      if (r.confirm) {
        // #ifdef MP-WEIXIN
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const w = wx as any;
        if (typeof w.exitMiniProgram === 'function') {
          w.exitMiniProgram();
        }
        // #endif
        // H5 / 其他平台 — 留在弹窗, 不可绕过
      }
    },
  });
}

function openPrivacy() {
  uni.navigateTo({ url: '/pages/legal/privacy' });
}

function openTerms() {
  uni.navigateTo({ url: '/pages/legal/terms' });
}
</script>

<template>
  <view v-if="visible" class="mask">
    <view class="card">
      <view class="title">服务协议与隐私政策</view>
      <view class="body">
        <text class="paragraph">
          欢迎使用 BiuMind. 在使用前, 请认真阅读
          <text class="link" @tap="openTerms">《用户协议》</text>
          和
          <text class="link" @tap="openPrivacy">《隐私政策》</text>.
        </text>
        <text class="paragraph">
          我们将依据上述协议为你提供 AI 对话, 知识管理等服务,
          仅在为提供服务所必需的最小范围内收集和使用你的个人信息,
          包括登录凭证, 设备标识, 对话内容. 我们不会向第三方出售你的信息.
        </text>
        <text class="paragraph">
          点击「同意并继续」即表示你已阅读并同意上述协议. 你可随时在
          「我的 - 设置」中查看协议或撤回同意.
        </text>
      </view>
      <view class="actions">
        <button class="btn-ghost" @tap="disagree">不同意</button>
        <button class="btn-primary" @tap="agree">同意并继续</button>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 9999;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48rpx;
}
.card {
  background: #fff;
  border-radius: 24rpx;
  width: 100%;
  max-width: 640rpx;
  padding: 40rpx 36rpx 32rpx;
  display: flex;
  flex-direction: column;
}
.title {
  font-size: 34rpx;
  font-weight: 600;
  color: #1f2937;
  text-align: center;
  margin-bottom: 24rpx;
}
.body {
  max-height: 60vh;
  overflow-y: auto;
  font-size: 26rpx;
  color: #4b5563;
  line-height: 1.7;
}
.paragraph {
  display: block;
  margin-bottom: 18rpx;
}
.link {
  color: #2563eb;
  text-decoration: underline;
}
.actions {
  display: flex;
  gap: 16rpx;
  margin-top: 28rpx;
}
.btn-ghost {
  flex: 1;
  background: #f3f4f6;
  color: #4b5563;
  border-radius: 12rpx;
  font-size: 28rpx;
  height: 80rpx;
  line-height: 80rpx;
}
.btn-primary {
  flex: 1;
  background: #3b82f6;
  color: #fff;
  border-radius: 12rpx;
  font-size: 28rpx;
  height: 80rpx;
  line-height: 80rpx;
}
</style>
