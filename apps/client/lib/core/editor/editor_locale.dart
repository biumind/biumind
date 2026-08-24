/// 编辑器 UI 语言解析：自绘右键菜单 / crepe 文案跟随 App 内语言设置。
///
/// 规则（设计文档 BiuMind-Editor-ContextMenu-Design.md §7）：
///   1. chat v2 偏好里有 localeOverride（设置 > 外观的语言覆盖）就用它；
///   2. 否则跟系统 locale；
///   3. zh 系（zh / zh-CN / zh-Hans…）→ 'zh-Hans'，其余 → 'en'
///      （editor-web 目前只有 zh-Hans 一本字典，英文是 msgid 原文直通）。
library;

import 'dart:ui' show PlatformDispatcher;

String resolveEditorLocale(String? localeOverride) {
  final raw = (localeOverride != null && localeOverride.isNotEmpty)
      ? localeOverride
      : PlatformDispatcher.instance.locale.toLanguageTag();
  final lower = raw.toLowerCase();
  if (lower == 'zh' || lower.startsWith('zh-')) return 'zh-Hans';
  return 'en';
}
