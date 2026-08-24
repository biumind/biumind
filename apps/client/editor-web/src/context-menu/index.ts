// 自绘右键菜单控制器：挂/卸全局监听，open/close 状态机，随 teardownEditor()
// 销毁。触发链路（设计 §3）：
//   contextmenu 事件（capture）→ preventDefault → context.ts 探测 itemType
//   → model.ts 注册表 isActive 过滤 → view.ts 渲染定位 → run() 执行。
// 源码模式（SourceModeController 的 textarea）同样拦截，菜单只有
// 剪切/复制/粘贴/全选 四项（作用于 textarea 选区，不走 PM）。

import { editorViewCtx, serializerCtx } from '@milkdown/kit/core'
import { callCommand, insert } from '@milkdown/kit/utils'
import {
  deleteSelectedCellsCommand,
  selectTableCommand,
} from '@milkdown/kit/preset/gfm'
import { TextSelection } from 'prosemirror-state'
import { selectAll as pmSelectAll } from 'prosemirror-commands'
import type { EditorView } from 'prosemirror-view'
import type { Crepe } from '@milkdown/crepe'
import type { $Command } from '@milkdown/kit/utils'

import type { BridgeClient } from '../bridge/client'
import { createTranslator } from '../i18n'
import type { SourceModeController } from '../source-mode'
import type { ToolbarActions } from '../toolbar'
import { createClipboard, type ClipboardBackend } from './clipboard'
import { detectMenuContext } from './context'
import { fragmentToHtml } from './html'
import {
  buildMenuRegistry,
  filterMenuEntries,
  type MenuContext,
  type MenuDeps,
  type MenuEntry,
} from './model'
import { MenuView } from './view'

export interface ContextMenuControllerDeps {
  bridge: BridgeClient
  /** 与 toolbar 共用的同一批命令（main.ts 提取的 editorCommands） */
  commands: ToolbarActions
  getCrepe: () => Crepe | null
  getSourceMode: () => SourceModeController | null
  getReadOnly: () => boolean
  /** init features.aiActions：host 声明已接 AI 动作才渲染 AI 组（P1 恒 false） */
  aiActions: boolean
  /** init features.imageUpload：host 声明已接上传链路才渲染「替换图片…」 */
  imageUpload: boolean
  locale: string
}

export class ContextMenuController {
  private readonly deps: ContextMenuControllerDeps
  private readonly clipboard: ClipboardBackend
  private readonly registry: MenuEntry[]
  private readonly view = new MenuView()
  private locale: string
  private attached = false

  constructor(deps: ContextMenuControllerDeps) {
    this.deps = deps
    this.locale = deps.locale
    this.clipboard = createClipboard(deps.bridge, (msg) =>
      deps.bridge.sendLog({ level: 'warn', msg: `[context-menu] ${msg}` }),
    )
    this.registry = buildMenuRegistry(this.buildMenuDeps())
  }

  attach(): void {
    if (this.attached) return
    this.attached = true
    document.addEventListener('contextmenu', this.onContextMenu, true)
  }

  destroy(): void {
    if (this.attached) {
      this.attached = false
      document.removeEventListener('contextmenu', this.onContextMenu, true)
    }
    this.view.destroy()
  }

  /** setOptions.locale 推送：菜单每次打开现构建，换 translator 即可 */
  setLocale(locale: string): void {
    this.locale = locale
  }

  private onContextMenu = (event: MouseEvent): void => {
    const target = event.target as HTMLElement | null
    if (!target) return

    const sourceMode = this.deps.getSourceMode()
    const inSource =
      sourceMode?.active === true && target.closest('.kc-source-editor')
    const inEditor = target.closest('.milkdown')
    if (!inSource && !inEditor) return

    event.preventDefault()
    event.stopPropagation()
    void this.openMenu(event, inSource ? sourceMode : null)
  }

