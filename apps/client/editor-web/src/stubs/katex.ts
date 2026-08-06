// 构建期 stub：Crepe 的 esm bundle 顶部静态 `import katex from 'katex'`，
// 仅靠运行时 `features: { latex: false }` 无法让 Rollup 把 KaTeX (~586KB)
// tree-shake 出主 chunk。latex feature 已禁用，这些函数运行时永远不会被调用。
// vite.config.ts 里通过 resolve.alias 把 'katex' 指到这里。
function disabled(): never {
  throw new Error('katex is disabled (Crepe latex feature is turned off)')
}

export default {
  render: disabled,
  renderToString: disabled,
}
