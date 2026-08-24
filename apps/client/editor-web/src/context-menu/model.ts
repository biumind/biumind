// 右键菜单模型 —— 声明式注册表（Joplin 谓词模式）：每个菜单项声明
// isActive(ctx) 谓词，渲染前按上下文过滤，无 if-else 散弹。
// label 是英文原文（msgid），渲染时过 createTranslator（i18n 守护见
// tests/context-menu-i18n.spec.ts）。
// 表格说明：crepe table-block 的行列手柄已覆盖 增删行列/列对齐/拖拽移动，
// 菜单只补它没有「删除表格」，不建重复入口（设计 §11 决策点 2）。
// 图标：格式/转换/插入组复用 toolbar ICONS，剪贴板/链接/图片等用
// context-menu/icons.ts 补画的同款 SVG（P2 起配图标）。

import { ICONS, type ToolbarActions } from '../toolbar'
import type { ClipboardBackend } from './clipboard'
import { MENU_ICONS } from './icons'

/** 上下文类型：context.ts 探测产物；source = 源码模式 textarea。 */
export type MenuItemType =
  | 'link'
  | 'image'
  | 'tableCell'
  | 'codeBlock'
  | 'text'
  | 'source'

export interface MenuContext {
  itemType: MenuItemType
  /** text 类型再分「有选区 / 光标」两态 */
  hasSelection: boolean
  readOnly: boolean
  /** 剪贴板是否有可读文本（开菜单前探测一次；false → 粘贴项置灰） */
  canPaste: boolean
  /** host 声明已接 AI 动作（init features.aiActions；P1 恒 false → AI 组隐藏） */
  aiActions: boolean
  /** host 声明已接图片上传链路（init features.imageUpload，notes 专属）；
   *  false 时图片菜单不渲染「替换图片…」 */
  imageUpload: boolean
  /** 打开菜单时快照的选区（动作执行前恢复兜底，防焦点行为差异丢选区） */
  from: number
  to: number
  /** itemType = link 时的目标地址 */
  linkHref?: string
  /** itemType = image 时的节点位置（删除/编辑说明用） */
  nodePos?: number
}

export interface MenuItem {
  id: string
  /** i18n msgid（英文原文），渲染时过 translator */
  label: string
  /** 内联 SVG（toolbar 同款手法），可选 —— 无图标项由 view 留占位对齐 */
  icon?: string
  /** 展示用快捷键标注（不绑定按键） */
  shortcut?: string
  danger?: boolean
  isActive: (ctx: MenuContext) => boolean
  /** 如「粘贴」在剪贴板为空时禁用 */
  disabled?: (ctx: MenuContext) => boolean
  children?: MenuItem[]
  run?: (ctx: MenuContext) => void | Promise<void>
}

export type MenuEntry = MenuItem | 'separator'

/** 模型依赖：编辑器动作 + 剪贴板 + 文档级操作，全部在 controller 注入。 */
export interface MenuDeps {
  /** 与 toolbar 共用的同一批命令（main.ts 提取的 editorCommands） */
  commands: ToolbarActions
  clipboard: ClipboardBackend
  /** 复制/剪切：把当前 PM 选区序列化成 markdown 写剪贴板；cut 时再删选区 */
  copySelection: (ctx: MenuContext, cut: boolean) => Promise<void>
  /** 读剪贴板并按 markdown 解析插入 */
  pasteMarkdown: () => Promise<void>
  /** 读剪贴板按纯文本插入（不解析 markdown） */
  pastePlainText: () => Promise<void>
  selectAll: () => void
  removeLink: (ctx: MenuContext) => void
  openLink: (href: string) => void
  deleteTable: () => void
  copyCodeBlock: (ctx: MenuContext) => Promise<void>
  editImageCaption: (nodePos: number) => void
  deleteNode: (nodePos: number) => void
  /** 替换图片：经 bridge 请 host 走既有上传链路，成功后更新节点 src（不新建节点） */
  replaceImage: (nodePos: number) => Promise<void>
  /** 复制图片的 markdown 表示（![caption](uri)）到剪贴板纯文本 */
  copyImage: (nodePos: number) => Promise<void>
  aiAction: (action: 'ask' | 'edit', ctx: MenuContext) => void
  /** 源码模式 textarea 操作 */
  sourceCut: () => Promise<void>
  sourceCopy: () => Promise<void>
  sourcePaste: () => Promise<void>
  sourceSelectAll: () => void
}

