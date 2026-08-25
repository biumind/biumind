// 常驻格式工具栏：渲染在 Crepe 容器上方。按钮动作全部由 main.ts 注入，
// 本模块只负责 DOM、禁用态、选中态和源码模式态。
// 图标为内联 SVG / 文本符号，currentColor 跟随主题变量，不引新依赖。

import './toolbar.css'
import { INACTIVE_STATE, type ToolbarActiveState } from './types'

export interface ToolbarOptions {
  /** CM6 内核本身即源码编辑器，笔记页隐藏「源码切换」按钮 */
  hideSourceToggle?: boolean
  /**
   * 移动端浮动模式（F2）：position:fixed 贴可见视口底部（键盘上沿），
   * 显隐/定位由 FloatingToolbarController 驱动；按钮定义/动作/禁用态/
   * 选中态/源码模式逻辑与桌面完全同源。缺省 false = 顶部常驻（桌面现状）。
   */
  floating?: boolean
}

export interface ToolbarActions {
  undo: () => void
  redo: () => void
  toggleStrong: () => void
  toggleEmphasis: () => void
  toggleStrikeThrough: () => void
  toggleInlineCode: () => void
  wrapInHeading: (level: 1 | 2 | 3) => void
  wrapInBlockquote: () => void
  insertHr: () => void
  wrapInBulletList: () => void
  wrapInOrderedList: () => void
  toggleTaskList: () => void
  createCodeBlock: () => void
  insertTable: () => void
  insertTimestamp: () => void
  toggleSourceMode: () => void
}

export interface Toolbar {
  element: HTMLElement
  /** readOnly 时整体禁用 */
  setDisabled: (disabled: boolean) => void
  /** 光标格式上下文变化时刷新选中态高亮 */
  setActive: (active: ToolbarActiveState) => void
  /** 源码模式下除切换按钮外全部禁用，切换按钮高亮 */
  setSourceMode: (active: boolean) => void
}

export const svg = (body: string): string =>
  `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`

const textIcon = (html: string, cls = ''): string =>
  `<span class="kc-tb-text ${cls}" aria-hidden="true">${html}</span>`

// 导出供右键菜单复用（格式/转换/插入组用同款图标，见 context-menu/model.ts）
export const ICONS = {
  undo: svg('<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11"/>'),
  redo: svg('<path d="m15 14 5-5-5-5"/><path d="M20 9H9.5a5.5 5.5 0 0 0 0 11H13"/>'),
  strong: textIcon('<b>B</b>'),
  emphasis: textIcon('<i>I</i>'),
  strikeThrough: textIcon('<s>S</s>'),
  inlineCode: textIcon('&lt;/&gt;', 'kc-tb-mono'),
  h1: textIcon('H1', 'kc-tb-heading'),
  h2: textIcon('H2', 'kc-tb-heading'),
  h3: textIcon('H3', 'kc-tb-heading'),
  blockquote: textIcon('&#10077;'),
  hr: svg('<line x1="4" y1="12" x2="20" y2="12"/>'),
  bulletList: svg(
    '<line x1="9" y1="6" x2="20" y2="6"/><line x1="9" y1="12" x2="20" y2="12"/><line x1="9" y1="18" x2="20" y2="18"/>' +
      '<circle cx="5" cy="6" r="1.2" fill="currentColor" stroke="none"/><circle cx="5" cy="12" r="1.2" fill="currentColor" stroke="none"/><circle cx="5" cy="18" r="1.2" fill="currentColor" stroke="none"/>',
  ),
  orderedList: svg(
    '<line x1="10" y1="6" x2="20" y2="6"/><line x1="10" y1="12" x2="20" y2="12"/><line x1="10" y1="18" x2="20" y2="18"/>' +
      '<text x="4" y="8" font-size="7" fill="currentColor" stroke="none">1</text><text x="4" y="14" font-size="7" fill="currentColor" stroke="none">2</text><text x="4" y="20" font-size="7" fill="currentColor" stroke="none">3</text>',
  ),
  taskList: svg(
    '<rect x="3" y="5" width="7" height="7" rx="1"/><polyline points="5.5 8.5 6.8 9.8 9 6.8"/>' +
      '<line x1="13" y1="8.5" x2="20" y2="8.5"/><line x1="3" y1="17" x2="20" y2="17"/>',
  ),
  codeBlock: svg(
    '<rect x="3" y="4" width="18" height="16" rx="2"/><polyline points="10 9.5 8 12l2 2.5"/><polyline points="14 9.5 16 12l-2 2.5"/>',
  ),
  table: svg(
    '<rect x="3" y="5" width="18" height="14" rx="1"/><line x1="3" y1="10" x2="21" y2="10"/><line x1="3" y1="15" x2="21" y2="15"/><line x1="12" y1="5" x2="12" y2="19"/>',
  ),
  timestamp: svg('<circle cx="12" cy="12" r="8.5"/><polyline points="12 7 12 12 15 14"/>'),
  source: svg('<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>'),
} as const

interface ButtonDef {
  key: string
  title: string
  icon: string
  run: () => void
  /** 对应 ToolbarActiveState 的字段，用于选中态高亮 */
  activeKey?: keyof ToolbarActiveState
}

