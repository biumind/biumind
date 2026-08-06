import { EditorView } from '@codemirror/view'
import { afterEach, describe, expect, it } from 'vitest'

import { blockImagesExtension } from '../src/cm6/rendering/block-images'
import { makeCm6View } from './cm6-test-utils'

let view: EditorView | null = null

afterEach(() => {
  view?.destroy()
  view = null
  document.body.innerHTML = ''
})

function images(): NodeListOf<HTMLImageElement> {
  return view!.contentDOM.querySelectorAll<HTMLImageElement>('.cm-md-image img')
}

describe('cm6 block-images', () => {
  it('独占行 http(s) 图片 → 源码行下方挂 img widget', () => {
    const doc = 'before\n\n![alt](https://x.com/a.png)\n\nafter'
    view = makeCm6View(doc, 0, 0, [blockImagesExtension()])
    expect(images().length).toBe(1)
    expect(images()[0].src).toBe('https://x.com/a.png')
    expect(images()[0].alt).toBe('alt')
    // 定为：源码行保留（光标离开也不隐藏）
    expect(view.contentDOM.textContent).toContain('![alt](https://x.com/a.png)')
  })

  it('行内图片（周围有文字）不渲染', () => {
    const doc = 'see ![alt](https://x.com/a.png) here'
    view = makeCm6View(doc, 0, 0, [blockImagesExtension()])
    expect(images().length).toBe(0)
  })

  it('非 http(s) src（biu-file://）不渲染', () => {
    const doc = '![f](biu-file://uuid-1)'
    view = makeCm6View(doc, doc.length, doc.length, [blockImagesExtension()])
    expect(images().length).toBe(0)
  })

  it('光标在图片行上时图片保持显示', () => {
    const doc = '![alt](https://x.com/a.png)'
    view = makeCm6View(doc, 5, 5, [blockImagesExtension()])
    expect(images().length).toBe(1)
  })

  it('加载失败 → 轻量错误占位', () => {
    const doc = '![broken](https://x.com/404.png)'
    view = makeCm6View(doc, 0, 0, [blockImagesExtension()])
    const img = images()[0]
    img.dispatchEvent(new Event('error'))
    const err = view.contentDOM.querySelector('.cm-md-image-error')
    expect(err?.textContent).toBe('图片加载失败：broken')
    expect(view.contentDOM.querySelectorAll('img').length).toBe(0)
  })

  it('点击图片 → 光标落到图源码行行首', () => {
    const doc = 'before\n\n![alt](https://x.com/a.png)\n\nafter'
    view = makeCm6View(doc, 0, 0, [blockImagesExtension()])
    const wrap = view.contentDOM.querySelector('.cm-md-image')!
    wrap.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }))
    const line = view.state.doc.lineAt(8) // 图片所在行
    expect(view.state.selection.main.head).toBe(line.from)
  })
})
