import { describe, expect, it } from 'vitest'
import { unified } from 'unified'
import remarkParse from 'remark-parse'

import { remarkWikilink } from '../src/plugins/wikilink/remark'

function parse(input: string): unknown {
  return unified().use(remarkParse).use(remarkWikilink).parse(input)
}

interface Node {
  type: string
  children?: Node[]
  value?: string
  data?: { slug?: string; alias?: string | null }
}

function findNodes(tree: Node, type: string): Node[] {
  const out: Node[] = []
  const walk = (n: Node): void => {
    if (n.type === type) out.push(n)
    n.children?.forEach(walk)
  }
  walk(tree)
  return out
}

describe('remarkWikilink', () => {
  it('parses [[slug]] inside a paragraph', () => {
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('Hello [[world]] here.') as never) as Node
    const links = findNodes(tree, 'wikilink')
    expect(links).toHaveLength(1)
    expect(links[0].data?.slug).toBe('world')
    expect(links[0].data?.alias).toBeNull()
  })

  it('parses [[slug|alias]] preserving alias', () => {
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('Visit [[my-slug|My Page]] today.') as never) as Node
    const links = findNodes(tree, 'wikilink')
    expect(links).toHaveLength(1)
    expect(links[0].data?.slug).toBe('my-slug')
    expect(links[0].data?.alias).toBe('My Page')
  })

  it('extracts multiple wikilinks in one paragraph', () => {
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('See [[one]] and [[two|Two!]].') as never) as Node
    const links = findNodes(tree, 'wikilink')
    expect(links.map((n) => n.data?.slug)).toEqual(['one', 'two'])
    expect(links[1].data?.alias).toBe('Two!')
  })

  it('leaves text without wikilinks unchanged', () => {
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('Just plain text here, no wikilinks.') as never) as Node
    expect(findNodes(tree, 'wikilink')).toHaveLength(0)
  })

  it('does not split across paragraph boundaries (no multi-line span)', () => {
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('Line one with [[start\n\nLine two ends]]') as never) as Node
    expect(findNodes(tree, 'wikilink')).toHaveLength(0)
  })

  it('handles wikilinks inside list items', () => {
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('- item [[a]]\n- item [[b|Bee]]\n') as never) as Node
    const links = findNodes(tree, 'wikilink')
    expect(links.map((n) => n.data?.slug)).toEqual(['a', 'b'])
  })

  it('does not match an unclosed [[slug', () => {
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('Unclosed [[slug here') as never) as Node
    expect(findNodes(tree, 'wikilink')).toHaveLength(0)
  })

  it('skips empty slugs like [[]]', () => {
    // Regex requires at least one non-pipe non-bracket character.
    const tree = unified()
      .use(remarkParse)
      .use(remarkWikilink)
      .runSync(parse('Empty [[]] here.') as never) as Node
    expect(findNodes(tree, 'wikilink')).toHaveLength(0)
  })
})