/** 工具栏布局：分组数组，组间渲染分隔线；null 表示弹性间隔（源码按钮靠右） */
type ToolbarItem = ButtonDef[] | null

export function createToolbar(
  actions: ToolbarActions,
  options: ToolbarOptions = {},
): Toolbar {
  const groups: ToolbarItem[] = [
    [
      { key: 'undo', title: '撤销', icon: ICONS.undo, run: actions.undo },
      { key: 'redo', title: '重做', icon: ICONS.redo, run: actions.redo },
    ],
    [
      { key: 'strong', title: '加粗', icon: ICONS.strong, run: actions.toggleStrong, activeKey: 'strong' },
      { key: 'emphasis', title: '斜体', icon: ICONS.emphasis, run: actions.toggleEmphasis, activeKey: 'emphasis' },
      { key: 'strikeThrough', title: '删除线', icon: ICONS.strikeThrough, run: actions.toggleStrikeThrough, activeKey: 'strikeThrough' },
      { key: 'inlineCode', title: '行内代码', icon: ICONS.inlineCode, run: actions.toggleInlineCode, activeKey: 'inlineCode' },
    ],
    [
      { key: 'h1', title: '标题 1', icon: ICONS.h1, run: () => actions.wrapInHeading(1), activeKey: 'h1' },
      { key: 'h2', title: '标题 2', icon: ICONS.h2, run: () => actions.wrapInHeading(2), activeKey: 'h2' },
      { key: 'h3', title: '标题 3', icon: ICONS.h3, run: () => actions.wrapInHeading(3), activeKey: 'h3' },
    ],
    [
      { key: 'blockquote', title: '引用块', icon: ICONS.blockquote, run: actions.wrapInBlockquote, activeKey: 'blockquote' },
      { key: 'hr', title: '分割线', icon: ICONS.hr, run: actions.insertHr },
    ],
    [
      { key: 'bulletList', title: '无序列表', icon: ICONS.bulletList, run: actions.wrapInBulletList, activeKey: 'bulletList' },
      { key: 'orderedList', title: '有序列表', icon: ICONS.orderedList, run: actions.wrapInOrderedList, activeKey: 'orderedList' },
      { key: 'taskList', title: '任务列表', icon: ICONS.taskList, run: actions.toggleTaskList, activeKey: 'taskList' },
    ],
    [
      { key: 'codeBlock', title: '代码块', icon: ICONS.codeBlock, run: actions.createCodeBlock, activeKey: 'codeBlock' },
      { key: 'table', title: '插入表格', icon: ICONS.table, run: actions.insertTable },
    ],
    [
      { key: 'timestamp', title: '插入时间戳', icon: ICONS.timestamp, run: actions.insertTimestamp },
    ],
    // CM6 内核本身即源码编辑器：不渲染尾部弹性间隔与「源码切换」按钮
    ...(options.hideSourceToggle
      ? []
      : [
          null,
          [
            { key: 'source', title: '源码模式', icon: ICONS.source, run: actions.toggleSourceMode },
          ] as ButtonDef[],
        ]),
  ]

  const element = document.createElement('div')
  element.className = options.floating
    ? 'kc-toolbar kc-toolbar-floating'
    : 'kc-toolbar'
  element.setAttribute('role', 'toolbar')
  if (options.floating) {
    // 浮动条整条 pointerdown preventDefault 保焦点（touch 等价路径，
    // 与 M1 选区工具条一致；桌面按钮级 mousedown 路径不动）
    element.addEventListener('pointerdown', (event) => event.preventDefault())
  }

  const buttons: { def: ButtonDef; el: HTMLButtonElement }[] = []
  for (const item of groups) {
    if (item === null) {
      const spacer = document.createElement('span')
      spacer.className = 'kc-tb-spacer'
      element.appendChild(spacer)
      continue
    }
    for (const def of item) {
      const btn = document.createElement('button')
      btn.type = 'button'
      btn.className = 'kc-tb-btn'
      btn.dataset.key = def.key
      btn.title = def.title
      btn.setAttribute('aria-label', def.title)
      btn.innerHTML = def.icon
      // mousedown 阻止默认行为，避免点击按钮时编辑器失焦丢选区
      btn.addEventListener('mousedown', (event) => event.preventDefault())
      btn.addEventListener('click', () => def.run())
      element.appendChild(btn)
      buttons.push({ def, el: btn })
    }
    const sep = document.createElement('span')
    sep.className = 'kc-tb-sep'
    element.appendChild(sep)
  }

  let disabled = false
  let sourceActive = false
  let activeState: ToolbarActiveState = { ...INACTIVE_STATE }

  const refresh = (): void => {
    for (const { def, el } of buttons) {
      const isSourceBtn = def.key === 'source'
      el.disabled = disabled || (sourceActive && !isSourceBtn)
      const highlighted =
        (isSourceBtn && sourceActive) ||
        (!!def.activeKey && activeState[def.activeKey])
      el.classList.toggle('kc-tb-active', highlighted)
    }
  }

  refresh()

  return {
    element,
    setDisabled(value) {
      disabled = value
      refresh()
    },
    setActive(value) {
      activeState = value
      refresh()
    },
    setSourceMode(value) {
      sourceActive = value
      refresh()
    },
  }
}

export type { ToolbarActiveState }
