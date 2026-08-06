// lib/time_format.ts — 聊天时间分组工具.
//
// 微信式分组规则:
//   - 间隔 < 5 分钟: 不显示时间
//   - 同一天: HH:mm
//   - 昨天: 昨天 HH:mm
//   - 一周内: 周X HH:mm
//   - 一年内: MM-DD HH:mm
//   - 跨年: YYYY-MM-DD HH:mm

const MIN_INTERVAL_MS = 5 * 60 * 1000;
const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六'];

export function formatChatTime(ts: number): string {
  if (!ts) return '';
  const d = new Date(ts);
  const now = new Date();
  const HH = pad2(d.getHours());
  const mm = pad2(d.getMinutes());
  const sameY = d.getFullYear() === now.getFullYear();
  const sameDay =
    sameY && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  if (sameDay) return HH + ':' + mm;

  const yest = new Date(now);
  yest.setDate(now.getDate() - 1);
  if (
    d.getFullYear() === yest.getFullYear() &&
    d.getMonth() === yest.getMonth() &&
    d.getDate() === yest.getDate()
  ) {
    return '昨天 ' + HH + ':' + mm;
  }

  const diffDays = Math.floor((now.getTime() - d.getTime()) / 86400000);
  if (diffDays < 7 && diffDays > 0) {
    return '周' + WEEKDAYS[d.getDay()] + ' ' + HH + ':' + mm;
  }

  const MM = pad2(d.getMonth() + 1);
  const DD = pad2(d.getDate());
  if (sameY) return MM + '-' + DD + ' ' + HH + ':' + mm;
  return d.getFullYear() + '-' + MM + '-' + DD + ' ' + HH + ':' + mm;
}

/**
 * shouldShowTimeBefore — 判断当前消息前是否插入时间分隔栏.
 * @param prevTs 前一条消息时间戳 (0 表示首条)
 * @param curTs  当前消息时间戳
 */
export function shouldShowTimeBefore(prevTs: number, curTs: number): boolean {
  if (!curTs) return false;
  if (!prevTs) return true; // 首条永远显示
  return curTs - prevTs >= MIN_INTERVAL_MS;
}

function pad2(n: number): string {
  return n < 10 ? '0' + n : '' + n;
}
