// lib/share_card.ts — Canvas 2D 长截图 / 分享卡片渲染.
//
// 不依赖具体平台 — 接收 canvas + ctx + dpr, 业务层负责拿 node.
//
// 简化 markdown: 标题加粗放大, 列表 "•", 代码块独立背景, 粗体 / 斜体
// 退化为纯文本. 表格/图片当前阶段不画 — 可见即可分享, 不追求完美还原.
//
// 单条卡片 (single)  : 头部品牌 + 用户 prompt + AI 答案 + 底部二维码
// 长截图 (long)     : 头部品牌 + 多轮对话气泡 + 底部二维码

import type { WxacodeResult } from './wxacode';

// ── Types ─────────────────────────────────────────────────────────

export interface CardMessage {
  role: 'user' | 'assistant';
  content: string;
}

export interface SingleCardOptions {
  prompt: string;
  answer: string;
  qr: WxacodeResult;
}

export interface LongShotOptions {
  messages: CardMessage[];
  qr: WxacodeResult;
  /** 会话标题 (可选) — 头部副标题 */
  threadTitle?: string;
}

export interface RenderContext {
  /** Canvas 2D node — wx.canvasToTempFilePath 用得到 */
  canvas: CanvasNode;
  /** Canvas 2D context */
  ctx: CanvasRenderingContext2DLike;
  /** 设备像素比 */
  dpr: number;
}

interface CanvasNode {
  width: number;
  height: number;
}

// 绘制 API 取交集 — 微信 / H5 / 其它端的 Canvas 2D 都支持以下方法.
interface CanvasRenderingContext2DLike {
  fillStyle: string;
  strokeStyle: string;
  lineWidth: number;
  font: string;
  textBaseline: CanvasTextBaseline;
  textAlign: CanvasTextAlign;
  fillRect(x: number, y: number, w: number, h: number): void;
  strokeRect(x: number, y: number, w: number, h: number): void;
  fillText(text: string, x: number, y: number): void;
  measureText(text: string): { width: number };
  beginPath(): void;
  moveTo(x: number, y: number): void;
  lineTo(x: number, y: number): void;
  stroke(): void;
  closePath(): void;
  scale(x: number, y: number): void;
  setTransform(a: number, b: number, c: number, d: number, e: number, f: number): void;
  save(): void;
  restore(): void;
  fill(): void;
  arc(x: number, y: number, r: number, s: number, e: number): void;
  drawImage(...args: unknown[]): void;
}

// ── 设计常量 ──────────────────────────────────────────────────────

const W = 750;                       // 卡片显示宽度 (px)
const PAD = 36;                      // 边距
const CONTENT_W = W - PAD * 2;       // 内容宽 = 678

const COLOR = {
  bg: '#f6f7fb',
  card: '#ffffff',
  brand: '#3b82f6',
  text: '#1f2937',
  textSecondary: '#6b7280',
  textMuted: '#9ca3af',
  border: '#e5e7eb',
  codeBg: '#f3f4f6',
  codeText: '#374151',
  quoteBar: '#3b82f6',
  userBubble: '#3b82f6',
  userText: '#ffffff',
  assistantBubble: '#ffffff',
};

const FONT = {
  brand: 'bold 36px sans-serif',
  brandSub: '24px sans-serif',
  h1: 'bold 38px sans-serif',
  h2: 'bold 34px sans-serif',
  h3: 'bold 30px sans-serif',
  body: '28px sans-serif',
  bodyBold: 'bold 28px sans-serif',
  code: '26px Menlo, Consolas, monospace',
  codeLang: '22px Menlo, Consolas, monospace',
  quote: 'italic 28px sans-serif',
  small: '22px sans-serif',
  promptLabel: 'bold 24px sans-serif',
};

const LINE_H = {
  body: 42,
  code: 38,
  h1: 56,
  h2: 50,
  h3: 46,
  small: 32,
};

// ── 简化 markdown 解析 ────────────────────────────────────────────

type Block =
  | { kind: 'heading'; level: 1 | 2 | 3; text: string }
  | { kind: 'para'; text: string }
  | { kind: 'code'; lang: string; lines: string[] }
  | { kind: 'list'; items: string[]; ordered: boolean }
  | { kind: 'quote'; text: string }
  | { kind: 'hr' };