const editable = (ctx: MenuContext): boolean => !ctx.readOnly
const isText = (ctx: MenuContext): boolean => ctx.itemType === 'text'
const pasteDisabled = (ctx: MenuContext): boolean => !ctx.canPaste

/** 转换为 ▸ 子菜单（text 两态共用） */
function convertChildren(deps: MenuDeps): MenuItem[] {
  const { commands } = deps
  return [
    {
      id: 'convert-h1',
      label: 'Heading 1',
      icon: ICONS.h1,
      isActive: isText,
      run: () => commands.wrapInHeading(1),
    },
    {
      id: 'convert-h2',
      label: 'Heading 2',
      icon: ICONS.h2,
      isActive: isText,
      run: () => commands.wrapInHeading(2),
    },
    {
      id: 'convert-h3',
      label: 'Heading 3',
      icon: ICONS.h3,
      isActive: isText,
      run: () => commands.wrapInHeading(3),
    },
    {
      id: 'convert-quote',
      label: 'Quote',
      icon: ICONS.blockquote,
      isActive: isText,
      run: () => commands.wrapInBlockquote(),
    },
    {
      id: 'convert-bullet-list',
      label: 'Bullet List',
      icon: ICONS.bulletList,
      isActive: isText,
      run: () => commands.wrapInBulletList(),
    },
    {
      id: 'convert-ordered-list',
      label: 'Ordered List',
      icon: ICONS.orderedList,
      isActive: isText,
      run: () => commands.wrapInOrderedList(),
    },
    {
      id: 'convert-task-list',
      label: 'Task List',
      icon: ICONS.taskList,
      isActive: isText,
      run: () => commands.toggleTaskList(),
    },
    {
      id: 'convert-code-block',
      label: 'Code',
      icon: ICONS.codeBlock,
      isActive: isText,
      run: () => commands.createCodeBlock(),
    },
  ]
}

