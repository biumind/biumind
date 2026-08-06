// 格式字符隐藏：`## `、`` ` ``、`**`、`*`、`~~` → Decoration.replace({})。
// 粗/斜/删除线的视觉由 HighlightStyle（tags.strong/emphasis/strikethrough）
// 与 decorations 的 class 装饰承担，这里只负责「藏标记」。
// reveal：HeaderMark 用 'line'（光标同行即露），其余 'active'
// （光标/选区进入标记所在父节点即露）。

import { Decoration } from '@codemirror/view'
import { inlineReplacement } from './inline-replace'

const hidden = (): Decoration => Decoration.replace({})

export function formatMarksExtension(): ReturnType<typeof inlineReplacement>[] {
  return [
    // 标题标记连后随一个空格一起隐藏（`## ` 整段）
    inlineReplacement({
      nodeNames: ['HeaderMark'],
      create: hidden,
      range: (node, state) => {
        let to = node.to
        if (state.sliceDoc(to, to + 1) === ' ') to += 1
        return [node.from, to]
      },
      reveal: 'line',
    }),
    // 行内 code 反引号 + 围栏代码块的 ``` 标记；CodeInfo（```js 的 js）
    // 一并隐藏，避免父标记隐藏后留下孤儿 info 文本
    inlineReplacement({
      nodeNames: ['CodeMark', 'CodeInfo'],
      create: hidden,
      reveal: 'active',
    }),
    inlineReplacement({
      nodeNames: ['EmphasisMark', 'StrikethroughMark'],
      create: hidden,
      reveal: 'active',
    }),
  ]
}
