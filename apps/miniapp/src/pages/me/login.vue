<script setup lang="ts">
import { ref } from 'vue';
import {
  loginWithWechatMP,
  loginWithAlipayMP,
  loginWithToutiaoMP,
  loginWithBaiduMP,
  loginWithQQMP,
  loginWithKuaishouMP,
  loginWithJDMP,
  loginWithLarkMP,
  loginWithEmailPassword,
  loginH5WithWechat,
} from '@/data/api/auth';
import { detectPlatform } from '@/core/platform/detect';

const loading = ref(false);
const err = ref<string>('');
const platform = detectPlatform();

// H5 邮箱密码表单 state
const email = ref('');
const password = ref('');

async function run<T>(fn: () => Promise<T>) {
  loading.value = true;
  err.value = '';
  try {
    await fn();
    uni.switchTab({
      url: '/pages/chat/index',
      fail: () => {
        // H5 history 模式 switchTab 可能返 fail, 用 redirectTo 兜底
        uni.redirectTo({ url: '/pages/chat/index' });
      },
    });
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

const onWechat = () => run(loginWithWechatMP);
const onAlipay = () => run(loginWithAlipayMP);
const onToutiao = () => run(loginWithToutiaoMP);
const onBaidu = () => run(loginWithBaiduMP);
const onQQ = () => run(loginWithQQMP);
const onKuaishou = () => run(loginWithKuaishouMP);
const onJD = () => run(loginWithJDMP);
const onLark = () => run(loginWithLarkMP);

async function onEmailLogin() {
  if (!email.value.trim() || !password.value) {
    err.value = '请填写邮箱和密码';
    return;
  }
  await run(() => loginWithEmailPassword(email.value, password.value));
}

function onH5Wechat() {
  // 不走 run() — 这是 redirect 跳转, 不返回 Promise
  loginH5WithWechat('/pages/chat/index');
}

function goRegister() {
  uni.navigateTo({ url: '/pages/me/register' });
}
function goForgot() {
  uni.navigateTo({ url: '/pages/me/forgot' });
}
function goTerms() {
  uni.navigateTo({ url: '/pages/legal/terms' });
}
function goPrivacy() {
  uni.navigateTo({ url: '/pages/legal/privacy' });
}
</script>

<template>
  <view class="login">
    <view class="brand">
      <text class="brand-name">BiuMind</text>
      <text class="brand-tag">你的 AI 第二大脑</text>
    </view>

    <!-- ── H5 双轨 ────────────────────────── -->
    <view v-if="platform === 'h5'" class="h5">
      <view class="form">
        <input
          v-model="email"
          class="input"
          type="text"
          placeholder="邮箱"
          :disabled="loading"
        />
        <input
          v-model="password"
          class="input"
          type="password"
          placeholder="密码"
          :disabled="loading"
        />
        <button class="btn btn-primary" :disabled="loading" @tap="onEmailLogin">
          {{ loading ? '登录中...' : '登录' }}
        </button>
        <view class="links">
          <text class="link" @tap="goForgot">忘记密码?</text>
          <text class="link" @tap="goRegister">还没账号? 注册</text>
        </view>
      </view>

      <view class="divider">
        <view class="divider-line"></view>
        <text class="divider-text">或使用</text>
        <view class="divider-line"></view>
      </view>

      <view class="oauth">
        <button class="btn btn-wx" :disabled="loading" @tap="onH5Wechat">
          微信网页登录
        </button>
      </view>
    </view>

    <!-- ── 9 端小程序: 一键登录 ──────────── -->
    <view v-else class="actions">
      <button v-if="platform === 'mp-weixin'" class="btn btn-wx" :disabled="loading" @tap="onWechat">
        {{ loading ? '...' : '微信一键登录' }}
      </button>
      <button v-else-if="platform === 'mp-alipay'" class="btn btn-alipay" :disabled="loading" @tap="onAlipay">
        {{ loading ? '...' : '支付宝一键登录' }}
      </button>
      <button v-else-if="platform === 'mp-toutiao'" class="btn btn-tt" :disabled="loading" @tap="onToutiao">
        {{ loading ? '...' : '抖音一键登录' }}
      </button>
      <button v-else-if="platform === 'mp-baidu'" class="btn btn-baidu" :disabled="loading" @tap="onBaidu">
        {{ loading ? '...' : '百度一键登录' }}
      </button>
      <button v-else-if="platform === 'mp-qq'" class="btn btn-qq" :disabled="loading" @tap="onQQ">
        {{ loading ? '...' : 'QQ 一键登录' }}
      </button>
      <button v-else-if="platform === 'mp-kuaishou'" class="btn btn-ks" :disabled="loading" @tap="onKuaishou">
        {{ loading ? '...' : '快手一键登录' }}
      </button>
      <button v-else-if="platform === 'mp-jd'" class="btn btn-jd" :disabled="loading" @tap="onJD">
        {{ loading ? '...' : '京东一键登录' }}
      </button>
      <button v-else-if="platform === 'mp-lark'" class="btn btn-lark" :disabled="loading" @tap="onLark">
        {{ loading ? '...' : '飞书一键登录' }}
      </button>
      <view v-else class="hint">
        平台 ({{ platform }}) 暂不支持
      </view>
    </view>

    <view v-if="err" class="error">{{ err }}</view>

    <view class="footer">
      <text class="footer-text">登录即代表同意</text>
      <text class="footer-link" @tap="goTerms">用户协议</text>
      <text class="footer-text">和</text>
      <text class="footer-link" @tap="goPrivacy">隐私政策</text>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.login {
  padding: 80rpx 48rpx 200rpx; /* 底部留 footer 高度 + 安全区 */
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.brand {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-top: 80rpx;
}
.brand-name {
  font-size: 64rpx;
  font-weight: 700;
  color: #1f2937;
}
.brand-tag {
  margin-top: 16rpx;
  font-size: 28rpx;
  color: #6b7280;
}
.h5 {
  margin-top: 96rpx;
  width: 100%;
}
.form {
  display: flex;
  flex-direction: column;
}
.input {
  height: 88rpx;
  padding: 0 24rpx;
  margin-bottom: 24rpx;
  border: 1px solid #d1d5db;
  border-radius: 12rpx;
  font-size: 30rpx;
  background: #fff;
}
.links {
  display: flex;
  justify-content: space-between;
  margin-top: 16rpx;
  padding: 0 8rpx;
}
.link {
  font-size: 26rpx;
  color: #3b82f6;
}
.divider {
  display: flex;
  align-items: center;
  margin: 64rpx 0 32rpx;
}
.divider-line {
  flex: 1;
  height: 1px;
  background: #e5e7eb;
}
.divider-text {
  margin: 0 24rpx;
  color: #9ca3af;
  font-size: 26rpx;
}
.oauth {
  width: 100%;
}
.actions {
  margin-top: 160rpx;
  width: 100%;
}
.btn {
  width: 100%;
  height: 88rpx;
  border-radius: 12rpx;
  font-size: 32rpx;
  color: #fff;
  margin-bottom: 16rpx;
}
.btn[disabled] { opacity: 0.6; }
.btn-primary { background: #3b82f6; }
.btn-wx     { background: #07c160; }
.btn-alipay { background: #1677ff; }
.btn-tt     { background: #161823; }
.btn-baidu  { background: #2932e1; }
.btn-qq     { background: #1989fa; }
.btn-ks     { background: #ff5500; }
.btn-jd     { background: #e1251b; }
.btn-lark   { background: #00d6b9; }
.hint {
  text-align: center;
  color: #9ca3af;
  font-size: 28rpx;
}
.error {
  margin-top: 32rpx;
  color: #ef4444;
  font-size: 26rpx;
  text-align: center;
}
.footer {
  /* fixed 定位 — 微信小程序里 page 是滚动容器, .login 的 min-height: 100vh
     + margin-top: auto 不可靠. 改 fixed 保证始终在可视区底部. */
  position: fixed;
  bottom: 48rpx;
  left: 0;
  right: 0;
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  padding: 0 48rpx;
}
.footer-text {
  font-size: 22rpx;
  color: #9ca3af;
}
.footer-link {
  font-size: 22rpx;
  color: #6366f1;
  margin: 0 4rpx;
}
</style>
