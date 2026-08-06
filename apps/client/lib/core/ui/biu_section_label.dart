// BiuSectionLabel — prototype `.section-h` 等价的分组小标题。
//
// 对应 prototype CSS:
//   .section-h {
//     font-size: 12px;
//     font-weight: 600;
//     color: var(--text-3);
//     text-transform: uppercase;
//     letter-spacing: 0.08em;
//     margin: var(--gap-section) 0 12px;
//   }
//
// 中文不能 uppercase,但 letter-spacing + 字号 + 灰阶 仍能产生"克制小标题"的
// 节奏感。英文/拼音字符自动 toUpperCase()。
//
// 用法:
//   BiuSectionLabel('我的技能')
//   BiuSectionLabel('Recent', uppercase: true)  // 英文场景

import 'package:flutter/material.dart';

import '../../app/theme/extensions.dart';

class BiuSectionLabel extends StatelessWidget {
  const BiuSectionLabel(
    this.text, {
    super.key,
    this.uppercase,
    this.padding = const EdgeInsets.only(top: 24, bottom: 12),
  });

  final String text;

  /// null = 自动判断(纯 ASCII 自动 uppercase,中文等多字节字符保留原样)。
  final bool? uppercase;

  /// 上下间距 — prototype 是 `margin: gap-section 0 12px`,默认顶 24 底 12。
  final EdgeInsetsGeometry padding;

  bool get _isAscii =>
      text.codeUnits.every((c) => c < 128);

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final color = c?.text3 ?? theme.colorScheme.onSurfaceVariant;

    final shouldUpper = uppercase ?? _isAscii;
    final display = shouldUpper ? text.toUpperCase() : text;

    return Padding(
      padding: padding,
      child: Text(
        display,
        style: TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w600,
          color: color,
          letterSpacing: 0.08 * 12, // CSS letter-spacing: 0.08em → 0.96px
          height: 1.2,
        ),
      ),
    );
  }
}
