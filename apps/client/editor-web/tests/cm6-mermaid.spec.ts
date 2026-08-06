import { EditorView } from '@codemirror/view'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { mermaidExtension } from '../src/cm6/mermaid'
import { makeCm6View } from './cm6-test-utils'

// mermaid 渲染本身 mock 掉：动态 import('mermaid') 命中此 mock
vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async (id: string, source: string) => ({
      svg: `<svg data-id="${id}"><text>${source}</text></svg>`,
    })),
  },
}))

let view: EditorView | null = null

const DOC = ['text', '', '```mermaid', 'graph LR', '  A --> B', '```', '', 'after'].join('\n')

afterEach(() => {
  view?.destroy()
  view = null
  document.body.innerHTML = ''
})

async function flushRender(): Promise<void> {
  // loadMermaid（动态 import）+ render 两段异步，等几个宏任务
  for (let i = 0; i < 5; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0))
  }
}

describe('cm6 mermaid 预览', () => {
  it('mermaid 围栏块尾挂预览容器并渲染 SVG', async () => {
    view = makeCm6View(DOC, DOC.length, DOC.length, [mermaidExtension()])
    const preview = view.contentDOM.querySelector('.kc-mermaid-preview')
    expect(preview).not.toBeNull()
    await flushRender()
    expect(view.contentDOM.querySelector('.kc-mermaid-preview svg')).not.toBeNull()
    expect(view.contentDOM.querySelector('.kc-mermaid-preview')?.textContent).toContain('A --> B')
  })

  it('非 mermaid 围栏不挂预览', () => {
    const doc = '```ts\nconst x = 1\n```\n\nplain'
    view = makeCm6View(doc, doc.length, doc.length, [mermaidExtension()])
    expect(view.contentDOM.querySelector('.kc-mermaid-preview')).toBeNull()
  })

  it('光标进入代码块 → 预览隐藏；移出 → 恢复', () => {
    view = makeCm6View(DOC, DOC.length, DOC.length, [mermaidExtension()])
    expect(view.contentDOM.querySelector('.kc-mermaid-preview')).not.toBeNull()
    // 光标进入 mermaid 块
    view.dispatch({ selection: { anchor: DOC.indexOf('graph') } })
    expect(view.contentDOM.querySelector('.kc-mermaid-preview')).toBeNull()
    // 移出恢复
    view.dispatch({ selection: { anchor: DOC.length } })
    expect(view.contentDOM.querySelector('.kc-mermaid-preview')).not.toBeNull()
  })

  it('渲染失败 → 错误文本', async () => {
    const mermaid = (await import('mermaid')).default
    vi.mocked(mermaid.render).mockRejectedValueOnce(new Error('bad syntax'))
    view = makeCm6View(DOC, DOC.length, DOC.length, [mermaidExtension()])
    await flushRender()
    expect(view.contentDOM.querySelector('.kc-mermaid-preview')?.textContent).toContain(
      'Mermaid 渲染失败',
    )
  })
})
