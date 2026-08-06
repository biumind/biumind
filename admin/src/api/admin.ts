// admin REST 客户端. 跟 services/identity/internal/admin/admin.go 一一对应.

import { http } from './http'
import type {
  AdminUserDetail,
  AdminUserPage,
  AuditPage,
  AuthResponse,
  MeResponse,
} from './types'

// ── auth ───────────────────────────────────────
export async function login(email: string, password: string, deviceName?: string) {
  const r = await http.post<AuthResponse>('/v1/auth/login', {
    email,
    password,
    device_name: deviceName ?? defaultAdminDeviceName(),
    installation_id: ensureInstallationId(),
  })
  return r.data
}

// installation_id: 浏览器首次访问时生成 UUID v4 存 localStorage, 永久持久化.
// 同 (user, install) 在 identity 端复用同一行 refresh_token, 反复登入不堆积.
function ensureInstallationId(): string {
  const KEY = 'biumind.admin.installation_id'
  if (typeof localStorage === 'undefined') return ''
  let v = localStorage.getItem(KEY)
  if (v && v.length > 0) return v
  v = uuidV4()
  localStorage.setItem(KEY, v)
  return v
}

function uuidV4(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  // 兜底实现 (老浏览器). 不密码学敏感, 这里只要稳定唯一.
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}

// 拼一个比 'admin-web' 更具识别度的 device_name. 会话列表区分多个 admin
// 浏览器登录用. 解析 navigator.userAgent 取浏览器 + OS.
function defaultAdminDeviceName(): string {
  if (typeof navigator === 'undefined') return 'admin-web'
  const ua = navigator.userAgent || ''
  const browser = (() => {
    const m = ua.match(/Edg\/(\d+)/)
    if (m) return `Edge ${m[1]}`
    const o = ua.match(/OPR\/(\d+)/)
    if (o) return `Opera ${o[1]}`
    const f = ua.match(/Firefox\/(\d+)/)
    if (f) return `Firefox ${f[1]}`
    const c = ua.match(/Chrome\/(\d+)/)
    if (c) return `Chrome ${c[1]}`
    const s = ua.match(/Version\/(\d+).+Safari/)
    if (s) return `Safari ${s[1]}`
    return 'Browser'
  })()
  const os = (() => {
    if (ua.includes('Mac OS X') || ua.includes('Macintosh')) return 'macOS'
    if (ua.includes('Windows NT')) return 'Windows'
    if (ua.includes('Android')) return 'Android'
    if (ua.includes('iPhone')) return 'iOS'
    if (ua.includes('Linux')) return 'Linux'
    return ''
  })()
  return os ? `Admin · ${browser} on ${os}` : `Admin · ${browser}`
}

export async function logout(refreshToken: string) {
  await http.post('/v1/auth/logout', { refresh_token: refreshToken })
}

export async function fetchMe() {
  const r = await http.get<MeResponse>('/v1/identity/me')
  return r.data
}

// ── admin: users ───────────────────────────────
export async function listUsers(params: { q?: string; limit?: number; offset?: number } = {}) {
  const r = await http.get<AdminUserPage>('/v1/admin/users', { params })
  return r.data
}

export async function getUser(id: string) {
  const r = await http.get<AdminUserDetail>(`/v1/admin/users/${id}`)
  return r.data
}

export async function setUserPlan(id: string, plan: 'free' | 'pro' | 'team', reason: string) {
  const r = await http.post(`/v1/admin/users/${id}/plan`, { plan, reason })
  return r.data
}

// 给用户充值永久积分. kind 固定 permanent (后端决策), source 后端硬编码 admin.
// idempotency_key 由前端每次打开 dialog 生成, 双击同一笔确认被幂等拦截防重充.
export async function grantUserCredits(
  id: string,
  params: { amount: number; reason: string; idempotency_key: string },
) {
  const r = await http.post(`/v1/admin/users/${id}/credits/grant`, params)
  return r.data
}

export async function setUserRole(
  id: string,
  role: 'user' | 'support' | 'finance' | 'ops' | 'admin' | 'superadmin' | 'viewer',
  reason: string,
) {
  const r = await http.patch<{
    updated: string
    role: string
    sessions_revoked?: number
    noop?: boolean
  }>(`/v1/admin/users/${id}/role`, { role, reason })
  return r.data
}

export async function revokeUserSessions(id: string) {
  const r = await http.delete<{ target: string; revoked: number }>(
    `/v1/admin/users/${id}/sessions`,
  )
  return r.data
}

// ── admin: audit ──────────────────────────────
export async function listAudit(limit = 200) {
  const r = await http.get<AuditPage>('/v1/admin/audit', { params: { limit } })
  return r.data
}

