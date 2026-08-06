// lib/font_scale.ts — 全局字号档位 + 响应式 store.
//
// 4 档: small / normal / large / xlarge. 持久化在 storage, 内存里也维护
// 一个响应式 ref 让多个页面订阅同步.
//
// 应用方式: 页面顶层 view 用 :style="fontStyle" 注入 --font-scale CSS
// var, 关键文字字号用 calc(<base>rpx * var(--font-scale, 1)). mp-weixin
// 基础库 ≥ 2.10.4 支持 CSS 自定义属性 + calc.
//
// 当前阶段 (v0.2.1): chat 页接入 (msg-text / msg-rich / textarea / 引用 /
// 元信息). me 页字号选项现在真生效, 不再是假按钮.
// 其他页面 (threads / hero / wiki / notify) v0.3 再扫.

import { ref, computed, type ComputedRef } from 'vue';

export type FontScale = 'small' | 'normal' | 'large' | 'xlarge';

export const FONT_SCALES: { id: FontScale; label: string; ratio: number }[] = [
  { id: 'small', label: '小', ratio: 0.9 },
  { id: 'normal', label: '标准', ratio: 1.0 },
  { id: 'large', label: '大', ratio: 1.15 },
  { id: 'xlarge', label: '特大', ratio: 1.3 },
];

const KEY = 'biumind.font_scale';

export function loadFontScale(): FontScale {
  try {
    const v = uni.getStorageSync(KEY) as FontScale;
    if (v && FONT_SCALES.find((s) => s.id === v)) return v;
  } catch {
    /* noop */
  }
  return 'normal';
}

export function saveFontScale(scale: FontScale): void {
  try {
    uni.setStorageSync(KEY, scale);
  } catch {
    /* noop */
  }
}

export function ratioFor(scale: FontScale): number {
  return FONT_SCALES.find((s) => s.id === scale)?.ratio ?? 1.0;
}

export function labelFor(scale: FontScale): string {
  return FONT_SCALES.find((s) => s.id === scale)?.label ?? '标准';
}

// ── 响应式 store ──────────────────────────────────────────────────
// 单例 ref — 所有页面 useFontScale 共享同一个状态, 任意页面调
// applyFontScale 都会触发其他页面的模板重新计算.

const _scale = ref<FontScale>(loadFontScale());

/** 当前生效的字号档位 (响应式). 用于显示当前选中等场景. */
export function useFontScale(): typeof _scale {
  return _scale;
}

/**
 * 切换字号档位 — 同时更新响应式 ref + storage. 所有订阅了
 * useFontScaleStyle 的页面会立即重新渲染字号.
 */
export function applyFontScale(scale: FontScale): void {
  _scale.value = scale;
  saveFontScale(scale);
}

/**
 * 给页面顶层 view :style 用的 inline style. 形如 "--font-scale: 1.15".
 * 子元素用 calc(<base>rpx * var(--font-scale, 1)) 取值.
 */
export function useFontScaleStyle(): ComputedRef<string> {
  return computed(() => '--font-scale: ' + ratioFor(_scale.value));
}
