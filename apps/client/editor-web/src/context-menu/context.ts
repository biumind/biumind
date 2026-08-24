// 上下文探测：右键事件坐标 → itemType + 上下文数据。
// posAtCoords 命中失败后对 image 用 posAtDOM 兜底（crepe image-block 的
// DOM 比文本节点复杂，坐标可能落在 img 的 padding/装饰层上）。

import type { EditorView } from 'prosemirror-view'

import type { MenuContext, MenuItemType } from './model'

const IMAGE_NODE_NAMES = new Set(['image', 'image-block'])
const TABLE_CELL_NODE_NAMES = new Set(['table_cell', 'table_header'])
const CODE_BLOCK_NODE_NAMES = new Set(['code_block'])

/** 沿 $pos 链向上找第一个匹配类型名的节点位置（before 深度）。 */
function findNodeOnChain(
  view: EditorView,
  pos: number,
  names: Set<string>,
): { nodePos: number } | null {
  const $pos = view.state.doc.resolve(pos)
  for (let depth = $pos.depth; depth >= 0; depth -= 1) {
    if (names.has($pos.node(depth).type.name)) {
      return { nodePos: $pos.before(depth) }
    }
  }
  return null
}

/** 取光标/选区处的 link mark href（有选区时扫选区，光标态看 pos 处节点）。 */
function linkHrefAt(view: EditorView, pos: number): string | undefined {
  const linkMark = view.state.schema.marks.link
  if (!linkMark) return undefined
  const { selection, doc } = view.state
  const { from, to, empty } = selection
  if (!empty && doc.rangeHasMark(from, to, linkMark)) {
    let href: string | undefined
    doc.nodesBetween(from, to, (node) => {
      if (href !== undefined) return false
      const mark = linkMark.isInSet(node.marks)
      if (mark) href = mark.attrs.href as string
      return true
    })
    if (href !== undefined) return href
  }
  const $pos = doc.resolve(pos)
  const mark =
    linkMark.isInSet($pos.marks()) ??
    linkMark.isInSet($pos.nodeAfter?.marks ?? []) ??
    linkMark.isInSet($pos.nodeBefore?.marks ?? [])
  return (mark?.attrs.href as string | undefined) ?? undefined
}

/** 图片兜底：elementFromPoint 命中 .milkdown-image-block / img 时 posAtDOM。 */
function imagePosFromDom(
  view: EditorView,
  coords: { x: number; y: number },
): number | null {
  const el = document
    .elementFromPoint(coords.x, coords.y)
    ?.closest('.milkdown-image-block, .milkdown-image-inline, img')
  if (!el || !view.dom.contains(el)) return null
  try {
    const pos = view.posAtDOM(el, 0)
    const found = findNodeOnChain(view, pos, IMAGE_NODE_NAMES)
    if (found) return found.nodePos
    // posAtDOM 可能落在图片节点之后（兄弟文本里），向前一探
    if (pos > 0) {
      const before = findNodeOnChain(view, pos - 1, IMAGE_NODE_NAMES)
      if (before) return before.nodePos
    }
  } catch {
    // posAtDOM 对非编辑器 DOM 会抛 —— 视为未命中
  }
  return null
}

/**
 * 探测右键位置的菜单上下文。命中编辑器外（posAtCoords 返回 null）返回 null，
 * 调用方不弹菜单。
 */
export function detectMenuContext(
  view: EditorView,
  coords: { x: number; y: number },
  base: Pick<MenuContext, 'readOnly' | 'canPaste' | 'aiActions' | 'imageUpload'>,
): MenuContext | null {
  const { selection } = view.state
  const from = selection.from
  const to = selection.to
  const shared = {
    ...base,
    from,
    to,
    hasSelection: !selection.empty,
  }

  // 右键点在选区内 → 保持选区；点在选区外 → PM 已把光标移过去（mousedown 先于
  // contextmenu），selection 已是新位置，直接用即可。

  const pos = view.posAtCoords({ left: coords.x, top: coords.y })

  // 图片优先（atom 节点，链上查不到时 DOM 兜底）
  if (pos) {
    const image = findNodeOnChain(view, pos.pos, IMAGE_NODE_NAMES)
    if (image) return { ...shared, itemType: 'image', nodePos: image.nodePos }
  } else {
    const nodePos = imagePosFromDom(view, coords)
    if (nodePos !== null) {
      return { ...shared, itemType: 'image', nodePos }
    }
    return null
  }
  // posAtCoords 命中但图片链没找到，也走一次 DOM 兜底（命中装饰层的情形）
  const imageFallback = imagePosFromDom(view, coords)
  if (imageFallback !== null) {
    return { ...shared, itemType: 'image', nodePos: imageFallback }
  }

  const tableCell = findNodeOnChain(view, pos.pos, TABLE_CELL_NODE_NAMES)
  if (tableCell) return { ...shared, itemType: 'tableCell' }

  const codeBlock = findNodeOnChain(view, pos.pos, CODE_BLOCK_NODE_NAMES)
  if (codeBlock) return { ...shared, itemType: 'codeBlock' }

  const href = linkHrefAt(view, pos.pos)
  const itemType: MenuItemType = href !== undefined ? 'link' : 'text'
  return { ...shared, itemType, linkHref: href }
}
