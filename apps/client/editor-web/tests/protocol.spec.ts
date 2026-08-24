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

  it('clipboardWrite 往返：type/v/payload 结构', () => {
    const m = makeMessage('clipboardWrite', { text: 'hello **md**' })
    expect(m.type).toBe('clipboardWrite')
    expect(m.v).toBe(PROTOCOL_VERSION)
    expect(m.payload).toEqual({ text: 'hello **md**' })
    // JSON 往返后仍是合法消息
    const wire = JSON.parse(JSON.stringify(m)) as unknown
    expect(isMessage(wire)).toBe(true)
  })

  it('clipboardRead 请求带 id，reply 回显同一 id', () => {
    const req = makeMessage('clipboardRead', {}, 'req-1')
    expect(req.id).toBe('req-1')
    expect(req.payload).toEqual({})
    const reply = makeMessage('clipboardRead.reply', { text: 'pasted' }, 'req-1')
    expect(reply.id).toBe('req-1')
    expect(reply.type).toBe('clipboardRead.reply')
    expect(reply.payload).toEqual({ text: 'pasted' })
    // 剪贴板为空 / 读取失败 → text: null（粘贴项置灰）
    const empty = makeMessage('clipboardRead.reply', { text: null }, 'req-2')
    expect(empty.payload).toEqual({ text: null })
  })

  it('aiAction 往返：ask/edit 动作与选区快照', () => {
    const m = makeMessage('aiAction', {
      action: 'ask' as const,
      from: 3,
      to: 9,
      text: '选中文本',
    })
    expect(m.type).toBe('aiAction')
    expect(m.payload).toEqual({ action: 'ask', from: 3, to: 9, text: '选中文本' })
    const wire = JSON.parse(JSON.stringify(m)) as unknown
    expect(isMessage(wire)).toBe(true)
  })
})
