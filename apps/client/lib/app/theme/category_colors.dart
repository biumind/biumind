// Category & 3rd-party brand colors — 跟用户色板**无关**的"分类色"。
//
// 这层跟 BiuColors / BiuBrand 都不同:
//   * BiuColors  — 跟用户色板切的业务色 (brand / accent / banner...)
//   * BiuBrand   — 永久 BiuMind 品牌色 (logo gradient,分享卡)
//   * CategoryColors — 用来"区分类别"的色板,跟主题完全无关
//
// 用途:
//   * LLM provider 头像 (Anthropic 橘红 / OpenAI 绿 — 是它们家的官方品牌色,
//     用户切色板不应该让 OpenAI 头像变成紫色)
//   * 技能 tile 哈希分配 (8 色循环,基于 identifier 哈希)
//   * 知识图谱节点 (按类型 / 主题分色)
//
// 加新分类色规则:
//   * 是某个第三方厂商的官方色 → 加到 ProviderBrand
//   * 是 N 选 1 的循环分配 → 加到 CategoryPalette
//   * 是图谱 / 图表的语义节点 → 加到 GraphPalette

import 'package:flutter/material.dart';

// ═════════════════════════════════════════════════════════════════════════
// 第三方 LLM provider 品牌色
// ═════════════════════════════════════════════════════════════════════════

class ProviderBrand {
  ProviderBrand._();

  /// Anthropic Claude — 官方 warm orange (anthropic.com header)
  static const Color anthropic = Color(0xFFD97757);

  /// OpenAI — 官方品牌绿
  static const Color openai = Color(0xFF10A37F);

  /// Google Gemini — Material Indigo 500 近似
  static const Color google = Color(0xFF4285F4);

  /// DeepSeek — 紫蓝
  static const Color deepseek = Color(0xFF536DFE);

  /// Qwen / 通义 — 阿里橙
  static const Color qwen = Color(0xFFFF7A00);

  /// Ollama — 灰黑(图标 outline)
  static const Color ollama = Color(0xFF1F2937);

  /// Azure — Microsoft 蓝
  static const Color azure = Color(0xFF0078D4);
}

// ═════════════════════════════════════════════════════════════════════════
// 分类调色板 — 8 色循环,基于哈希分配给 skill / category 用
// ═════════════════════════════════════════════════════════════════════════

class CategoryPalette {
  CategoryPalette._();

  /// 8 色循环。色调均匀分布,每两色对比度高,排序按色环走。
  static const List<Color> rotation = [
    Color(0xFF7C5CFF), // purple
    Color(0xFF3B82F6), // blue
    Color(0xFF06B6D4), // cyan
    Color(0xFF10B981), // emerald
    Color(0xFFEAB308), // amber
    Color(0xFFF97316), // orange
    Color(0xFFEC4899), // pink
    Color(0xFFEF4444), // red
  ];

  /// 按字符串哈希取色(确定性 — 同一 seed 永远拿同一色)。
  static Color colorFor(String seed) {
    if (seed.isEmpty) return rotation.first;
    var h = 0;
    for (final c in seed.codeUnits) {
      h = (h * 31 + c) & 0x7fffffff;
    }
    return rotation[h % rotation.length];
  }
}

// ═════════════════════════════════════════════════════════════════════════
// 图谱节点 / 图表色 — 按类型语义分色
// ═════════════════════════════════════════════════════════════════════════

// ═════════════════════════════════════════════════════════════════════════
// 命名色板 — Tailwind-style 22 色查表(用户给数据打标签时的可选色名)
// ═════════════════════════════════════════════════════════════════════════
//
// 用户在 RSS feed / page tag 等地方可以指定 'red' / 'sky' / 'emerald' 等
// 颜色名,这层负责"色名 → Color"。值来自 Tailwind v3 的 -500 档。
//
// 为什么不用 BiuColors:
//   * 这是用户**直接选**的颜色 (你给 feed 标记 'red' 就是要红色)
//   * 跟用户主题色板互不相干 (用户主题切到墨蓝,'red' tag 仍然是红)

class NamedPalette {
  NamedPalette._();

  static const Color red       = Color(0xFFEF4444);
  static const Color orange    = Color(0xFFF97316);
  static const Color amber     = Color(0xFFF59E0B);
  static const Color yellow    = Color(0xFFEAB308);
  static const Color lime      = Color(0xFF84CC16);
  static const Color green     = Color(0xFF22C55E);
  static const Color emerald   = Color(0xFF10B981);
  static const Color teal      = Color(0xFF14B8A6);
  static const Color cyan      = Color(0xFF06B6D4);
  static const Color sky       = Color(0xFF0EA5E9);
  static const Color blue      = Color(0xFF3B82F6);
  static const Color indigo    = Color(0xFF6366F1);
  static const Color violet    = Color(0xFF8B5CF6);
  static const Color purple    = Color(0xFFA855F7);
  static const Color fuchsia   = Color(0xFFD946EF);
  static const Color pink      = Color(0xFFEC4899);
  static const Color rose      = Color(0xFFF43F5E);
  static const Color slate     = Color(0xFF64748B);
  static const Color gray      = Color(0xFF6B7280);
  static const Color zinc      = Color(0xFF71717A);
  static const Color neutral   = Color(0xFF737373);
  static const Color stone     = Color(0xFF78716C);