/** 构建菜单注册表（唯一数据源）。每次打开菜单前用 isActive 过滤。 */
export function buildMenuRegistry(deps: MenuDeps): MenuEntry[] {
  const { commands, clipboard } = deps

  const cut: MenuItem = {
    id: 'cut',
    label: 'Cut',
    icon: MENU_ICONS.cut,
    shortcut: '⌘X',
    isActive: (ctx) =>
      editable(ctx) &&
      ((ctx.itemType === 'source' ||
        ctx.itemType === 'text' ||
        ctx.itemType === 'link') &&
        ctx.hasSelection),
    run: (ctx) =>
      ctx.itemType === 'source'
        ? deps.sourceCut()
        : deps.copySelection(ctx, true),
  }
  const copy: MenuItem = {
    id: 'copy',
    label: 'Copy',
    icon: MENU_ICONS.copy,
    shortcut: '⌘C',
    isActive: (ctx) =>
      (ctx.itemType === 'source' && ctx.hasSelection) ||
      ((ctx.itemType === 'text' || ctx.itemType === 'link') && ctx.hasSelection),
    run: (ctx) =>
      ctx.itemType === 'source'
        ? deps.sourceCopy()
        : deps.copySelection(ctx, false),
  }
  const paste: MenuItem = {
    id: 'paste',
    label: 'Paste',
    icon: MENU_ICONS.paste,
    shortcut: '⌘V',
    isActive: (ctx) =>
      editable(ctx) && (ctx.itemType === 'source' || isText(ctx) || ctx.itemType === 'link'),
    disabled: pasteDisabled,
    run: (ctx) =>
      ctx.itemType === 'source' ? deps.sourcePaste() : deps.pasteMarkdown(),
  }
  const pastePlain: MenuItem = {
    id: 'paste-plain',
    label: 'Paste as Plain Text',
    icon: MENU_ICONS.pastePlain,
    shortcut: '⇧⌘V',
    isActive: (ctx) => editable(ctx) && (isText(ctx) || ctx.itemType === 'link'),
    disabled: pasteDisabled,
    run: () => deps.pastePlainText(),
  }
  const selectAll: MenuItem = {
    id: 'select-all',
    label: 'Select All',
    icon: MENU_ICONS.selectAll,
    shortcut: '⌘A',
    isActive: (ctx) =>
      editable(ctx) &&
      (ctx.itemType === 'source' || (isText(ctx) && !ctx.hasSelection)),
    run: (ctx) =>
      ctx.itemType === 'source' ? deps.sourceSelectAll() : deps.selectAll(),
  }

  return [
    // ── 剪贴板组（text / link / source）──
    cut,
    copy,
    paste,
    pastePlain,
    selectAll,
    'separator',

    // ── 格式组（text 有选区）──
    {
      id: 'format-strong',
      label: 'Bold',
      icon: ICONS.strong,
      isActive: (ctx) => editable(ctx) && isText(ctx) && ctx.hasSelection,
      run: () => commands.toggleStrong(),
    },
    {
      id: 'format-emphasis',
      label: 'Italic',
      icon: ICONS.emphasis,
      isActive: (ctx) => editable(ctx) && isText(ctx) && ctx.hasSelection,
      run: () => commands.toggleEmphasis(),
    },
    {
      id: 'format-strikethrough',
      label: 'Strikethrough',
      icon: ICONS.strikeThrough,
      isActive: (ctx) => editable(ctx) && isText(ctx) && ctx.hasSelection,
      run: () => commands.toggleStrikeThrough(),
    },
    {
      id: 'format-inline-code',
      label: 'Inline code',
      icon: ICONS.inlineCode,
      isActive: (ctx) => editable(ctx) && isText(ctx) && ctx.hasSelection,
      run: () => commands.toggleInlineCode(),
    },
    {
      id: 'convert',
      label: 'Convert to',
      isActive: (ctx) => editable(ctx) && isText(ctx),
      children: convertChildren(deps),
    },
    'separator',

    // ── 插入 ▸（text 光标态）──
    {
      id: 'insert',
      label: 'Insert',
      isActive: (ctx) => editable(ctx) && isText(ctx) && !ctx.hasSelection,
      children: [
        {
          id: 'insert-timestamp',
          label: 'Timestamp',
          icon: ICONS.timestamp,
          isActive: (ctx) => isText(ctx) && !ctx.hasSelection,
          run: () => commands.insertTimestamp(),
        },
        {
          id: 'insert-divider',
          label: 'Divider',
          icon: ICONS.hr,
          isActive: (ctx) => isText(ctx) && !ctx.hasSelection,
          run: () => commands.insertHr(),
        },
        {
          id: 'insert-table',
          label: 'Table',
          icon: ICONS.table,
          isActive: (ctx) => isText(ctx) && !ctx.hasSelection,
          run: () => commands.insertTable(),
        },
      ],
    },

    // ── 链接组 ──
    {
      id: 'link-open',
      label: 'Open Link',
      icon: MENU_ICONS.openLink,
      isActive: (ctx) => ctx.itemType === 'link',
      run: (ctx) => {
        if (ctx.linkHref) deps.openLink(ctx.linkHref)
      },
    },
    {
      id: 'link-copy',
      label: 'Copy Link',
      icon: MENU_ICONS.link,
      isActive: (ctx) => ctx.itemType === 'link',
      run: async (ctx) => {
        if (ctx.linkHref) await clipboard.write({ text: ctx.linkHref })
      },
    },
    {
      id: 'link-remove',
      label: 'Remove Link',
      icon: MENU_ICONS.unlink,
      isActive: (ctx) => editable(ctx) && ctx.itemType === 'link',
      run: (ctx) => deps.removeLink(ctx),
    },

    // ── 表格组（crepe 行列手柄已覆盖增删行列/对齐/拖拽，只补删除表格）──
    {
      id: 'table-delete',
      label: 'Delete Table',
      icon: MENU_ICONS.trash,
      danger: true,
      isActive: (ctx) => editable(ctx) && ctx.itemType === 'tableCell',
      run: () => deps.deleteTable(),
    },

    // ── 图片组（P2 完整版：替换/说明/复制/删除）──
    {
      id: 'image-replace',
      label: 'Replace Image...',
      icon: MENU_ICONS.imageReplace,
      isActive: (ctx) =>
        editable(ctx) && ctx.itemType === 'image' && ctx.imageUpload,
      run: (ctx) => {
        if (ctx.nodePos !== undefined) return deps.replaceImage(ctx.nodePos)
      },
    },
    {
      id: 'image-caption',
      label: 'Edit Caption',
      icon: MENU_ICONS.caption,
      isActive: (ctx) => editable(ctx) && ctx.itemType === 'image',
      run: (ctx) => {
        if (ctx.nodePos !== undefined) deps.editImageCaption(ctx.nodePos)
      },
    },
    {
      id: 'image-copy',
      label: 'Copy Image',
      icon: MENU_ICONS.image,
      isActive: (ctx) => ctx.itemType === 'image',
      run: (ctx) => {
        if (ctx.nodePos !== undefined) return deps.copyImage(ctx.nodePos)
      },
    },
    {
      id: 'image-delete',
      label: 'Delete',
      icon: MENU_ICONS.trash,
      danger: true,
      isActive: (ctx) => editable(ctx) && ctx.itemType === 'image',
      run: (ctx) => {
        if (ctx.nodePos !== undefined) deps.deleteNode(ctx.nodePos)
      },
    },

    // ── 代码块组 ──
    {
      id: 'code-copy',
      label: 'Copy Code',
      icon: MENU_ICONS.copyCode,
      isActive: (ctx) => ctx.itemType === 'codeBlock',
      run: (ctx) => deps.copyCodeBlock(ctx),
    },

    'separator',

    // ── AI 组（协议已就位；features.aiActions = true 时才渲染，P1 恒隐）──
    {
      id: 'ai-ask',
      label: 'Ask AI',
      icon: MENU_ICONS.ai,
      isActive: (ctx) => ctx.aiActions && isText(ctx) && ctx.hasSelection,
      run: (ctx) => deps.aiAction('ask', ctx),
    },
    {
      id: 'ai-edit',
      label: 'Edit with AI',
      icon: MENU_ICONS.ai,
      isActive: (ctx) => ctx.aiActions && isText(ctx) && ctx.hasSelection,
      run: (ctx) => deps.aiAction('edit', ctx),
    },
  ]
}

/** 过滤出当前上下文可见的项：剥掉不可见项与多余分隔线（开头/结尾/连续）。 */
export function filterMenuEntries(
  registry: MenuEntry[],
  ctx: MenuContext,
): MenuEntry[] {
  const visible = registry.filter(
    (entry) => entry === 'separator' || entry.isActive(ctx),
  )
  const out: MenuEntry[] = []
  for (const entry of visible) {
    if (entry === 'separator') {
      if (out.length === 0 || out[out.length - 1] === 'separator') continue
    }
    out.push(entry)
  }
  while (out.length > 0 && out[out.length - 1] === 'separator') out.pop()
  return out
}

/** 收集注册表全部 label（含子菜单），i18n 守护测试用。 */
export function collectLabels(registry: MenuEntry[]): string[] {
  const labels: string[] = []
  const walk = (entries: MenuEntry[]): void => {
    for (const entry of entries) {
      if (entry === 'separator') continue
      labels.push(entry.label)
      if (entry.children) walk(entry.children)
    }
  }
  walk(registry)
  return [...new Set(labels)]
}
