// HTML 段渲染 — flutter_widget_from_html_core 沙箱化展示。
//
// 我们 _不_ 跑 JS, 不加载外部资源 (除 img + http link); 模型生成的
// HTML 只当展示用, 不当应用执行。库本身就是这种行为, 默认安全。
//
// 流式 (closed=false): 退到 code 显示, 避免半截 HTML 解析出怪布局。

import 'package:flutter/material.dart';
import 'package:flutter_widget_from_html_core/flutter_widget_from_html_core.dart';

import '../../../../app/theme.dart';
import 'code_segment_view.dart';

class HtmlSegmentView extends StatelessWidget {
  const HtmlSegmentView({
    super.key,
    required this.html,
    required this.closed,
  });

  final String html;
  final bool closed;

  @override
  Widget build(BuildContext context) {
    if (!closed) {
      return CodeSegmentView(language: 'html', code: html, closed: false);
    }
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: HtmlWidget(
        html,
        textStyle: TextStyle(
          color: BiuTokens.text,
          fontSize: 14,
          height: 1.6,
        ),
      ),
    );
  }
}
