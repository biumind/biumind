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
import { ContextMenuController } from './context-menu'
import { SelectionToolbar } from './context-menu/selection-toolbar'
import { buildLocalizedFeatureConfigs } from './i18n'
import { STRINGIFY_OPTIONS } from './markdown/stringify-options'
import { createImagePresignConfig } from './plugins/image-presign'
import { mermaidPlugins } from './plugins/mermaid'
import { applyWikilinkRemark, wikilinkPlugins } from './plugins/wikilink'
import { SourceModeController } from './source-mode'
import { formatTimestamp } from './timestamp'
import { computeActiveState } from './toolbar/active-state'
import { createToolbar, type Toolbar, type ToolbarActions } from './toolbar'
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
  toolbar: Toolbar | null
  sourceMode: SourceModeController | null
  contextMenu: ContextMenuController | null
  /** 移动端选区浮动工具条（M1；仅 platform = ios/android 时创建） */
  selectionToolbar: SelectionToolbar | null
  readOnly: boolean
  /** 当前 UI 语言（init 定初值，setOptions.locale 可运行时切换） */
  locale: string
  revision: number
  lastSentMarkdown: string
  /** 防抖窗口内待发送的 markdown（WYSIWYG 与源码模式共用） */
  pendingMarkdown: string | null
  /**
   * 文档纪元 = 最近一次 setDoc 的 revision（host 每次 setDoc 递增）。
   * 切换笔记 = 新纪元。docChanged 携带变更发生时的纪元，host 对不上
   * 即丢弃 —— 防止切换瞬间防抖/在途的旧笔记内容存进新笔记。
   */
  docEpoch: number
  /** scheduleDocChanged 时捕获的纪元，随 pendingMarkdown 一起等防抖 */
  pendingEpoch: number | null
  applyingExternalEdit: boolean
  docChangeTimer: ReturnType<typeof setTimeout> | null
}

const state: EditorState = {
  crepe: null,
  toolbar: null,
  sourceMode: null,
  contextMenu: null,
  selectionToolbar: null,
  readOnly: false,
  locale: 'en',
  revision: 0,
  lastSentMarkdown: '',
  pendingMarkdown: null,
  docEpoch: 0,
  pendingEpoch: null,
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
  if (!window.flutter_inappwebview && (!window.parent || window.parent === window)) {
    void mountEditor({
      markdown: '# Hello Milkdown\n\nStart typing — `npm run dev` mode.\n',
      theme: 'light',
      readOnly: false,
      locale: 'en',
      features: { wikilink: true, mermaid: true },
    })
  }
}

async function handleInit(payload: InitPayload): Promise<void> {
  // 纪元与 host 对齐（含 webview 重载后再 ready 的场景）。
  state.docEpoch = payload.epoch ?? 0
  await mountEditor(payload)
}

async function handleSetDoc(payload: SetDocPayload): Promise<void> {
  if (!state.crepe) return
  // 换文档 = 旧文档的防抖队列作废：pending 里的内容属于上一篇，若放它
  // 发出会被 host 记到新笔记头上（跨笔记串内容的根因）。纪元无条件跟
  // 进（即使内容相同跳过替换），保持与 host 的 _hostRevision 同步。
  if (state.docChangeTimer) {
    clearTimeout(state.docChangeTimer)
    state.docChangeTimer = null
  }
  state.pendingMarkdown = null
  state.pendingEpoch = null
  state.docEpoch = payload.revision
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
  }
  if (payload.readOnly !== undefined) {
    state.readOnly = payload.readOnly
    state.crepe?.setReadonly(payload.readOnly)
    state.toolbar?.setDisabled(payload.readOnly)
    state.sourceMode?.setReadOnly(payload.readOnly)
  }
  if (payload.locale) {
    state.locale = payload.locale
    // 菜单每次打开现构建，换 translator 即刻生效；crepe 自身文案维持 init
    state.contextMenu?.setLocale(payload.locale)
  }
}

function handleCommand(payload: CommandPayload): void {
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
  await mountCrepeEditor(payload)
}

/** 重建前的公共清理 */
async function teardownEditor(): Promise<void> {
  // 重建前清掉源码模式 DOM 状态，避免残留 textarea 指向旧容器
  state.selectionToolbar?.destroy()
  state.selectionToolbar = null
  state.contextMenu?.destroy()
  state.contextMenu = null
  state.sourceMode?.destroy()
  state.sourceMode = null
  state.toolbar = null
  if (state.crepe) {
    await state.crepe.destroy()
    state.crepe = null
  }
}

