// Floating wikilink completion menu.
//
// State machine: when the user types `[[`, we begin a "session" that
// tracks the trigger range. Every time the document changes inside
// that range, we ask the host (via bridge `wikilinkQuery`) for
// matches, then render a menu next to the caret.
//
// Accept: ↵ inserts a wikilink atom for the highlighted item and
// removes the literal `[[prefix` text. ↑/↓ moves the highlight.
// Esc / clicking outside / typing `]]` ends the session.

import { $prose } from '@milkdown/kit/utils'
import { Plugin, PluginKey } from '@milkdown/kit/prose/state'
import type { EditorView } from '@milkdown/kit/prose/view'

import type { BridgeClient } from '../../bridge/client'
import { wikilinkSchema } from './node'

interface SuggestItem {
  slug: string
  title: string
}

interface SuggestState {
  // Document position right after the opening `[[`.
  triggerFrom: number
  prefix: string
  items: SuggestItem[]
  highlight: number
  loading: boolean
}

const KEY = new PluginKey<SuggestState | null>('kc-wikilink-suggest')

export function wikilinkSuggestPlugin(bridge: BridgeClient) {
  return $prose((ctx) => {
    void ctx // schema lookup happens lazily inside view to avoid stale type
    const menu = createMenuElement()

    return new Plugin<SuggestState | null>({
      key: KEY,
      state: {
        init: () => null,
        apply(tr, prev) {
          const meta = tr.getMeta(KEY) as
            | { reset?: boolean; patch?: Partial<SuggestState> }
            | undefined
          if (meta?.reset) return null
          const next = computeStateAfter(tr, prev)
          if (meta?.patch && next) Object.assign(next, meta.patch)
          return next
        },
      },
      view(view) {
        const root = view.dom.parentElement ?? view.dom
        root.appendChild(menu.element)

        let queryId = 0
        let debounceTimer: ReturnType<typeof setTimeout> | null = null

        const requestSuggestions = (prefix: string) => {
          const myId = ++queryId
          if (debounceTimer) clearTimeout(debounceTimer)
          debounceTimer = setTimeout(async () => {
            const reply = await bridge.requestWikilinkQuery({ prefix })
            if (myId !== queryId) return
            const items = (reply.items ?? []) as SuggestItem[]
            view.dispatch(
              view.state.tr.setMeta(KEY, {
                patch: { items, highlight: 0, loading: false },
              }),
            )
          }, 200)
        }

        const onMenuClick = (ev: MouseEvent) => {
          const target = ev.target as HTMLElement | null
          const row = target?.closest<HTMLElement>('.kc-wikilink-suggest__item')
          if (!row) return
          const idx = Number(row.dataset.index)
          const state = KEY.getState(view.state)
          if (!state || Number.isNaN(idx) || !state.items[idx]) return
          ev.preventDefault()
          ev.stopPropagation()
          acceptItem(view, state.items[idx])
        }
        menu.element.addEventListener('mousedown', onMenuClick)

        return {
          update(view) {
            const state = KEY.getState(view.state)
            if (!state) {
              menu.hide()
              return
            }
            if (menu.lastPrefix !== state.prefix) {
              menu.lastPrefix = state.prefix
              requestSuggestions(state.prefix)
              menu.setLoading(true)
            }
            menu.render(state)
            positionMenu(menu, view, state)
          },
          destroy() {
            menu.element.removeEventListener('mousedown', onMenuClick)
            if (debounceTimer) clearTimeout(debounceTimer)
            if (menu.element.parentElement) {
              menu.element.parentElement.removeChild(menu.element)
            }
          },
        }
      },
      props: {
        handleKeyDown(view, event) {
          const state = KEY.getState(view.state)
          if (!state) return false
          if (event.key === 'ArrowDown') {
            const next = Math.min(state.highlight + 1, state.items.length - 1)
            view.dispatch(view.state.tr.setMeta(KEY, {
              patch: { highlight: Math.max(0, next) },
            }))
            event.preventDefault()
            return true
          }
          if (event.key === 'ArrowUp') {
            const next = Math.max(state.highlight - 1, 0)
            view.dispatch(view.state.tr.setMeta(KEY, {
              patch: { highlight: next },
            }))
            event.preventDefault()
            return true
          }
          if (event.key === 'Enter' && state.items.length > 0) {
            acceptItem(view, state.items[state.highlight])
            event.preventDefault()
            return true
          }
          if (event.key === 'Escape') {
            dismiss(view)
            event.preventDefault()
            return true
          }
          return false
        },
      },
    })
  })
}

