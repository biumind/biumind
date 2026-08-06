// Knowcode editor bundle entry. Phase 1 scope: Crepe up + bridge protocol v1
// roundtrip (init / ready / docChanged / setDoc / setOptions / command / log).
// Wikilink and mermaid plugins are installed in later phases.

import { Crepe } from '@milkdown/crepe'
import { listener, listenerCtx } from '@milkdown/kit/plugin/listener'
import {
  editorStateCtx,
  editorViewCtx,
  remarkStringifyOptionsCtx,
} from '@milkdown/kit/core'
import { callCommand, insert, replaceAll } from '@milkdown/kit/utils'
import {
  createCodeBlockCommand,
  insertHrCommand,
  toggleEmphasisCommand,
  toggleInlineCodeCommand,
  toggleStrongCommand,
  wrapInBlockquoteCommand,
  wrapInBulletListCommand,
  wrapInHeadingCommand,
  wrapInOrderedListCommand,
} from '@milkdown/kit/preset/commonmark'
import {
  insertTableCommand,
  toggleStrikethroughCommand,
} from '@milkdown/kit/preset/gfm'
import { redoCommand, undoCommand } from '@milkdown/kit/plugin/history'
import { liftListItem, wrapInList } from '@milkdown/kit/prose/schema-list'
import type { Ctx } from '@milkdown/kit/ctx'
import type { $Command } from '@milkdown/kit/utils'
import { TextSelection } from 'prosemirror-state'

// 逐项引入 crepe 主题 css（替代整包 style.css），排除 latex.css ——
// 它会 @import katex/dist/katex.min.css 并拖入全部 KaTeX 字体文件。
import '@milkdown/crepe/theme/common/prosemirror.css'
import '@milkdown/crepe/theme/common/reset.css'
import '@milkdown/crepe/theme/common/block-edit.css'
import '@milkdown/crepe/theme/common/code-mirror.css'
import '@milkdown/crepe/theme/common/cursor.css'
import '@milkdown/crepe/theme/common/image-block.css'
import '@milkdown/crepe/theme/common/link-tooltip.css'
import '@milkdown/crepe/theme/common/list-item.css'
import '@milkdown/crepe/theme/common/placeholder.css'
import '@milkdown/crepe/theme/common/toolbar.css'
import '@milkdown/crepe/theme/common/table.css'
import '@milkdown/crepe/theme/common/top-bar.css'
import '@milkdown/crepe/theme/common/diff.css'
import '@milkdown/crepe/theme/common/ai.css'
import '@milkdown/crepe/theme/frame.css'
import './theme/light.css'
import './theme/dark.css'

import { BridgeClient } from './bridge/client'
import { STRINGIFY_OPTIONS } from './markdown/stringify-options'
import { mermaidPlugins } from './plugins/mermaid'
import { applyWikilinkRemark, wikilinkPlugins } from './plugins/wikilink'
import { SourceModeController } from './source-mode'
import { formatTimestamp } from './timestamp'
import { computeActiveState } from './toolbar/active-state'
import { createToolbar, type Toolbar } from './toolbar'
import type { Cm6Handle } from './cm6'
import type {
  CommandPayload,
  InitPayload,
  ReplaceSelectionArgs,
  SetDocPayload,
  SetOptionsPayload,
} from './bridge/protocol'

const EDITOR_VERSION = '0.1.0'
const DOC_CHANGED_DEBOUNCE_MS = 200

interface EditorState {
  crepe: Crepe | null
  cm6: Cm6Handle | null
  toolbar: Toolbar | null
  sourceMode: SourceModeController | null
  readOnly: boolean
  revision: number
  lastSentMarkdown: string
  /** 防抖窗口内待发送的 markdown（WYSIWYG 与源码模式共用） */
  pendingMarkdown: string | null
  applyingExternalEdit: boolean
  docChangeTimer: ReturnType<typeof setTimeout> | null
}

const state: EditorState = {
  crepe: null,
  cm6: null,
  toolbar: null,
  sourceMode: null,
  readOnly: false,
  revision: 0,
  lastSentMarkdown: '',
  pendingMarkdown: null,
  applyingExternalEdit: false,
  docChangeTimer: null,
}

const bridge = new BridgeClient()

bootstrap()

