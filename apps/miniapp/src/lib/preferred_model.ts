// lib/preferred_model.ts — 用户偏好的默认模型, 本地持久化.
//
// 当前模型选择优先级:
//   1. 当前 thread.model (如果在已有 thread 内对话, 由后端 thread 记录决定)
//   2. preferredModel (用户设置)
//   3. fallback 'claude-sonnet-4-6' (与后端 ensureThread 默认一致)
//
// 切换模型行为:
//   - 在 hero 状态 (无 thread): 只 savePreferredModel, 下次 ensureThread 用
//   - 在 thread 内: patchThread + savePreferredModel (双写)

const KEY = 'biumind.preferred_model';
const DEFAULT_MODEL = 'claude-sonnet-4-6';

export function loadPreferredModel(): string {
  try {
    return uni.getStorageSync(KEY) || DEFAULT_MODEL;
  } catch {
    return DEFAULT_MODEL;
  }
}

export function savePreferredModel(modelId: string): void {
  if (!modelId) return;
  try {
    uni.setStorageSync(KEY, modelId);
  } catch {
    /* noop */
  }
}
