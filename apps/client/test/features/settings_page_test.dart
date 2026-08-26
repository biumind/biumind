import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/features/settings/presentation/settings_page.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _wrap(InMemorySettingsRepo repo) {
  return ProviderScope(
    overrides: [settingsRepoProvider.overrideWithValue(repo)],
    // 真实主题 (注册 BiuColors extension) —— AppearancePane 等 pane 依赖它。
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
      home: const Scaffold(body: SettingsPage()),
    ),
  );
}

void main() {
  testWidgets(
    'renders three nav groups + 模型服务 nav item by default',
    (tester) async {
      final repo = InMemorySettingsRepo(
        const AppSettings(
          identityUrl: 'http://existing:7004',
          accessToken: 'tok',
          userEmail: 'u@e.com',
        ),
      );
      tester.view.physicalSize = const Size(1400, 1000);
      tester.view.devicePixelRatio = 1.0;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);

      await tester.pumpWidget(_wrap(repo));
      await tester.pumpAndSettle();

      // Three nav-group headings (uppercase EN labels)
      expect(find.text('GENERAL'), findsOneWidget);
      expect(find.text('AGENT'), findsOneWidget);
      expect(find.text('SYSTEM'), findsOneWidget);

      // Nav items present (active + greyed all rendered)
      expect(find.text('模型服务'), findsOneWidget);
      expect(find.text('Statistics'), findsOneWidget);
      // 「我的分享」加入后左栏变长，About 滚出首屏 —— 先滚到可见再断言。
      await tester.dragUntilVisible(
        find.text('About'),
        find.byType(ListView).first,
        const Offset(0, -200),
      );
      expect(find.text('About'), findsOneWidget);
    },
  );

  testWidgets('groups appear in expected vertical order', (tester) async {
    final repo = InMemorySettingsRepo();
    tester.view.physicalSize = const Size(1600, 1600);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(_wrap(repo));
    await tester.pumpAndSettle();

    final general = tester.getTopLeft(find.text('GENERAL')).dy;
    final agent = tester.getTopLeft(find.text('AGENT')).dy;
    final system = tester.getTopLeft(find.text('SYSTEM')).dy;
    expect(general, lessThan(agent));
    expect(agent, lessThan(system));
  });

  testWidgets('phone: lands on category list, tap opens detail, back returns', (
    tester,
  ) async {
    final repo = InMemorySettingsRepo();
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(_wrap(repo));
    await tester.pumpAndSettle();

    // 列表态: ☰ + 分类导航全宽, 无三栏
    expect(find.byIcon(Icons.more_vert), findsOneWidget);
    expect(find.text('模型服务'), findsOneWidget);
    expect(find.byIcon(Icons.arrow_back), findsNothing);

    // 点 Appearance → 详情态 (← + pane)
    await tester.tap(find.text('Appearance'));
    await tester.pumpAndSettle();
    expect(find.byIcon(Icons.arrow_back), findsOneWidget);

    // 返回 → 列表态
    await tester.tap(find.byIcon(Icons.arrow_back));
    await tester.pumpAndSettle();
    expect(find.text('模型服务'), findsOneWidget);
    expect(find.byIcon(Icons.more_vert), findsOneWidget);
  });

  testWidgets('phone: system back in detail returns to list (PopScope)', (
    tester,
  ) async {
    final repo = InMemorySettingsRepo();
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(_wrap(repo));
    await tester.pumpAndSettle();

    // 进详情
    await tester.tap(find.text('Appearance'));
    await tester.pumpAndSettle();
    expect(find.byIcon(Icons.arrow_back), findsOneWidget);

    // Android 系统返回 → 回列表 (而不是退出/黑屏)
    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.text('模型服务'), findsOneWidget);
    expect(find.byIcon(Icons.more_vert), findsOneWidget);
  });
}
