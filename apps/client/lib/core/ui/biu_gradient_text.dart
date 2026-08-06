// BiuGradientText — Hero 标题的渐变文字,等价 prototype:
//
//   .hero h1 {
//     background: var(--hero-grad);
//     -webkit-background-clip: text;
//     background-clip: text;
//     color: transparent;
//   }
//
// CSS 是把背景渐变"裁"到文字 alpha 通道。Flutter 用 ShaderMask + BlendMode.srcIn
// 等价实现:Shader 渲染整块,然后只保留跟文字 alpha 重合的部分。
//
// 渐变取自当前色板的 brandGradientFor(brightness),跟 hero brand mark 一致 —
// "夜深了" 这类标题文字跟左侧 logo 是同色族渐变,跟 prototype 完全对齐。
//
// 用法:
//   BiuGradientText(
//     '夜深了',
//     style: theme.textTheme.headlineMedium,
//   )

import 'package:flutter/material.dart';

import '../../app/theme/palettes.dart';

class BiuGradientText extends StatelessWidget {
  const BiuGradientText(
    this.text, {
    super.key,
    required this.palette,
    this.style,
    this.maxLines,
    this.overflow,
    this.textAlign,
  });

  final String text;
  final PaletteId palette;
  final TextStyle? style;
  final int? maxLines;
  final TextOverflow? overflow;
  final TextAlign? textAlign;

  @override
  Widget build(BuildContext context) {
    final brightness = Theme.of(context).brightness;
    final spec = paletteSpecOf(palette);
    final colors = spec.brandGradientFor(brightness);

    // 文字本身要给 ShaderMask 一个非透明的 base color, BlendMode.srcIn 才能拿到
    // alpha mask;color 字段会被 shader 完全覆盖,但不能为 transparent。
    final baseStyle = (style ?? const TextStyle()).copyWith(color: Colors.white);

    return ShaderMask(
      blendMode: BlendMode.srcIn,
      shaderCallback: (rect) => LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: colors,
        stops: colors.length == 3 ? const [0.0, 0.6, 1.0] : null,
      ).createShader(rect),
      child: Text(
        text,
        style: baseStyle,
        maxLines: maxLines,
        overflow: overflow,
        textAlign: textAlign,
      ),
    );
  }
}
