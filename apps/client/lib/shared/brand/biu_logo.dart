// BiuMind 横版 logo — mark + "BiuMind" 文字。
// 字号 / 间距按 size 档位预设，与 web/site Logo.astro 视觉对齐。

import 'package:flutter/material.dart';

import 'biu_mark.dart';

enum BiuLogoSize {
  small,   // 紧凑入口 (compact rail / icon bar): 20px mark + 13px 字
  sidebar, // 主 sidebar 顶 brand: 32px mark + 15px 字 (prototype `.brand`)
  medium,  // settings / about: 32px mark + 18px 字
  large,   // login brand: 56px mark + 22px 字
  xlarge,  // splash: 96px mark + 28px 字
}

/// BiuMind 横版 logo（mark + 文字 "BiuMind"）。
class BiuLogo extends StatelessWidget {
  const BiuLogo({
    super.key,
    this.size = BiuLogoSize.medium,
    this.onlyMark = false,
    this.inkColor,
    this.circuitColor,
    this.spacing,
  });

  final BiuLogoSize size;
  final bool onlyMark;

  /// 头 + 手 + 文字颜色（null = 跟随 DefaultTextStyle）。
  final Color? inkColor;

  /// 电路色（null = 品牌紫）。
  final Color? circuitColor;

  /// 自定义 mark 与文字之间的间距，null = 用 size 档位默认值。
  final double? spacing;

  ({double mark, double font, double gap}) _spec() => switch (size) {
        BiuLogoSize.small => (mark: 20, font: 13, gap: 8),
        BiuLogoSize.sidebar => (mark: 32, font: 15, gap: 10),
        BiuLogoSize.medium => (mark: 32, font: 18, gap: 10),
        BiuLogoSize.large => (mark: 56, font: 22, gap: 12),
        BiuLogoSize.xlarge => (mark: 96, font: 28, gap: 14),
      };

  @override
  Widget build(BuildContext context) {
    final spec = _spec();
    final mark = BiuMark(
      size: spec.mark,
      inkColor: inkColor,
      circuitColor: circuitColor,
    );
    if (onlyMark) return mark;

    final ink = inkColor ?? DefaultTextStyle.of(context).style.color;

    return Row(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        mark,
        SizedBox(width: spacing ?? spec.gap),
        Text(
          'BiuMind',
          style: TextStyle(
            fontSize: spec.font,
            // sidebar 档跟 prototype `.brand .name { weight 700;
            //   letter-spacing -0.02em }` 对齐;其它档保持 w600 /-0.6。
            fontWeight: size == BiuLogoSize.sidebar
                ? FontWeight.w700
                : FontWeight.w600,
            letterSpacing: size == BiuLogoSize.sidebar
                ? -spec.font * 0.02
                : -0.6,
            color: ink,
            height: 1.0,
          ),
        ),
      ],
    );
  }
}
