// data/api/notes.ts — brain 笔记服务 client (速记最小闭环).
//
// 端点与字段对齐 apps/client/lib/data/api/notes_client.dart:
//   GET  /v1/notes?limit=50   列表 (按 updated_at 倒序, 只看活笔记)
//   POST /v1/notes            创建 {title, content_md}
//   PUT  /v1/notes/{id}       更新, If-Match 头带 version 乐观锁;
//                             版本不匹配服务端 409 (code=version_conflict)

import { get, post, put } from './client';

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
