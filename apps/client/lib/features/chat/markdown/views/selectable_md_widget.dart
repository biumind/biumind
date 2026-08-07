// 可选中 Markdown 渲染 — 复用 gpt_markdown 的公开 API
// `MarkdownComponent.generate()` 生成 InlineSpan 树, 但最后一步不走它内部的
// `Text.rich` (getRich), 而是把 span 树**拍平成块序列**: 连续文本段合成一个
// `Text.rich` (在 SelectionArea 内自动注册为可选中), 块级 WidgetSpan
// (标题/列表/引用/表格/代码…) 拆出为
// 独立 widget, 整体用 Column 排列。
//
// 为什么必须拍平: gpt_markdown 的块级组件输出 WidgetSpan > ... > 文本,
// 若把整个 span 树塞进单个富文本, 块内文本会成为「嵌套在另一
// 个 Selectable 里的 Selectable」— SelectionArea 的选择/复制聚合在这种几
// 何嵌套下会静默失败 (拖拽选中复制为空 / 复制出 U+FFFC 占位符)。拍平后所有
// Text 是 SelectionArea 下的兄弟可选节点 (Text 在 SelectionArea 内自动注册
// 为 selectable), 跨块拖拽选择与复制正常。
//
// 设计: docs/BiuMind-Chat-Text-Selection-Design.md §3.2
//
// 注意: generate() 是 gpt_markdown 的非合同性公开 API (其自家 MdWidget 同款
// 用法), 升级 gpt_markdown 版本时需回归本文件单测。

import 'package:flutter/material.dart';
import 'package:gpt_markdown/custom_widgets/markdown_config.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

import 'selectable_md_components.dart';

class SelectableMdWidget extends StatefulWidget {
  const SelectableMdWidget(
    this.text, {
    super.key,
    this.style,
    this.useDollarSignsForLatex = false,
    this.onLinkTap,
    this.codeBuilder,
    this.tableBuilder,
  });

  final String text;
  final TextStyle? style;
  final bool useDollarSignsForLatex;
  final void Function(String url, String title)? onLinkTap;
  final CodeBlockBuilder? codeBuilder;
  final TableBuilder? tableBuilder;

  @override
  State<SelectableMdWidget> createState() => _SelectableMdWidgetState();
}

class _SelectableMdWidgetState extends State<SelectableMdWidget> {
  String? _cachedTex;
  TextStyle? _cachedStyle;
  List<Widget> _blocks = const [];

  // 与 GptMarkdown.build 中 useDollarSignsForLatex 预处理逐行一致
  // (gpt_markdown.dart:163-178): $$..$$ → \[..\], $..$ → \(..\), \$ 还原。
  static String _preprocess(String data, bool useDollarSignsForLatex) {
    String tex = data.replaceAll('\r\n', '\n').replaceAll('\r', '\n').trim();
    if (useDollarSignsForLatex) {
      tex = tex.replaceAllMapped(
        RegExp(r"(?<!\\)\$\$(.*?)(?<!\\)\$\$", dotAll: true),
        (match) => "\\[${match[1] ?? ""}\\]",
      );
      if (!tex.contains(r"\(")) {
        tex = tex.replaceAllMapped(
          RegExp(r"(?<!\\)\$(.*?)(?<!\\)\$"),
          (match) => "\\(${match[1] ?? ""}\\)",
        );
        tex = tex.splitMapJoin(
          RegExp(r"\[.*?\]|\(.*?\)"),
          onNonMatch: (p0) {
            return p0.replaceAll("\\\$", "\$");
          },
        );
      }
    }
    return tex;
  }

  static bool _containsWidgetSpan(InlineSpan span) {
    if (span is WidgetSpan) return true;
    if (span is TextSpan) {
      return span.children?.any(_containsWidgetSpan) ?? false;
    }
    return false;
  }

  // 把 generate() 的 span 树拍平成块序列: 文本段 → Text.rich,
  // WidgetSpan → 原样拆出为独立 widget。嵌套 TextSpan 里的文本片段用
  // 祖先链的 style/recognizer 重新包裹, 保持样式与点击行为不变。
  List<Widget> _flattenBlocks(List<InlineSpan> spans) {
    final blocks = <Widget>[];
    var pending = <InlineSpan>[];

    void flush() {
      if (pending.isEmpty) return;
      blocks.add(
        Text.rich(
          TextSpan(children: pending, style: widget.style),
          textDirection: TextDirection.ltr,
        ),
      );
      pending = [];
    }

    void visit(InlineSpan span, List<TextSpan> ancestors) {
      if (span is WidgetSpan) {
        flush();
        blocks.add(span.child);
        return;
      }
      if (span is! TextSpan) {
        pending.add(span);
        return;
      }
      final children = span.children ?? const <InlineSpan>[];
      if (!children.any(_containsWidgetSpan)) {
        // 纯文本子树: 祖先样式链重包后整段保留。
        var wrapped = span;
        for (final ancestor in ancestors.reversed) {
          wrapped = TextSpan(
            style: ancestor.style,
            recognizer: ancestor.recognizer,
            mouseCursor: ancestor.mouseCursor,
            children: [wrapped],
          );
        }
        pending.add(wrapped);
        return;
      }
      for (final child in children) {
        visit(child, [...ancestors, span]);
      }
    }

    for (final span in spans) {
      visit(span, const []);
    }
    flush();
    return blocks;
  }

  void _ensureBlocks(BuildContext context) {
    final tex = _preprocess(widget.text, widget.useDollarSignsForLatex);
    // 与 MdWidget 的缓存策略对齐: text / style 不变就不重解析。
    if (tex == _cachedTex && widget.style == _cachedStyle) return;
    final config = GptMarkdownConfig(
      style: widget.style,
      onLinkTap: widget.onLinkTap,
      codeBuilder: widget.codeBuilder,
      tableBuilder: widget.tableBuilder,
      // 文本类块/链接换成可选中变体 (见 selectable_md_components.dart);
      // 默认组件里块级文本是 WidgetSpan > Text.rich, 不可划选。
      components: selectableBlockComponents(),
      inlineComponents: selectableInlineComponents(),
    );
    _blocks = _flattenBlocks(
      MarkdownComponent.generate(context, tex, config, true),
    );
    _cachedTex = tex;
    _cachedStyle = widget.style;
  }

  @override
  Widget build(BuildContext context) {
    _ensureBlocks(context);
    if (_blocks.length == 1) return _blocks.single;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: _blocks,
    );
  }
}
