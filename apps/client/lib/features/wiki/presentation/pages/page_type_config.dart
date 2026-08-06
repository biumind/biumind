/// Visual config for grouping pages by `page_type` in the project
/// browser. Mirrors `TYPE_CONFIG` from llm_wiki's knowledge-tree so
/// users coming from the original Tauri client see the same colors,
/// icons, and ordering.
///
/// 标签当前用硬编码中文（B0/B1 决策：l10n 跟模块迁完后统一补）。
/// 后续在 app_localizations 加 pageTypeOverview/Entity/Concept/...
/// 后把 labelOf 改回 `(t) => t.pageTypeOverview` 形态。
library;

import 'package:flutter/material.dart';

import '../../../../app/theme/category_colors.dart';

class PageTypeConfig {
  const PageTypeConfig({
    required this.icon,
    required this.color,
    required this.order,
    required this.label,
  });

  final IconData icon;
  final Color color;
  final int order;
  final String label;
}

const _kOverview = PageTypeConfig(
  icon: Icons.dashboard_outlined,
  color: NamedPalette.yellow,
  order: 0,
  label: '概览',
);
const _kEntity = PageTypeConfig(
  icon: Icons.people_outline,
  color: NamedPalette.blue,
  order: 1,
  label: '实体',
);
const _kConcept = PageTypeConfig(
  icon: Icons.lightbulb_outline,
  color: NamedPalette.purple,
  order: 2,
  label: '概念',
);
const _kSource = PageTypeConfig(
  icon: Icons.menu_book_outlined,
  color: NamedPalette.orange,
  order: 3,
  label: '来源',
);
const _kSynthesis = PageTypeConfig(
  icon: Icons.merge_type,
  color: NamedPalette.red,
  order: 4,
  label: '综合',
);
const _kComparison = PageTypeConfig(
  icon: Icons.compare_arrows,
  color: NamedPalette.emerald,
  order: 5,
  label: '对比',
);
const _kQuery = PageTypeConfig(
  icon: Icons.help_outline,
  color: NamedPalette.green,
  order: 6,
  label: '问题',
);
const _kOther = PageTypeConfig(
  icon: Icons.description_outlined,
  color: WikiPageTypeColors.other,
  order: 99,
  label: '其他',
);

const Map<String, PageTypeConfig> _byType = <String, PageTypeConfig>{
  'overview': _kOverview,
  'entity': _kEntity,
  'concept': _kConcept,
  'source': _kSource,
  'synthesis': _kSynthesis,
  'comparison': _kComparison,
  'query': _kQuery,
};

PageTypeConfig pageTypeConfigOf(String? type) {
  if (type == null) return _kOther;
  return _byType[type.toLowerCase()] ?? _kOther;
}

/// Types expanded by default when no per-project preference is saved.
const Set<String> kDefaultExpandedPageTypes = <String>{
  'overview',
  'entity',
  'concept',
  'source',
};
