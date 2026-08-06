// lib/draft_store.ts — 输入框草稿持久化.
//
// 按 thread 隔离, 切换 thread 不串. 没有 thread 时用 NEW_KEY.
// 退出页面前持久化, 进入恢复; 发送/取消后清除.

const PREFIX = 'biumind.draft.';
const NEW_KEY = '__new__';

export function loadDraft(threadId: string | undefined): string {
  const key = PREFIX + (threadId || NEW_KEY);
  try {
    return uni.getStorageSync(key) || '';
  } catch {
    return '';
  }
}

export function saveDraft(
  threadId: string | undefined,
  text: string,
): void {
  const key = PREFIX + (threadId || NEW_KEY);
  try {
    if (text) {
      uni.setStorageSync(key, text);
    } else {
      uni.removeStorageSync(key);
    }
  } catch {
    /* noop */
  }
}

export function clearDraft(threadId: string | undefined): void {
  saveDraft(threadId, '');
}