async function mountCrepeEditor(payload: InitPayload): Promise<void> {
  await teardownEditor()
  applyTheme(payload.theme)
  // 平台标记（M1）：<html data-platform> 供 CSS/动画/移动端裁剪分流；
  // 缺省（老 host）不标注 = 非移动端行为。
  const platform = payload.features.platform
  if (platform) {
    document.documentElement.dataset.platform = platform
  } else {
    delete document.documentElement.dataset.platform
  }
  // 移动端自绘选区工具条启用时，关掉 crepe 自带选区浮动工具栏（防双重 UI）；
  // 桌面保持现状（crepe toolbar + 右键菜单各司其职）。
  const mobileCustom =
    (platform === 'ios' || platform === 'android') &&
    payload.features.contextMenu !== 'native'
  const root = document.getElementById('root')
  if (!root) throw new Error('#root not found')
  root.innerHTML = ''

  const editorWrap = document.createElement('div')
  editorWrap.className = 'kc-editor-wrap'
  root.appendChild(editorWrap)

  const localizedConfigs = buildLocalizedFeatureConfigs(payload.locale)
  const crepe = new Crepe({
    root: editorWrap,
    defaultValue: payload.markdown,
    features: {
      // 关闭 latex：KaTeX (~586KB) 及其字体不随包分发（katex 本身被
      // crepe 静态 import，另由 vite alias 指向 stub 才能真正移出产物）
      [Crepe.Feature.Latex]: false,
      ...(mobileCustom ? { [Crepe.Feature.Toolbar]: false } : {}),
    },
    featureConfigs: {
      // UI 文案本地化（字典 → featureConfigs，见 src/i18n）；宿主行为配置
      // 在其后展开，同名字段宿主优先。
      ...localizedConfigs,
      // biu-file:// 附件渲染时换 presigned URL（文档里只存规范 URI），
      // 过期 403 由 onImageLoadError 强刷重换。block/inline 一处配置都覆盖。
      [Crepe.Feature.ImageBlock]: {
        ...localizedConfigs[Crepe.Feature.ImageBlock],
        ...createImagePresignConfig((fileId) =>
          bridge.requestPresignGet({ fileId }).then((r) => r.url ?? ''),
        ),
      },
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
        // 移动端选区浮动工具条（M1）：选区落定/塌缩驱动显隐
        state.selectionToolbar?.onSelectionUpdated()
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

  // toolbar 与右键菜单共用的同一批命令（避免两套命令绑定，设计 §4.3）
  const editorCommands: ToolbarActions = {
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
  }
  const toolbar = createToolbar(editorCommands)
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

  // 自绘右键菜单：features.contextMenu = 'native'（移动端）时不挂，走系统菜单
  state.locale = payload.locale
  if (payload.features.contextMenu !== 'native') {
    const menu = new ContextMenuController({
      bridge,
      commands: editorCommands,
      getCrepe: () => state.crepe,
      getSourceMode: () => state.sourceMode,
      getReadOnly: () => state.readOnly,
      aiActions: payload.features.aiActions === true,
      imageUpload: payload.features.imageUpload === true,
      locale: payload.locale,
    })
    menu.attach()
    state.contextMenu = menu
    // M1 场景 A：移动端选区浮动工具条（桌面端绝不创建 —— 桌面有右键菜单
    // + crepe 自带选区工具栏，三重 UI 不可接受）
    if (mobileCustom) {
      state.selectionToolbar = new SelectionToolbar(
        menu.selectionToolbarDeps(true),
      )
    }
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
  // 进源码模式收起选区工具条（textarea 场景不出 PM 工具条）
  state.selectionToolbar?.hide()
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
  // 变更发生当下的纪元随内容一起入队 —— flush 时 setDoc 可能已换文档，
  // 用此刻的 state.docEpoch 会把旧内容标成新纪元，host 就丢不掉了。
  state.pendingEpoch = state.docEpoch
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
  const epoch = state.pendingEpoch ?? state.docEpoch
  state.pendingMarkdown = null
  state.pendingEpoch = null
  if (markdown === state.lastSentMarkdown) return
  state.lastSentMarkdown = markdown
  state.revision += 1
  bridge.sendDocChanged({ markdown, revision: state.revision, epoch })
}

function applyTheme(theme: 'light' | 'dark'): void {
  const root = document.documentElement
  if (theme === 'dark') {
    root.classList.add('dark')
  } else {
    root.classList.remove('dark')
  }
}
