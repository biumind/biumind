// 公告 / 通知 inbox admin REST client. 跟 services/identity/internal/api/announcements.go
// 的后台端点(requireAdmin: admin / superadmin)一一对应.
//
// 复用共享的 `http` axios 实例(自动 Bearer + 401 refresh). 路径前缀 /v1/admin/announcements/*.
// 公共端点(客户端拉取 / 标记已读)由 Flutter 端调用, 后台只管 list/create/update/delete.

import { http } from './http'

export type AnnouncementLevel = 'info' | 'warning' | 'error'

// 一条公告. 字段与后端 announcementOut 一一对应.
export interface Announcement {
  id: string
  level: AnnouncementLevel
  title: string
  body: string
  body_zh: string
  url: string
  min_app_version: string
  max_app_version: string
  published: boolean
  created_at: string
  expires_at: string | null
  is_read: boolean
}

// 新建 / 编辑入参. 与后端 announcementReq 一一对应.
export interface AnnouncementInput {
  level: AnnouncementLevel
  title: string
  body: string
  body_zh: string
  url: string
  min_app_version: string
  max_app_version: string
  published: boolean
  expires_at: string | null
}

// ─── announcements (admin) ────────────────────────────────────────
// 列全部(含草稿), 按创建倒序.
export async function listAnnouncements() {
  const r = await http.get<{ announcements: Announcement[] }>('/v1/admin/announcements')
  return r.data.announcements ?? []
}

export async function createAnnouncement(body: AnnouncementInput) {
  const r = await http.post<Announcement>('/v1/admin/announcements', body)
  return r.data
}

export async function updateAnnouncement(id: string, body: AnnouncementInput) {
  await http.put(`/v1/admin/announcements/${id}`, body)
}

export async function deleteAnnouncement(id: string) {
  await http.delete(`/v1/admin/announcements/${id}`)
}