  private async openMenu(
    event: MouseEvent,
    sourceMode: SourceModeController | null,
  ): Promise<void> {
    let ctx: MenuContext | null
    if (sourceMode) {
      const sel = sourceMode.getSelection()
      ctx = {
        itemType: 'source',
        hasSelection: sel !== null && sel.start !== sel.end,
        readOnly: this.deps.getReadOnly(),
        canPaste: false,
        aiActions: false,
        imageUpload: false,
        from: 0,
        to: 0,
      }
    } else {
      const view = this.pmView()
      if (!view) return
      ctx = detectMenuContext(view, { x: event.clientX, y: event.clientY }, {
        readOnly: this.deps.getReadOnly(),
        canPaste: false,
        aiActions: this.deps.aiActions,
        imageUpload: this.deps.imageUpload,
      })
      if (!ctx) return
    }
    // 开菜单前探测一次剪贴板：空/读不到 → 粘贴项置灰（readText 拒绝不崩）
    ctx.canPaste = (await this.clipboard.read()) !== null
    const entries = filterMenuEntries(this.registry, ctx)
    if (entries.length === 0) return
    this.view.open(entries, { x: event.clientX, y: event.clientY }, {
      ctx,
      t: createTranslator(this.locale),
      onClose: () => {},
    })
  }

  private pmView(): EditorView | null {
    let view: EditorView | null = null
    this.deps
      .getCrepe()
      ?.editor.action((ctx) => {
        view = ctx.get(editorViewCtx)
      })
    return view
  }

  /** 动作执行前用打开时快照的 {from, to} 恢复一次选区（防焦点行为差异） */
  private restoreSelection(view: EditorView, ctx: MenuContext): void {
    if (ctx.itemType === 'source') return
    const doc = view.state.doc
    const from = Math.min(ctx.from, doc.content.size)
    const to = Math.min(ctx.to, doc.content.size)
    try {
      view.dispatch(
        view.state.tr.setSelection(TextSelection.create(doc, from, to)),
      )
    } catch {
      // 区间在菜单打开期间已失效（文档被改）——放弃恢复，用当前选区
    }
  }

  private runCommand<T>(command: $Command<T>, payload?: T): void {
    this.deps
      .getCrepe()
      ?.editor.action(callCommand(command.key, payload as T))
  }

