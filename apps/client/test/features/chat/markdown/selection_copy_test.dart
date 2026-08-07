// selection_copy_test — 桌面/移动端划选复制的回归测试。
//
// 背景: gpt_markdown 块级组件输出 WidgetSpan > ... > Text.rich; 若把整棵
// span 树塞进单个 SelectableText.rich, 块内嵌套的 SelectableText 会毒化
// SelectionArea 的选择聚合 — 拖拽选中后 Cmd+C 复制为空 / 复制出 U+FFFC
// 占位符 / 右键菜单不出现。修复方案是把 span 树拍平成块序列 (见
// selectable_md_widget.dart 头注释)。本文件锁定该行为:
//
//   - 每种 markdown 构造, 拖拽选择 + Cmd+C 必须复制出真实文本;
//   - 右键 (含拖拽后右键) 必须弹出选择菜单且复制项可用;
//   - 移动端长按必须弹出选择工具栏。

import 'package:flutter/foundation.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/markdown/views/selectable_md_widget.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  List<MethodCall> mockClipboard(WidgetTester tester) {
    final calls = <MethodCall>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') calls.add(call);
        return null;
      },
    );
    return calls;
  }

  Future<void> dragSelect(WidgetTester tester, Offset from, Offset to) async {
    final g = await tester.startGesture(from, kind: PointerDeviceKind.mouse);
    await tester.pump(const Duration(milliseconds: 50));
    await g.moveTo(to);
    await tester.pump(const Duration(milliseconds: 50));
    await g.up();
    await tester.pump();
  }

  Future<void> pressCmdC(WidgetTester tester) async {
    await tester.sendKeyDownEvent(LogicalKeyboardKey.metaLeft);
    await tester.sendKeyDownEvent(LogicalKeyboardKey.keyC);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.keyC);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.metaLeft);
    await tester.pump();
  }

  Widget app(Widget child) => MaterialApp(
    home: Scaffold(body: SelectionArea(child: child)),
  );

  // 拍平后文本块是 Text.rich (textSpan 非空), 在 SelectionArea 内可选中。
  Finder findRichTexts() =>
      find.byWidgetPredicate((w) => w is Text && w.textSpan != null);

  String copiedText(List<MethodCall> calls) =>
      calls.map((c) => (c.arguments as Map)['text'] as String).join();

  // 拖拽 + Cmd+C: 每种构造都必须复制出真实文本 (非空 / 非 U+FFFC 占位符)。
  final dragCases = <String, (String, String)>{
    '纯段落': ('hello plain paragraph', 'hello pl'),
    '标题': ('# 标题一', '标题一'),
    '无序列表': ('- 列表项一', '列表项一'),
    '引用': ('> 引用一段文字', '引用一段文字'),
    '链接': ('see [链接](https://example.com) here', '链接'),
    '加粗': ('some **加粗** text', 'some 加粗'),
    '有序列表': ('1. 第一项', '第一项'),
    '任务列表': ('[x] 已完成事项', '已完成事项'),
    '行内公式': (r'formula $x^2$ here', 'formula'),
    '组合': ('# 标题\n\n这是正文 **加粗**。\n\n- 列表项\n', '标题'),
  };

  for (final entry in dragCases.entries) {
    testWidgets('拖拽+Cmd+C: ${entry.key}', (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      final calls = mockClipboard(tester);
      try {
        await tester.pumpWidget(
          app(
            SelectableMdWidget(
              entry.value.$1,
              useDollarSignsForLatex: true,
              style: const TextStyle(fontSize: 20),
            ),
          ),
        );
        await tester.pump();
        final origin = tester.getTopLeft(findRichTexts().first);
        await dragSelect(
          tester,
          origin + const Offset(2, 10),
          origin + const Offset(160, 10),
        );
        await pressCmdC(tester);
        expect(
          copiedText(calls),
          contains(entry.value.$2),
          reason: '拖拽选择后 Cmd+C 应复制出「${entry.value.$2}」',
        );
      } finally {
        debugDefaultTargetPlatformOverride = null;
      }
    });
  }

  testWidgets('macOS 右键点标题块 → 弹选择菜单', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    try {
      await tester.pumpWidget(
        app(
          const SelectableMdWidget(
            '# 标题一\n\n正文段落文字',
            style: TextStyle(fontSize: 20),
          ),
        ),
      );
      await tester.pump();
      // 标题块整行宽 800, getCenter 会落在无字形空白处; 点实际字形位置。
      await tester.tapAt(
        tester.getTopLeft(findRichTexts().first) + const Offset(40, 20),
        buttons: kSecondaryMouseButton,
      );
      await tester.pump();
      await tester.pump(const Duration(seconds: 1));
      expect(find.byType(AdaptiveTextSelectionToolbar), findsOneWidget);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('macOS 拖拽后右键 → 菜单复制项可用', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    final calls = mockClipboard(tester);
    try {
      await tester.pumpWidget(
        app(
          const SelectableMdWidget(
            '拖拽选择这段文字试试',
            style: TextStyle(fontSize: 20),
          ),
        ),
      );
      await tester.pump();
      final origin = tester.getTopLeft(findRichTexts().first);
      await dragSelect(
        tester,
        origin + const Offset(2, 10),
        origin + const Offset(150, 10),
      );
      await tester.tapAt(
        origin + const Offset(60, 10),
        buttons: kSecondaryMouseButton,
      );
      await tester.pump();
      await tester.pump(const Duration(seconds: 1));
      expect(find.byType(AdaptiveTextSelectionToolbar), findsOneWidget);
      await tester.tap(find.text('Copy'));
      await tester.pump();
      expect(copiedText(calls), isNotEmpty);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('Android 长按 → 弹选择工具栏', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    try {
      await tester.pumpWidget(
        app(
          const SelectableMdWidget(
            '# 移动标题\n\n移动端长按选择文字',
            style: TextStyle(fontSize: 20),
          ),
        ),
      );
      await tester.pump();
      // 段落块含前导空行, 字形在最后一行; 点最后一行的文字位置。
      final lastRect = tester.getRect(findRichTexts().last);
      final g = await tester.startGesture(
        lastRect.bottomLeft + const Offset(40, -12),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await g.up();
      await tester.pump();
      await tester.pump(const Duration(seconds: 1));
      expect(find.byType(AdaptiveTextSelectionToolbar), findsOneWidget);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('跨气泡拖拽 + Cmd+C 复制两条消息内容', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    final calls = mockClipboard(tester);
    try {
      await tester.pumpWidget(
        app(
          ListView(
            children: const [
              Padding(
                padding: EdgeInsets.all(8),
                child: SelectableMdWidget(
                  '第一条消息内容aaa',
                  style: TextStyle(fontSize: 20),
                ),
              ),
              Padding(
                padding: EdgeInsets.all(8),
                child: SelectableMdWidget(
                  '第二条消息内容bbb',
                  style: TextStyle(fontSize: 20),
                ),
              ),
            ],
          ),
        ),
      );
      await tester.pump();
      final first = tester.getTopLeft(findRichTexts().first);
      // 终点落在第二条消息的字形上 (bottomLeft 的 x 是左缘, 需向右偏移)。
      final last = tester.getRect(findRichTexts().last).bottomRight;
      final start = first + const Offset(2, 10);
      final end = last - const Offset(20, 4);
      final g = await tester.startGesture(start, kind: PointerDeviceKind.mouse);
      await tester.pump(const Duration(milliseconds: 50));
      // 跨 selectable 拖拽需要中间轨迹点, 让 region 逐步推进选区边缘。
      for (var i = 1; i <= 8; i++) {
        await g.moveTo(start + (end - start) * (i / 8));
        await tester.pump(const Duration(milliseconds: 20));
      }
      await g.up();
      await tester.pump();
      await pressCmdC(tester);
      final copied = copiedText(calls);
      expect(copied, contains('第一条消息内容'));
      expect(copied, contains('第二条消息内容'));
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });
}
