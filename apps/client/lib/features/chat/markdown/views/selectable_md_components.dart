// 可选中 Markdown 组件覆写 — gpt_markdown 的块级组件 (BlockMd) 默认把渲染
// 结果包成 WidgetSpan > ... > Text.rich, 文本不可划选。这里把「文本叶子」
// 逐个换成 Text.rich (SelectionArea 注册为可选中) / recognizer span:
//
//   标题 / 列表 / 任务列表 / 引用 / 缩进  → Text.rich (SelectionArea 下可划选)
//   链接 (ATagMd)                        → TapGestureRecognizer span
//                                          (可划选 + 可点击 + 手型光标)
//
// 其余组件 (代码 / 表格 / 块级公式 / 分割线 / 图片) 保持原样, 作为
// WidgetSpan 原子单位参与选择 (复制源码走各自视图的复制按钮)。
//
// 实现方式: 子类化原组件, 仅覆写 build()/span(), 逻辑逐行对齐
// gpt_markdown 1.1.8 上游实现 (不 fork 包本体, 遵守 C3)。升级
// gpt_markdown 时需对照上游 diff 这些覆写 — 见
// docs/BiuMind-Chat-Text-Selection-Design.md §3.4 R4。

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:gpt_markdown/custom_widgets/custom_divider.dart';
import 'package:gpt_markdown/custom_widgets/custom_rb_cb.dart';
import 'package:gpt_markdown/custom_widgets/indent_widget.dart';
import 'package:gpt_markdown/custom_widgets/markdown_config.dart';
import 'package:gpt_markdown/custom_widgets/unordered_ordered_list.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

/// 与 [MarkdownComponent.globalComponents] 同序, 文本类块换成可选中变体。
List<MarkdownComponent> selectableBlockComponents() => [
  CodeBlockMd(),
  LatexMathMultiLine(),
  NewLines(),
  _SelectableBlockQuote(),
  TableMd(),
  _SelectableHTag(),
  _SelectableUnOrderedList(),
  _SelectableOrderedList(),
  _SelectableRadioButton(),
  _SelectableCheckBox(),
  HrLine(),
  _SelectableIndent(),
];

/// 与 [MarkdownComponent.inlineComponents] 同序, 链接换成可划选变体。
List<MarkdownComponent> selectableInlineComponents() => [
  _SelectableATag(),
  ImageMd(),
  TableMd(),
  StrikeMd(),
  BoldMd(),
  ItalicMd(),
  UnderLineMd(),
  LatexMath(),
  LatexMathMultiLine(),
  HighlightedText(),
  SourceTag(),
];

/// MdWidget 的可选中替身: 同一 generate 管线, 叶子换 Text.rich —
/// Text 在 SelectionArea 内自动注册为 selectable (widgets/text.dart 的
/// _SelectableTextContainer), 比 SelectableText.rich 更适合区域选择。
class SelectableMdLeaf extends StatelessWidget {
  const SelectableMdLeaf(
    this.text,
    this.includeGlobal, {
    super.key,
    required this.config,
  });

  final String text;
  final bool includeGlobal;
  final GptMarkdownConfig config;

  @override
  Widget build(BuildContext context) {
    final spans = MarkdownComponent.generate(
      context,
      text,
      config,
      includeGlobal,
    );
    return Text.rich(
      TextSpan(children: spans, style: config.style?.copyWith()),
      textDirection: config.textDirection,
    );
  }
}

// 对齐上游 HTag.build: getRich → Text.rich, H1 尾部 divider 保留。
class _SelectableHTag extends HTag {
  @override
  Widget build(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    final theme = GptMarkdownTheme.of(context);
    final match = exp.firstMatch(text.trim());
    final conf = config.copyWith(
      style: [
        theme.h1,
        theme.h2,
        theme.h3,
        theme.h4,
        theme.h5,
        theme.h6,
      ][match![1]!.length - 1],
    );
    return Text.rich(
      TextSpan(
        children: [
          ...MarkdownComponent.generate(
            context,
            "${match.namedGroup('data')}",
            conf,
            false,
          ),
          if (match.namedGroup('hash')!.length == 1 &&
              theme.autoAddDividerLineAfterH1) ...[
            const TextSpan(
              text: "\n ",
              style: TextStyle(fontSize: 0, height: 0),
            ),
            WidgetSpan(
              child: CustomDivider(
                height: theme.hrLineThickness,
                color: theme.hrLineColor,
                padding: theme.hrLinePadding,
              ),
            ),
          ],
        ],
      ),
      textDirection: config.textDirection,
    );
  }
}

// 对齐上游 UnOrderedList.build: 条目内容 MdWidget → SelectableMdLeaf。
class _SelectableUnOrderedList extends UnOrderedList {
  @override
  Widget build(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    final match = exp.firstMatch(text);
    final child = SelectableMdLeaf(
      "${match?[1]?.trim()}",
      true,
      config: config,
    );
    return config.unOrderedListBuilder?.call(
          context,
          child,
          config.copyWith(),
        ) ??
        UnorderedListView(
          bulletColor:
              (config.style?.color ?? DefaultTextStyle.of(context).style.color),
          padding: 7,
          spacing: 10,
          bulletSize:
              0.3 *
              (config.style?.fontSize ??
                  DefaultTextStyle.of(context).style.fontSize ??
                  14.0),
          textDirection: config.textDirection,
          child: child,
        );
  }
}