  /// 名 → 色查表。未识别返回 null,调用方决定 fallback。
  static Color? byName(String name) => switch (name) {
        'red' => red,
        'orange' => orange,
        'amber' => amber,
        'yellow' => yellow,
        'lime' => lime,
        'green' => green,
        'emerald' => emerald,
        'teal' => teal,
        'cyan' => cyan,
        'sky' => sky,
        'blue' => blue,
        'indigo' => indigo,
        'violet' => violet,
        'purple' => purple,
        'fuchsia' => fuchsia,
        'pink' => pink,
        'rose' => rose,
        'slate' => slate,
        'gray' || 'grey' => gray,
        'zinc' => zinc,
        'neutral' => neutral,
        'stone' => stone,
        _ => null,
      };
}

// ═════════════════════════════════════════════════════════════════════════
// Callout 色对 — banner / 通知 inline 用的"bg + icon + text"三色组
// ═════════════════════════════════════════════════════════════════════════
//
// 跟 BiuColors.warning/info 不同 — 这里是手调过的强对比 callout 色,用于:
//   * 警告条 (第三方技能提示 / API 限流警示 / 实验性功能横幅)
//   * 信息条 (版本变更 / 引导 / context 解释)
//
// 加新 callout: 复用现有,实在不行加新 class — 不要在业务代码硬编码 hex。

class WarningCallout {
  WarningCallout._();
  static const Color bg     = Color(0xFFFEF3C7); // amber-100
  static const Color iconFg = Color(0xFFB45309); // amber-700
  static const Color textFg = Color(0xFF92400E); // amber-800
}

class IndigoCallout {
  IndigoCallout._();
  static const Color bg         = Color(0xFFEEF2FF); // indigo-50
  static const Color border     = Color(0xFFC7D2FE); // indigo-200
  static const Color iconFg     = Color(0xFF4338CA); // indigo-700
  static const Color titleFg    = Color(0xFF312E81); // indigo-900
  static const Color subtitleFg = Color(0xFF4F46E5); // indigo-600
}

// ═════════════════════════════════════════════════════════════════════════
// Tailwind v3 -600 强变体 (项目里部分 chip 用的"略深一档")
// ═════════════════════════════════════════════════════════════════════════
//
// 跟 NamedPalette (-500) 平行,用在需要"saturated/可读性优先"的场景
// (frontmatter type chip / origin callout 等)。

class NamedPaletteStrong {
  NamedPaletteStrong._();

  static const Color blue    = Color(0xFF2563EB);
  static const Color purple  = Color(0xFF7C3AED);
  static const Color emerald = Color(0xFF059669);
  static const Color red     = Color(0xFFDC2626);
  static const Color amber   = Color(0xFFB45309);
  static const Color amberMid = Color(0xFFD97706); // amber-600 — 比 amber-700 浅一档
  static const Color cyan    = Color(0xFF0891B2);
  static const Color orange  = Color(0xFFEA580C); // orange-600
}

// ═════════════════════════════════════════════════════════════════════════
// 优先级色 — RSS 文章 / 任务的紧急度标识
// ═════════════════════════════════════════════════════════════════════════
//
// 三档:high (红) / medium (黄) / low (蓝)。跟 review status 的 dedup 等
// 不同 — 这里强调"重要性梯度",不是"任务类型"。

class PriorityColors {
  PriorityColors._();
  static const Color high   = Color(0xFFDC2626); // red-600
  static const Color medium = Color(0xFFF59E0B); // amber-500
  static const Color low    = Color(0xFF3B82F6); // blue-500
}

// ═════════════════════════════════════════════════════════════════════════
// 收藏 / Star 色 — 黄星固定色系
// ═════════════════════════════════════════════════════════════════════════
//
// 用户给消息 / 文章打"星标"用的固定黄色;不跟主题切。

class StarredColors {
  StarredColors._();

  static const Color icon       = Color(0xFFFFB400); // 主黄(图标)
  static const Color iconAlt    = Color(0xFFEAB308); // 替代黄(列表/RSS)
  static const Color highlight  = Color(0xFFFFF59D); // 黄底高亮
  static const Color textOnHighlight = Color(0xFF6B5B00); // 黄底上深字
  static const Color lintGold   = Color(0xFFD29922); // lint warning 暗金
}

