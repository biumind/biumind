// lib/greeting.ts — 时间问候 + 相对时间格式化.
//
// 直接返回中文字符串 — 当前小程序未引入 i18n, 全站中文.
// 与 Flutter 端 (apps/client/.../greeting.dart) 同切分逻辑.
//
//   06:00-10:59  morning    早上好
//   11:00-12:59  noon       中午好
//   13:00-17:59  afternoon  下午好
//   18:00-22:59  evening    晚上好
//   23:00-05:59  night      还在工作?

export type GreetingSlot =
  | 'morning'
  | 'noon'
  | 'afternoon'
  | 'evening'
  | 'night';

export function greetingSlotFor(when: Date = new Date()): GreetingSlot {
  const h = when.getHours();
  if (h >= 6 && h < 11) return 'morning';
  if (h >= 11 && h < 13) return 'noon';
  if (h >= 13 && h < 18) return 'afternoon';
  if (h >= 18 && h < 23) return 'evening';
  return 'night';
}

export function greetingTextFor(when: Date = new Date()): string {
  switch (greetingSlotFor(when)) {
    case 'morning':
      return '早上好';
    case 'noon':
      return '中午好';
    case 'afternoon':
      return '下午好';
    case 'evening':
      return '晚上好';
    case 'night':
      return '还在工作?';
  }
}

// 相对时间 — 7 天内显示相对, 否则返回 null 让调用方显示绝对日期.
export interface RelativeTime {
  kind: 'justNow' | 'minutes' | 'hours' | 'days';
  value: number;
}

export function relativeTimeFor(
  when: Date,
  now: Date = new Date(),
): RelativeTime | null {
  const ms = now.getTime() - when.getTime();
  if (ms < 0 || ms < 60_000) return { kind: 'justNow', value: 0 };
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 60) return { kind: 'minutes', value: minutes };
  const hours = Math.floor(ms / 3_600_000);
  if (hours < 24) return { kind: 'hours', value: hours };
  const days = Math.floor(ms / 86_400_000);
  if (days <= 7) return { kind: 'days', value: days };
  return null;
}

export function formatRelative(when: Date, now: Date = new Date()): string {
  const r = relativeTimeFor(when, now);
  if (!r) {
    const m = String(when.getMonth() + 1).padStart(2, '0');
    const d = String(when.getDate()).padStart(2, '0');
    return m + '-' + d;
  }
  switch (r.kind) {
    case 'justNow':
      return '刚刚';
    case 'minutes':
      return r.value + ' 分钟前';
    case 'hours':
      return r.value + ' 小时前';
    case 'days':
      return r.value + ' 天前';
  }
}
