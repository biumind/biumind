// 后端 DTO 类型 — 跟 services/identity/internal/admin/admin.go 对齐.

export interface AdminUser {
  id: string
  email: string
  display_name?: string
  plan: 'free' | 'pro' | 'team'
  role: Role
  created_at: string
}

export interface AdminLimits {
  HubRPM: number
  HubTPM: number
  SandboxDaily: number
  SandboxConcurrent: number
  MemoryQuota: number
  BrainProjects: number
}

export interface AdminBalance {
  user_id: string
  permanent_balance: number
  time_limited_balance: number
  time_limited_earliest_expires?: string
  updated_at: string
}

export interface AdminUserDetail {
  user: AdminUser
  limits: AdminLimits
  balance: AdminBalance | null
}

export interface AdminUserPage {
  users: AdminUser[]
  total: number
  limit: number
  offset: number
}

export interface AuditEvent {
  at: string
  actor_id: string
  actor_email?: string
  actor_role?: string
  actor_ip?: string
  actor_ua?: string
  action: string
  resource?: string
  target?: string
  target_type?: string
  detail?: string
  success: boolean
  error_code?: string
  error_message?: string
}

export interface ServiceProbe {
  name: string
  url: string
  status: 'healthy' | 'degraded' | 'unhealthy' | 'unknown'
  version?: string
  http_status?: number
  latency_ms?: number
  last_check_at: string
  error?: string
  probes?: Array<{ name: string; status: string }>
}

export interface AuditPage {
  events: AuditEvent[]
}

// /v1/identity/me 返回
export interface MeResponse {
  id: string
  email: string
  display_name?: string
  role?: Role
  plan?: 'free' | 'pro' | 'team'
  permissions?: string[]
}

// /v1/auth/login /register 返回
export interface AuthResponse {
  access_token: string
  refresh_token: string
  expires_in_seconds: number
  user: MeResponse
}

export type Role =
  | 'superadmin'
  | 'admin'
  | 'support'
  | 'finance'
  | 'ops'
  | 'viewer'
  | 'user'

// 7 个角色的中文显示
export const ROLE_LABELS: Record<Role, string> = {
  superadmin: '超级管理员',
  admin: '管理员',
  support: '客服',
  finance: '财务',
  ops: '运维',
  viewer: '只读',
  user: '普通用户',
}

// 角色对应的 tag 颜色 (element-plus tag type)
export const ROLE_TAG_TYPES: Record<Role, '' | 'success' | 'warning' | 'info' | 'danger'> = {
  superadmin: 'danger',
  admin: '',
  support: 'info',
  finance: 'warning',
  ops: 'success',
  viewer: 'info',
  user: 'info',
}

export const PLAN_LABELS: Record<'free' | 'pro' | 'team', string> = {
  free: '免费',
  pro: '专业版',
  team: '团队版',
}