function bootstrap(): void {
  bridge.start({
    onInit: handleInit,
    onSetDoc: handleSetDoc,
    onSetOptions: handleSetOptions,
    onCommand: handleCommand,
  })
  bridge.sendReady(EDITOR_VERSION)

  // Standalone dev shortcut — when no host is around, render an empty editor
  // so `npm run dev` is useful without bootstrapping a Flutter shell.
  // `?engine=cm6` 切到 CM6 内核并喂一份覆盖各格式元素的示例文档，
  // 方便人工验证 Rich Markdown 渲染。
  if (!window.flutter_inappwebview && (!window.parent || window.parent === window)) {
    const cm6 = new URLSearchParams(window.location.search).get('engine') === 'cm6'
    void mountEditor({
      markdown: cm6 ? CM6_DEMO_MARKDOWN : '# Hello Milkdown\n\nStart typing — `npm run dev` mode.\n',
      theme: 'light',
      readOnly: false,
      locale: 'en',
      features: { wikilink: true, mermaid: true, engine: cm6 ? 'cm6' : undefined },
    })
  }
}

// standalone CM6 示例：覆盖粗/斜/行内 code/链接/checkbox/标题/引用/代码块/
// 图片/mermaid
const CM6_DEMO_MARKDOWN = [
  '# CM6 渲染自测',
  '',
  '正文 **粗体** *斜体* ~~删除线~~ `inline code`，以及 [示例链接](https://example.com)（Ctrl/Cmd+点击触发 navigate）。',
  '',
  '## 任务列表',
  '',
  '- [ ] 未完成项',
  '- [x] 已完成项（整行半透明）',
  '',
  '> 引用块：光标离开本行后只剩左边框。',
  '',
  '```ts',
  'const answer: number = 42',
  '```',
  '',
  '## 图片（独占行 → 源码行下方挂图）',
  '',
  '![示例图片](https://picsum.photos/400/200)',
  '',
  '行内图片 ![不会渲染](https://picsum.photos/40) 保持源码。',
  '',
  '## Mermaid（光标进入代码块隐藏预览）',
  '',
  '```mermaid',
  'graph LR',
  '  A[开始] --> B{判断}',
  '  B -->|是| C[结束]',
  '  B -->|否| A',
  '```',
  '',
  '---',
  '',
].join('\n')

async function handleInit(payload: InitPayload): Promise<void> {
  await mountEditor(payload)
}

async function handleSetDoc(payload: SetDocPayload): Promise<void> {
  if (state.cm6) {
    if (payload.markdown === state.lastSentMarkdown) return
    applyExternalMarkdownCm6(payload.markdown, payload.preserveSelection)
    state.revision = Math.max(state.revision, payload.revision)
    return
  }
  if (!state.crepe) return
  if (payload.markdown === state.lastSentMarkdown) return
  if (state.sourceMode?.active) {
    // 源码模式：直接更新 textarea，不动隐藏的 Crepe —— replaceAll 进隐藏编辑器
    // 再灌回会覆盖用户正在编辑的源文本。
    state.sourceMode.setExternalMarkdown(payload.markdown)
    state.lastSentMarkdown = payload.markdown
    state.revision = Math.max(state.revision, payload.revision)
    return
  }
  applyExternalMarkdown(payload.markdown)
  state.revision = Math.max(state.revision, payload.revision)
}

function handleSetOptions(payload: SetOptionsPayload): void {
  if (payload.theme) {
    applyTheme(payload.theme)
    state.cm6?.setTheme(payload.theme)
  }
  if (payload.readOnly !== undefined) {
    state.readOnly = payload.readOnly
    state.crepe?.setReadonly(payload.readOnly)
    state.cm6?.setReadOnly(payload.readOnly)
    state.toolbar?.setDisabled(payload.readOnly)
    state.sourceMode?.setReadOnly(payload.readOnly)
  }
}

function handleCommand(payload: CommandPayload): void {
  if (state.cm6) {
    if (payload.name === 'focus') state.cm6.focus()
    // undo/redo 协议里早已定义，CM6 内核顺手接上（CM history 现成）
    if (payload.name === 'undo' || payload.name === 'redo') {
      state.cm6.execCommand(payload.name)
    }
    if (payload.name === 'insertText') {
      const text = payload.args?.text
      if (typeof text === 'string' && text.length > 0) state.cm6.insertText(text)
    }
    return
  }
  if (!state.crepe) return
  // Phase 1: only `focus` is wired. The rest land in phase 6.
  if (payload.name === 'focus') {
    const dom = document.querySelector<HTMLElement>('.milkdown .ProseMirror')
    dom?.focus()
  }
  // `insertText` — 在当前光标处插入一段 markdown（解析成节点，不是纯文本）。
  // 笔记附件图片走这里：host 上传完成后发 `![name](url)` 片段。
  // 插入触发 listener → docChanged，host 侧无需额外同步。
  if (payload.name === 'insertText') {
    const text = payload.args?.text
    if (typeof text === 'string' && text.length > 0) {
      state.crepe.editor.action(insert(text))
    }
  }
  // selection-edit (S3 P1-6): LLM 跑完后回写选区（TOCTOU 校验 + milkdown insert 替换）。
  if (payload.name === 'replaceSelection') {
    replaceSelectionMilkdown(
      payload.args as unknown as ReplaceSelectionArgs | undefined,
    )
  }
}

