<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import type { SystemConfigEntry } from '@/api/admin'
import { usePermission } from '@/composables/usePermission'

const { isSuper } = usePermission()

const configs = ref<SystemConfigEntry[]>([])
const loading = ref(false)

// alert.email 表单
interface EmailConfig {
  enabled: boolean
  smtp_host: string
  smtp_port: number
  smtp_user: string
  smtp_pass: string
  smtp_tls: boolean
  from: string
  to: string[]
  webhook_secret?: string
}

const emailCfg = ref<EmailConfig>({
  enabled: false,
  smtp_host: '',
  smtp_port: 465,
  smtp_user: '',
  smtp_pass: '',
  smtp_tls: true,
  from: '',
  to: [],
})
const emailToInput = ref('')
const emailExists = ref(false)
const emailIsRedacted = ref(false)
const savingEmail = ref(false)

// ─── W5 支付通道 ─────────────────────────────────────
//
// 三个 secret key (payment.stripe / payment.wechat / payment.alipay).
// 配置 schema 与 services/identity/internal/billing/{wechat,alipay}.go 的
// Config struct 对齐. 私钥字段都是密码框, 留空 = 保持不变.

interface StripeConfig {
  enabled: boolean
  secret_key: string
  webhook_secret: string
  price_to_plan: Record<string, string>
}
interface WechatPayConfig {
  enabled: boolean
  app_id: string
  mch_id: string
  apiv3_key: string
  cert_serial_no: string
  apiclient_key_pem: string
  platform_public_key: string
  notify_url: string
}
interface AlipayPayConfig {
  enabled: boolean
  app_id: string
  private_key_pem: string
  alipay_public_key_pem: string
  notify_url: string
  return_url: string
}

const stripeCfg = ref<StripeConfig>({
  enabled: false, secret_key: '', webhook_secret: '', price_to_plan: {},
})
const stripePriceMap = ref('') // textarea 编辑: 一行一对 price_xxx=plan_code
const stripeIsRedacted = ref(false)
const savingStripe = ref(false)

const wechatCfg = ref<WechatPayConfig>({
  enabled: false, app_id: '', mch_id: '', apiv3_key: '', cert_serial_no: '',
  apiclient_key_pem: '', platform_public_key: '', notify_url: '',
})
const wechatIsRedacted = ref(false)
const savingWechat = ref(false)

const alipayCfg = ref<AlipayPayConfig>({
  enabled: false, app_id: '', private_key_pem: '', alipay_public_key_pem: '',
  notify_url: '', return_url: '',
})
const alipayIsRedacted = ref(false)
const savingAlipay = ref(false)

// auth.email — 注册邮箱验证码 SMTP. 收件人由注册流程动态指定 (用户邮箱),
// 这里只配 SMTP 凭据 + 验证码 TTL + 主题模板.
interface AuthEmailConfig {
  enabled: boolean
  smtp_host: string
  smtp_port: number
  smtp_user: string
  smtp_pass: string
  smtp_tls: boolean
  from: string
  code_ttl_seconds: number
  subject: string
}

const authEmailCfg = ref<AuthEmailConfig>({
  enabled: false,
  smtp_host: '',
  smtp_port: 465,
  smtp_user: '',
  smtp_pass: '',
  smtp_tls: true,
  from: '',
  code_ttl_seconds: 600,
  subject: '[BiuMind] 邮箱验证码',
})
const authEmailExists = ref(false)
const authEmailIsRedacted = ref(false)
const savingAuthEmail = ref(false)

