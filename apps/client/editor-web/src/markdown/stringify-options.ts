// Lock down remark-stringify options so Milkdown's markdown round-trip does
// not silently rewrite source style (e.g. `*x*` → `_x_`, ``` ``` ``` → `~~~`).
// Future Obsidian / git export depends on this stability.

export const STRINGIFY_OPTIONS = {
  bullet: '-',
  emphasis: '*',
  strong: '*',
  fence: '`',
  rule: '-',
  listItemIndent: 'one',
  resourceLink: false,
  tightDefinitions: true,
  incrementListMarker: false,
} as const
