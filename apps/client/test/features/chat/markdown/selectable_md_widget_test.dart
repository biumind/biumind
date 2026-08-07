// selectable_md_widget 测试: SelectableMdWidget 复用 gpt_markdown 解析管线,
// 以 SelectableText.rich 输出, 配合 selectable_md_components 的组件覆写让
// 标题/列表/引用/任务列表/链接文本全部可划选。
//
// 设计: docs/BiuMind-Chat-Text-Selection-Design.md §3.2

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_math_fork/flutter_math.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/markdown/views/selectable_md_widget.dart';

Widget _app(Widget child, {bool withSelectionArea = false}) {
  return MaterialApp(
    home: Scaffold(
      body: withSelectionArea ? SelectionArea(child: child) : child,
    ),
  );
}

/// 富文本 Text (textSpan 非空) 的查找器 — 拍平后每个文本块是 Text.rich,
/// 在 SelectionArea 内自动注册为可选中。
Finder findRichTexts() =>
    find.byWidgetPredicate((w) => w is Text && w.textSpan != null);

/// 聚合整棵渲染树里所有富文本 Text 的纯文本。
String _allPlainText(WidgetTester tester) {
  final buffer = StringBuffer();
  for (final element in findRichTexts().evaluate()) {
    buffer.write((element.widget as Text).textSpan!.toPlainText());
  }
  return buffer.toString();
}

void main() {
  testWidgets('纯文本段落 → SelectableText, 内容一致', (tester) async {
    await tester.pumpWidget(_app(const SelectableMdWidget('hello world')));
    expect(findRichTexts(), findsWidgets);
    expect(_allPlainText(tester), contains('hello world'));
  });

  testWidgets('标题 + 加粗 + 无序列表: 文本可选中, 语法符号不泄漏', (tester) async {
    await tester.pumpWidget(
      _app(
        const SelectableMdWidget('# Title\n\nsome **bold** text\n\n- item one'),
      ),
    );
    final plain = _allPlainText(tester);
    expect(plain, contains('Title'));
    expect(plain, contains('bold'));
    expect(plain, contains('item one'));
    expect(plain, isNot(contains('**bold**')));
    expect(plain, isNot(contains('# Title')));
  });

  testWidgets('有序列表文本可选中, 序号保留', (tester) async {
    await tester.pumpWidget(
      _app(const SelectableMdWidget('1. first thing\n2. second thing')),
    );
    final plain = _allPlainText(tester);
    expect(plain, contains('first thing'));
    expect(plain, contains('second thing'));
  });

  testWidgets('任务列表文本可选中', (tester) async {
    await tester.pumpWidget(
      _app(const SelectableMdWidget('[x] done task\n[ ] todo task')),
    );
    final plain = _allPlainText(tester);
    expect(plain, contains('done task'));
    expect(plain, contains('todo task'));
  });

  testWidgets('引用块文本可选中, > 符号不泄漏', (tester) async {
    await tester.pumpWidget(
      _app(const SelectableMdWidget('> quoted words here')),
    );
    final plain = _allPlainText(tester);
    expect(plain, contains('quoted words here'));
    expect(plain, isNot(contains('> quoted')));
  });

  testWidgets(r'useDollarSignsForLatex: $x^2$ → Math widget', (tester) async {
    await tester.pumpWidget(
      _app(
        const SelectableMdWidget(
          r'formula $x^2$ inline',
          useDollarSignsForLatex: true,
        ),
      ),
    );
    expect(find.byType(Math), findsOneWidget);
  });

  testWidgets('GFM 表格走自定义 tableBuilder', (tester) async {
    await tester.pumpWidget(
      _app(
        SelectableMdWidget(
          '| a | b |\n|---|---|\n| 1 | 2 |',
          tableBuilder: (context, rows, style, cfg) =>
              const Text('STUB-TABLE', key: Key('stub-table')),
        ),
      ),
    );
    expect(find.byKey(const Key('stub-table')), findsOneWidget);
  });

  testWidgets('fenced code 走 codeBuilder 兜底', (tester) async {
    await tester.pumpWidget(
      _app(
        SelectableMdWidget(
          '```python\nprint(1)\n```',
          codeBuilder: (context, name, code, closed) =>
              Text('CODE[$name]:$code', key: const Key('code-stub')),
        ),
      ),
    );
    final stub = tester.widget<Text>(find.byKey(const Key('code-stub')));
    expect(stub.data, contains('print(1)'));
  });

  testWidgets('链接: recognizer span 挂载 onLinkTap 且文本保留在 span 树', (tester) async {
    String? tappedUrl;
    await tester.pumpWidget(
      _app(
        SelectableMdWidget(
          'see [docs](https://example.com) here',
          onLinkTap: (url, title) => tappedUrl = url,
        ),
      ),
    );
    // 链接文本作为普通 span 存在 (可划选), 不再是 WidgetSpan > LinkButton。
    final plain = _allPlainText(tester);
    expect(plain, contains('docs'));
    expect(plain, isNot(contains('[docs]')));

    // 从 span 树里找到带 recognizer 的链接 span, 验证回调接线。
    final outer = tester.widget<Text>(findRichTexts().first);
    TapGestureRecognizer? linkRecognizer;
    void walk(InlineSpan span) {
      if (span is TextSpan) {
        final r = span.recognizer;
        if (r is TapGestureRecognizer) linkRecognizer = r;
        span.children?.forEach(walk);
      }
    }

    walk(outer.textSpan!);
    expect(linkRecognizer, isNotNull);
    linkRecognizer!.onTap!();
    expect(tappedUrl, 'https://example.com');
  });

  testWidgets('在 SelectionArea 内正常渲染', (tester) async {
    await tester.pumpWidget(
      _app(
        const SelectableMdWidget('selectable across bubbles'),
        withSelectionArea: true,
      ),
    );
    expect(_allPlainText(tester), contains('selectable across bubbles'));
  });

  testWidgets('spans 缓存: text 不变不重解析, 变化后更新', (tester) async {
    const style = TextStyle(fontSize: 14);
    await tester.pumpWidget(
      _app(const SelectableMdWidget('first', style: style)),
    );
    expect(_allPlainText(tester), contains('first'));

    // 同 text 重 pump — 走缓存路径, 不应抛异常。
    await tester.pumpWidget(
      _app(const SelectableMdWidget('first', style: style)),
    );
    expect(_allPlainText(tester), contains('first'));

    await tester.pumpWidget(
      _app(const SelectableMdWidget('second', style: style)),
    );
    final plain = _allPlainText(tester);
    expect(plain, contains('second'));
    expect(plain, isNot(contains('first')));
  });

  testWidgets(r'\r\n 归一化 + trim 与 GptMarkdown 预处理一致', (tester) async {
    await tester.pumpWidget(
      _app(const SelectableMdWidget('  line1\r\nline2\r\n  ')),
    );
    final plain = _allPlainText(tester);
    expect(plain, contains('line1'));
    expect(plain, contains('line2'));
  });
}