// ═════════════════════════════════════════════════════════════════════════
// AgentKind 色 — Claude Code / Codex CLI 等本地 agent 标识色
// ═════════════════════════════════════════════════════════════════════════
//
// 跟 ProviderBrand 不同 — 后者是 LLM 厂商品牌色,这里是"本地 agent CLI"
// 区分色,跟具体 agent 工具产品的视觉标识对齐(略浅一档,适配 chip 用)。

class AgentKindColors {
  AgentKindColors._();

  static const Color claude = Color(0xFFE07A3E); // 比 ProviderBrand.anthropic 浅
  static const Color codex  = Color(0xFF10A37F); // = ProviderBrand.openai
}

// ═════════════════════════════════════════════════════════════════════════
// Wiki page-type 色 — 7 种页面类型的语义色
// ═════════════════════════════════════════════════════════════════════════

// ═════════════════════════════════════════════════════════════════════════
// Starter prompt 卡片色 — Hero 起点卡 6 个 tone (pastel)
// ═════════════════════════════════════════════════════════════════════════

class StarterPromptTones {
  StarterPromptTones._();

  static const Color writing   = Color(0xFFB388FF); // 紫粉 — 文案
  static const Color concept   = Color(0xFFFFD180); // 杏黄 — 概念
  static const Color code      = Color(0xFF80D8FF); // 浅蓝 — 代码
  static const Color translate = Color(0xFFA7FFEB); // 薄荷 — 翻译
  static const Color summarize = Color(0xFFFF8A80); // 珊瑚 — 总结
  static const Color debug     = Color(0xFFCCFF90); // 浅青柠 — debug
}

class WikiPageTypeColors {
  WikiPageTypeColors._();

  static const Color note     = NamedPalette.yellow;
  static const Color spec     = NamedPalette.blue;
  static const Color research = NamedPalette.purple;
  static const Color decision = NamedPalette.orange;
  static const Color incident = NamedPalette.red;
  static const Color howto    = NamedPalette.emerald;
  static const Color reference = NamedPalette.green;
  static const Color other    = Color(0xFF94A3B8); // slate-400
}

// ═════════════════════════════════════════════════════════════════════════
// Wiki review-status 色 — 4 种清理任务状态
// ═════════════════════════════════════════════════════════════════════════
//
// 每个状态有一对 bg + fg,用于 chip / banner / tile。
// fg 是手调过的"在 bg 上 ≥ 4.5:1 对比度"的深色对应。

// ═════════════════════════════════════════════════════════════════════════
// 技能来源标 — bg + fg 配对色 (5 个 source: bundled/org/marketplace/imported/user)
// ═════════════════════════════════════════════════════════════════════════

class SkillSourceBadge {
  SkillSourceBadge._();

  // bundled — 紫
  static const Color bundledBg = Color(0xFFEDE9FE);
  static const Color bundledFg = Color(0xFF6D28D9);

  // org — 蓝
  static const Color orgBg = Color(0xFFDBEAFE);
  static const Color orgFg = Color(0xFF1D4ED8);

  // marketplace — 橙
  static const Color marketplaceBg = Color(0xFFFFEDD5);
  static const Color marketplaceFg = Color(0xFFC2410C);

  // imported — 青
  static const Color importedBg = Color(0xFFCFFAFE);
  static const Color importedFg = Color(0xFF0E7490);

  // user — 灰
  static const Color userBg = Color(0xFFE5E7EB);
  static const Color userFg = Color(0xFF374151);
}

// ═════════════════════════════════════════════════════════════════════════
// 搜索结果来源标 — bg + fg 配对色
// ═════════════════════════════════════════════════════════════════════════

class SearchSourceBadge {
  SearchSourceBadge._();

  // wiki / BM25 — 蓝
  static const Color wikiBg = Color(0xFFE3F2FD);
  static const Color wikiFg = Color(0xFF014361);

  // vector — 紫
  static const Color vectorBg = Color(0xFFEEDCFA);
  static const Color vectorFg = Color(0xFF4A1B7E);

  // graph — 绿
  static const Color graphBg = Color(0xFFC8E6C9);
  static const Color graphFg = Color(0xFF1B5E20);

  // web — 橙
  static const Color webBg = Color(0xFFFFE0B2);
  static const Color webFg = Color(0xFF8D4900);

  // 默认
  static const Color otherBg = Color(0xFFE0E0E0);
  static const Color otherFg = Color(0xFF424242);

  // 关键词高亮 (黄)
  static const Color highlightBg = Color(0xFFFFF3B0);
}

class WikiReviewStatus {
  WikiReviewStatus._();

