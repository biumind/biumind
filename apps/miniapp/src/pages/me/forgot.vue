<script setup lang="ts">
import { ref } from 'vue';
import { forgotPassword, resetPassword } from '@/data/api/auth';

const step = ref<'request' | 'reset'>('request');
const email = ref('');
const code = ref('');
const newPassword = ref('');
const loading = ref(false);
const err = ref('');

async function onRequest() {
  if (!email.value) {
    err.value = '请填写邮箱';
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    await forgotPassword(email.value);
    step.value = 'reset';
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

async function onReset() {
  if (!code.value || !newPassword.value) {
    err.value = '请填写验证码和新密码';
    return;
  }
  if (newPassword.value.length < 8) {
    err.value = '新密码至少 8 位';
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    await resetPassword(email.value, code.value, newPassword.value);
    uni.showToast({ title: '密码已重置, 请登录', icon: 'success' });
    setTimeout(() => uni.redirectTo({ url: '/pages/me/login' }), 800);
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

function goBack() {
  uni.navigateBack();
}
</script>

<template>
  <view class="page">
    <view class="header">
      <text class="title">{{ step === 'request' ? '找回密码' : '设置新密码' }}</text>
      <text class="back" @tap="goBack">取消</text>
    </view>

    <view v-if="step === 'request'" class="form">
      <text class="hint">输入注册邮箱, 我们将发送重置验证码</text>
      <input v-model="email" class="input" type="text" placeholder="邮箱" :disabled="loading" />
      <button class="btn-primary" :disabled="loading" @tap="onRequest">
        {{ loading ? '发送中...' : '发送验证码' }}
      </button>
    </view>

    <view v-else class="form">
      <text class="hint">验证码已发送到 {{ email }}</text>
      <input v-model="code" class="input" type="text" placeholder="6 位验证码" :disabled="loading" />
      <input v-model="newPassword" class="input" type="password" placeholder="新密码 (≥8 位)" :disabled="loading" />
      <button class="btn-primary" :disabled="loading" @tap="onReset">
        {{ loading ? '提交中...' : '重置密码' }}
      </button>
    </view>

    <view v-if="err" class="error">{{ err }}</view>
  </view>
</template>

<style lang="scss" scoped>
.page { padding: 32rpx; min-height: 100vh; }
.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 24rpx 0 64rpx;
}
.title { font-size: 40rpx; font-weight: 600; color: #1f2937; }
.back { font-size: 28rpx; color: #6b7280; }
.form { display: flex; flex-direction: column; }
.hint { color: #6b7280; font-size: 26rpx; margin-bottom: 24rpx; }
.input {
  height: 88rpx;
  padding: 0 24rpx;
  margin-bottom: 24rpx;
  border: 1px solid #d1d5db;
  border-radius: 12rpx;
  font-size: 30rpx;
  background: #fff;
}
.btn-primary {
  height: 88rpx;
  background: #3b82f6;
  color: #fff;
  border-radius: 12rpx;
  font-size: 32rpx;
}
.btn-primary[disabled] { opacity: 0.6; }
.error {
  margin-top: 32rpx;
  color: #ef4444;
  font-size: 26rpx;
  text-align: center;
}
</style>
