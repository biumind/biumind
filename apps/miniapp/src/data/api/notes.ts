// data/api/notes.ts — brain 笔记服务 client (速记最小闭环).
//
// 端点与字段对齐 apps/client/lib/data/api/notes_client.dart:
//   GET  /v1/notes?limit=50   列表 (按 updated_at 倒序, 只看活笔记)
//   POST /v1/notes            创建 {title, content_md}
//   PUT  /v1/notes/{id}       更新, If-Match 头带 version 乐观锁;
//                             版本不匹配服务端 409 (code=version_conflict)
//
// 分享 (S2 最小入口, 契约见 docs/BiuMind-Technical-Architecture.md §7.6):
//   GET  /v1/notes/{id}/share 当前分享状态, 404 = 未分享
//   PUT  /v1/notes/{id}/share body {} 全缺省 = 无密码/永久, 幂等返回现有分享
//   GET  /v1/notes/shares     我的分享列表 (列表"已分享"徽标)
// 分享 URL 由客户端自行拼接 `${origin}/s/n/${token}`, 服务端不返回 url 字段.

import { get, post, put, type ApiError } from './client';

export interface NoteItem {
  id: string;
  notebook_id?: string | null;
  title: string;
  content_md: string;
  is_todo?: boolean;
  version: number;
  updated_at: string;
}

export async function listNotes(limit = 50): Promise<NoteItem[]> {
  const r = await get<{ notes: NoteItem[] }>('/v1/notes?limit=' + limit);
  return r.notes || [];
}

export async function createNote(
  title: string,
  contentMd: string,
): Promise<NoteItem> {
  return post<{ title: string; content_md: string }, NoteItem>('/v1/notes', {
    title,
    content_md: contentMd,
  });
}

// updateNote — If-Match 带当前 version; 409 时调用方重拉列表再提示.
export async function updateNote(
  id: string,
  version: number,
  fields: { title?: string; content_md?: string },
): Promise<NoteItem> {
  return put<{ title?: string; content_md?: string }, NoteItem>(
    '/v1/notes/' + id,
    fields,
    { 'If-Match': String(version) },
  );
}

// ── 分享 ─────────────────────────────────────────────────────

// 管理端 share 对象 (S1 冻结契约); 小程序只消费 token / disabled_at.
export interface NoteShare {
  token: string;
  password_set?: boolean;
  expires_at?: string | null;
  credential_version?: number;
  view_count?: number;
  disabled_at?: string | null;
  created_at?: string;
  updated_at?: string;
}

// getShare — 未分享返回 null (服务端 404), 其他错误照常抛出.
export async function getShare(noteId: string): Promise<NoteShare | null> {
  try {
    return await get<NoteShare>('/v1/notes/' + noteId + '/share');
  } catch (e: unknown) {
    if ((e as ApiError)?.status === 404) return null;
    throw e;
  }
}

// ensureShare — 有则返回现有分享, 无则创建默认分享 (无密码/永久).
// 已有带密码的分享也直接复用 (密码在落地页输入, 小程序不做配置 UI).
export async function ensureShare(noteId: string): Promise<NoteShare> {
  const existing = await getShare(noteId);
  if (existing) return existing;
  return put<Record<string, never>, NoteShare>(
    '/v1/notes/' + noteId + '/share',
    {},
  );
}

// GET /v1/notes/shares 列表项 = share 对象 + note_id / note_title / status.
export interface NoteShareListItem extends NoteShare {
  note_id: string;
  note_title?: string;
  status?: string; // "active" | "disabled" | "expired"
}

export async function listShares(): Promise<NoteShareListItem[]> {
  const r = await get<{ shares: NoteShareListItem[] }>('/v1/notes/shares');
  return r.shares || [];
}
