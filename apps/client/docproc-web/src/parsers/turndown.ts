// 共享的 Turndown 配置：DOCX / HTML 两条路径的 HTML→Markdown 行为一致。

import TurndownService from 'turndown'

export function createTurndown(): TurndownService {
  return new TurndownService({
    headingStyle: 'atx',
    codeBlockStyle: 'fenced',
    bulletListMarker: '-',
  })
}
