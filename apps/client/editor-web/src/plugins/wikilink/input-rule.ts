// Closing trigger: when the user finishes typing `[[slug|alias?]]`,
// transform the literal range into a real wikilink atom.

import { $inputRule } from '@milkdown/kit/utils'
import { InputRule } from '@milkdown/kit/prose/inputrules'

import { wikilinkSchema } from './node'

export const wikilinkInputRule = $inputRule((ctx) => {
  const type = wikilinkSchema.type(ctx)
  return new InputRule(
    /\[\[([^\]|\n]+?)(?:\|([^\]\n]+?))?\]\]$/,
    (state, match, start, end) => {
      const slug = match[1]?.trim() ?? ''
      if (!slug) return null
      const alias = match[2] ? match[2].trim() : null
      const node = type.create({ slug, alias })
      return state.tr.replaceWith(start, end, node)
    },
  )
})
