// WikiReaderView body_md 数据源测试。
//
// 覆盖 reader 切 body_md 后的关键边界：
//   * body_md 原文渲染（heading / 正文 / 行内格式不经过 blocks 投影拍平）
//   * `[[wikilink]]` 渲染为可点链接，点击走 wiki controller 路由
//     （未解析时给 SnackBar 提示而非静默）
//   * markdown 图片 `![alt](url)` 不断（渲染出 Image widget）
//   * fenced code block 内的 `[[…]]` 保持字面量（不 linkify）
//   * 空 body 空态 / loading 态
//
// 无网络依赖：wikiControllerProvider 用默认 WikiController（repo null →
// noCredentials 空态，wikilink 全部落入「找不到页面」分支）。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/features/wiki/presentation/reader/wiki_reader_view.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _wrap(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      theme: buildTheme(
        palette: PaletteId.inkblueOrange,
        mode: Brightness.light,
        fontSize: FontSize.small,
      ),
      home: Scaffold(body: child),
    ),
  );
}

void main() {
  testWidgets('renders body_md headings and paragraphs', (tester) async {
    await tester.pumpWidget(
      _wrap(const WikiReaderView(bodyMd: '# 第一章\n\n正文段落。')),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('第一章'), findsOneWidget);
    expect(find.textContaining('正文段落。'), findsOneWidget);
  });

  testWidgets('inline markdown survives (no blocks projection flattening)',
      (tester) async {
    await tester.pumpWidget(
      _wrap(const WikiReaderView(bodyMd: '这是 **加粗** 内容。')),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('加粗'), findsOneWidget);
  });

  testWidgets('wikilink is tappable and routes via wiki controller',
      (tester) async {
    await tester.pumpWidget(
      _wrap(const WikiReaderView(bodyMd: '参见 [[Foo]] 页面。')),
    );
    await tester.pumpAndSettle();
    // 链接 label 渲染出来了。
    expect(find.textContaining('Foo'), findsOneWidget);
    // 点击 wikilink —— 测试环境无 pages，落入未解析分支弹 SnackBar
    // （证明 onLinkTap 走 wiki:// 拦截而非外部浏览器）。
    await tester.tapOnText(find.textRange.ofSubstring('Foo'));
    await tester.pumpAndSettle();
    expect(find.text('找不到页面：Foo'), findsOneWidget);
  });

  testWidgets('markdown image renders an Image widget', (tester) async {
    await tester.pumpWidget(
      _wrap(
        const WikiReaderView(
          bodyMd: '![示意图](https://example.com/a.png)',
        ),
      ),
    );
    await tester.pump();
    expect(find.byType(Image), findsOneWidget);
  });

  testWidgets('wikilink inside fenced code stays literal', (tester) async {
    await tester.pumpWidget(
      _wrap(const WikiReaderView(bodyMd: '```md\nx = [[Foo]]\n```')),
    );
    await tester.pumpAndSettle();
    // 代码块内容原样展示（SelectAllableText / SelectableText 原文），
    // 没有被改写成 wiki:// 链接 —— 点击不应触发任何路由。
    expect(find.textContaining('x = [[Foo]]'), findsOneWidget);
  });

  testWidgets('empty body shows the placeholder hint', (tester) async {
    await tester.pumpWidget(_wrap(const WikiReaderView(bodyMd: '  ')));
    await tester.pumpAndSettle();
    expect(find.textContaining('此页面还没有内容'), findsOneWidget);
  });

  testWidgets('loading shows indicator instead of empty hint', (tester) async {
    await tester.pumpWidget(
      _wrap(const WikiReaderView(bodyMd: '', loading: true)),
    );
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    expect(find.textContaining('此页面还没有内容'), findsNothing);
  });
}