function parseBlocks(md: string): Block[] {
  const blocks: Block[] = [];
  const lines = md.replace(/\r\n/g, '\n').split('\n');
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];

    // 代码块 ```
    const fence = /^\s*```(\w*)\s*$/.exec(line);
    if (fence) {
      const lang = fence[1] || '';
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) {
        codeLines.push(lines[i]);
        i++;
      }
      i++; // skip closing fence (or end)
      blocks.push({ kind: 'code', lang, lines: codeLines });
      continue;
    }

    // 分隔线
    if (/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      blocks.push({ kind: 'hr' });
      i++;
      continue;
    }

    // 标题
    const h = /^(#{1,3})\s+(.+)$/.exec(line);
    if (h) {
      blocks.push({
        kind: 'heading',
        level: h[1].length as 1 | 2 | 3,
        text: stripInline(h[2]),
      });
      i++;
      continue;
    }

    // 引用 (连续 > )
    if (/^\s*>\s?/.test(line)) {
      const buf: string[] = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^\s*>\s?/, ''));
        i++;
      }
      blocks.push({ kind: 'quote', text: stripInline(buf.join(' ')) });
      continue;
    }

    // 列表 (无序 -/*  + 有序 1. 2.)
    if (/^\s*(?:[-*+]|\d+\.)\s+/.test(line)) {
      const ordered = /^\s*\d+\./.test(line);
      const items: string[] = [];
      while (i < lines.length && /^\s*(?:[-*+]|\d+\.)\s+/.test(lines[i])) {
        items.push(stripInline(lines[i].replace(/^\s*(?:[-*+]|\d+\.)\s+/, '')));
        i++;
      }
      blocks.push({ kind: 'list', items, ordered });
      continue;
    }

    // 空行 — 跳过
    if (line.trim() === '') {
      i++;
      continue;
    }

    // 普通段落 — 把后续非空白非块行合并成一段 (markdown 段落语义)
    const buf = [line];
    i++;
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !/^\s*```/.test(lines[i]) &&
      !/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/.test(lines[i]) &&
      !/^#{1,3}\s+/.test(lines[i]) &&
      !/^\s*>\s?/.test(lines[i]) &&
      !/^\s*(?:[-*+]|\d+\.)\s+/.test(lines[i])
    ) {
      buf.push(lines[i]);
      i++;
    }
    blocks.push({ kind: 'para', text: stripInline(buf.join(' ')) });
  }
  return blocks;
}

// 去掉 markdown inline 标记 — 粗体/斜体/inline code/链接保留可读文本.
// 卡片不做富格式渲染 (canvas 文本不支持混排不同字重), 信息保留即可.
function stripInline(s: string): string {
  return s
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '[图片:$1]')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '$1 ($2)')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\*\*\*([^*]+)\*\*\*/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .replace(/__([^_]+)__/g, '$1')
    .replace(/_([^_]+)_/g, '$1')
    .replace(/~~([^~]+)~~/g, '$1');
}

// ── 文本换行 (按字符 wrap, 中英文混排都对) ──────────────────────────

function wrapText(
  ctx: CanvasRenderingContext2DLike,
  text: string,
  maxWidth: number,
): string[] {
  const out: string[] = [];
  // 先按硬换行切, 每段再 wrap
  for (const para of text.split('\n')) {
    if (!para) {
      out.push('');
      continue;
    }
    let line = '';
    for (const ch of Array.from(para)) {
      const test = line + ch;
      if (ctx.measureText(test).width > maxWidth && line) {
        out.push(line);
        line = ch;
      } else {
        line = test;
      }
    }
    if (line) out.push(line);
  }
  return out;
}

// ── 测量阶段 — 计算总高度 ────────────────────────────────────────

interface LaidBlock {
  block: Block;
  /** 已 wrap 好的行 */
  lines: string[];
  /** 此 block 占用的总高度 (含上下 gap) */
  height: number;
}

