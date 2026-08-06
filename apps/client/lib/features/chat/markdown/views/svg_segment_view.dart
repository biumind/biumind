// SVG 段渲染 — flutter_svg 的 SvgPicture.string.
//
// 容错: parse 失败时 fallback 到 code 显示 (用户能看到原码, 也方便复制
// 到外面调试)。流式 closed=false 时同样 fallback。

import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../../../../app/theme.dart';
import 'code_segment_view.dart';

class SvgSegmentView extends StatelessWidget {
  const SvgSegmentView({
    super.key,
    required this.svg,
    required this.closed,
  });

  final String svg;
  final bool closed;

  @override
  Widget build(BuildContext context) {
    if (!closed) {
      return CodeSegmentView(language: 'svg', code: svg, closed: false);
    }
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      alignment: Alignment.center,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxHeight: 480),
        child: SvgPicture.string(
          svg,
          fit: BoxFit.contain,
          placeholderBuilder: (_) => const Padding(
            padding: EdgeInsets.all(BiuTokens.space3),
            child: SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 1.5),
            ),
          ),
        ),
      ),
    );
  }
}
