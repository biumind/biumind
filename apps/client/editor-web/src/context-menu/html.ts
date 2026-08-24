// 选区 HTML 序列化（P2 双格式复制）：DOMSerializer 把 PM slice 渲染成
// DOM，包临时 div 取 innerHTML。图片 <img src> 此时已是 proxyDomURL 换出
// 的 presigned URL（序列化走 schema 的 toDOM，与渲染同源），外部应用
// 粘贴时可直接加载（URL 15 分钟 TTL，超时裂开可接受）。

import { DOMSerializer, type Fragment, type Schema } from 'prosemirror-model'

export function fragmentToHtml(schema: Schema, fragment: Fragment): string {
  const div = document.createElement('div')
  div.appendChild(
    DOMSerializer.fromSchema(schema).serializeFragment(fragment),
  )
  return div.innerHTML
}
