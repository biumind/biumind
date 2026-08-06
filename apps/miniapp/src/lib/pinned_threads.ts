// lib/pinned_threads.ts — threads 本地置顶 storage.
//
// 纯前端实现 — 不依赖后端 API. 多端不同步, 但单端体验立即可用.
// 后端做 thread.pinned 字段后再迁移.

const STORAGE_KEY = 'biumind.pinned_threads';

export function loadPinned(): Set<string> {
  try {
    const raw = uni.getStorageSync(STORAGE_KEY);
    if (!raw) return new Set();
    const arr: string[] =
      typeof raw === 'string' ? JSON.parse(raw) : (raw as string[]);
    return new Set(arr);
  } catch {
    return new Set();
  }
}

function persist(set: Set<string>): void {
  try {
    uni.setStorageSync(STORAGE_KEY, JSON.stringify(Array.from(set)));
  } catch {
    /* noop */
  }
}

export function pin(threadId: string): Set<string> {
  const s = loadPinned();
  s.add(threadId);
  persist(s);
  return s;
}

export function unpin(threadId: string): Set<string> {
  const s = loadPinned();
  s.delete(threadId);
  persist(s);
  return s;
}

export function isPinned(threadId: string): boolean {
  return loadPinned().has(threadId);
}