// 对齐上游 OrderedList.build: 条目内容 MdWidget → SelectableMdLeaf。
class _SelectableOrderedList extends OrderedList {
  @override
  Widget build(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    final match = exp.firstMatch(text);
    final no = "${match?[1]}".trim();
    final child = SelectableMdLeaf("${match?[2]}".trim(), true, config: config);
    return config.orderedListBuilder?.call(
          context,
          no,
          child,
          config.copyWith(),
        ) ??
        OrderedListView(
          no: "$no.",
          textDirection: config.textDirection,
          style: (config.style ?? const TextStyle()).copyWith(
            fontWeight: FontWeight.w100,
          ),
          child: child,
        );
  }
}

// 对齐上游 CheckBoxMd.build: 文本 MdWidget → SelectableMdLeaf。
class _SelectableCheckBox extends CheckBoxMd {
  @override
  Widget build(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    final match = exp.firstMatch(text.trim());
    return CustomCb(
      value: ("${match?[1]}" == "x"),
      textDirection: config.textDirection,
      child: SelectableMdLeaf("${match?[2]}", false, config: config),
    );
  }
}

// 对齐上游 RadioButtonMd.build: 文本 MdWidget → SelectableMdLeaf。
class _SelectableRadioButton extends RadioButtonMd {
  @override
  Widget build(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    final match = exp.firstMatch(text.trim());
    return CustomRb(
      value: ("${match?[1]}" == "x"),
      textDirection: config.textDirection,
      child: SelectableMdLeaf("${match?[2]}", false, config: config),
    );
  }
}

// 对齐上游 IndentMd.build: getRich → Text.rich。
class _SelectableIndent extends IndentMd {
  @override
  Widget build(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    final match = exp.firstMatch(text);
    final conf = config.copyWith();
    return Directionality(
      textDirection: config.textDirection,
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Flexible(
            child: Text.rich(
              TextSpan(
                children: MarkdownComponent.generate(
                  context,
                  match?[2]?.trim() ?? "",
                  conf,
                  false,
                ),
              ),
              textDirection: config.textDirection,
            ),
          ),
        ],
      ),
    );
  }
}

// 对齐上游 BlockQuote.span: 引文 getRich → Text.rich。
class _SelectableBlockQuote extends BlockQuote {
  @override
  InlineSpan span(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    final match = exp.firstMatch(text);
    final dataBuilder = StringBuffer();
    final m = match?[0] ?? '';
    for (final each in m.split('\n')) {
      if (each.startsWith(RegExp(r'\ *>'))) {
        var subString = each.trimLeft().substring(1);
        if (subString.startsWith(' ')) {
          subString = subString.substring(1);
        }
        dataBuilder.writeln(subString);
      } else {
        dataBuilder.writeln(each);
      }
    }
    final data = dataBuilder.toString().trim();
    final child = TextSpan(
      children: MarkdownComponent.generate(context, data, config, true),
    );
    return TextSpan(
      children: [
        WidgetSpan(
          child: Directionality(
            textDirection: config.textDirection,
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: BlockQuoteWidget(
                color: Theme.of(context).colorScheme.onSurfaceVariant,
                direction: config.textDirection,
                width: 3,
                child: Padding(
                  padding: const EdgeInsetsDirectional.only(start: 8.0),
                  child: Text.rich(child, textDirection: config.textDirection),
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}

// 对齐上游 ATagMd.span 的 URL 解析, 渲染从 LinkButton (WidgetSpan > Text.rich,
// 不可划选) 换成 recognizer span — 链接文本可划选、可点击, 桌面端有手型
// 光标 (mouseCursor)。代价: 失去 hover 变色, P1 可加回。
class _SelectableATag extends ATagMd {
  @override
  InlineSpan span(
    BuildContext context,
    String text,
    final GptMarkdownConfig config,
  ) {
    // 自定义 linkBuilder 场景不在 chat 使用范围, 回退上游实现。
    if (config.linkBuilder != null) {
      return super.span(context, text, config);
    }
    var bracketCount = 0;
    var start = 1;
    var end = 0;
    for (var i = 0; i < text.length; i++) {
      if (text[i] == '[') {
        bracketCount++;
      } else if (text[i] == ']') {
        bracketCount--;
        if (bracketCount == 0) {
          end = i;
          break;
        }
      }
    }

    if (text[end + 1] != '(') {
      return const TextSpan();
    }

    final linkText = text.substring(start, end);
    final urlStart = end + 2;

    var parenCount = 0;
    var urlEnd = urlStart;
    for (int i = urlStart; i < text.length; i++) {
      final char = text[i];
      if (char == '(') {
        parenCount++;
      } else if (char == ')') {
        if (parenCount == 0) {
          urlEnd = i;
          break;
        } else {
          parenCount--;
        }
      }
    }

    if (urlEnd == urlStart) {
      return const TextSpan();
    }

    final url = text.substring(urlStart, urlEnd).trim();
    final ending = text.substring(urlEnd + 1);
    final endingSpans = MarkdownComponent.generate(
      context,
      ending,
      config,
      false,
    );
    final theme = GptMarkdownTheme.of(context);
    final linkStyle = (config.style ?? const TextStyle()).copyWith(
      color: theme.linkColor,
      decorationColor: theme.linkColor,
      decoration: TextDecoration.underline,
    );
    return TextSpan(
      children: [
        TextSpan(
          children: MarkdownComponent.generate(
            context,
            linkText,
            config.copyWith(style: linkStyle),
            false,
          ),
          style: linkStyle,
          mouseCursor: SystemMouseCursors.click,
          recognizer: TapGestureRecognizer()
            ..onTap = () => config.onLinkTap?.call(url, linkText),
        ),
        ...endingSpans,
      ],
    );
  }
}