async function mountEditor(payload: InitPayload): Promise<void> {
  // 双内核分支：features.engine === 'cm6' 走 CM6 源码内核（懒加载），
  // 否则维持 Milkdown Crepe（wiki 路径，行为不变）。
  if (payload.features.engine === 'cm6') {
    await mountCm6Editor(payload)
    return
  }
  await mountCrepeEditor(payload)
}

/** 重建前的公共清理：两个内核分支共用（另一内核残留一并清掉） */
async function teardownEditor(): Promise<void> {
  // 重建前清掉源码模式 DOM 状态，避免残留 textarea 指向旧容器
  state.sourceMode?.destroy()
  state.sourceMode = null
  state.toolbar = null
  state.cm6?.destroy()
  state.cm6 = null
  if (state.crepe) {
    await state.crepe.destroy()
    state.crepe = null
  }
}

async function mountCrepeEditor(payload: InitPayload): Promise<void> {
  await teardownEditor()
  applyTheme(payload.theme)
  const root = document.getElementById('root')
  if (!root) throw new Error('#root not found')
  root.innerHTML = ''

  const editorWrap = document.createElement('div')
  editorWrap.className = 'kc-editor-wrap'
  root.appendChild(editorWrap)

  const crepe = new Crepe({
    root: editorWrap,
    defaultValue: payload.markdown,
    features: {
      // 关闭 latex：KaTeX (~586KB) 及其字体不随包分发（katex 本身被
      // crepe 静态 import，另由 vite alias 指向 stub 才能真正移出产物）
      [Crepe.Feature.Latex]: false,
    },
  })

  crepe.editor
    .config((ctx) => {
      ctx.set(remarkStringifyOptionsCtx, { ...STRINGIFY_OPTIONS })
      if (payload.features.wikilink) applyWikilinkRemark(ctx)
      ctx.get(listenerCtx).markdownUpdated((_ctx, markdown, prevMarkdown) => {
        if (markdown === prevMarkdown) return
        if (state.applyingExternalEdit) return
        scheduleDocChanged(markdown)
      })
      // 工具栏选中态：文档或选区变化后重新计算光标格式上下文
      ctx.get(listenerCtx).updated((updatedCtx) => {
        updateToolbarActive(updatedCtx)
      })
      ctx.get(listenerCtx).selectionUpdated((updatedCtx) => {
        updateToolbarActive(updatedCtx)
        pushSelection(updatedCtx)
      })
    })
    .use(listener)

  if (payload.features.wikilink) {
    crepe.editor.use(wikilinkPlugins(bridge))
  }
  if (payload.features.mermaid) {
    crepe.editor.use(mermaidPlugins())
  }

  await crepe.create()
  state.readOnly = !!payload.readOnly
  if (payload.readOnly) crepe.setReadonly(true)
  state.crepe = crepe
  state.lastSentMarkdown = payload.markdown
  state.pendingMarkdown = null

  const toolbar = createToolbar({
    undo: () => runCommand(undoCommand),
    redo: () => runCommand(redoCommand),
    toggleStrong: () => runCommand(toggleStrongCommand),
    toggleEmphasis: () => runCommand(toggleEmphasisCommand),
    toggleStrikeThrough: () => runCommand(toggleStrikethroughCommand),
    toggleInlineCode: () => runCommand(toggleInlineCodeCommand),
    wrapInHeading: (level) => runCommand(wrapInHeadingCommand, level),
    wrapInBlockquote: () => runCommand(wrapInBlockquoteCommand),
    insertHr: () => runCommand(insertHrCommand),
    wrapInBulletList: () => runCommand(wrapInBulletListCommand),
    wrapInOrderedList: () => runCommand(wrapInOrderedListCommand),
    toggleTaskList,
    createCodeBlock: () => runCommand(createCodeBlockCommand),
    insertTable: () => runCommand(insertTableCommand, { row: 3, col: 3 }),
    insertTimestamp: () => {
      state.crepe?.editor.action(insert(formatTimestamp(new Date())))
    },
    toggleSourceMode,
  })
  root.insertBefore(toolbar.element, editorWrap)
  toolbar.setDisabled(state.readOnly)
  state.toolbar = toolbar

  state.sourceMode = new SourceModeController({
    parent: root,
    container: editorWrap,
    getMarkdown: () => state.crepe?.getMarkdown() ?? '',
    applyMarkdown: applyExternalMarkdown,
    onEdit: (markdown) => scheduleDocChanged(markdown),
  })
  state.sourceMode.setReadOnly(state.readOnly)
}

