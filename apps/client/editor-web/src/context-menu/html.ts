// 选区 HTML 序列化（P2 双格式复制）：DOMSerializer 把 PM slice 渲染成
// DOM，包临时 div 取 innerHTML。注意：schema toDOM 用的是节点原始 attrs，
// 图片 src 仍是 biu-file:// 规范 URI —— 外部应用加载不了，由调用方
// （copySelection）序列化后再把 biu-file:// 批量换成 presigned URL。

import { DOMSerializer, type Fragment, type Schema } from 'prosemirror-model'

export function fragmentToHtml(schema: Schema, fragment: Fragment): string {
  const div = document.createElement('div')
  div.appendChild(
    DOMSerializer.fromSchema(schema).serializeFragment(fragment),
  )
  return div.innerHTML
}
