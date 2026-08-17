// 编辑器 UI 文案本地化 —— Joplin 思路在 Crepe 约束下的等价物：
//   * 字典扁平化，key = crepe 源码英文原文（msgid），与 crepe config 结构解耦；
//   * 本文件是唯一知道 crepe featureConfigs 结构的地方（mapper），crepe 升级
//     重构 config 形状时只改这里，翻译数据不动；
//   * 未命中回退英文原文（增量补全安全）。
// locale 由宿主经 bridge init payload 传入（editor_bridge_controller.dart
// 默认 zh-Hans），init 时一次性解析，无运行时 IPC。
// 覆盖完整性由 tests/i18n-coverage.spec.ts 守护：crepe 升级新增 UI 文案时
// 测试会红，防止英文悄悄漏回。

import { Crepe } from '@milkdown/crepe'
import type { CrepeConfig } from '@milkdown/crepe'

import { zhHans } from './locales/zh-hans'

// CrepeFeatureConfig 未从包根导出，经 CrepeConfig 索引访问拿到同一类型。
type FeatureConfigs = NonNullable<CrepeConfig['featureConfigs']>

const LOCALES: Record<string, Record<string, string>> = {
  'zh-Hans': zhHans,
}

/** 归一化宿主 locale：zh / zh-CN / zh-Hans / zh-Hans-CN … 都落到 zh-Hans。 */
function normalizeLocale(locale: string): string | null {
  const lower = locale.toLowerCase()
  if (lower === 'zh' || lower.startsWith('zh-hans') || lower === 'zh-cn') {
    return 'zh-Hans'
  }
  return LOCALES[locale] ? locale : null
}

/** 返回翻译函数；locale 无字典时返回恒等函数（英文原文直通）。 */
export function createTranslator(locale: string): (s: string) => string {
  const dict = LOCALES[normalizeLocale(locale) ?? '']
  if (!dict) return (s) => s
  return (s) => dict[s] ?? s
}

/** 字典 → crepe featureConfigs。英文 locale 返回空对象（不动 crepe 默认值）。 */
export function buildLocalizedFeatureConfigs(locale: string): FeatureConfigs {
  if (!normalizeLocale(locale)) return {}
  const t = createTranslator(locale)
  return {
    [Crepe.Feature.BlockEdit]: {
      textGroup: {
        label: t('Text'),
        text: { label: t('Text') },
        h1: { label: t('Heading 1') },
        h2: { label: t('Heading 2') },
        h3: { label: t('Heading 3') },
        h4: { label: t('Heading 4') },
        h5: { label: t('Heading 5') },
        h6: { label: t('Heading 6') },
        quote: { label: t('Quote') },
        divider: { label: t('Divider') },
      },
      listGroup: {
        label: t('List'),
        bulletList: { label: t('Bullet List') },
        orderedList: { label: t('Ordered List') },
        taskList: { label: t('Task List') },
      },
      advancedGroup: {
        label: t('Advanced'),
        image: { label: t('Image') },
        codeBlock: { label: t('Code') },
        table: { label: t('Table') },
        math: { label: t('Math') },
      },
    },
    [Crepe.Feature.Placeholder]: {
      text: t('Please enter...'),
    },
    [Crepe.Feature.LinkTooltip]: {
      inputPlaceholder: t('Paste link...'),
    },
    // 上传/加载行为（onUpload、presign 换 URL 等）由 main.ts 合并时覆盖，
    // 这里只出文案字段。
    [Crepe.Feature.ImageBlock]: {
      inlineUploadButton: t('Upload'),
      inlineUploadPlaceholderText: t('or paste link'),
      blockUploadButton: t('Upload file'),
      blockConfirmButton: t('Confirm'),
      blockCaptionPlaceholderText: t('Write Image Caption'),
      blockUploadPlaceholderText: t('or paste link'),
    },
    [Crepe.Feature.CodeMirror]: {
      searchPlaceholder: t('Search language'),
      copyText: t('Copy'),
      noResultText: t('No result'),
      previewLabel: t('Preview'),
      previewLoading: t('Loading...'),
      // 与 crepe 默认同语义：预览独占模式按钮显示「编辑」，否则「隐藏」。
      previewToggleText: (previewOnlyMode: boolean) =>
        previewOnlyMode ? t('Edit') : t('Hide'),
    },
    [Crepe.Feature.Toolbar]: {
      boldLabel: t('Bold'),
      italicLabel: t('Italic'),
      strikethroughLabel: t('Strikethrough'),
      codeLabel: t('Inline code'),
      latexLabel: t('Inline math'),
      linkLabel: t('Link'),
    },
  }
}
