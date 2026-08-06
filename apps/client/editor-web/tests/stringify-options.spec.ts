import { describe, expect, it } from 'vitest'

import { STRINGIFY_OPTIONS } from '../src/markdown/stringify-options'

describe('stringify options', () => {
  it('locks emphasis style to *', () => {
    expect(STRINGIFY_OPTIONS.emphasis).toBe('*')
  })
  it('locks strong style to *', () => {
    expect(STRINGIFY_OPTIONS.strong).toBe('*')
  })
  it('locks bullet to -', () => {
    expect(STRINGIFY_OPTIONS.bullet).toBe('-')
  })
  it('locks code fence to backtick', () => {
    expect(STRINGIFY_OPTIONS.fence).toBe('`')
  })
  it('locks horizontal rule to -', () => {
    expect(STRINGIFY_OPTIONS.rule).toBe('-')
  })
  it('keeps list-item indent compact', () => {
    expect(STRINGIFY_OPTIONS.listItemIndent).toBe('one')
  })
})
