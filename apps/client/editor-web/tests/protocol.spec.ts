import { describe, expect, it } from 'vitest'

import {
  isMessage,
  makeMessage,
  PROTOCOL_VERSION,
} from '../src/bridge/protocol'

describe('bridge protocol', () => {
  it('makeMessage stamps the protocol version', () => {
    const m = makeMessage('docChanged', { markdown: '# hi', revision: 1 })
    expect(m.v).toBe(PROTOCOL_VERSION)
    expect(m.type).toBe('docChanged')
    expect(m.payload).toEqual({ markdown: '# hi', revision: 1 })
    expect(m.id).toBeUndefined()
  })

  it('makeMessage attaches id when provided', () => {
    const m = makeMessage('wikilinkQuery.reply', { items: [] }, 'abc')
    expect(m.id).toBe('abc')
  })

  it('isMessage accepts a well-formed wire object', () => {
    const wire = { type: 'ready', v: 1, payload: { editorVersion: '0.1.0' } }
    expect(isMessage(wire)).toBe(true)
  })

  it('isMessage rejects nulls and missing fields', () => {
    expect(isMessage(null)).toBe(false)
    expect(isMessage({})).toBe(false)
    expect(isMessage({ type: 'x', v: 1 })).toBe(false)
    expect(isMessage({ type: 'x', payload: {} })).toBe(false)
    expect(isMessage({ v: 1, payload: {} })).toBe(false)
    expect(isMessage({ type: 1, v: 1, payload: {} })).toBe(false)
  })

  it('isMessage rejects primitive payload', () => {
    expect(isMessage({ type: 'x', v: 1, payload: 'string' })).toBe(false)
    expect(isMessage({ type: 'x', v: 1, payload: null })).toBe(false)
  })
})
