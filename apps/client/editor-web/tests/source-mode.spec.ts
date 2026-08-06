import { describe, expect, it, vi } from 'vitest'

import { SourceModeController } from '../src/source-mode'

function setup(markdown = '# hello') {
  const parent = document.createElement('div')
  const container = document.createElement('div')
  parent.appendChild(container)
  const deps = {
    parent,
    container,
    getMarkdown: vi.fn(() => markdown),
    applyMarkdown: vi.fn(),
    onEdit: vi.fn(),
  }
  const controller = new SourceModeController(deps)
  return { controller, deps, parent, container }
}

const getTextarea = (parent: HTMLElement): HTMLTextAreaElement => {
  const ta = parent.querySelector('textarea')
  if (!ta) throw new Error('textarea not found')
  return ta
}

describe('SourceModeController', () => {
  it('enter 隐藏编辑器容器并填充当前 markdown', () => {
    const { controller, deps, parent, container } = setup('# doc\n')
    controller.enter()
    expect(controller.active).toBe(true)
    expect(container.style.display).toBe('none')
    expect(getTextarea(parent).value).toBe('# doc\n')
    expect(deps.getMarkdown).toHaveBeenCalledTimes(1)
  })

  it('重复 enter 不重复创建 textarea', () => {
    const { controller, parent } = setup()
    controller.enter()
    controller.enter()
    expect(parent.querySelectorAll('textarea')).toHaveLength(1)
  })

  it('textarea input 事件走 onEdit 回调', () => {
    const { controller, deps, parent } = setup()
    controller.enter()
    const ta = getTextarea(parent)
    ta.value = 'edited **source**'
    ta.dispatchEvent(new Event('input'))
    expect(deps.onEdit).toHaveBeenCalledWith('edited **source**')
  })

  it('exit 把 textarea 内容灌回编辑器并恢复容器显示', () => {
    const { controller, deps, parent, container } = setup()
    controller.enter()
    getTextarea(parent).value = 'final markdown'
    controller.exit()
    expect(controller.active).toBe(false)
    expect(deps.applyMarkdown).toHaveBeenCalledWith('final markdown')
    expect(container.style.display).toBe('')
    expect(parent.querySelector('textarea')).toBeNull()
  })

  it('toggle 在 enter / exit 之间往返', () => {
    const { controller, deps } = setup()
    controller.toggle()
    expect(controller.active).toBe(true)
    controller.toggle()
    expect(controller.active).toBe(false)
    expect(deps.applyMarkdown).toHaveBeenCalledTimes(1)
  })

  it('源码模式下 setExternalMarkdown 只更新 textarea', () => {
    const { controller, deps, parent } = setup()
    controller.enter()
    controller.setExternalMarkdown('from host')
    expect(getTextarea(parent).value).toBe('from host')
    expect(deps.applyMarkdown).not.toHaveBeenCalled()
  })

  it('未进入源码模式时 setExternalMarkdown 为空操作', () => {
    const { controller, deps } = setup()
    controller.setExternalMarkdown('from host')
    expect(deps.applyMarkdown).not.toHaveBeenCalled()
    expect(deps.onEdit).not.toHaveBeenCalled()
  })

  it('readOnly 同步到 textarea', () => {
    const { controller, parent } = setup()
    controller.setReadOnly(true)
    controller.enter()
    expect(getTextarea(parent).readOnly).toBe(true)
    controller.setReadOnly(false)
    expect(getTextarea(parent).readOnly).toBe(false)
  })

  it('destroy 清理 DOM 与状态，exit 时不再 applyMarkdown', () => {
    const { controller, deps, parent } = setup()
    controller.enter()
    controller.destroy()
    expect(controller.active).toBe(false)
    expect(parent.querySelector('textarea')).toBeNull()
    controller.exit()
    expect(deps.applyMarkdown).not.toHaveBeenCalled()
  })
})