function layoutBlocks(
  ctx: CanvasRenderingContext2DLike,
  blocks: Block[],
  contentWidth: number,
): { laid: LaidBlock[]; height: number } {
  const laid: LaidBlock[] = [];
  let total = 0;

  for (const b of blocks) {
    let height = 0;
    let lines: string[] = [];

    switch (b.kind) {
      case 'heading': {
        ctx.font =
          b.level === 1 ? FONT.h1 : b.level === 2 ? FONT.h2 : FONT.h3;
        const lh =
          b.level === 1 ? LINE_H.h1 : b.level === 2 ? LINE_H.h2 : LINE_H.h3;
        lines = wrapText(ctx, b.text, contentWidth);
        height = lines.length * lh + 16;
        break;
      }
      case 'para': {
        ctx.font = FONT.body;
        lines = wrapText(ctx, b.text, contentWidth);
        height = lines.length * LINE_H.body + 16;
        break;
      }
      case 'code': {
        ctx.font = FONT.code;
        // 代码块每行单独 wrap (太长也 wrap, 用户能看清)
        const wrapped: string[] = [];
        for (const cl of b.lines) {
          const ws = wrapText(ctx, cl, contentWidth - 32);
          wrapped.push(...(ws.length ? ws : ['']));
        }
        lines = wrapped;
        // padding 内 16 上 16 下 + lang label (如果有) 30px
        const langExtra = b.lang ? 28 : 0;
        height = wrapped.length * LINE_H.code + 32 + langExtra + 16;
        break;
      }
      case 'list': {
        ctx.font = FONT.body;
        const itemLines: string[] = [];
        b.items.forEach((it, idx) => {
          const prefix = b.ordered ? idx + 1 + '. ' : '• ';
          const wrapped = wrapText(ctx, prefix + it, contentWidth);
          itemLines.push(...wrapped);
        });
        lines = itemLines;
        height = itemLines.length * LINE_H.body + 16;
        break;
      }
      case 'quote': {
        ctx.font = FONT.quote;
        lines = wrapText(ctx, b.text, contentWidth - 24);
        height = lines.length * LINE_H.body + 16;
        break;
      }
      case 'hr': {
        height = 32;
        break;
      }
    }
    laid.push({ block: b, lines, height });
    total += height;
  }
  return { laid, height: total };
}

// ── 绘制阶段 ──────────────────────────────────────────────────────

function drawBlocks(
  ctx: CanvasRenderingContext2DLike,
  laid: LaidBlock[],
  x: number,
  startY: number,
  contentWidth: number,
): number {
  let y = startY;
  for (const item of laid) {
    const b = item.block;
    switch (b.kind) {
      case 'heading': {
        ctx.font =
          b.level === 1 ? FONT.h1 : b.level === 2 ? FONT.h2 : FONT.h3;
        const lh =
          b.level === 1 ? LINE_H.h1 : b.level === 2 ? LINE_H.h2 : LINE_H.h3;
        ctx.fillStyle = COLOR.text;
        ctx.textBaseline = 'top';
        for (const line of item.lines) {
          ctx.fillText(line, x, y);
          y += lh;
        }
        y += 16;
        break;
      }
      case 'para': {
        ctx.font = FONT.body;
        ctx.fillStyle = COLOR.text;
        ctx.textBaseline = 'top';
        for (const line of item.lines) {
          ctx.fillText(line, x, y);
          y += LINE_H.body;
        }
        y += 16;
        break;
      }
      case 'code': {
        const langExtra = b.lang ? 28 : 0;
        const innerH = item.lines.length * LINE_H.code + 32 + langExtra;
        ctx.fillStyle = COLOR.codeBg;
        roundRect(ctx, x, y, contentWidth, innerH, 12);
        ctx.fill();
        let cy = y + 16;
        if (b.lang) {
          ctx.font = FONT.codeLang;
          ctx.fillStyle = COLOR.textMuted;
          ctx.textBaseline = 'top';
          ctx.fillText(b.lang, x + 16, cy);
          cy += 28;
        }
        ctx.font = FONT.code;
        ctx.fillStyle = COLOR.codeText;
        ctx.textBaseline = 'top';
        for (const line of item.lines) {
          ctx.fillText(line, x + 16, cy);
          cy += LINE_H.code;
        }
        y += innerH + 16;
        break;
      }
      case 'list': {
        ctx.font = FONT.body;
        ctx.fillStyle = COLOR.text;
        ctx.textBaseline = 'top';
        for (const line of item.lines) {
          ctx.fillText(line, x, y);
          y += LINE_H.body;
        }
        y += 16;
        break;
      }
      case 'quote': {
        // 左侧蓝色竖线
        const innerH = item.lines.length * LINE_H.body;
        ctx.fillStyle = COLOR.quoteBar;
        ctx.fillRect(x, y, 6, innerH);
        ctx.font = FONT.quote;
        ctx.fillStyle = COLOR.textSecondary;
        ctx.textBaseline = 'top';
        let qy = y;
        for (const line of item.lines) {
          ctx.fillText(line, x + 24, qy);
          qy += LINE_H.body;
        }
        y += innerH + 16;
        break;
      }
      case 'hr': {
        ctx.strokeStyle = COLOR.border;
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(x, y + 16);
        ctx.lineTo(x + contentWidth, y + 16);
        ctx.stroke();
        y += 32;
        break;
      }
    }
  }
  return y;
}

