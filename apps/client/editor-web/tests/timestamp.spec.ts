import { describe, expect, it } from 'vitest'

import { formatTimestamp } from '../src/timestamp'

describe('formatTimestamp', () => {
  it('输出 YYYY-MM-DD HH:mm 本地时间', () => {
    const date = new Date(2026, 7, 3, 14, 30)
    expect(formatTimestamp(date)).toBe('2026-08-03 14:30')
  })

  it('个位数月日时分补零', () => {
    const date = new Date(2026, 0, 5, 9, 5)
    expect(formatTimestamp(date)).toBe('2026-01-05 09:05')
  })
})
