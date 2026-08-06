// CM6 编辑器主题：排版对标方案 §4.3（15px / 1.55 / padding 20px 16px 通栏 /
// 标题阶梯）。色值全部引用 --kc-editor-* CSS 变量，
// 明暗切换由 applyTheme 给 <html> 切 .dark class 完成，theme 本身无需重写；
// setTheme 走 Compartment reconfigure 仅为兑现内核句柄契约。

import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import type { Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import type { Theme } from '../bridge/protocol'

// 与 index.html 一致的系统字体栈
const FONT_STACK =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", ' +
  '"Microsoft YaHei", Helvetica, Arial, sans-serif'
const MONO_STACK = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'

const layoutTheme = EditorView.theme(
  {
    '&': {
      backgroundColor: 'var(--kc-editor-bg)',
      color: 'var(--kc-editor-fg)',
      fontSize: '15px',
      height: '100%',
    },
    '.cm-scroller': {
      fontFamily: FONT_STACK,
      lineHeight: '1.55',
      overflow: 'auto',
    },
    '.cm-content': {
      fontFamily: FONT_STACK,
      // 通栏正文（用户反馈去掉 760 限宽居中），两侧各留 16px 呼吸位
      padding: '20px 16px',
      caretColor: 'var(--kc-editor-fg)',
    },
    '&.cm-focused': { outline: 'none' },
    '.cm-cursor': { borderLeftColor: 'var(--kc-editor-fg)' },
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
      backgroundColor:
        'color-mix(in srgb, var(--kc-editor-accent) 22%, transparent)',
    },
    '.cm-activeLine': {
      backgroundColor:
        'color-mix(in srgb, var(--kc-editor-muted) 7%, transparent)',
    },
    // 以下 class 选择器服务 M2 的 decorations（行/内联 class 装饰），
    // 装饰加上后即生效；当前 lezer 高亮 class（tok-*）已命中一部分。
    '.cm-h1': {
      fontSize: '1.5em',
      fontWeight: 'bold',
      borderBottom: '1px solid var(--kc-editor-border)',
    },
    '.cm-h2': { fontSize: '1.4em', fontWeight: 'bold' },
    '.cm-h3': { fontSize: '1.3em', fontWeight: 'bold' },
    '.cm-h4': { fontSize: '1.2em', fontWeight: 'bold' },
    '.cm-h5': { fontSize: '1.1em', fontWeight: 'bold' },
    '.cm-h6': { fontSize: '1.0em', fontWeight: 'bold' },
    '.cm-codeBlock': {
      backgroundColor: 'var(--kc-editor-code-bg)',
      fontFamily: MONO_STACK,
    },
    '.cm-codeBlock-first': {
      borderTopLeftRadius: '6px',
      borderTopRightRadius: '6px',
    },
    '.cm-codeBlock-last': {
      borderBottomLeftRadius: '6px',
      borderBottomRightRadius: '6px',
    },
    '.cm-blockQuote': {
      borderLeft: '4px solid var(--kc-editor-border)',
      paddingLeft: '12px',
      color: 'var(--kc-editor-muted)',
    },
    '.cm-inlineCode': {
      fontFamily: MONO_STACK,
      backgroundColor: 'var(--kc-editor-code-bg)',
      border: '1px solid var(--kc-editor-border)',
      borderRadius: '4px',
      padding: '0 3px',
    },
    '.cm-url': { opacity: '0.66' },
    '.cm-hr': { color: 'var(--kc-editor-muted)' },
    '.cm-strike': { textDecoration: 'line-through' },
    // checkbox widget（rendering/checkbox.ts）：1.1em 见方、垂直居中
    '.cm-md-checkbox': {
      display: 'inline-flex',
      alignItems: 'center',
      marginRight: '0.35em',
      verticalAlign: 'middle',
    },
    '.cm-md-checkbox input': {
      width: '1.1em',
      height: '1.1em',
      margin: '0',
      accentColor: 'var(--kc-editor-accent)',
      cursor: 'pointer',
    },
    // 已完成任务项整行半透明
    '.cm-md-completed-item': { opacity: '0.6' },
    // 独占行图片 block widget（rendering/block-images.ts）
    '.cm-md-image': {
      margin: '4px 0 8px',
      lineHeight: '0',
    },
    '.cm-md-image img': {
      maxWidth: '100%',
      borderRadius: '6px',
    },
    '.cm-md-image-error': {
      lineHeight: '1.55',
      fontSize: '13px',
      color: 'var(--kc-editor-muted)',
      padding: '6px 10px',
      backgroundColor: 'var(--kc-editor-code-bg)',
      borderRadius: '6px',
    },
  },
  // 颜色全部走 CSS 变量，dark 只改变量（见 theme/dark.css）
  { dark: false },
)

// lezer 语法高亮：格式标记淡化、粗/斜/删除线视觉、行内 code 边框、链接色。
// 标题字号阶梯由 decorations 的行 class（.cm-h1..h6）承担 —— 标记隐藏后
// 行 class 与高亮 span 会叠加，这里只保留 bold 防止双重缩放。
const highlightStyle = HighlightStyle.define([
  { tag: tags.heading, fontWeight: 'bold' },
  { tag: tags.strong, fontWeight: 'bold' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: tags.strikethrough, textDecoration: 'line-through' },
  {
    tag: tags.monospace,
    fontFamily: MONO_STACK,
    backgroundColor: 'var(--kc-editor-code-bg)',
    border: '1px solid var(--kc-editor-border)',
    borderRadius: '4px',
    padding: '0 3px',
  },
  {
    tag: tags.link,
    color: 'var(--kc-editor-link)',
    textDecoration: 'underline',
  },
  { tag: tags.url, color: 'var(--kc-editor-muted)', opacity: '0.66' },
  { tag: tags.quote, color: 'var(--kc-editor-muted)' },
  // 格式字符（**、`、#、~~ 等）淡化 —— 露源码（reveal）时的视觉
  { tag: tags.processingInstruction, color: 'var(--kc-editor-muted)' },
  { tag: tags.meta, color: 'var(--kc-editor-muted)' },
  { tag: tags.contentSeparator, color: 'var(--kc-editor-muted)' },
])

/** 装配主题扩展。theme 参数预留（明暗由 CSS 变量承载，无需分支）。 */
export function createThemeExtensions(_theme: Theme): Extension {
  return [layoutTheme, syntaxHighlighting(highlightStyle)]
}