/** CM6 源码内核挂载：动态 import 懒加载，不进 Milkdown 路径主 chunk */
async function mountCm6Editor(payload: InitPayload): Promise<void> {
  await teardownEditor()
  applyTheme(payload.theme)
  const root = document.getElementById('root')
  if (!root) throw new Error('#root not found')
  root.innerHTML = ''

  const editorWrap = document.createElement('div')
  editorWrap.className = 'kc-editor-wrap'
  root.appendChild(editorWrap)

  const { mountCm6Editor: mount } = await import('./cm6')
  const handle = mount({
    container: editorWrap,
    markdown: payload.markdown,
    theme: payload.theme,
    readOnly: !!payload.readOnly,
    mermaid: !!payload.features.mermaid,
    onDocChanged: (markdown) => {
      if (state.applyingExternalEdit) return
      scheduleDocChanged(markdown)
    },
    onActiveStateChange: (active) => {
      state.toolbar?.setActive(active)
    },
    onNavigate: (url) => {
      bridge.sendNavigate({ kind: 'external', url })
    },
  })
  state.cm6 = handle
  state.readOnly = !!payload.readOnly
  state.lastSentMarkdown = payload.markdown
  state.pendingMarkdown = null

  // CM6 即源码编辑器：隐藏「源码切换」按钮，其余 16 闭包走命令路由表
  const exec = (name: string): void => {
    handle.execCommand(name)
  }
  const toolbar = createToolbar(
    {
      undo: () => exec('undo'),
      redo: () => exec('redo'),
      toggleStrong: () => exec('toggleStrong'),
      toggleEmphasis: () => exec('toggleEmphasis'),
      toggleStrikeThrough: () => exec('toggleStrikeThrough'),
      toggleInlineCode: () => exec('toggleInlineCode'),
      wrapInHeading: (level) => exec(`h${level}`),
      wrapInBlockquote: () => exec('blockquote'),
      insertHr: () => exec('hr'),
      wrapInBulletList: () => exec('bulletList'),
      wrapInOrderedList: () => exec('orderedList'),
      toggleTaskList: () => exec('taskList'),
      createCodeBlock: () => exec('codeBlock'),
      insertTable: () => exec('table'),
      insertTimestamp: () => exec('timestamp'),
      toggleSourceMode: () => {},
    },
    { hideSourceToggle: true },
  )
  root.insertBefore(toolbar.element, editorWrap)
  toolbar.setDisabled(state.readOnly)
  toolbar.setActive(handle.computeActiveState())
  state.toolbar = toolbar
}

/** CM6 内核的外部灌入（setDoc）：防回环标志与 Crepe 路径同一套 */
function applyExternalMarkdownCm6(markdown: string, preserveSelection: boolean): void {
  if (!state.cm6) return
  state.applyingExternalEdit = true
  try {
    state.cm6.applyMarkdown(markdown, preserveSelection)
    state.lastSentMarkdown = markdown
  } finally {
    state.applyingExternalEdit = false
  }
}

/** 走 Milkdown 命令体系执行一个 $Command */
function runCommand<T>(command: $Command<T>, payload?: T): void {
  state.crepe?.editor.action(callCommand(command.key, payload as T))
}

/** selection-edit (S3 P1-6): 把当前选区 + viewport 坐标推给 host，让浮层跟随。
 *  coords 相对编辑器视口；host 加 WebView 屏幕原点换算成绝对坐标。 */
function pushSelection(ctx: Ctx): void {
  if (!state.crepe) return
  const view = ctx.get(editorViewCtx)
  const { selection, doc } = view.state
  const { from, to, empty } = selection
  const text = empty ? '' : doc.textBetween(from, to, '\n')
  let coords = { left: 0, top: 0, right: 0, bottom: 0 }
  try {
    coords = view.coordsAtPos(selection.head)
  } catch {
    // coordsAtPos 在 detach/重挂时偶抛 —— 上报 0,0 让 host 隐藏浮层
  }
  bridge.sendSelectionChanged({ from, to, text, empty, coords })
}

