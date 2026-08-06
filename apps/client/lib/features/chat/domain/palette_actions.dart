// PaletteAction —— Cmd+K 命令面板的一条可执行动作。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 命令面板）。
//
// 面向 UI 的 view-model（持有 run 闭包，本就耦合表现层）；过滤算法
// filterPaletteActions 只读 label/id，不碰 icon，纯逻辑可单测不受影响。
// icon 直接存 const IconData —— Flutter web release 的图标 tree-shaking
// 需要编译期可分析的 const IconData，运行时 IconData(codePoint) 会让
// `flutter build web --release` 失败（Avoid non-constant invocations）。

import 'package:flutter/widgets.dart';

class PaletteAction {
  const PaletteAction({
    required this.id,
    required this.label,
    required this.run,
    this.hint,
    this.icon,
    this.group,
  });

  /// 稳定 ID，让过滤前缀 / 测试断言可用。
  final String id;
  final String label;
  final String? hint;
  /// const IconData（如 Icons.add）—— 直接存对象以保 web tree-shaking。
  final IconData? icon;
  /// 分组名（"对话操作" / "切换对话" / "其他"）。null 走默认。
  final String? group;
  /// 选中后执行的副作用。
  final void Function() run;
}

/// 模糊匹配：query 大小写不敏感，要求 label 或 id 包含 query 的所有字符
/// 顺序（不要求连续，子序列匹配 —— 比 startsWith 更宽松）。
/// 空 query → 全部返回。
List<PaletteAction> filterPaletteActions(
  List<PaletteAction> all,
  String query,
) {
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return all;
  return all
      .where((a) =>
          _subseq(a.label.toLowerCase(), q) ||
          _subseq(a.id.toLowerCase(), q))
      .toList(growable: false);
}

/// "abc" 子序列匹配：text 中存在 a, b, c 顺序出现（不要求连续）。
bool _subseq(String text, String pattern) {
  if (pattern.isEmpty) return true;
  var i = 0;
  for (var j = 0; j < text.length && i < pattern.length; j++) {
    if (text.codeUnitAt(j) == pattern.codeUnitAt(i)) i++;
  }
  return i == pattern.length;
}
