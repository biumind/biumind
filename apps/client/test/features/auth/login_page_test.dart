// login_page_test — 锁「固定表单页零溢出」不变量 (规约见
// lib/core/ui/biu_scroll_behavior.dart 顶部注释): 最小窗口尺寸 (1024×640,
// 见 macos/Runner/MainFlutterWindow.swift + linux/runner/my_application.cc)
// 下默认登录表单必须恰好放得下 — maxScrollExtent == 0, 桌面滚动条没有
// 出现的机会。哪天这里红了, 说明内容长高了, 该收紧布局而不是让登录页
// 长出滚动条。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/features/auth/presentation/login_page.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _wrap() {
  return ProviderScope(
    overrides: [settingsRepoProvider.overrideWithValue(InMemorySettingsRepo())],
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
      home: const LoginPage(),
    ),
  );
}

void main() {
  testWidgets('最小窗口 1024×640 下默认 signIn 表单零溢出', (tester) async {
    tester.view.physicalSize = const Size(1024, 640);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(_wrap());
    await tester.pumpAndSettle();

    expect(_pageMaxScrollExtent(tester), 0.0,
        reason: '默认 signIn 表单在最小窗口下必须放得下 — 溢出就该收紧布局, '
            '而不是让登录页出现滚动条');
  });

  testWidgets('视口极矮时退化为可滚动 (兜底路径不断)', (tester) async {
    tester.view.physicalSize = const Size(1024, 400);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(_wrap());
    await tester.pumpAndSettle();

    expect(_pageMaxScrollExtent(tester), greaterThan(0.0));
  });
}

// 页面级滚动位置 — TextField 内部也有 Scrollable (单行横向), 按垂直轴
// 筛出页面那个。
double _pageMaxScrollExtent(WidgetTester tester) {
  final states = tester.stateList<ScrollableState>(find.byType(Scrollable));
  final page = states.firstWhere((s) => s.position.axis == Axis.vertical);
  return page.position.maxScrollExtent;
}
