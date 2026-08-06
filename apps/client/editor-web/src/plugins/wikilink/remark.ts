// Tiny remark plugin: walk the mdast and split `[[slug|alias?]]` out of
// any text node into a synthetic `wikilink` mdast node.
//
// We deliberately do this as an mdast post-processor rather than a
// micromark/remark inline extension — the `[[…]]` syntax is simple
// enough that a regex pass is unambiguous, and we avoid pulling
// micromark-extension-* into the bundle.

import type { Plugin } from 'unified'
import type { Parent, PhrasingContent, Root, Text } from 'mdast'

const PATTERN = /\[\[([^\]|\n]+?)(?:\|([^\]\n]+?))?\]\]/g

interface WikilinkNode {
  type: 'wikilink'
  data: { slug: string; alias: string | null }
  // remark needs *some* visible representation — children: [] keeps it
  // an mdast leaf, which Milkdown then maps to a ProseMirror atom.
  children: []
}

declare module 'mdast' {
  interface PhrasingContentMap {
    wikilink: WikilinkNode
  }
  interface RootContentMap {
    wikilink: WikilinkNode
  }
}

export const remarkWikilink: Plugin<[], Root> = () => {
  return (tree) => {
    visit(tree, (node, index, parent) => {
      if (!parent || index == null) return
      if (node.type !== 'text') return
      const text = (node as Text).value
      if (!text.includes('[[')) return
      const replacement = splitTextWithWikilinks(text)
      if (replacement.length === 1 && replacement[0].type === 'text') return
      const arr = (parent as Parent).children as PhrasingContent[]
      arr.splice(index, 1, ...(replacement as PhrasingContent[]))
    })
  }
}

function splitTextWithWikilinks(text: string): PhrasingContent[] {
  const out: PhrasingContent[] = []
  let lastIndex = 0
  PATTERN.lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = PATTERN.exec(text)) !== null) {
    if (match.index > lastIndex) {
      out.push({ type: 'text', value: text.slice(lastIndex, match.index) })
    }
    out.push({
      type: 'wikilink',
      data: {
        slug: match[1].trim(),
        alias: match[2] ? match[2].trim() : null,
      },
      children: [],
    } as WikilinkNode)
    lastIndex = match.index + match[0].length
  }
  if (lastIndex === 0) return [{ type: 'text', value: text }]
  if (lastIndex < text.length) {
    out.push({ type: 'text', value: text.slice(lastIndex) })
  }
  return out
}

// Lightweight mdast visitor — avoids depending on `unist-util-visit`.
type Visitor = (node: PhrasingContent | Root, index: number | null, parent: Parent | null) => void

function visit(node: Root | PhrasingContent | Parent, callback: Visitor): void {
  callback(node as PhrasingContent | Root, null, null)
  walk(node as Parent, callback)
}

function walk(parent: Parent, callback: Visitor): void {
  if (!('children' in parent) || !Array.isArray(parent.children)) return
  // Iterate descending so splice during iteration is safe.
  for (let i = parent.children.length - 1; i >= 0; i--) {
    const child = parent.children[i]
    if ('children' in child && Array.isArray(child.children)) {
      walk(child as Parent, callback)
    }
    callback(child as PhrasingContent, i, parent)
  }
}
