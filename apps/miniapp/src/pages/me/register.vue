<script setup lang="ts">
import { ref } from 'vue';
import { register, verifyEmail } from '@/data/api/auth';

// 注册流程: 输入 email/password/display_name → 后端发邮箱验证码 → 用户输入 code → 自动登录
const step = ref<'form' | 'verify'>('form');
const email = ref('');
const password = ref('');
const displayName = ref('');
const code = ref('');
const loading = ref(false);
const err = ref('');

async function onSubmit() {
  if (!email.value || !password.value) {
    err.value = '请填写邮箱和密码';
    return;
  }
  if (password.value.length < 8) {
    err.value = '密码至少 8 位';
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    await register(email.value, password.value, displayName.value || email.value.split('@')[0]);
    step.value = 'verify';
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

async function onVerify() {
  if (!code.value) {
    err.value = '请输入邮箱验证码';
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    await verifyEmail(email.value, code.value);
    uni.redirectTo({ url: '/pages/chat/index' });
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
      <text class="title">{{ step === 'form' ? '注册' : '验证邮箱' }}</text>
      <text class="back" @tap="goBack">取消</text>
    </view>

    <view v-if="step === 'form'" class="form">
      <input v-model="email" class="input" type="text" placeholder="邮箱" :disabled="loading" />
      <input v-model="displayName" class="input" type="text" placeholder="显示名 (可选)" :disabled="loading" />
      <input v-model="password" class="input" type="password" placeholder="密码 (≥8 位)" :disabled="loading" />
      <button class="btn-primary" :disabled="loading" @tap="onSubmit">
        {{ loading ? '提交中...' : '创建账号' }}
      </button>
    </view>

    <view v-else class="form">
      <text class="hint">已发送验证码到 {{ email }}, 请查收</text>
      <input v-model="code" class="input" type="text" placeholder="6 位验证码" :disabled="loading" />
      <button class="btn-primary" :disabled="loading" @tap="onVerify">
        {{ loading ? '验证中...' : '验证并登录' }}
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
