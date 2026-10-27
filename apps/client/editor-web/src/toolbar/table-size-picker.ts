// 表格尺寸选择器：插入表格时弹出的网格浮层（Notion/WordPress 式）。
// hover 预览 N×M 高亮区，点击落格回调 onPick(row, col)。
// 触屏无 hover：tap 落格即选即插（click 事件鼠标/触屏同路径）。
// 选区保持：容器 pointerdown preventDefault（与工具栏按钮同模式），
// 交互全程不 focus 任何元素，PM 选区不失。

import './table-size-picker.css'

export interface TableSizePicker {
  readonly element: HTMLElement
  readonly isOpen: boolean
  /** 以按钮等元素为锚打开（下方优先，空间不足翻转上方） */
  openAt: (anchor: HTMLElement) => void
  /** 以视口坐标为锚打开（右键菜单路径：菜单已关闭，用原菜单位置） */
  openAtPoint: (point: { x: number; y: number }) => void
  close: () => void
  /** setOptions.locale 推送时换文案 */
  setTranslator: (t: (s: string) => string) => void
  destroy: () => void
}

interface PickerOptions {
  onPick: (row: number, col: number) => void
  t: (s: string) => string
}

const MAX_ROWS = 8
const MAX_COLS = 10
/** 浮层与锚点/屏幕边缘的间距 */
const GAP = 6

export function createTableSizePicker(opts: PickerOptions): TableSizePicker {
  let t = opts.t
  let open = false
  /** 0 基；null = 无高亮 */
  let hover: { row: number; col: number } | null = null

  const element = document.createElement('div')
  element.className = 'kc-tsp'
  element.setAttribute('role', 'menu')
  element.setAttribute('aria-label', 'table size')

  const grid = document.createElement('div')
  grid.className = 'kc-tsp-grid'
  const cells: HTMLButtonElement[] = []
  for (let r = 0; r < MAX_ROWS; r += 1) {
    for (let c = 0; c < MAX_COLS; c += 1) {
      const cell = document.createElement('button')
      cell.type = 'button'
      cell.className = 'kc-tsp-cell'
      cell.dataset.row = String(r)
      cell.dataset.col = String(c)
      // pointerover 而非 mouseover：不入子元素反复触发；触屏不产 hover
      cell.addEventListener('pointerover', (event) => {
        if (event.pointerType !== 'mouse') return
        setHover(r, c)
      })
      cell.addEventListener('click', () => {
        opts.onPick(r + 1, c + 1)
        close()
      })
      grid.appendChild(cell)
      cells.push(cell)
    }
  }

  const label = document.createElement('div')
  label.className = 'kc-tsp-label'

  element.appendChild(grid)
  element.appendChild(label)
  refreshLabel()

  const setHover = (row: number, col: number): void => {
    hover = { row, col }
    for (const cell of cells) {
      const r = Number(cell.dataset.row)
      const c = Number(cell.dataset.col)
      cell.classList.toggle('kc-tsp-on', r <= row && c <= col)
    }
    refreshLabel()
  }

  function refreshLabel(): void {
    label.textContent = hover
      ? `${hover.col + 1} × ${hover.row + 1} ${t('Table')}`
      : `${t('Table')}`
  }

  // 容器级阻止默认行为：点击格子不抢焦点、不丢 PM 选区
  element.addEventListener('pointerdown', (event) => event.preventDefault())

  const onDocPointerDown = (event: PointerEvent): void => {
    const target = event.target as HTMLElement | null
    if (target && element.contains(target)) return
    close()
  }

  const onDocKeyDown = (event: KeyboardEvent): void => {
    if (event.key === 'Escape') {
      close()
      event.stopPropagation()
    }
  }

  function place(x: number, y: number, anchorHeight: number): void {
    // 先显示才能量尺寸；藏在意屏幕外避免闪烁
    element.style.visibility = 'hidden'
    element.style.display = 'block'
    const width = element.offsetWidth
    const height = element.offsetHeight
    let left = Math.min(Math.max(4, x), window.innerWidth - width - 4)
    let top = y + GAP
    // 下方放不下 → 翻到锚点上方（工具栏在底部时的常见形态）
    if (top + height > window.innerHeight - 4) {
      top = y - anchorHeight - GAP - height
    }
    top = Math.max(4, top)
    element.style.left = `${left}px`
    element.style.top = `${top}px`
    element.style.visibility = 'visible'
  }

  function openCommon(x: number, y: number, anchorHeight: number): void {
    if (open) {
      close()
      return
    }
    open = true
    hover = null
    for (const cell of cells) cell.classList.remove('kc-tsp-on')
    refreshLabel()
    document.addEventListener('pointerdown', onDocPointerDown, true)
    document.addEventListener('keydown', onDocKeyDown, true)
    place(x, y, anchorHeight)
  }

  function close(): void {
    if (!open) return
    open = false
    element.style.display = 'none'
    document.removeEventListener('pointerdown', onDocPointerDown, true)
    document.removeEventListener('keydown', onDocKeyDown, true)
  }

  return {
    element,
    get isOpen() {
      return open
    },
    openAt(anchor) {
      const rect = anchor.getBoundingClientRect()
      openCommon(rect.left, rect.bottom, rect.height)
    },
    openAtPoint(point) {
      openCommon(point.x, point.y, 0)
    },
    close,
    setTranslator(next) {
      t = next
      refreshLabel()
    },
    destroy() {
      close()
      element.remove()
    },
  }
}
