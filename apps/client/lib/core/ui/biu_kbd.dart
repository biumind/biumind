// BiuKbd — `<kbd>` 键盘按键样式的 inline pill。
//
// 对应 prototype CSS:
//   .tip-bar kbd {
//     background: var(--surf-0);
//     border: 1px solid var(--border-hairline);
//     border-radius: 4px;
//     padding: 1px 5px;
//     font-family: inherit;
//     font-size: 11px;
//     font-weight: 600;
//     color: var(--text-1);
//   }
//
// Flutter 没有 inline-block 概念,在 RichText 里用 WidgetSpan 嵌入 Container,
// 让按键 pill 像普通 inline 文字一样跟随行高排版。
//
// 用法 (RichText 内 inline):
//   TextSpan(children: [
//     TextSpan(text: '按 '),
//     BiuKbd.span('⌘ K'),
//     TextSpan(text: ' 召唤命令面板'),
//   ])
//
// 也支持作为独立 widget(脱离 RichText 时):
//   BiuKbd('⌘ K')

import 'package:flutter/material.dart';

import '../../app/theme/extensions.dart';

class BiuKbd extends StatelessWidget {
  const BiuKbd(this.label, {super.key});

  final String label;

  /// inline span 工厂 — 直接塞进 TextSpan.children。
  static InlineSpan span(String label) {
    return WidgetSpan(
      alignment: PlaceholderAlignment.middle,
      child: BiuKbd(label),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final cs = theme.colorScheme;
    final bg = c?.surface0 ?? cs.surface;
    final border = c?.borderHairline ?? cs.outlineVariant;
    final fg = c?.text1 ?? cs.onSurface;

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: border),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          color: fg,
          height: 1.2,
        ),
      ),
    );
  }
}