  // bg (浅色填充)
  static const Color dedupBg = Color(0xFFFFE0B2); // 浅琥珀
  static const Color lintBg  = Color(0xFFFFCDD2); // 浅红
  static const Color sweepBg = Color(0xFFC8E6C9); // 浅绿
  static const Color mergeBg = Color(0xFFB3E5FC); // 浅蓝
  static const Color otherBg = Color(0xFFE0E0E0);
  static const Color contradictionBg = Color(0xFFFFCCBC); // 浅橙红 — 跨页矛盾冲突警告

  // fg (深色文字,对应 bg 上 ≥ 4.5:1)
  static const Color dedupFg = Color(0xFF8D4900);
  static const Color lintFg  = Color(0xFF7A1F1F);
  static const Color sweepFg = Color(0xFF1B5E20);
  static const Color mergeFg = Color(0xFF014361);
  static const Color otherFg = Color(0xFF424242);
  static const Color contradictionFg = Color(0xFFBF360C); // 深橙红 — contradictionBg 上 ≥4.5:1
}

// ═════════════════════════════════════════════════════════════════════════
// 知识图谱主题色板 — 4 套配色,12 色循环
// ═════════════════════════════════════════════════════════════════════════
//
// 用户在项目知识图谱页可切换观感,跟主题色板正交(图谱节点不应跟随用户
// 选的色板,否则切到 onyx 主题图谱节点全黑)。

class GraphTheme {
  const GraphTheme({
    required this.id,
    required this.name,
    required this.icon,
    required this.palette,
  });

  final String id;
  final String name;
  final IconData icon;
  final List<Color> palette;

  static const cosmic = GraphTheme(
    id: 'cosmic',
    name: 'Cosmic',
    icon: Icons.brightness_5_outlined,
    palette: [
      NamedPalette.yellow, NamedPalette.blue, NamedPalette.purple,
      NamedPalette.orange, NamedPalette.red, NamedPalette.emerald,
      NamedPalette.green, NamedPalette.teal, NamedPalette.pink,
      NamedPalette.indigo, NamedPalette.lime, NamedPalette.amber,
    ],
  );

  static const forest = GraphTheme(
    id: 'forest',
    name: 'Forest',
    icon: Icons.park_outlined,
    palette: [
      Color(0xFF22C55E), Color(0xFF15803D), Color(0xFF65A30D),
      Color(0xFF166534), Color(0xFF84CC16), Color(0xFF14B8A6),
      Color(0xFF16A34A), Color(0xFF0D9488), Color(0xFF4D7C0F),
      Color(0xFF059669), Color(0xFF10B981), Color(0xFFA3E635),
    ],
  );

  static const sunset = GraphTheme(
    id: 'sunset',
    name: 'Sunset',
    icon: Icons.wb_sunny_outlined,
    palette: [
      Color(0xFFF97316), Color(0xFFEF4444), Color(0xFFEC4899),
      Color(0xFFEAB308), Color(0xFFF59E0B), Color(0xFFDB2777),
      Color(0xFFC026D3), Color(0xFFE11D48), Color(0xFFEA580C),
      Color(0xFFD97706), Color(0xFFBE123C), Color(0xFF9333EA),
    ],
  );

  static const mono = GraphTheme(
    id: 'mono',
    name: 'Mono',
    icon: Icons.tonality,
    palette: [
      Color(0xFF374151), Color(0xFF4B5563), Color(0xFF6B7280),
      Color(0xFF9CA3AF), Color(0xFF1F2937), Color(0xFF111827),
      Color(0xFF374151), Color(0xFF4B5563), Color(0xFF6B7280),
      Color(0xFF9CA3AF), Color(0xFF1F2937), Color(0xFF111827),
    ],
  );

  static const all = [cosmic, forest, sunset, mono];
}

class GraphNodeColors {
  GraphNodeColors._();

  /// 节点类型语义色 (page / project / link / tag / external / unknown)
  static const Color page     = Color(0xFF7C3AED); // violet
  static const Color project  = Color(0xFF059669); // emerald
  static const Color link     = Color(0xFF2563EB); // blue
  static const Color tag      = Color(0xFFD97706); // amber
  static const Color external = Color(0xFF0EA5E9); // sky
  static const Color unknown  = Color(0xFF6B7280); // slate

  /// 边类型语义色
  static const Color edgeRef       = Color(0xFF3B82F6);
  static const Color edgeBacklink  = Color(0xFF10B981);
  static const Color edgeSimilar   = Color(0xFFF59E0B);
  static const Color edgeChild     = Color(0xFF8B5CF6);
  static const Color edgeTagged    = Color(0xFFEC4899);
  static const Color edgeUnknown   = Color(0xFF94A3B8);
}