function roundRect(
  ctx: CanvasRenderingContext2DLike,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
): void {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.lineTo(x + w - r, y);
  ctx.arc(x + w - r, y + r, r, -Math.PI / 2, 0);
  ctx.lineTo(x + w, y + h - r);
  ctx.arc(x + w - r, y + h - r, r, 0, Math.PI / 2);
  ctx.lineTo(x + r, y + h);
  ctx.arc(x + r, y + h - r, r, Math.PI / 2, Math.PI);
  ctx.lineTo(x, y + r);
  ctx.arc(x + r, y + r, r, Math.PI, -Math.PI / 2);
  ctx.closePath();
}

// ── 各区块: 头部 / prompt / 底部 ─────────────────────────────────

function drawHeader(
  ctx: CanvasRenderingContext2DLike,
  y: number,
  subtitle: string,
): number {
  // 蓝色品牌色块
  ctx.fillStyle = COLOR.brand;
  roundRect(ctx, PAD, y, 80, 80, 16);
  ctx.fill();
  ctx.font = FONT.brand;
  ctx.fillStyle = '#fff';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.fillText('B', PAD + 40, y + 44);
  ctx.textAlign = 'left';

  // 文字
  ctx.font = FONT.brand;
  ctx.fillStyle = COLOR.text;
  ctx.textBaseline = 'top';
  ctx.fillText('BiuMind', PAD + 100, y + 8);
  ctx.font = FONT.brandSub;
  ctx.fillStyle = COLOR.textSecondary;
  ctx.fillText(subtitle, PAD + 100, y + 50);

  return y + 80 + 32;
}

function drawPromptBlock(
  ctx: CanvasRenderingContext2DLike,
  prompt: string,
  y: number,
): number {
  ctx.font = FONT.promptLabel;
  ctx.fillStyle = COLOR.brand;
  ctx.textBaseline = 'top';
  ctx.fillText('提问', PAD, y);
  y += 36;

  // 引用块: 浅灰底, 左竖线
  ctx.font = FONT.body;
  const lines = wrapText(ctx, prompt, CONTENT_W - 32);
  const innerH = lines.length * LINE_H.body + 24;
  ctx.fillStyle = COLOR.codeBg;
  roundRect(ctx, PAD, y, CONTENT_W, innerH, 12);
  ctx.fill();
  ctx.fillStyle = COLOR.brand;
  ctx.fillRect(PAD, y, 6, innerH);
  ctx.fillStyle = COLOR.text;
  ctx.textBaseline = 'top';
  let py = y + 12;
  for (const line of lines) {
    ctx.fillText(line, PAD + 24, py);
    py += LINE_H.body;
  }
  return y + innerH + 32;
}

function drawAnswerLabel(
  ctx: CanvasRenderingContext2DLike,
  y: number,
): number {
  ctx.font = FONT.promptLabel;
  ctx.fillStyle = COLOR.brand;
  ctx.textBaseline = 'top';
  ctx.fillText('回答', PAD, y);
  return y + 36;
}

