// lib/markdown.ts — 轻量 Markdown → rich-text nodes 解析器.
//
// 为什么自己写: 微信 <rich-text> 只接受白名单 HTML 节点 + inline style
// (scoped CSS 注入不进 rich-text), npm 上 marked / markdown-it 体积大
// 且产 HTML 后还得二次清洗. 自己实现 ~400 行覆盖 chat 流式 99% 场景.
//
// 支持:
//   Block 级 — 代码块(含语言标签 + 流式未闭合)、标题 h1-h6、无序/有序列表、
//             引用块(多行)、表格(GFM 含对齐)、水平线、段落
//   Inline 级 — 行内代码、加粗、斜体、删除线、链接、图片
//   其他 — HTML 实体转义 (& < > 等), 防 rich-text 把 < 当标签
//
// 流式友好: 每次 delta 累加后整段 re-parse. 未闭合 ``` / ** / 表格头单行
// 都退化成普通文本/未结束代码块, 不抛异常不闪烁.
//
// 输出 rich-text nodes 数组, 业务侧 <rich-text :nodes="parseMarkdown(text)" />.
//
// rich-text 限制 (https://developers.weixin.qq.com/miniprogram/dev/component/rich-text.html):
//   - 不响应 click — 链接/图片只显示, 长按整个气泡触发上层菜单
//   - 标签白名单内有 a/abbr/b/blockquote/br/code/del/div/em/h1-h6/hr/i/img/
//     ins/li/ol/p/span/strong/sub/sup/table/tbody/td/tfoot/th/thead/tr/u/ul
//   - inline style 部分 CSS 生效 (字体/颜色/边框/padding/margin OK,
//     flex/grid 不行)
//   - <img> 的 src 必须是 https; max-width:100% 自适应

export interface RichNode {
  type: 'node' | 'text';
  name?: string;
  attrs?: Record<string, string>;
  children?: RichNode[];
  text?: string;
}

// 全部 inline style — rich-text 不能用 scoped class. 字号跟随用户字号
// 档位 (ratio) 动态生成, ratio=1.0 即默认.
type StyleSet = Record<
  | 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'
  | 'p' | 'ul' | 'ol' | 'li' | 'blockquote' | 'hr'
  | 'preWrapper' | 'preLang' | 'pre' | 'inlineCode'
  | 'bold' | 'italic' | 'strike' | 'link'
  | 'table' | 'th' | 'td' | 'img',
  string
>;

const _styleCache = new Map<number, StyleSet>();

function buildStyle(ratio: number): StyleSet {
  const cached = _styleCache.get(ratio);
  if (cached) return cached;
  const fs = (n: number) => Math.round(n * ratio); // 字号缩放, 保留整数 rpx
  const s: StyleSet = {
    h1: `font-size:${fs(38)}rpx;font-weight:700;margin:18rpx 0 12rpx;line-height:1.4;`,
    h2: `font-size:${fs(34)}rpx;font-weight:700;margin:16rpx 0 10rpx;line-height:1.4;`,
    h3: `font-size:${fs(30)}rpx;font-weight:600;margin:14rpx 0 8rpx;line-height:1.4;`,
    h4: `font-size:${fs(28)}rpx;font-weight:600;margin:12rpx 0 6rpx;line-height:1.4;`,
    h5: `font-size:${fs(26)}rpx;font-weight:600;margin:10rpx 0 4rpx;line-height:1.4;`,
    h6: `font-size:${fs(26)}rpx;font-weight:600;color:#6b7280;margin:10rpx 0 4rpx;line-height:1.4;`,
    p: 'margin:8rpx 0;line-height:1.6;',
    ul: 'padding-left:32rpx;margin:8rpx 0;list-style:disc;',
    ol: 'padding-left:32rpx;margin:8rpx 0;list-style:decimal;',
    li: 'line-height:1.6;margin:4rpx 0;',
    blockquote:
      'border-left:6rpx solid #cbd5e1;padding:8rpx 20rpx;margin:12rpx 0;color:#475569;background:#f8fafc;border-radius:0 8rpx 8rpx 0;',
    hr: 'border:none;border-top:1px solid #e5e7eb;margin:24rpx 0;height:0;',
    preWrapper:
      'margin:12rpx 0;border-radius:8rpx;overflow:hidden;background:#0f172a;',
    preLang:
      `display:block;background:#1e293b;color:#94a3b8;padding:6rpx 20rpx;font-size:${fs(22)}rpx;font-family:Menlo,Consolas,monospace;letter-spacing:1rpx;`,
    pre: `display:block;background:#0f172a;color:#e2e8f0;padding:16rpx 20rpx;font-size:${fs(26)}rpx;font-family:Menlo,Consolas,monospace;white-space:pre-wrap;word-break:break-all;line-height:1.5;`,
    inlineCode:
      `background:#f1f5f9;color:#dc2626;padding:2rpx 10rpx;border-radius:4rpx;font-size:${fs(26)}rpx;font-family:Menlo,Consolas,monospace;`,
    bold: 'font-weight:600;',
    italic: 'font-style:italic;',
    strike: 'text-decoration:line-through;color:#9ca3af;',
    link: 'color:#2563eb;text-decoration:underline;',
    table:
      `border-collapse:collapse;margin:12rpx 0;width:100%;font-size:${fs(26)}rpx;line-height:1.5;`,
    th: 'border:1px solid #e5e7eb;padding:10rpx 14rpx;background:#f9fafb;font-weight:600;',
    td: 'border:1px solid #e5e7eb;padding:10rpx 14rpx;',
    img: 'max-width:100%;border-radius:8rpx;margin:8rpx 0;display:block;',
  };
  _styleCache.set(ratio, s);
  return s;
}


