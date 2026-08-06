// rendering/ 聚合出口：editor.ts 装配处统一挂这一个扩展。

import type { Extension } from '@codemirror/state'
import { blockImagesExtension } from './block-images'
import { checkboxExtension, completedTaskLineExtension } from './checkbox'
import { formatMarksExtension } from './format-marks'
import { linksExtension } from './links'

export function renderingExtension(): Extension {
  return [
    formatMarksExtension(),
    linksExtension(),
    checkboxExtension(),
    completedTaskLineExtension(),
    blockImagesExtension(),
  ]
}