function drawFooter(
  ctx: CanvasRenderingContext2DLike,
  qr: WxacodeResult,
  y: number,
  totalWidth: number,
): number {
  // 顶部分隔线
  ctx.strokeStyle = COLOR.border;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(PAD, y);
  ctx.lineTo(totalWidth - PAD, y);
  ctx.stroke();
  y += 32;

  const qrSize = 140;
  const qrX = totalWidth - PAD - qrSize;

  // QR 占位 (后端 ready 后画真图)
  if (qr.isPlaceholder) {
    ctx.fillStyle = COLOR.codeBg;
    roundRect(ctx, qrX, y, qrSize, qrSize, 8);
    ctx.fill();
    ctx.strokeStyle = COLOR.border;
    ctx.lineWidth = 1;
    roundRect(ctx, qrX, y, qrSize, qrSize, 8);
    ctx.stroke();
    ctx.font = FONT.small;
    ctx.fillStyle = COLOR.textMuted;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText('小程序码', qrX + qrSize / 2, y + qrSize / 2 - 12);
    ctx.fillText('占位', qrX + qrSize / 2, y + qrSize / 2 + 16);
    ctx.textAlign = 'left';
  }
  // TODO: qr.src 非空时 drawImage(image, qrX, y, qrSize, qrSize)
  // 微信端: image 需要 canvas.createImage() 异步 load

  // 左侧文案
  ctx.font = FONT.brand;
  ctx.fillStyle = COLOR.text;
  ctx.textBaseline = 'top';
  ctx.fillText('BiuMind', PAD, y);
  ctx.font = FONT.brandSub;
  ctx.fillStyle = COLOR.textSecondary;
  ctx.fillText(qr.hint || '你的 AI 第二大脑', PAD, y + 50);
  ctx.font = FONT.small;
  ctx.fillStyle = COLOR.textMuted;
  ctx.fillText('your-biumind.example.com', PAD, y + 88);

  return y + qrSize + 32;
}

// ── 入口 1: 单条卡片 ──────────────────────────────────────────────

export function renderSingleCard(
  rc: RenderContext,
  opts: SingleCardOptions,
): { width: number; height: number } {
  const { canvas, ctx, dpr } = rc;

  // 临时设个 1x1 让 ctx.measureText 能用 (有些端要求 canvas 有尺寸)
  canvas.width = 1;
  canvas.height = 1;

  // ── 1. 测量 ───────────────────────────────────
  ctx.font = FONT.body; // 先 set 一下避免 measureText 用错字号
  const blocks = parseBlocks(opts.answer);
  const { laid: answerLaid, height: answerH } = layoutBlocks(
    ctx,
    blocks,
    CONTENT_W,
  );

  // prompt 高度
  const promptLines = wrapText(ctx, opts.prompt, CONTENT_W - 32);
  const promptH = 36 + promptLines.length * LINE_H.body + 24 + 32; // label + block + gap

  const headerH = 80 + 32;
  const answerLabelH = 36;
  const footerH = 32 + 140 + 32 + 32; // line + qrSize + 顶部 gap + 底部 gap

  const totalH = PAD + headerH + promptH + answerLabelH + answerH + 32 + footerH + PAD;

  // ── 2. 设 canvas 尺寸 (按 dpr 放大, ctx.scale) ──
  canvas.width = W * dpr;
  canvas.height = totalH * dpr;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.scale(dpr, dpr);

  // ── 3. 背景 ──
  ctx.fillStyle = COLOR.card;
  ctx.fillRect(0, 0, W, totalH);

  // ── 4. 绘制各 section ──
  let y = PAD;
  y = drawHeader(ctx, y, '你的 AI 第二大脑');
  y = drawPromptBlock(ctx, opts.prompt, y);
  y = drawAnswerLabel(ctx, y);
  y = drawBlocks(ctx, answerLaid, PAD, y, CONTENT_W);
  y += 32;
  drawFooter(ctx, opts.qr, y, W);

  return { width: W, height: totalH };
}

// ── 入口 2: 长截图 (多轮对话) ─────────────────────────────────────