export function parseMarkdown(src: string, ratio: number = 1.0): RichNode[] {
  if (!src) return [];
  const style = buildStyle(ratio);
  const blocks = splitBlocks(src);
  const out: RichNode[] = [];
  for (const b of blocks) {
    const r = renderBlock(b, style);
    if (r) out.push(r);
  }
  return out;
}

// ── Segments —— chat 渲染用 ─────────────────────────────────────
//
// 把整段消息切成 markdown 段 (rich-text 渲染) + code 段 (CodeBlock 渲染)
// 交错的数组. 这样代码块能用 Vue 组件接管点击事件 (复制按钮 / 高亮),
// 其余 markdown 仍走 rich-text 单一节点效率高.

export type MessageSegment =
  | { kind: 'markdown'; nodes: RichNode[] }
  | { kind: 'code'; lang: string; code: string };

export function parseMessageSegments(
  src: string,
  ratio: number = 1.0,
): MessageSegment[] {
  if (!src) return [];
  const style = buildStyle(ratio);
  const blocks = splitBlocks(src);
  const segments: MessageSegment[] = [];
  let mdBuf: RichNode[] = [];

  const flushMd = () => {
    if (mdBuf.length > 0) {
      segments.push({ kind: 'markdown', nodes: mdBuf });
      mdBuf = [];
    }
  };

  for (const b of blocks) {
    if (b.kind === 'code') {
      flushMd();
      segments.push({
        kind: 'code',
        lang: b.lang || '',
        code: b.lines.join('\n'),
      });
    } else {
      const r = renderBlock(b, style);
      if (r) mdBuf.push(r);
    }
  }
  flushMd();
  return segments;
}

// ── block layer ───────────────────────────────────────────────

interface Block {
  kind:
    | 'code'
    | 'heading'
    | 'ul'
    | 'ol'
    | 'p'
    | 'blockquote'
    | 'hr'
    | 'table';
  lang?: string;
  level?: number; // 1-6
  lines: string[]; // 行集合 / 代码原文 / list items / blockquote 内容
  align?: ('left' | 'center' | 'right' | null)[]; // 表格列对齐
  rows?: string[][]; // 表格 [header, ...body] 已切好的 cells
}

