// ChatMarkdownView — 顶层入口。message_bubble_v2 渲染每条消息时调一次。
//
// 数据流:
//   raw text → preNormalize → splitSegments → 每段一个 view widget
//
// 缓存策略 (双层, 见设计文档 §7):
//   * Pipeline level: State 缓存最近一次 (content → segments) 映射, content
//     不变直接复用 list — 流式同 frame 重 build 不重 split。
//   * Widget level: 每段 widget 用 ValueKey('seg-$order-$contentHash')
//     让 Flutter Element tree 自动 reuse 未变段, 长会话流式不抖。
//
// 不依赖 Riverpod/ref — 纯 widget; 调用方直接用就行。

import 'package:flutter/material.dart';

import 'pre_normalize.dart';
import 'segments.dart';
import 'split_segments.dart';
import 'views/code_segment_view.dart';
import 'views/html_segment_view.dart';
import 'views/markdown_segment_view.dart';
import 'views/math_segment_view.dart';
import 'views/mermaid_segment_view.dart';
import 'views/svg_segment_view.dart';

class ChatMarkdownView extends StatefulWidget {
  const ChatMarkdownView({super.key, required this.text});

  final String text;

  @override
  State<ChatMarkdownView> createState() => _ChatMarkdownViewState();
}

class _ChatMarkdownViewState extends State<ChatMarkdownView> {
  String? _lastInput;
  List<Segment>? _lastSegments;

  List<Segment> _resolve() {
    if (_lastInput == widget.text && _lastSegments != null) {
      return _lastSegments!;
    }
    final norm = preNormalize(widget.text);
    final segs = splitSegments(norm);
    _lastInput = widget.text;
    _lastSegments = segs;
    return segs;
  }

  @override
  Widget build(BuildContext context) {
    final segs = _resolve();
    if (segs.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final s in segs)
          KeyedSubtree(
            key: ValueKey('seg-${s.order}-${s.contentHash}'),
            child: _viewFor(s),
          ),
      ],
    );
  }

  Widget _viewFor(Segment s) {
    return switch (s) {
      MarkdownSegment(:final text) => MarkdownSegmentView(text: text),
      CodeSegment(:final language, :final code, :final closed) =>
        CodeSegmentView(language: language, code: code, closed: closed),
      MermaidSegment(:final source, :final closed) =>
        MermaidSegmentView(source: source, closed: closed),
      MathSegment(:final latex, :final display) =>
        MathSegmentView(latex: latex, display: display),
      HtmlSegment(:final html, :final closed) =>
        HtmlSegmentView(html: html, closed: closed),
      SvgSegment(:final svg, :final closed) =>
        SvgSegmentView(svg: svg, closed: closed),
    };
  }
}
