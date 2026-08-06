<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Wallet, Monitor, Document } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'
import { errorMessage } from '@/api/http'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const email = ref('')
const password = ref('')
const submitting = ref(false)
const errMsg = ref('')

const reasonText = computed(() => {
  if (route.query.reason === 'session_expired') return '会话已过期, 请重新登录'
  if (route.query.error === 'not_admin') return '当前账号无后台访问权限'
  return ''
})

onMounted(() => {
  // 已登录 → 跳首页
  if (auth.token && auth.user) router.replace('/')
})

async function submit() {
  errMsg.value = ''
  if (!email.value || !password.value) {
    errMsg.value = '邮箱和密码必填'
    return
  }
  submitting.value = true
  try {
    await auth.login(email.value.trim(), password.value)
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || '/'
    router.replace(redirect)
  } catch (e) {
    errMsg.value = errorMessage(e) || '登录失败'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="brand-side">
      <div class="brand-bg" aria-hidden="true"></div>
      <div class="brand-grid" aria-hidden="true"></div>

      <div class="brand-content">
        <div class="brand-header">
          <div class="logo-mark">
            <img src="/favicon.svg" alt="BiuMind" />
          </div>
          <div class="brand-title">
            <h1>BiuMind</h1>
            <div class="brand-sub">Admin Console · 后台管理</div>
          </div>
        </div>

        <p class="brand-tagline">统一管理用户、套餐、模型与计费 — 一个面板看清整个 SaaS。</p>

        <div class="features">
          <div class="feature">
            <el-icon class="fi"><User /></el-icon>
            <div class="ft">用户管理</div>
            <div class="fd">订阅 · 积分 · 状态</div>
          </div>
          <div class="feature">
            <el-icon class="fi"><Wallet /></el-icon>
            <div class="ft">套餐与计费</div>
            <div class="fd">价格 · 试用 · 退款</div>
          </div>
          <div class="feature">
            <el-icon class="fi"><Monitor /></el-icon>
            <div class="ft">服务监控</div>
            <div class="fd">模型 · 渠道 · 汇率</div>
          </div>
          <div class="feature">
            <el-icon class="fi"><Document /></el-icon>
            <div class="ft">审计日志</div>
            <div class="fd">操作可追溯</div>
          </div>
        </div>

        <div class="brand-footer">
          <span class="status">
            <span class="dot"></span>
            服务运行中
          </span>
          <span class="copyright">© 2026 BiuMind</span>
        </div>
      </div>
    </div>

    <div class="form-side">
      <div class="login-card">
        <h2>{{ $t('login.title') }}</h2>
        <p class="subtitle">{{ $t('login.subtitle') }}</p>

        <el-alert
          v-if="reasonText"
          :title="reasonText"
          type="warning"
          show-icon
          :closable="false"
          style="margin-bottom: 16px"
        />

        <el-form @submit.prevent="submit" label-position="top">
          <el-form-item :label="$t('login.email')">
            <el-input
              v-model="email"
              type="email"
              autocomplete="username"
              placeholder="you@example.com"
              size="large"
              @keyup.enter="submit"
            />
          </el-form-item>
          <el-form-item :label="$t('login.password')">
            <el-input
              v-model="password"
              type="password"
              autocomplete="current-password"
              show-password
              size="large"
              @keyup.enter="submit"
            />
          </el-form-item>

          <el-alert
            v-if="errMsg"
            :title="errMsg"
            type="error"
            show-icon
            :closable="false"
            style="margin-bottom: 12px"
          />

          <el-button
            type="primary"
            size="large"
            style="width: 100%"
            :loading="submitting"
            @click="submit"
          >
            {{ submitting ? $t('login.submitting') : $t('login.submit') }}
          </el-button>
        </el-form>

        <div class="login-tip">
          仅限授权管理员 · 所有操作将被审计记录
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.login-page {
  display: flex;
  height: 100vh;
  min-height: 640px;
  background: #0f1226;
}

/* ============ 左侧 brand 区 ============ */
.brand-side {
  flex: 1;
  position: relative;
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.brand-bg {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(ellipse 80% 60% at 80% 0%, rgba(124, 58, 237, 0.55) 0%, transparent 60%),
    radial-gradient(ellipse 70% 60% at 0% 100%, rgba(37, 99, 235, 0.5) 0%, transparent 60%),
    linear-gradient(135deg, #1e1b4b 0%, #0f1226 60%, #0a0e1f 100%);
}

.brand-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.04) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.04) 1px, transparent 1px);
  background-size: 56px 56px;
  mask-image: radial-gradient(ellipse 70% 70% at 50% 50%, #000 30%, transparent 80%);
  -webkit-mask-image: radial-gradient(ellipse 70% 70% at 50% 50%, #000 30%, transparent 80%);
}

.brand-content {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 520px;
  padding: 48px;
  display: flex;
  flex-direction: column;
  gap: 32px;
}

.brand-header {
  display: flex;
  align-items: center;
  gap: 16px;
}

.logo-mark {
  width: 64px;
  height: 64px;
  border-radius: 16px;
  overflow: hidden;
  filter: drop-shadow(0 12px 32px rgba(124, 58, 237, 0.45));
  flex-shrink: 0;
  img {
    display: block;
    width: 100%;
    height: 100%;
  }
}

.brand-title {
  h1 {
    margin: 0;
    font-size: 28px;
    font-weight: 700;
    letter-spacing: -0.02em;
    background: linear-gradient(180deg, #fff 0%, #cbd5e1 100%);
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
    background-clip: text;
  }
  .brand-sub {
    margin-top: 4px;
    font-size: 13px;
    color: #94a3b8;
    letter-spacing: 0.02em;
  }
}

.brand-tagline {
  margin: 0;
  font-size: 15px;
  line-height: 1.7;
  color: #cbd5e1;
  max-width: 420px;
}

/* ============ 2x2 features ============ */
.features {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-top: 8px;
}

.feature {
  padding: 16px 18px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  backdrop-filter: blur(10px);
  transition: background 0.2s, border-color 0.2s, transform 0.2s;
  &:hover {
    background: rgba(255, 255, 255, 0.07);
    border-color: rgba(124, 58, 237, 0.4);
    transform: translateY(-2px);
  }
  .fi {
    font-size: 20px;
    color: #a78bfa;
    margin-bottom: 8px;
  }
  .ft {
    font-size: 14px;
    font-weight: 600;
    color: #f1f5f9;
    margin-bottom: 2px;
  }
  .fd {
    font-size: 12px;
    color: #94a3b8;
  }
}

/* ============ brand 底部 ============ */
.brand-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 12px;
  font-size: 12px;
  color: #94a3b8;
  .status {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: #22c55e;
    box-shadow: 0 0 8px rgba(34, 197, 94, 0.7);
    animation: pulse 2s ease-in-out infinite;
  }
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}

/* ============ 右侧表单区 ============ */
.form-side {
  flex: 0 0 440px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px 40px;
  background: #fafafa;
}

.login-card {
  width: 100%;
  max-width: 380px;
  background: #fff;
  border-radius: 16px;
  padding: 36px 32px;
  box-shadow:
    0 1px 2px rgba(15, 23, 42, 0.04),
    0 12px 32px rgba(15, 23, 42, 0.08);
  h2 {
    margin: 0 0 8px;
    font-size: 24px;
    font-weight: 600;
    color: #0f172a;
    letter-spacing: -0.01em;
  }
  .subtitle {
    color: #64748b;
    margin: 0 0 28px;
    font-size: 13px;
  }
}

.login-tip {
  margin-top: 20px;
  text-align: center;
  font-size: 12px;
  color: #94a3b8;
  line-height: 1.5;
}

/* ============ 响应式 ============ */
@media (max-width: 960px) {
  .form-side { flex: 0 0 400px; }
  .brand-content { padding: 32px; }
}

@media (max-width: 768px) {
  .login-page { flex-direction: column; height: auto; min-height: 100vh; }
  .brand-side { flex: 0 0 auto; padding: 32px 0; }
  .brand-content { padding: 24px; gap: 20px; max-width: 100%; }
  .features { grid-template-columns: 1fr 1fr; }
  .brand-tagline { font-size: 14px; }
  .form-side { flex: 1; padding: 32px 20px; }
  .login-card { padding: 28px 24px; }
}
</style>