function splitBlocks(src: string): Block[] {
  const blocks: Block[] = [];
  const lines = src.split('\n');
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];

    // ── 代码块 ```lang ──
    const fence = /^```\s*(\S*)\s*$/.exec(line);
    if (fence) {
      const lang = fence[1] || '';
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        codeLines.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++; // 跳过 closing fence
      blocks.push({ kind: 'code', lang, lines: codeLines });
      continue;
    }

    // ── 水平线 --- / *** / ___ (3+ 个相同字符, 单独一行) ──
    if (/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      blocks.push({ kind: 'hr', lines: [] });
      i++;
      continue;
    }

    // ── 标题 # ~ ###### ──
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      blocks.push({
        kind: 'heading',
        level: h[1].length,
        lines: [h[2]],
      });
      i++;
      continue;
    }

    // ── 表格 (GFM): 当前行 + 下一行 separator ──
    // 必须 header 行 + 分隔行都满足才认, 否则可能流式中只出了 header,
    // 那就当普通段落, 等下一帧分隔行到达再升级成表格.
    if (
      /^\s*\|.*\|\s*$/.test(line) &&
      i + 1 < lines.length &&
      isTableSeparator(lines[i + 1])
    ) {
      const header = splitTableRow(line);
      const align = parseAlignRow(lines[i + 1]);
      const rows: string[][] = [header];
      i += 2;
      while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
        rows.push(splitTableRow(lines[i]));
        i++;
      }
      blocks.push({ kind: 'table', lines: [], rows, align });
      continue;
    }

    // ── 引用块 > xxx (多行连续, 中间空行结束) ──
    if (/^\s*>\s?/.test(line)) {
      const buf: string[] = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^\s*>\s?/, ''));
        i++;
      }
      blocks.push({ kind: 'blockquote', lines: buf });
      continue;
    }

    // ── 无序列表 - / * / + ──
    if (/^\s*[-*+]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*+]\s+/, ''));
        i++;
      }
      blocks.push({ kind: 'ul', lines: items });
      continue;
    }

    // ── 有序列表 1. 2. ──
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+\.\s+/, ''));
        i++;
      }
      blocks.push({ kind: 'ol', lines: items });
      continue;
    }

    // ── 空行跳过 ──
    if (line.trim() === '') {
      i++;
      continue;
    }

    // ── 段落 — 收到下一个空行/特殊块前 ──
    const para: string[] = [line];
    i++;
    while (i < lines.length && !isParaBreaker(lines[i])) {
      para.push(lines[i]);
      i++;
    }
    blocks.push({ kind: 'p', lines: para });
  }
  return blocks;
}

function isParaBreaker(line: string): boolean {
  if (line.trim() === '') return true;
  if (/^```/.test(line)) return true;
  if (/^#{1,6}\s/.test(line)) return true;
  if (/^\s*[-*+]\s+/.test(line)) return true;
  if (/^\s*\d+\.\s+/.test(line)) return true;
  if (/^\s*>\s?/.test(line)) return true;
  if (/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) return true;
  return false;
}

function isTableSeparator(line: string): boolean {
  // | --- | :---: | ---: |  允许首尾 | 可省
  return /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)+\|?\s*$/.test(line);
}

function splitTableRow(line: string): string[] {
  // 去掉首尾 | 再 split,trim 每个 cell
  const trimmed = line.trim().replace(/^\|/, '').replace(/\|$/, '');
  return trimmed.split('|').map((c) => c.trim());
}

function parseAlignRow(
  line: string,
): ('left' | 'center' | 'right' | null)[] {
  const cells = splitTableRow(line);
  return cells.map((c) => {
    const left = c.startsWith(':');
    const right = c.endsWith(':');
    if (left && right) return 'center';
    if (right) return 'right';
    if (left) return 'left';
    return null;
  });
}

// ── render layer ──────────────────────────────────────────────

function renderBlock(b: Block, style: StyleSet): RichNode | null {
  switch (b.kind) {
    case 'code':
      return renderCode(b, style);
    case 'heading': {
      const lvl = b.level || 1;
      const styleKey = ('h' + lvl) as keyof StyleSet;
      return {
        type: 'node',
        name: 'h' + lvl,
        attrs: { style: style[styleKey] as string },
        children: parseInline(b.lines[0], style),
      };
    }
    case 'ul':
      return {
        type: 'node',
        name: 'ul',
        attrs: { style: style.ul },
        children: b.lines.map((t) => ({
          type: 'node' as const,
          name: 'li',
          attrs: { style: style.li },
          children: parseInline(t, style),
        })),
      };
    case 'ol':
      return {
        type: 'node',
        name: 'ol',
        attrs: { style: style.ol },
        children: b.lines.map((t) => ({
          type: 'node' as const,
          name: 'li',
          attrs: { style: style.li },
          children: parseInline(t, style),
        })),
      };
    case 'blockquote': {
      const children: RichNode[] = [];
      b.lines.forEach((line, idx) => {
        if (idx > 0) children.push({ type: 'node', name: 'br' });
        if (line) children.push(...parseInline(line, style));
      });
      return {
        type: 'node',
        name: 'blockquote',
        attrs: { style: style.blockquote },
        children,
      };
    }
    case 'hr':
      return { type: 'node', name: 'hr', attrs: { style: style.hr } };
    case 'table':
      return renderTable(b, style);
    case 'p': {
      const children: RichNode[] = [];
      b.lines.forEach((line, idx) => {
        if (idx > 0) children.push({ type: 'node', name: 'br' });
        children.push(...parseInline(line, style));
      });
      return {
        type: 'node',
        name: 'div',
        attrs: { style: style.p },
        children,
      };
    }
  }
}

function renderCode(b: Block, style: StyleSet): RichNode {
  const children: RichNode[] = [];
  if (b.lang) {
    children.push({
      type: 'node',
      name: 'div',
      attrs: { style: style.preLang },
      children: [{ type: 'text', text: b.lang }],
    });
  }
  children.push({
    type: 'node',
    name: 'div',
    attrs: { style: style.pre },
    children: [{ type: 'text', text: b.lines.join('\n') }],
  });
  return {
    type: 'node',
    name: 'div',
    attrs: { style: style.preWrapper },
    children,
  };
}