export function renderLongShot(
  rc: RenderContext,
  opts: LongShotOptions,
): { width: number; height: number } {
  const { canvas, ctx, dpr } = rc;

  canvas.width = 1;
  canvas.height = 1;
  ctx.font = FONT.body;

  // 每条消息布局 — user 蓝色气泡 (右), assistant markdown (左, 浅灰底)
  interface LaidMsg {
    role: 'user' | 'assistant';
    height: number;
    // user: lines  /  assistant: laid blocks
    userLines?: string[];
    laidBlocks?: LaidBlock[];
  }
  const BUBBLE_PAD = 24;
  const BUBBLE_MAX_W = CONTENT_W - 80; // 留 80 给气泡的左/右间距
  const laidMsgs: LaidMsg[] = [];

  for (const m of opts.messages) {
    if (m.role === 'user') {
      ctx.font = FONT.body;
      const lines = wrapText(ctx, m.content, BUBBLE_MAX_W - BUBBLE_PAD * 2);
      const h = lines.length * LINE_H.body + BUBBLE_PAD * 2 + 24;
      laidMsgs.push({ role: 'user', height: h, userLines: lines });
    } else {
      const blocks = parseBlocks(m.content);
      const { laid, height } = layoutBlocks(
        ctx,
        blocks,
        CONTENT_W - BUBBLE_PAD * 2,
      );
      laidMsgs.push({
        role: 'assistant',
        height: height + BUBBLE_PAD * 2 + 24,
        laidBlocks: laid,
      });
    }
  }

  const headerH = 80 + 32 + (opts.threadTitle ? 36 : 0);
  const footerH = 32 + 140 + 32 + 32;
  const msgsH = laidMsgs.reduce((s, m) => s + m.height, 0);
  const totalH = PAD + headerH + msgsH + 32 + footerH + PAD;

  canvas.width = W * dpr;
  canvas.height = totalH * dpr;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.scale(dpr, dpr);

  // 背景
  ctx.fillStyle = COLOR.bg;
  ctx.fillRect(0, 0, W, totalH);

  let y = PAD;
  y = drawHeader(ctx, y, opts.threadTitle || '会话长截图');
  if (opts.threadTitle) {
    ctx.font = FONT.small;
    ctx.fillStyle = COLOR.textMuted;
    ctx.textBaseline = 'top';
    ctx.fillText(
      '导出于 ' + new Date().toLocaleString('zh-CN'),
      PAD,
      y - 28,
    );
    y += 8;
  }

  // 绘制每条消息
  for (const m of laidMsgs) {
    if (m.role === 'user') {
      const lines = m.userLines!;
      const bubbleW =
        Math.min(
          BUBBLE_MAX_W,
          Math.max(...lines.map((l) => ctx.measureText(l).width)) +
            BUBBLE_PAD * 2,
        ) || BUBBLE_PAD * 2 + 60;
      const bubbleH = lines.length * LINE_H.body + BUBBLE_PAD * 2;
      const bx = W - PAD - bubbleW;
      ctx.fillStyle = COLOR.userBubble;
      roundRect(ctx, bx, y, bubbleW, bubbleH, 16);
      ctx.fill();
      ctx.font = FONT.body;
      ctx.fillStyle = COLOR.userText;
      ctx.textBaseline = 'top';
      let ly = y + BUBBLE_PAD;
      for (const line of lines) {
        ctx.fillText(line, bx + BUBBLE_PAD, ly);
        ly += LINE_H.body;
      }
      y += bubbleH + 24;
    } else {
      const innerH = m.height - BUBBLE_PAD * 2 - 24;
      ctx.fillStyle = COLOR.assistantBubble;
      roundRect(ctx, PAD, y, CONTENT_W, innerH + BUBBLE_PAD * 2, 16);
      ctx.fill();
      drawBlocks(
        ctx,
        m.laidBlocks!,
        PAD + BUBBLE_PAD,
        y + BUBBLE_PAD,
        CONTENT_W - BUBBLE_PAD * 2,
      );
      y += innerH + BUBBLE_PAD * 2 + 24;
    }
  }

  y += 32;
  drawFooter(ctx, opts.qr, y, W);

  return { width: W, height: totalH };
}
