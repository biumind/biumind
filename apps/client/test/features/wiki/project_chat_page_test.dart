// ProjectChatPage 手机单栏化测试 — R2 任务1。
//
// project_chat 原是 N0 遗留的桌面双栏 (240 左栏 + 聊天体)。R2 把手机形态
// 改为单栏: 会话列表收进 bottom sheet (顶栏「会话」按钮触发), 聊天体占满。
//
// 覆盖:
//   * 手机 (390×844): 顶栏有会话入口 + 新建, 聊天体 _Placeholder 占满,
//     无桌面左栏的空态文案;
//   * 桌面 (1200×800): 240 左栏空态 + 右侧 _Placeholder, 无会话入口;
//   * 手机点会话入口 → bottom sheet 弹出 (空态文案 + 头标题)。
//
// 数据依赖 (wikiRepositoryProvider) override 为 null → 会话列表空,
// 聚焦布局/交互而非数据流 (数据流是 project_chat 原有逻辑, 非 R2 改动)。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/data/wiki_providers.dart';
import 'package:biumind/features/wiki/presentation/chat/project_chat_page.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _wrap() => ProviderScope(
      overrides: [wikiRepositoryProvider.overrideWithValue(null)],
      child: MaterialApp(
        localizationsDelegates: const [
          AppLocalizations.delegate,
          GlobalMaterialLocalizations.delegate,
          GlobalWidgetsLocalizations.delegate,
          GlobalCupertinoLocalizations.delegate,
        ],
        supportedLocales: AppLocalizations.supportedLocales,
        theme: buildTheme(
          palette: PaletteId.inkblueOrange,
          mode: Brightness.light,
          fontSize: FontSize.small,
        ),
        home: const Scaffold(body: ProjectChatPage(projectId: 'p1')),
      ),
    );

void _setView(WidgetTester tester, Size size) {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  testWidgets('手机: 顶栏会话入口 + 新建, 聊天体占满, 无左栏空态', (tester) async {
    _setView(tester, const Size(390, 844));
    await tester.pumpWidget(_wrap());
    await tester.pumpAndSettle();

    // 顶栏: 会话入口 (format_list_bulleted) + 新建 (add)。
    expect(find.byIcon(Icons.format_list_bulleted), findsOneWidget);
    expect(find.byIcon(Icons.add), findsNWidgets(2));
    // 聊天体 _Placeholder 占满 (无 active 会话)。
    expect(find.text('选择一个会话，或新建一个开始'), findsOneWidget);
    // 桌面左栏的空态文案不在 (左栏收 sheet 了)。
    expect(find.text('点击 + 新建对话'), findsNothing);
  });

  testWidgets('桌面: 240 左栏空态 + 右侧 _Placeholder, 无会话入口', (tester) async {
    _setView(tester, const Size(1200, 800));
    await tester.pumpWidget(_wrap());
    await tester.pumpAndSettle();

    // 左栏空态 + 右侧 _Placeholder。
    expect(find.text('点击 + 新建对话'), findsOneWidget);
    expect(find.text('选择一个会话，或新建一个开始'), findsOneWidget);
    // 桌面无会话入口 (onOpenConvs=null)。
    expect(find.byIcon(Icons.format_list_bulleted), findsNothing);
    // 新建按钮仍在左栏头。
    expect(find.byIcon(Icons.add), findsNWidgets(2));
  });

  testWidgets('手机: 点会话入口弹 bottom sheet, 含空态 + 头标题', (tester) async {
    _setView(tester, const Size(390, 844));
    await tester.pumpWidget(_wrap());
    await tester.pumpAndSettle();

    expect(find.byType(BottomSheet), findsNothing);
    await tester.tap(find.byIcon(Icons.format_list_bulleted));
    await tester.pumpAndSettle();

    expect(find.byType(BottomSheet), findsOneWidget);
    // sheet 头标题 + 空态文案 (空列表)。
    expect(find.text('会话'), findsOneWidget);
    expect(find.text('点击 + 新建对话'), findsOneWidget);
  });
}
