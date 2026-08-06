// Mermaid plugin bundle. Currently just the live-preview ProseMirror
// plugin; in the future we may add a slash-menu shortcut to insert a
// pre-filled mermaid block.

import type { MilkdownPlugin } from '@milkdown/kit/ctx'

import { mermaidPreviewPlugin } from './preview'

export function mermaidPlugins(): MilkdownPlugin[] {
  return [mermaidPreviewPlugin].flat() as MilkdownPlugin[]
}
