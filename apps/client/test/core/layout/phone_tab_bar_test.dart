// phone_tab_bar 高亮判定测试 (R1.1)。
//
// phoneTabIndexFor 是底部 tab 选中的核心逻辑 (路由前缀 → tab 索引),
// 改坏会让用户切到某页却高亮错的 tab。渲染 / 集成测试 (GoRouter 树 +
// Localizations) 见 R1.8。

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/core/layout/phone_tab_bar.dart';
import 'package:biumind/l10n/app_localizations.dart';

void main() {
  group('phoneTabIndexFor — 主 tab 精确匹配', () {
    test('6 个顶层目的地各归其位', () {
      expect(phoneTabIndexFor('/chat'), 0, reason: '对话');
      expect(phoneTabIndexFor('/wiki'), 1, reason: '知识库');
      expect(phoneTabIndexFor('/notes'), 2, reason: '笔记');
      expect(phoneTabIndexFor('/creation'), 3, reason: '创作');
      expect(phoneTabIndexFor('/apps'), 4, reason: '应用');
      expect(phoneTabIndexFor('/profile'), 5, reason: '我的 (tab6 主路径, R1.3)');
      expect(phoneTabIndexFor('/settings'), 5, reason: '我的 (伞下, R1.3 起设置降为子项)');
    });
  });

  group('phoneTabIndexFor — 子路径按前缀归 tab', () {
    test('模块内子页归上层 tab', () {
      expect(phoneTabIndexFor('/wiki/p/abc'), 1);
      expect(phoneTabIndexFor('/wiki/p/abc/pages/foo'), 1);
      expect(phoneTabIndexFor('/notes/trash'), 2);
      expect(phoneTabIndexFor('/creation/center'), 3);
      expect(phoneTabIndexFor('/creation/works/x'), 3);
      expect(phoneTabIndexFor('/apps/detail/foo'), 4);
      expect(phoneTabIndexFor('/apps/host/i1/home'), 4);
      expect(phoneTabIndexFor('/apps/installed'), 4);
      expect(phoneTabIndexFor('/apps/customize'), 4);
      expect(phoneTabIndexFor('/settings/devices'), 5);
    });
  });

  group('phoneTabIndexFor — 「我的」伞下三入口同归末位 tab', () {
    test('settings / membership / skills 都点亮我的 (R1.3 收口为统一页)', () {
      expect(phoneTabIndexFor('/membership'), 5);
      expect(phoneTabIndexFor('/membership/checkout'), 5);
      expect(phoneTabIndexFor('/membership/orders'), 5);
      expect(phoneTabIndexFor('/skills'), 5);
    });
  });

  group('phoneTabIndexFor — 无匹配兜底', () {
    test('横切 / 顶层独立路由兜底 tab 0 (对话)', () {
      // /search 是横切能力 (R1.5 做顶部入口), 不属任一 tab。
      // /splash /login /suggestions /connect 在 shell 外, 但函数仍兜底。
      expect(phoneTabIndexFor('/search'), 0);
      expect(phoneTabIndexFor('/suggestions'), 0);
      expect(phoneTabIndexFor('/connect'), 0);
      expect(phoneTabIndexFor('/splash'), 0);
      expect(phoneTabIndexFor('/login'), 0);
    });
  });

  group('phoneTabIndexFor — 边界: 不误吞相近前缀', () {
    test('/wikifoo 不归知识库 (实现用 path==prefix || startsWith(prefix/))', () {
      // 若误用纯 startsWith(prefix) 会把 /wikifoo 吞进 tab 1 (知识库);
      // 正确实现走精确匹配 + "path/" 前缀, /wikifoo 兜底 tab 0。
      expect(phoneTabIndexFor('/chat'), 0);
      expect(phoneTabIndexFor('/wikifoo'), 0, reason: '兜底对话, 不误配知识库');
    });
  });

  // ─── widget 渲染 (R1.8 集成) ────────────────────────────────
  testWidgets('PhoneTabBar: 390 尺寸渲染 6 destination + 高亮对话 (loc=/)',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);
    // PhoneTabBar.build 用 GoRouterState.of(context) 取 loc, 需 GoRouter 树。
    final router = GoRouter(routes: [
      GoRoute(
        path: '/',
        builder: (_, _) => const Scaffold(body: PhoneTabBar()),
      ),
    ]);
    await tester.pumpWidget(MaterialApp.router(
      routerConfig: router,
      theme: buildTheme(
        palette: PaletteId.inkblueOrange,
        mode: Brightness.light,
        fontSize: FontSize.small,
      ),
      locale: const Locale('zh'),
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
    ));
    await tester.pump();
    // 6 个 tab destination 都渲染; loc=/ 兜底高亮 tab 0 (对话)。
    expect(find.byType(NavigationDestination), findsNWidgets(6));
    expect(find.text('聊天'), findsOneWidget, reason: 'tab1 navChat zh');
    expect(find.text('笔记'), findsOneWidget, reason: 'tab3 notes 硬编码 zh');
    expect(find.text('我的'), findsOneWidget, reason: 'tab6 硬编码 zh');
  });
}