/** replaceSelection — host LLM 跑完后回写。TOCTOU 校验区间内容未变，再设
 *  选区到 from..to 跑 milkdown insert（解析 markdown + 替换选区）。dispatch
 *  触 listener → docChanged → host autosave 落库（无额外保存路径）。 */
function replaceSelectionMilkdown(args: ReplaceSelectionArgs | undefined): void {
  if (!args || !state.crepe) return
  state.crepe.editor.action((ctx) => {
    const view = ctx.get(editorViewCtx)
    const { state: pmState } = view
    if (pmState.doc.textBetween(args.from, args.to, '\n') !== args.expectedText) {
      bridge.sendLog({
        level: 'warn',
        msg: 'replaceSelection aborted: selection changed since capture (TOCTOU)',
      })
      return false
    }
    const tr = pmState.tr.setSelection(
      TextSelection.create(pmState.doc, args.from, args.to),
    )
    view.dispatch(tr)
    return true
  })
  // insert 解析 markdown → nodes，替换当前选区（选区非空则替换，PM 惯例）
  state.crepe.editor.action(insert(args.markdown))
}

// Crepe/gfm 未导出任务列表命令，这里直接走 ProseMirror：
// 已在任务列表 → lift 出来；在普通列表 → 就地转成任务项；否则包一层任务列表。
function toggleTaskList(): void {
  state.crepe?.editor.action((ctx) => {
    const view = ctx.get(editorViewCtx)
    const listItem = view.state.schema.nodes.list_item
    if (!listItem) return false
    const { state: pmState, dispatch } = view
    const { $from } = pmState.selection
    for (let depth = $from.depth; depth > 0; depth -= 1) {
      const node = $from.node(depth)
      if (node.type !== listItem) continue
      if (node.attrs.checked !== null && node.attrs.checked !== undefined) {
        return liftListItem(listItem)(pmState, dispatch)
      }
      if (dispatch) {
        dispatch(
          pmState.tr.setNodeMarkup($from.before(depth), undefined, {
            ...node.attrs,
            checked: false,
          }),
        )
      }
      return true
    }
    return wrapInList(listItem, { checked: false })(pmState, dispatch)
  })
}

function toggleSourceMode(): void {
  if (!state.crepe || !state.sourceMode || state.readOnly) return
  // 切模式前把防抖窗口里的变更先落掉，保证 getMarkdown / lastSentMarkdown 都是最新的
  flushDocChanged()
  state.sourceMode.toggle()
  state.toolbar?.setSourceMode(state.sourceMode.active)
}

/** replaceAll 灌入外部 markdown 并防回环（WYSIWYG setDoc 与源码模式退出共用） */
function applyExternalMarkdown(markdown: string): void {
  if (!state.crepe) return
  state.applyingExternalEdit = true
  try {
    state.crepe.editor.action(replaceAll(markdown))
    state.lastSentMarkdown = markdown
  } finally {
    state.applyingExternalEdit = false
  }
}

function updateToolbarActive(ctx: Ctx): void {
  if (!state.toolbar) return
  try {
    state.toolbar.setActive(computeActiveState(ctx.get(editorStateCtx)))
  } catch {
    // 编辑器视图未就绪（初始化早期），忽略本次刷新
  }
}

function scheduleDocChanged(markdown: string): void {
  state.pendingMarkdown = markdown
  if (state.docChangeTimer) clearTimeout(state.docChangeTimer)
  state.docChangeTimer = setTimeout(flushDocChanged, DOC_CHANGED_DEBOUNCE_MS)
}

function flushDocChanged(): void {
  if (state.docChangeTimer) {
    clearTimeout(state.docChangeTimer)
    state.docChangeTimer = null
  }
  const markdown = state.pendingMarkdown
  if (markdown === null) return
  state.pendingMarkdown = null
  if (markdown === state.lastSentMarkdown) return
  state.lastSentMarkdown = markdown
  state.revision += 1
  bridge.sendDocChanged({ markdown, revision: state.revision })
}

function applyTheme(theme: 'light' | 'dark'): void {
  const root = document.documentElement
  if (theme === 'dark') {
    root.classList.add('dark')
  } else {
    root.classList.remove('dark')
  }
}
