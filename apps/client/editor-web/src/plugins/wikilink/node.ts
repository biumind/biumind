// Wikilink inline node — an atomic, inline ProseMirror node that
// renders `[[slug|alias?]]` and round-trips through markdown losslessly.
//
// The node is *atomic* (the user cannot place the caret inside the
// wikilink; clicking selects the whole node). Click handling is wired
// up by the plugin in ./suggest.ts via a ProseMirror plugin so the
// host can be told to navigate.

import { $nodeAttr, $nodeSchema } from '@milkdown/kit/utils'

const SLUG = 'slug'
const ALIAS = 'alias'

export const wikilinkAttr = $nodeAttr('wikilink', (node) => ({
  'data-type': 'wikilink',
  'data-slug': node.attrs[SLUG] as string,
  'data-alias': (node.attrs[ALIAS] as string | null) ?? '',
  class: 'kc-wikilink',
}))

export const wikilinkSchema = $nodeSchema('wikilink', (ctx) => ({
  inline: true,
  group: 'inline',
  atom: true,
  attrs: {
    [SLUG]: { validate: 'string' },
    [ALIAS]: { default: null, validate: 'string|null' },
  },
  parseDOM: [
    {
      tag: 'a[data-type="wikilink"]',
      getAttrs: (dom) => {
        if (!(dom instanceof HTMLElement)) return false
        return {
          [SLUG]: dom.getAttribute('data-slug') ?? '',
          [ALIAS]: dom.getAttribute('data-alias') || null,
        }
      },
    },
  ],
  toDOM: (node) => {
    const slug = node.attrs[SLUG] as string
    const alias = (node.attrs[ALIAS] as string | null) ?? null
    return [
      'a',
      ctx.get(wikilinkAttr.key)(node),
      alias && alias.length > 0 ? alias : slug,
    ]
  },
  // Markdown round-trip: emit `[[slug]]` or `[[slug|alias]]` as inline
  // text. We do NOT serialize as a regular Markdown link — that would
  // require remark to parse `[[...]]` back as a link mark, which it
  // doesn't. The matching micromark extension lives in ./remark.ts.
  parseMarkdown: {
    match: ({ type }) => type === 'wikilink',
    runner: (state, node, type) => {
      state.addNode(type, {
        [SLUG]: (node.data as { slug?: string } | undefined)?.slug ?? '',
        [ALIAS]: (node.data as { alias?: string | null } | undefined)
            ?.alias ?? null,
      })
    },
  },
  toMarkdown: {
    match: (node) => node.type.name === 'wikilink',
    runner: (state, node) => {
      const slug = node.attrs[SLUG] as string
      const alias = (node.attrs[ALIAS] as string | null) ?? null
      const literal = alias && alias.length > 0
        ? `[[${slug}|${alias}]]`
        : `[[${slug}]]`
      state.addNode('text', undefined, literal)
    },
  },
}))