// ── admin: monitor ────────────────────────────
import type { ServiceProbe } from './types'

export async function listServices() {
  const r = await http.get<{ services: ServiceProbe[] }>('/v1/admin/monitor/services')
  return r.data.services ?? []
}

// Prometheus 查询代理. 直接返 prom 标准格式: { status, data: { resultType, result: [...] } }
export interface PromResponse<TResult = unknown> {
  status: 'success' | 'error'
  data: { resultType: 'matrix' | 'vector' | 'scalar' | 'string'; result: TResult }
  errorType?: string
  error?: string
}

export interface PromMatrixSeries {
  metric: Record<string, string>
  values: [number, string][] // [unix_ts_sec, value_string]
}

export async function promQuery(query: string) {
  const r = await http.get<PromResponse<Array<{ metric: Record<string, string>; value: [number, string] }>>>(
    '/v1/admin/monitor/query',
    { params: { type: 'instant', query } },
  )
  return r.data
}

export async function promQueryRange(query: string, start: number, end: number, step = '15s') {
  const r = await http.get<PromResponse<PromMatrixSeries[]>>('/v1/admin/monitor/query', {
    params: { type: 'range', query, start, end, step },
  })
  return r.data
}

// ── system config ──
export interface SystemConfigEntry {
  key: string
  value: unknown
  secret: boolean
  description: string
  updated_at: string
}

export async function listSystemConfig() {
  const r = await http.get<{ configs: SystemConfigEntry[] }>('/v1/admin/system/config')
  return r.data.configs ?? []
}

export async function setSystemConfig(key: string, value: unknown) {
  await http.put(`/v1/admin/system/config/${encodeURIComponent(key)}`, { value })
}

// 发测试邮件验证 SMTP. body 为空字段时后端按 key 回退到存值 (尤其
// smtp_pass — UI 里密码留空 = 保持不变, 这里也跟着 fallback).
export interface TestEmailReq {
  key?: 'alert.email' | 'auth.email'
  smtp_host?: string
  smtp_port?: number
  smtp_user?: string
  smtp_pass?: string
  smtp_tls?: boolean
  from?: string
  to: string
  subject?: string
}
export async function sendTestEmail(req: TestEmailReq) {
  const r = await http.post<{ sent: boolean; to: string }>(
    '/v1/admin/system/test-email',
    req,
  )
  return r.data
}

// ── 我的已登录设备 (self-serve sessions) ──
export interface MySession {
  id: string
  device_name: string
  device_kind: 'mobile' | 'desktop' | 'browser' | 'unknown'
  last_ip?: string
  last_ua?: string
  last_used_at?: string
  expires_at: string
  created_at: string
  ttl_days: number
  is_current: boolean
}
export async function listMySessions() {
  const r = await http.get<{ sessions: MySession[] }>('/v1/identity/me/sessions')
  return r.data.sessions ?? []
}
export async function revokeMySession(id: string) {
  const r = await http.delete<{ revoked: boolean; self: boolean }>(
    `/v1/identity/me/sessions/${encodeURIComponent(id)}`,
  )
  return r.data.self ?? false
}
export async function revokeOtherSessions() {
  const r = await http.delete<{ revoked: number }>(
    '/v1/identity/me/sessions/others',
  )
  return r.data.revoked ?? 0
}

// ── Audit summary (dashboard 卡片) ──
export interface AuditSummary {
  window_seconds: number
  failed_logins: number
  brute_force_hits: number
  role_changes: number
  system_config_changes: number
  email_verifications: number
  password_resets: number
  total_events: number
  total_failures: number
}
export async function getAuditSummary(window: string = '24h') {
  const r = await http.get<{ summary: AuditSummary }>(
    `/v1/admin/audit/summary?window=${encodeURIComponent(window)}`,
  )
  return r.data.summary
}

// ── RBAC matrix ──
export interface RBACRole {
  name: string
  display_name: string
  description: string
  is_system: boolean
}
export interface RBACPermission {
  name: string
  resource: string
  action: string
  scope?: string
  description: string
}
export interface RBACMatrix {
  roles: RBACRole[]
  permissions: RBACPermission[]
  matrix: Record<string, string[]> // role.name → permission names (含通配 e.g. '*')
}

export async function getRBACMatrix() {
  const r = await http.get<RBACMatrix>('/v1/admin/rbac/matrix')
  return r.data
}

export async function setRolePermissions(role: string, permissions: string[]) {
  const r = await http.put<{
    role: string
    added: number
    removed: number
    total: number
    reload_warning?: string
  }>(`/v1/admin/rbac/roles/${encodeURIComponent(role)}/permissions`, {
    permissions,
  })
  return r.data
}