async function load() {
  loading.value = true
  try {
    configs.value = await api.listSystemConfig()
    const entry = configs.value.find((c) => c.key === 'alert.email')
    if (entry) {
      emailExists.value = true
      const v = entry.value as EmailConfig & { _redacted?: boolean }
      if (v?._redacted) {
        emailIsRedacted.value = true
      } else {
        Object.assign(emailCfg.value, v)
        emailToInput.value = (v.to ?? []).join(', ')
      }
    }
    const authEntry = configs.value.find((c) => c.key === 'auth.email')
    if (authEntry) {
      authEmailExists.value = true
      const v = authEntry.value as AuthEmailConfig & { _redacted?: boolean }
      if (v?._redacted) {
        authEmailIsRedacted.value = true
      } else {
        Object.assign(authEmailCfg.value, v)
      }
    }

    // W5 支付通道
    const stripeEntry = configs.value.find((c) => c.key === 'payment.stripe')
    if (stripeEntry) {
      const v = stripeEntry.value as StripeConfig & { _redacted?: boolean }
      if (v?._redacted) {
        stripeIsRedacted.value = true
      } else {
        Object.assign(stripeCfg.value, v)
        stripePriceMap.value = Object.entries(stripeCfg.value.price_to_plan ?? {})
          .map(([k, v]) => `${k}=${v}`).join('\n')
      }
    }
    const wechatEntry = configs.value.find((c) => c.key === 'payment.wechat')
    if (wechatEntry) {
      const v = wechatEntry.value as WechatPayConfig & { _redacted?: boolean }
      if (v?._redacted) {
        wechatIsRedacted.value = true
      } else {
        Object.assign(wechatCfg.value, v)
      }
    }
    const alipayEntry = configs.value.find((c) => c.key === 'payment.alipay')
    if (alipayEntry) {
      const v = alipayEntry.value as AlipayPayConfig & { _redacted?: boolean }
      if (v?._redacted) {
        alipayIsRedacted.value = true
      } else {
        Object.assign(alipayCfg.value, v)
      }
    }
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function saveEmail() {
  // 校验
  if (emailCfg.value.enabled) {
    if (!emailCfg.value.smtp_host || !emailCfg.value.from) {
      ElMessage.warning('启用时 smtp_host / from 必填')
      return
    }
  }
  emailCfg.value.to = emailToInput.value
    .split(/[,\n;]+/)
    .map((s) => s.trim())
    .filter(Boolean)

  try {
    await ElMessageBox.confirm(
      `${emailCfg.value.enabled ? '启用' : '禁用'} 告警邮件并保存配置?`,
      '确认',
      { type: 'warning' },
    )
  } catch {
    return
  }

  savingEmail.value = true
  try {
    await api.setSystemConfig('alert.email', emailCfg.value)
    ElMessage.success('已保存')
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    savingEmail.value = false
  }
}

// ─── 发测试邮件 ───
// dialog 让用户输入收件人, 一键校验 SMTP 是否可用. UI 里没改的字段
// (尤其 smtp_pass 保持不变留空) 由后端按 key fallback.
const testDialog = ref<{
  visible: boolean
  loading: boolean
  key: 'alert.email' | 'auth.email'
  to: string
}>({ visible: false, loading: false, key: 'alert.email', to: '' })

function openTestDialog(key: 'alert.email' | 'auth.email') {
  testDialog.value.key = key
  testDialog.value.visible = true
  // alert.email: 默认拿第一个收件人; auth.email: 默认空让 superadmin 输自己邮箱
  if (key === 'alert.email') {
    const first = (emailToInput.value || '').split(/[,\n;]+/).map(s => s.trim()).filter(Boolean)[0]
    testDialog.value.to = first || ''
  } else {
    testDialog.value.to = ''
  }
}

async function runTestEmail() {
  const to = testDialog.value.to.trim()
  if (!to || !/^.+@.+\..+$/.test(to)) {
    ElMessage.warning('请输入合法收件邮箱')
    return
  }
  testDialog.value.loading = true
  try {
    const cfg = testDialog.value.key === 'alert.email' ? emailCfg.value : authEmailCfg.value
    await api.sendTestEmail({
      key: testDialog.value.key,
      smtp_host: cfg.smtp_host || undefined,
      smtp_port: cfg.smtp_port || undefined,
      smtp_user: cfg.smtp_user || undefined,
      smtp_pass: cfg.smtp_pass || undefined, // 留空 → 后端 fallback 到存值
      smtp_tls: cfg.smtp_tls,
      from: cfg.from || undefined,
      to,
      subject: testDialog.value.key === 'auth.email'
        ? (authEmailCfg.value.subject || undefined)
        : undefined,
    })
    ElMessage.success(`测试邮件已发往 ${to}`)
    testDialog.value.visible = false
  } catch (e) {
    ElMessage.error(`SMTP 发送失败: ${errorMessage(e)}`)
  } finally {
    testDialog.value.loading = false
  }
}

async function saveAuthEmail() {
  if (authEmailCfg.value.enabled) {
    if (!authEmailCfg.value.smtp_host || !authEmailCfg.value.from) {
      ElMessage.warning('启用时 smtp_host / from 必填')
      return
    }
  }
  if (authEmailCfg.value.code_ttl_seconds <= 0) {
    authEmailCfg.value.code_ttl_seconds = 600
  }
  try {
    await ElMessageBox.confirm(
      `${authEmailCfg.value.enabled ? '启用' : '禁用'} 注册邮箱验证邮件并保存配置?`,
      '确认',
      { type: 'warning' },
    )
  } catch {
    return
  }

  savingAuthEmail.value = true
  try {
    await api.setSystemConfig('auth.email', authEmailCfg.value)
    ElMessage.success('已保存')
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    savingAuthEmail.value = false
  }
}

// ─── 支付通道保存 ─────────────────────────────────

async function saveStripe() {
  if (stripeCfg.value.enabled && !stripeCfg.value.secret_key) {
    ElMessage.warning('启用时 secret_key 必填')
    return
  }
  // 解析 price_to_plan textarea (line: price_xxx=pro)
  const map: Record<string, string> = {}
  for (const line of stripePriceMap.value.split(/\n+/).map(s => s.trim()).filter(Boolean)) {
    const [k, v] = line.split('=').map(s => s.trim())
    if (k && v) map[k] = v
  }
  stripeCfg.value.price_to_plan = map

  try {
    await ElMessageBox.confirm(
      `${stripeCfg.value.enabled ? '启用' : '禁用'} Stripe 支付并保存配置?`,
      '确认', { type: 'warning' },
    )
  } catch { return }

  savingStripe.value = true
  try {
    await api.setSystemConfig('payment.stripe', stripeCfg.value)
    ElMessage.success('已保存')
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    savingStripe.value = false
  }
}

async function saveWechat() {
  if (wechatCfg.value.enabled) {
    if (!wechatCfg.value.app_id || !wechatCfg.value.mch_id || !wechatCfg.value.notify_url) {
      ElMessage.warning('启用时 app_id / mch_id / notify_url 必填')
      return
    }
    if (wechatCfg.value.apiv3_key && wechatCfg.value.apiv3_key.length !== 32) {
      ElMessage.warning('APIv3 密钥必须是 32 字节 (微信商户后台手动设置)')
      return
    }
    if (!wechatCfg.value.notify_url.startsWith('https://')) {
      ElMessage.warning('notify_url 必须 https')
      return
    }
  }
  try {
    await ElMessageBox.confirm(
      `${wechatCfg.value.enabled ? '启用' : '禁用'} 微信支付并保存配置?`,
      '确认', { type: 'warning' },
    )
  } catch { return }

  savingWechat.value = true
  try {
    await api.setSystemConfig('payment.wechat', wechatCfg.value)
    ElMessage.success('已保存')
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    savingWechat.value = false
  }
}

async function saveAlipay() {
  if (alipayCfg.value.enabled) {
    if (!alipayCfg.value.app_id || !alipayCfg.value.private_key_pem || !alipayCfg.value.alipay_public_key_pem) {
      ElMessage.warning('启用时 app_id / 私钥 / 支付宝公钥 必填')
      return
    }
    if (!alipayCfg.value.notify_url.startsWith('https://')) {
      ElMessage.warning('notify_url 必须 https')
      return
    }
  }
  try {
    await ElMessageBox.confirm(
      `${alipayCfg.value.enabled ? '启用' : '禁用'} 支付宝并保存配置?`,
      '确认', { type: 'warning' },
    )
  } catch { return }

  savingAlipay.value = true
  try {
    await api.setSystemConfig('payment.alipay', alipayCfg.value)
    ElMessage.success('已保存')
    await load()
  } catch (e) {
    ElMessage.error(errorMessage(e))
  } finally {
    savingAlipay.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <h1 class="page-title">系统配置</h1>

    <el-alert
      v-if="!isSuper()"
      title="只有超级管理员可以修改这里的配置"
      type="info"
      show-icon
      :closable="false"
      style="margin-bottom: 16px"
    />

    <el-card v-loading="loading" shadow="never" class="cfg-card">
      <template #header>
        <span>告警邮件</span>
        <span class="text-muted" style="margin-left: 8px; font-size: 13px">
          alertmanager → identity webhook → SMTP 发邮件
        </span>
      </template>

      <el-alert
        v-if="emailIsRedacted"
        title="此配置含敏感字段, 仅 superadmin 可见 / 可编辑"
        type="warning"
        show-icon
        :closable="false"
        style="margin-bottom: 16px"
      />

      <el-form :disabled="!isSuper() || emailIsRedacted" label-width="120px">
        <el-form-item label="启用">
          <el-switch v-model="emailCfg.enabled" />
          <span class="text-muted" style="margin-left: 12px; font-size: 12px">
            关闭时 alertmanager 仍会推过来, 但 identity 不发邮件 (会写 audit)
          </span>
        </el-form-item>

        <el-form-item label="SMTP 服务器">
          <el-input v-model="emailCfg.smtp_host" placeholder="smtp.qiye.aliyun.com" />
        </el-form-item>
        <el-form-item label="SMTP 端口">
          <el-input-number v-model="emailCfg.smtp_port" :min="1" :max="65535" :step="1" />
          <span class="text-muted" style="margin-left: 12px; font-size: 12px">
            隐式 TLS (SMTPS) 用 465; STARTTLS 用 587; 明文用 25
          </span>
        </el-form-item>
        <el-form-item label="使用 TLS">
          <el-switch v-model="emailCfg.smtp_tls" />
          <span class="text-muted" style="margin-left: 12px; font-size: 12px">
            打开 = 隐式 TLS (SMTPS, 465); 关闭 = 走 net/smtp 自动协商 (587/25)
          </span>
        </el-form-item>
        <el-form-item label="用户名">
          <el-input v-model="emailCfg.smtp_user" autocomplete="off" />
        </el-form-item>
        <el-form-item label="密码 / 授权码">
          <el-input
            v-model="emailCfg.smtp_pass"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="emailExists ? '保持不变留空' : ''"
          />
        </el-form-item>

        <el-divider />

        <el-form-item label="发件人">
          <el-input v-model="emailCfg.from" placeholder="alert@biumind.com" />
        </el-form-item>
        <el-form-item label="收件人">
          <el-input
            v-model="emailToInput"
            type="textarea"
            :rows="3"
            placeholder="一行一个邮箱, 或逗号 / 分号分隔"
          />
        </el-form-item>

        <el-form-item label="Webhook 密钥">
          <el-input
            v-model="emailCfg.webhook_secret"
            placeholder="可选 — alertmanager 推送时校验 X-Biumind-Webhook 头"
          />
        </el-form-item>

        <el-form-item>
          <el-button
            type="primary"
            :loading="savingEmail"
            :disabled="!isSuper() || emailIsRedacted"
            @click="saveEmail"
          >
            保存
          </el-button>
          <el-button
            :disabled="!isSuper() || emailIsRedacted"
            @click="openTestDialog('alert.email')"
          >
            发送测试邮件
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card v-loading="loading" shadow="never" class="cfg-card">
      <template #header>
        <span>注册邮箱验证</span>
        <span class="text-muted" style="margin-left: 8px; font-size: 13px">
          用户注册时发送 6 位验证码 — 关闭时 code 仅写入 identity 日志 (dev 兜底)
        </span>
      </template>

      <el-alert
        v-if="authEmailIsRedacted"
        title="此配置含敏感字段, 仅 superadmin 可见 / 可编辑"
        type="warning"
        show-icon
        :closable="false"
        style="margin-bottom: 16px"
      />

      <el-form :disabled="!isSuper() || authEmailIsRedacted" label-width="120px">
        <el-form-item label="启用">
          <el-switch v-model="authEmailCfg.enabled" />
          <span class="text-muted" style="margin-left: 12px; font-size: 12px">
            关闭时注册流程照常但 code 仅写日志 (运维从 docker logs identity 拿)
          </span>
        </el-form-item>

        <el-form-item label="SMTP 服务器">
          <el-input v-model="authEmailCfg.smtp_host" placeholder="smtp.qiye.aliyun.com" />
        </el-form-item>
        <el-form-item label="SMTP 端口">
          <el-input-number v-model="authEmailCfg.smtp_port" :min="1" :max="65535" :step="1" />
          <span class="text-muted" style="margin-left: 12px; font-size: 12px">
            隐式 TLS (SMTPS) 用 465; STARTTLS 用 587; 明文用 25
          </span>
        </el-form-item>
        <el-form-item label="使用 TLS">
          <el-switch v-model="authEmailCfg.smtp_tls" />
          <span class="text-muted" style="margin-left: 12px; font-size: 12px">
            打开 = 隐式 TLS (SMTPS, 465); 关闭 = STARTTLS / 明文 (587/25)
          </span>
        </el-form-item>
        <el-form-item label="用户名">
          <el-input v-model="authEmailCfg.smtp_user" autocomplete="off" />
        </el-form-item>
        <el-form-item label="密码 / 授权码">
          <el-input
            v-model="authEmailCfg.smtp_pass"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="authEmailExists ? '保持不变留空' : ''"
          />
        </el-form-item>

        <el-divider />

        <el-form-item label="发件人">
          <el-input v-model="authEmailCfg.from" placeholder="BiuMind <noreply@biumind.com>" />
        </el-form-item>
        <el-form-item label="邮件主题">
          <el-input v-model="authEmailCfg.subject" placeholder="[BiuMind] 邮箱验证码" />
        </el-form-item>
        <el-form-item label="验证码有效期">
          <el-input-number
            v-model="authEmailCfg.code_ttl_seconds"
            :min="60"
            :max="3600"
            :step="60"
          />
          <span class="text-muted" style="margin-left: 12px; font-size: 12px">秒, 默认 600 (10 分钟)</span>
        </el-form-item>

        <el-form-item>
          <el-button
            type="primary"
            :loading="savingAuthEmail"
            :disabled="!isSuper() || authEmailIsRedacted"
            @click="saveAuthEmail"
          >
            保存
          </el-button>
          <el-button
            :disabled="!isSuper() || authEmailIsRedacted"
            @click="openTestDialog('auth.email')"
          >
            发送测试邮件
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- ─── W5 支付通道 ─────────────────────────── -->

    <el-card v-loading="loading" shadow="never" class="cfg-card">
      <template #header>
        <span>Stripe 支付</span>
        <span class="text-muted" style="margin-left: 8px; font-size: 13px">
          海外信用卡 — secret key + webhook secret + Price ID 映射
        </span>
      </template>
      <el-alert
        v-if="stripeIsRedacted"
        title="此配置含敏感字段, 仅 superadmin 可见 / 可编辑"
        type="warning" show-icon :closable="false"
        style="margin-bottom: 16px"
      />
      <el-form :disabled="!isSuper() || stripeIsRedacted" label-width="140px">
        <el-form-item label="启用">
          <el-switch v-model="stripeCfg.enabled" />
        </el-form-item>
        <el-form-item label="Secret Key">
          <el-input
            v-model="stripeCfg.secret_key"
            type="password" show-password autocomplete="new-password"
            placeholder="sk_live_... / sk_test_..."
          />
        </el-form-item>
        <el-form-item label="Webhook Secret">
          <el-input
            v-model="stripeCfg.webhook_secret"
            type="password" show-password autocomplete="new-password"
            placeholder="whsec_..."
          />
        </el-form-item>
        <el-form-item label="Price → Plan 映射">
          <el-input
            v-model="stripePriceMap"
            type="textarea" :rows="4"
            placeholder="一行一个: price_1ABC=pro&#10;price_1XYZ=team"
          />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary" :loading="savingStripe"
            :disabled="!isSuper() || stripeIsRedacted"
            @click="saveStripe"
          >保存</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card v-loading="loading" shadow="never" class="cfg-card">
      <template #header>
        <span>微信支付</span>
        <span class="text-muted" style="margin-left: 8px; font-size: 13px">
          v3 API — Native (扫码) / JSAPI (公众号) / H5 (移动浏览器) 三模式
        </span>
      </template>
      <el-alert
        v-if="wechatIsRedacted"
        title="此配置含敏感字段, 仅 superadmin 可见 / 可编辑"
        type="warning" show-icon :closable="false"
        style="margin-bottom: 16px"
      />
      <el-form :disabled="!isSuper() || wechatIsRedacted" label-width="140px">
        <el-form-item label="启用">
          <el-switch v-model="wechatCfg.enabled" />
        </el-form-item>
        <el-form-item label="AppID">
          <el-input v-model="wechatCfg.app_id" placeholder="wx... (公众号 / 小程序 / App ID)" />
        </el-form-item>
        <el-form-item label="商户号 mch_id">
          <el-input v-model="wechatCfg.mch_id" placeholder="10 位数字" />
        </el-form-item>
        <el-form-item label="APIv3 密钥">
          <el-input
            v-model="wechatCfg.apiv3_key"
            type="password" show-password autocomplete="new-password"
            placeholder="32 字节 — 商户后台手动设置"
          />
          <span class="text-muted" style="font-size: 12px">
            用于 AES-GCM 解密回调 resource. 商户号设置后不可重置, 须保存.
          </span>
        </el-form-item>
        <el-form-item label="证书序列号">
          <el-input v-model="wechatCfg.cert_serial_no" placeholder="apiclient_cert.pem 序列号" />
        </el-form-item>
        <el-form-item label="商户私钥 PEM">
          <el-input
            v-model="wechatCfg.apiclient_key_pem"
            type="textarea" :rows="6" autocomplete="off"
            placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
          />
        </el-form-item>
        <el-form-item label="平台公钥 PEM">
          <el-input
            v-model="wechatCfg.platform_public_key"
            type="textarea" :rows="4"
            placeholder="-----BEGIN PUBLIC KEY-----&#10;...&#10;-----END PUBLIC KEY-----"
          />
          <span class="text-muted" style="font-size: 12px">
            用于回调验签. 从 /v3/certificates 拉到的解密后内容.
          </span>
        </el-form-item>
        <el-form-item label="回调 URL">
          <el-input v-model="wechatCfg.notify_url" placeholder="https://api.biumind.com/v1/billing/wechat/callback" />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary" :loading="savingWechat"
            :disabled="!isSuper() || wechatIsRedacted"
            @click="saveWechat"
          >保存</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card v-loading="loading" shadow="never" class="cfg-card">
      <template #header>
        <span>支付宝</span>
        <span class="text-muted" style="margin-left: 8px; font-size: 13px">
          PC 网站 / 手机网站 / 周期扣款协议
        </span>
      </template>
      <el-alert
        v-if="alipayIsRedacted"
        title="此配置含敏感字段, 仅 superadmin 可见 / 可编辑"
        type="warning" show-icon :closable="false"
        style="margin-bottom: 16px"
      />
      <el-form :disabled="!isSuper() || alipayIsRedacted" label-width="140px">
        <el-form-item label="启用">
          <el-switch v-model="alipayCfg.enabled" />
        </el-form-item>
        <el-form-item label="AppID">
          <el-input v-model="alipayCfg.app_id" placeholder="2021... (开放平台应用 ID)" />
        </el-form-item>
        <el-form-item label="应用私钥 PEM">
          <el-input
            v-model="alipayCfg.private_key_pem"
            type="textarea" :rows="6" autocomplete="off"
            placeholder="支持裸 base64 (开放平台直接复制) 或完整 PEM"
          />
        </el-form-item>
        <el-form-item label="支付宝公钥 PEM">
          <el-input
            v-model="alipayCfg.alipay_public_key_pem"
            type="textarea" :rows="4"
            placeholder="支付宝控制台 → 应用 → 接口加签 → 支付宝公钥"
          />
        </el-form-item>
        <el-form-item label="异步通知 URL">
          <el-input v-model="alipayCfg.notify_url" placeholder="https://api.biumind.com/v1/billing/alipay/callback" />
        </el-form-item>
        <el-form-item label="同步跳转 URL">
          <el-input v-model="alipayCfg.return_url" placeholder="可选: https://biumind.com/membership/return" />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary" :loading="savingAlipay"
            :disabled="!isSuper() || alipayIsRedacted"
            @click="saveAlipay"
          >保存</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-dialog
      v-model="testDialog.visible"
      :title="`发送测试邮件 — ${testDialog.key}`"
      width="480"
    >
      <el-alert
        type="info"
        show-icon
        :closable="false"
        style="margin-bottom: 16px"
        title="使用当前表单中的 SMTP 配置直接投递; 密码字段留空时使用已保存的值"
      />
      <el-form label-width="80px">
        <el-form-item label="收件人">
          <el-input
            v-model="testDialog.to"
            placeholder="me@example.com"
            autofocus
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="testDialog.visible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="testDialog.loading"
          @click="runTestEmail"
        >
          发送
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.page-title { margin: 0 0 16px; font-size: 24px; font-weight: 600; }
.cfg-card { margin-bottom: 16px; }
</style>
