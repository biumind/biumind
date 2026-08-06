// Wikilink plugin bundle: schema + remark parser + input rule + click +
// completion suggest. Two pieces:
//   * `wikilinkPlugins(bridge)` — array of MilkdownPlugin values
//     (schema, attr, inputrule, prose plugins) to pass to `.use(...)`.
//   * `applyWikilinkRemark(ctx)` — register the remark transformer in
//     the editor config callback so existing `[[...]]` in source
//     markdown is parsed into wikilink mdast nodes on load.

import type { MilkdownPlugin } from '@milkdown/kit/ctx'
import type { Ctx } from '@milkdown/kit/ctx'
import { remarkPluginsCtx } from '@milkdown/kit/core'

import type { BridgeClient } from '../../bridge/client'

import { wikilinkClickPlugin } from './click-handler'
import { wikilinkInputRule } from './input-rule'
import { remarkWikilink } from './remark'
import { wikilinkAttr, wikilinkSchema } from './node'
import { wikilinkSuggestPlugin } from './suggest'

export function wikilinkPlugins(bridge: BridgeClient): MilkdownPlugin[] {
  // $nodeAttr / $nodeSchema / $inputRule / $prose all return either a
  // single MilkdownPlugin or a tuple-like array of plugins; flatten so
  // the caller can `.use(plugins)` in one shot.
  return [
    wikilinkAttr,
    wikilinkSchema,
    wikilinkInputRule,
    wikilinkClickPlugin(bridge),
    wikilinkSuggestPlugin(bridge),
  ].flat() as MilkdownPlugin[]
}

export function applyWikilinkRemark(ctx: Ctx): void {
  ctx.update(remarkPluginsCtx, (plugins) => [
    ...plugins,
    remarkWikilink as unknown as (typeof plugins)[number],
  ])
}
