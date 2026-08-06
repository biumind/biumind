// Mermaid 段渲染 — 包一层 padding 然后委托给现成 MermaidPreview。
//
// fence 未闭合时降级为 code 显示 (避免每个 frame 都向 mermaid.ink
// 发请求被 rate-limit; 等内容稳定再渲染图)。

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import '../../../wiki/presentation/mermaid/mermaid_preview.dart';
import 'code_segment_view.dart';

class MermaidSegmentView extends StatelessWidget {
  const MermaidSegmentView({
    super.key,
    required this.source,
    required this.closed,
  });

  final String source;
  final bool closed;

  @override
  Widget build(BuildContext context) {
    if (!closed) {
      return CodeSegmentView(language: 'mermaid', code: source, closed: false);
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      child: MermaidPreview(source: source),
    );
  }
}