  private buildMenuDeps(): MenuDeps {
    const { bridge, commands } = this.deps
    const clipboard = this.clipboard

    const withView = (fn: (view: EditorView) => void): void => {
      this.deps.getCrepe()?.editor.action((ctx) => {
        fn(ctx.get(editorViewCtx))
      })
    }

    return {
      commands,
      clipboard,

      copySelection: async (menuCtx, cut) => {
        if (cut && this.deps.getReadOnly()) return
        let markdown = ''
        let html = ''
        this.deps.getCrepe()?.editor.action((ctx) => {
          const view = ctx.get(editorViewCtx)
          this.restoreSelection(view, menuCtx)
          const slice = view.state.selection.content()
          // Serializer 只吃 Node：把选区 slice 包进临时 doc 节点再序列化
          const serializer = ctx.get(serializerCtx)
          markdown = serializer(
            view.state.schema.topNodeType.create(null, slice.content),
          ).trim()
          // P2 双格式：同一 slice 再出一份 HTML（macOS 写 NSPasteboard
          // text+html，粘到 Word/飞书等外部应用保留格式）
          html = fragmentToHtml(view.state.schema, slice.content)
          if (cut) view.dispatch(view.state.tr.deleteSelection())
        })
        if (markdown) {
          await clipboard.write(html ? { text: markdown, html } : { text: markdown })
        }
      },

      pasteMarkdown: async () => {
        if (this.deps.getReadOnly()) return
        const data = await clipboard.read()
        if (!data) return
        // 与 host insertText 命令同路径：markdown 解析成节点插入
        this.deps.getCrepe()?.editor.action(insert(data.text))
      },

      pastePlainText: async () => {
        if (this.deps.getReadOnly()) return
        const data = await clipboard.read()
        if (!data) return
        withView((view) => view.pasteText(data.text))
      },

      selectAll: () => {
        withView((view) => pmSelectAll(view.state, view.dispatch))
      },

      removeLink: (menuCtx) => {
        withView((view) => {
          this.restoreSelection(view, menuCtx)
          const linkMark = view.state.schema.marks.link
          if (!linkMark) return
          const { from, to } = view.state.selection
          view.dispatch(view.state.tr.removeMark(from, to, linkMark))
        })
      },

      openLink: (href) => {
        bridge.sendNavigate({ kind: 'external', url: href })
      },

      deleteTable: () => {
        if (this.deps.getReadOnly()) return
        this.runCommand(selectTableCommand)
        this.runCommand(deleteSelectedCellsCommand)
      },

      copyCodeBlock: async (menuCtx) => {
        let code = ''
        withView((view) => {
          const $pos = view.state.doc.resolve(
            Math.min(menuCtx.from, view.state.doc.content.size),
          )
          for (let depth = $pos.depth; depth >= 0; depth -= 1) {
            const node = $pos.node(depth)
            if (node.type.name === 'code_block') {
              code = node.textContent
              break
            }
          }
        })
        if (code) await clipboard.write({ text: code })
      },

      editImageCaption: (nodePos) => {
        withView((view) => {
          const dom = view.nodeDOM(nodePos)
          const input =
            dom instanceof HTMLElement ? dom.querySelector('input') : null
          if (!input) {
            bridge.sendLog({
              level: 'warn',
              msg: '[context-menu] editImageCaption: caption input not found',
            })
            return
          }
          input.focus()
        })
      },

      deleteNode: (nodePos) => {
        if (this.deps.getReadOnly()) return
        withView((view) => {
          const node = view.state.doc.nodeAt(nodePos)
          if (!node) return
          view.dispatch(view.state.tr.delete(nodePos, nodePos + node.nodeSize))
        })
      },

      replaceImage: async (nodePos) => {
        if (this.deps.getReadOnly()) return
        // host 走既有上传链路（选图 → presign 直传）；取消/失败回 null 不动节点
        const reply = await bridge.requestImageUpload()
        const uri = reply.uri
        if (typeof uri !== 'string' || uri.length === 0) return
        withView((view) => {
          const node = view.state.doc.nodeAt(nodePos)
          if (!node) return
          // 更新 attrs.src（保留 caption/alt），不新建节点 —— undo 一步可逆
          view.dispatch(
            view.state.tr.setNodeMarkup(nodePos, undefined, {
              ...node.attrs,
              src: uri,
            }),
          )
        })
      },

      copyImage: async (nodePos) => {
        let markdown = ''
        withView((view) => {
          const node = view.state.doc.nodeAt(nodePos)
          if (!node) return
          const src = (node.attrs.src as string | undefined) ?? ''
          if (!src) return
          const alt =
            (node.attrs.alt as string | undefined) ??
            (node.attrs.caption as string | undefined) ??
            ''
          markdown = `![${alt}](${src})`
        })
        if (markdown) await clipboard.write({ text: markdown })
      },

      aiAction: (action, menuCtx) => {
        let text = ''
        withView((view) => {
          text = view.state.doc.textBetween(menuCtx.from, menuCtx.to, '\n')
        })
        bridge.sendAiAction({
          action,
          from: menuCtx.from,
          to: menuCtx.to,
          text,
        })
      },

      // ── 源码模式 textarea ──

      sourceCut: async () => {
        const sm = this.deps.getSourceMode()
        const sel = sm?.getSelection()
        if (!sm || !sel || sel.start === sel.end) return
        await clipboard.write({ text: sel.text })
        if (!this.deps.getReadOnly()) sm.replaceRange(sel.start, sel.end, '')
      },

      sourceCopy: async () => {
        const sel = this.deps.getSourceMode()?.getSelection()
        if (sel && sel.start !== sel.end) {
          await clipboard.write({ text: sel.text })
        }
      },

      sourcePaste: async () => {
        if (this.deps.getReadOnly()) return
        const sm = this.deps.getSourceMode()
        const sel = sm?.getSelection()
        if (!sm || !sel) return
        const data = await clipboard.read()
        if (!data) return
        sm.replaceRange(sel.start, sel.end, data.text)
      },

      sourceSelectAll: () => {
        this.deps.getSourceMode()?.selectAll()
      },
    }
  }
}