function computeStateAfter(
  tr: import('@milkdown/kit/prose/state').Transaction,
  prev: SuggestState | null,
): SuggestState | null {
  const sel = tr.selection
  if (!sel.empty) return null
  const $pos = sel.$from
  const blockStart = $pos.start()
  const text = tr.doc.textBetween(blockStart, $pos.pos, '\n', '\n')
  // Find the latest `[[` to the left of the caret on the same block.
  const triggerLocalIndex = text.lastIndexOf('[[')
  if (triggerLocalIndex < 0) return null
  // Reject if a `]]` was typed after the latest `[[` (range closed).
  if (text.slice(triggerLocalIndex).includes(']]')) return null
  // Reject if a newline appears in the prefix (multi-line trigger).
  const after = text.slice(triggerLocalIndex + 2)
  if (after.includes('\n')) return null
  const triggerFrom = blockStart + triggerLocalIndex + 2
  const prefix = after
  if (prev && prev.triggerFrom === triggerFrom && prev.prefix === prefix) {
    return prev
  }
  return {
    triggerFrom,
    prefix,
    items: prev?.triggerFrom === triggerFrom ? prev.items : [],
    highlight: 0,
    loading: true,
  }
}

function acceptItem(view: EditorView, item: SuggestItem) {
  const state = KEY.getState(view.state)
  if (!state) return
  const type = view.state.schema.nodes[wikilinkSchema.node.name]
  if (!type) return
  const from = state.triggerFrom - 2 // include the `[[`
  const to = view.state.selection.from
  const node = type.create({ slug: item.slug, alias: null })
  const tr = view.state.tr
    .replaceWith(from, to, node)
    .setMeta(KEY, { reset: true })
  view.dispatch(tr)
  view.focus()
}

function dismiss(view: EditorView) {
  view.dispatch(view.state.tr.setMeta(KEY, { reset: true }))
}

// ── DOM ──────────────────────────────────────────────────────────────

interface MenuHandle {
  element: HTMLElement
  list: HTMLElement
  lastPrefix: string | null
  hide(): void
  setLoading(loading: boolean): void
  render(state: SuggestState): void
}

function createMenuElement(): MenuHandle {
  const element = document.createElement('div')
  element.className = 'kc-wikilink-suggest'
  element.style.position = 'absolute'
  element.style.zIndex = '1000'
  element.style.minWidth = '220px'
  element.style.maxWidth = '320px'
  element.style.padding = '4px 0'
  element.style.background = 'var(--kc-editor-bg, #fff)'
  element.style.color = 'var(--kc-editor-fg, #1f2328)'
  element.style.border = '1px solid var(--kc-editor-border, #d0d7de)'
  element.style.borderRadius = '8px'
  element.style.boxShadow = '0 4px 12px rgba(0,0,0,0.15)'
  element.style.fontSize = '14px'
  element.style.display = 'none'

  const list = document.createElement('div')
  list.className = 'kc-wikilink-suggest__list'
  element.appendChild(list)

  return {
    element,
    list,
    lastPrefix: null,
    hide() {
      element.style.display = 'none'
    },
    setLoading(loading: boolean) {
      element.dataset.loading = loading ? '1' : '0'
    },
    render(state: SuggestState) {
      element.style.display = 'block'
      list.innerHTML = ''
      if (state.loading && state.items.length === 0) {
        const row = document.createElement('div')
        row.textContent = '搜索中…'
        row.style.padding = '6px 12px'
        row.style.color = 'var(--kc-editor-muted, #6e7781)'
        list.appendChild(row)
        return
      }
      if (state.items.length === 0) {
        const row = document.createElement('div')
        row.textContent = `没有匹配 “${state.prefix}”`
        row.style.padding = '6px 12px'
        row.style.color = 'var(--kc-editor-muted, #6e7781)'
        list.appendChild(row)
        return
      }
      state.items.forEach((it, i) => {
        const row = document.createElement('div')
        row.className = 'kc-wikilink-suggest__item'
        row.dataset.index = String(i)
        row.style.padding = '6px 12px'
        row.style.cursor = 'pointer'
        row.style.background =
          i === state.highlight
            ? 'var(--kc-editor-accent, #7c3aed)'
            : 'transparent'
        row.style.color =
          i === state.highlight ? '#fff' : 'inherit'
        const title = document.createElement('div')
        title.style.fontWeight = '500'
        title.textContent = it.title || it.slug
        row.appendChild(title)
        if (it.title && it.title !== it.slug) {
          const slug = document.createElement('div')
          slug.style.fontSize = '12px'
          slug.style.opacity = '0.7'
          slug.textContent = it.slug
          row.appendChild(slug)
        }
        list.appendChild(row)
      })
    },
  }
}

function positionMenu(
  menu: MenuHandle,
  view: EditorView,
  state: SuggestState,
) {
  try {
    const coords = view.coordsAtPos(state.triggerFrom)
    const root = menu.element.parentElement
    if (!root) return
    const rootRect = root.getBoundingClientRect()
    menu.element.style.left = `${coords.left - rootRect.left}px`
    menu.element.style.top = `${coords.bottom - rootRect.top + 4}px`
  } catch {
    menu.hide()
  }
}