function renderTable(b: Block, style: StyleSet): RichNode {
  const rows = b.rows || [];
  const align = b.align || [];
  if (rows.length === 0) {
    return { type: 'node', name: 'div', attrs: {}, children: [] };
  }
  const [header, ...body] = rows;

  const cellStyle = (base: string, col: number): string => {
    const a = align[col];
    return a ? base + 'text-align:' + a + ';' : base;
  };

  const thead: RichNode = {
    type: 'node',
    name: 'thead',
    children: [
      {
        type: 'node',
        name: 'tr',
        children: header.map((c, idx) => ({
          type: 'node' as const,
          name: 'th',
          attrs: { style: cellStyle(style.th, idx) },
          children: parseInline(c, style),
        })),
      },
    ],
  };

  const tbody: RichNode = {
    type: 'node',
    name: 'tbody',
    children: body.map((row) => ({
      type: 'node' as const,
      name: 'tr',
      children: row.map((c, idx) => ({
        type: 'node' as const,
        name: 'td',
        attrs: { style: cellStyle(style.td, idx) },
        children: parseInline(c, style),
      })),
    })),
  };

  return {
    type: 'node',
    name: 'table',
    attrs: { style: style.table },
    children: [thead, tbody],
  };
}

// ── inline layer ──────────────────────────────────────────────
//
// 顺序: 代码 ` (内不递归) → 图片 ![](url) → 链接 [](url) →
//       加粗 ** → 斜体 * → 删除线 ~~. 未闭合标记当普通文本.

function parseInline(text: string, style: StyleSet): RichNode[] {
  if (!text) return [];
  const out: RichNode[] = [];
  let i = 0;
  let buf = '';
  const flush = () => {
    if (buf) {
      out.push({ type: 'text', text: buf });
      buf = '';
    }
  };
  while (i < text.length) {
    const ch = text[i];

    if (ch === '`') {
      const end = text.indexOf('`', i + 1);
      if (end > i) {
        flush();
        out.push({
          type: 'node',
          name: 'code',
          attrs: { style: style.inlineCode },
          children: [{ type: 'text', text: text.slice(i + 1, end) }],
        });
        i = end + 1;
        continue;
      }
    }

    if (ch === '!' && text[i + 1] === '[') {
      const close = text.indexOf(']', i + 2);
      if (close > i && text[close + 1] === '(') {
        const urlEnd = text.indexOf(')', close + 2);
        if (urlEnd > close) {
          flush();
          const alt = text.slice(i + 2, close);
          const url = text.slice(close + 2, urlEnd);
          out.push({
            type: 'node',
            name: 'img',
            attrs: { src: url, alt, style: style.img },
          });
          i = urlEnd + 1;
          continue;
        }
      }
    }

    if (ch === '[') {
      const close = text.indexOf(']', i + 1);
      if (close > i && text[close + 1] === '(') {
        const urlEnd = text.indexOf(')', close + 2);
        if (urlEnd > close) {
          flush();
          const linkText = text.slice(i + 1, close);
          const url = text.slice(close + 2, urlEnd);
          out.push({
            type: 'node',
            name: 'a',
            attrs: { style: style.link, href: url },
            children: parseInline(linkText, style),
          });
          i = urlEnd + 1;
          continue;
        }
      }
    }

    if (ch === '*' && text[i + 1] === '*') {
      const end = text.indexOf('**', i + 2);
      if (end > i) {
        flush();
        out.push({
          type: 'node',
          name: 'strong',
          attrs: { style: style.bold },
          children: parseInline(text.slice(i + 2, end), style),
        });
        i = end + 2;
        continue;
      }
    }

    if (ch === '*') {
      const end = text.indexOf('*', i + 1);
      if (end > i && text[end + 1] !== '*') {
        flush();
        out.push({
          type: 'node',
          name: 'em',
          attrs: { style: style.italic },
          children: parseInline(text.slice(i + 1, end), style),
        });
        i = end + 1;
        continue;
      }
    }

    if (ch === '~' && text[i + 1] === '~') {
      const end = text.indexOf('~~', i + 2);
      if (end > i) {
        flush();
        out.push({
          type: 'node',
          name: 'del',
          attrs: { style: style.strike },
          children: parseInline(text.slice(i + 2, end), style),
        });
        i = end + 2;
        continue;
      }
    }

    buf += ch;
    i++;
  }
  flush();
  return out;
}
