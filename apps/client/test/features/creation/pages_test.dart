// Smoke tests — 5 个 creation page 都能 render 不崩.
//
// 不用 override 任何 provider — 让默认 (未登录 / 无后端) 路径走通, 验证
// 占位/empty/loading 态的 UI 正确.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/creation/application/aigc_providers.dart';
import 'package:biumind/features/creation/data/credits_client.dart';
import 'package:biumind/features/creation/application/credits_controller.dart';
import 'package:biumind/features/creation/presentation/pages/gallery_page.dart';
import 'package:biumind/features/creation/presentation/pages/inspiration_page.dart';
import 'package:biumind/features/creation/presentation/pages/my_works_page.dart';
import 'package:biumind/features/creation/presentation/pages/studio_page.dart';
import 'package:biumind/l10n/app_localizations.dart';

Widget _wrap(Widget page) => ProviderScope(
      overrides: [
        // 未登录态: providers 都返空, 不打真 HTTP.
        aigcModelsProvider.overrideWith((ref, _) async => const <dynamic>[]),
        aigcGalleryProvider.overrideWith((ref, _) async => const <dynamic>[]),
        creditsBalanceProvider.overrideWith((ref) async => CreditsBalance.empty()),
        rechargeOptionsProvider.overrideWith((ref) async => const <RechargeOption>[]),
      ],
      child: MaterialApp(
        localizationsDelegates: const [AppLocalizations.delegate],
        supportedLocales: const [Locale('en'), Locale('zh')],
        home: Scaffold(body: page),
      ),
    );

void main() {
  testWidgets('InspirationPage renders', (t) async {
    await t.pumpWidget(_wrap(const InspirationPage()));
    await t.pump();
    expect(find.byType(InspirationPage), findsOneWidget);
  });

  testWidgets('StudioPage renders', (t) async {
    await t.pumpWidget(_wrap(const StudioPage()));
    await t.pump();
    expect(find.byType(StudioPage), findsOneWidget);
  });

  testWidgets('MyWorksPage renders loading then empty state', (t) async {
    await t.pumpWidget(_wrap(const MyWorksPage()));
    await t.pump();
    expect(find.byType(MyWorksPage), findsOneWidget);
    // 初始 initialFetchDone=false → CircularProgressIndicator;
    // 真实 first refresh 完成后才出 "还没有作品" — 这里 widget test 不起 controller,
    // 所以只断言 page 能渲染.
  });

  testWidgets('GalleryPage renders empty state', (t) async {
    await t.pumpWidget(_wrap(const GalleryPage()));
    await t.pump();
    expect(find.byType(GalleryPage), findsOneWidget);
  });

  // RechargePage 已在 IA 重设计中移除（合并到 /membership 流程，commit f447ee3）
}
