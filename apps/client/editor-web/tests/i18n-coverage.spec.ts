// i18n 覆盖守护 —— 防止 crepe / @milkdown/components 升级后新增 UI 文案
// 悄悄漏回英文。做法：从内核 esm 产物里提取字符串字面量形式的默认文案，
// 断言每条都能在 zh-Hans 字典里找到翻译（或显式列入 IGNORE）。
// crepe 升级后此测试变红 → 把新文案补进 src/i18n/locales/zh-hans.ts 即可。

import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { createTranslator } from '../src/i18n'
import { zhHans } from '../src/i18n/locales/zh-hans'

const CREPE_FEATURE_DIR = 'node_modules/@milkdown/crepe/lib/esm/feature'
// code-block 的 previewLabel / previewLoading 等兜底在 components 包里
const EXTRA_FILES = ['node_modules/@milkdown/components/lib/code-block/index.js']

// 非 UI 文案 / 未启用功能（Crepe.Feature.AI 默认关）的字符串，显式豁免。
const IGNORE = new Set([
  ' ',
  ';',
  'Ctrl',
  'Space',
  'Backspace',
  'block+',
  'inline*',
  'const DEFAULT_SUBMIT_BUTTON_LABEL = ',
  'CodeBlock',
  // ── AI feature（未启用）──
  'Ask AI',
  'Adjusting tone',
  'Change tone',
  'Change tone…',
  'Expand this with more detail and examples.',
  'Expanding',
  'Fix grammar & spelling',
  'Fixing grammar & spelling',
  'Improve the writing while preserving the original meaning.',
  'Improve writing',
  'Improving writing',
  'Make longer',
  'Make shorter',
  'Make this shorter while preserving the key information.',
  'Making shorter',
  'Translate',
  'Translate…',
  'Search tones…',
])

/** 从编译产物提取 `: "..."` / `|| "..."` 形式的默认串，滤掉图标/类名/代码碎片。 */
function extractCandidates(src: string): string[] {
  const out: string[] = []
  for (const m of src.matchAll(/[:|]\s*"([^"]{1,60})"/g)) {
    // 源码里是 转义形式，归一成真实字符再和字典比对
    const s = m[1].replace(/\\u2026/g, '…')
    if (/[<>{}$]/.test(s)) continue // 模板 / svg 图标
    if (s.includes('\n')) continue // 跨行匹配的代码碎片
    if (s.startsWith('\\')) continue // 转义字符（如 ⌫ ⬇）
    if (s.startsWith('@') || s.startsWith('Arrow')) continue // 包名 / 方向键名
    if (/^\d/.test(s)) continue // 数字与尺寸（"0"、"200px"）
    if (s.includes('Mod-')) continue // 快捷键名
    if (/^[a-z]/.test(s) && s.includes('-')) continue // css 类名
    if (/^[a-z][a-zA-Z]*$/.test(s) && !s.includes(' ')) continue // 小写标识符
    out.push(s)
  }
  return out
}

function collectCandidates(): string[] {
  const files = readdirSync(CREPE_FEATURE_DIR)
    .map((d) => join(CREPE_FEATURE_DIR, d, 'index.js'))
    .concat(EXTRA_FILES)
  const all = new Set<string>()
  for (const f of files) {
    for (const s of extractCandidates(readFileSync(f, 'utf8'))) all.add(s)
  }
  return [...all].sort()
}

describe('i18n 覆盖守护', () => {
  it('内核全部 UI 默认文案都有 zh-Hans 翻译（或被显式豁免）', () => {
    const missing = collectCandidates().filter(
      (s) => !(s in zhHans) && !IGNORE.has(s),
    )
    expect(missing).toEqual([])
  })
})

describe('createTranslator', () => {
  it('zh 系 locale 归一化到 zh-Hans', () => {
    for (const locale of ['zh-Hans', 'zh-CN', 'zh', 'zh-hans']) {
      expect(createTranslator(locale)('Text')).toBe('文本')
    }
  })

  it('无字典的 locale 原样返回英文', () => {
    expect(createTranslator('en')('Text')).toBe('Text')
    expect(createTranslator('fr')('Text')).toBe('Text')
  })

  it('字典未命中时回退英文原文', () => {
    expect(createTranslator('zh-Hans')('Something New')).toBe('Something New')
  })
})
