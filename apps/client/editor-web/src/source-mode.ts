// 源码模式：隐藏 Crepe 容器，用等高 textarea 直接编辑 markdown。
// 状态机（enter / exit / setExternalMarkdown / destroy）与 Crepe 解耦，方便单测。

import './source-mode.css'

export interface SourceModeDeps {
  /** textarea 挂载点（与编辑器容器同级） */
  parent: HTMLElement
  /** Crepe 编辑器容器，进入源码模式时隐藏 */
  container: HTMLElement
  /** 从编辑器取当前 markdown */
  getMarkdown: () => string
  /** 退出源码模式时把编辑后的 markdown 灌回编辑器（调用方负责防回环） */
  applyMarkdown: (markdown: string) => void
  /** textarea 内容变化回调（走与 WYSIWYG 相同的 docChanged 通路） */
  onEdit: (markdown: string) => void
}

export class SourceModeController {
  private textarea: HTMLTextAreaElement | null = null
  private readOnly = false
  active = false

  constructor(private readonly deps: SourceModeDeps) {}

  toggle(): void {
    if (this.active) {
      this.exit()
    } else {
      this.enter()
    }
  }

  enter(): void {
    if (this.active) return
    this.active = true
    const textarea = document.createElement('textarea')
    textarea.className = 'kc-source-editor'
    textarea.value = this.deps.getMarkdown()
    textarea.spellcheck = false
    textarea.readOnly = this.readOnly
    textarea.addEventListener('input', this.handleInput)
    this.deps.container.style.display = 'none'
    this.deps.parent.appendChild(textarea)
    this.textarea = textarea
    textarea.focus()
  }

  exit(): void {
    if (!this.active || !this.textarea) return
    const markdown = this.textarea.value
    this.active = false
    this.removeTextarea()
    this.deps.container.style.display = ''
    this.deps.applyMarkdown(markdown)
  }

  /** 源码模式下 host 的 setDoc 直达 textarea，不动隐藏的 Crepe */
  setExternalMarkdown(markdown: string): void {
    if (this.textarea) this.textarea.value = markdown
  }

  setReadOnly(readOnly: boolean): void {
    this.readOnly = readOnly
    if (this.textarea) this.textarea.readOnly = readOnly
  }

  /** mountEditor 重建时调用，清掉残留 DOM 和状态 */
  destroy(): void {
    this.active = false
    this.removeTextarea()
  }

  private handleInput = (): void => {
    if (this.textarea) this.deps.onEdit(this.textarea.value)
  }

  private removeTextarea(): void {
    if (!this.textarea) return
    this.textarea.removeEventListener('input', this.handleInput)
    this.textarea.remove()
    this.textarea = null
  }
}
