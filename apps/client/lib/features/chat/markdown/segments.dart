// Markdown 渲染管线 — 类型化段定义。
//
// 设计文档: 第 9 项. Pipeline V2.
//
// Segment 是 splitSegments() 的输出, 也是 view 层的输入。pipeline 唯一
// 跨阶段的"语言"。每条 Segment 是 immutable 值对象。
//
// closed 字段: 流式期间最后一段的 fence 还没闭合时为 false。view 层据此
// 决定是 partial code 显示还是升级到 Mermaid/HTML 等渲染器。
//
// contentHash 用于 widget 层 ValueKey, 让 Flutter Element tree 识别"同
// 一段在新 frame 是否变了" — 没变就 reuse, 长会话流式不抖。

import 'package:meta/meta.dart';

@immutable
sealed class Segment {
  const Segment({required this.order, required this.closed});

  /// 在原文中的顺序 (0-based)。配合 contentHash 当 ValueKey。
  final int order;

  /// fence 是否闭合 (`` ``` `` 配对)。普通 markdown 段恒为 true。
  final bool closed;

  /// 内容 hash — 同段同 hash → widget reuse。
  /// 用 hashCode 即可: Dart String 是 immutable, hashCode 稳定。
  int get contentHash;
}

class MarkdownSegment extends Segment {
  const MarkdownSegment({
    required super.order,
    required this.text,
  }) : super(closed: true);

  final String text;

  @override
  int get contentHash => text.hashCode;
}

class CodeSegment extends Segment {
  const CodeSegment({
    required super.order,
    required this.language,
    required this.code,
    required super.closed,
  });

  /// Normalized 小写, 已剥参数。空字符串表示无 lang。
  final String language;
  final String code;

  @override
  int get contentHash => Object.hash(language, code, closed);
}

class MermaidSegment extends Segment {
  const MermaidSegment({
    required super.order,
    required this.source,
    required super.closed,
  });

  final String source;

  @override
  int get contentHash => Object.hash(source, closed);
}

class MathSegment extends Segment {
  const MathSegment({
    required super.order,
    required this.latex,
    required this.display,
  }) : super(closed: true);

  /// raw LaTeX, 不含 $$ / \[ \] 包裹。
  final String latex;
  /// true=block ($$), false=inline ($) — V1 splitter 只产出 display=true。
  final bool display;

  @override
  int get contentHash => Object.hash(latex, display);
}

class HtmlSegment extends Segment {
  const HtmlSegment({
    required super.order,
    required this.html,
    required super.closed,
  });

  final String html;

  @override
  int get contentHash => Object.hash(html, closed);
}

class SvgSegment extends Segment {
  const SvgSegment({
    required super.order,
    required this.svg,
    required super.closed,
  });

  final String svg;

  @override
  int get contentHash => Object.hash(svg, closed);
}
