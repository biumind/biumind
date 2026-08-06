// Click handling for the wikilink atom: tells the host to navigate.

import { $prose } from '@milkdown/kit/utils'
import { Plugin, PluginKey } from '@milkdown/kit/prose/state'

import type { BridgeClient } from '../../bridge/client'

export function wikilinkClickPlugin(bridge: BridgeClient) {
  return $prose(() => {
    return new Plugin({
      key: new PluginKey('kc-wikilink-click'),
      props: {
        handleClickOn(_view, _pos, node, _nodePos, event) {
          if (node.type.name !== 'wikilink') return false
          event.preventDefault()
          const slug = (node.attrs as { slug: string }).slug
          bridge.sendNavigate({ kind: 'wikilink', slug })
          return true
        },
      },
    })
  })
}
