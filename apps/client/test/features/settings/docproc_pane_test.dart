// DocprocPane（设置 > 通用 > 文档处理）widget 冒烟测试（P2 W3）：
// 三态渲染、点击持久化到 SharedPreferences、hasLocalDocproc=false 时
// 「优先本机」禁用 + 提示语出现。

import 'package:biumind/app/theme.dart';
import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/features/settings/presentation/docproc_pane.dart';
import 'package:biumind/features/wiki/application/docproc_preferences.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

PlatformCaps _caps({bool localDocproc = true}) => PlatformCaps(
      hasLocalPty: true,
      hasFileSystem: true,
      hasNotifications: true,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: true,
      hasEmbeddedWebView: true,
      hasRepoAppRunner: false,
      hasLocalDocproc: localDocproc,
    );

Widget _wrap({PlatformCaps? caps}) {
  return ProviderScope(
    overrides: [
      platformCapsProvider.overrideWithValue(caps ?? _caps()),
    ],
    child: MaterialApp(
      locale: const Locale('zh'),
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
      home: const Scaffold(body: DocprocPane()),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets('三态渲染，默认选中自动', (tester) async {
    await tester.pumpWidget(_wrap());
    await tester.pumpAndSettle();

    expect(find.text('自动'), findsOneWidget);
    expect(find.text('优先本机'), findsOneWidget);
    expect(find.text('优先云端'), findsOneWidget);
    expect(find.textContaining('OCR'), findsOneWidget); // 说明文案
  });

  testWidgets('点击优先云端：状态 + SharedPreferences 持久化', (tester) async {
    await tester.pumpWidget(_wrap());
    await tester.pumpAndSettle();

    await tester.tap(find.text('优先云端'));
    // 不用 pumpAndSettle（Radio 切换动画 + prefs 写盘会让 settle 等满超时），
    // 手动泵几帧即可。
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 200));

    // testWidgets 的 FakeAsync 区域里直接 await 平台通道会挂死，
    // SharedPreferences 读写包 runAsync（真事件循环）。
    await tester.runAsync(() async {
      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('biu.wiki.docproc'), contains('preferCloud'));

      // 新 notifier 实例能恢复（跨会话持久化）。
      final n = DocprocPreferencesNotifier();
      await Future<void>.delayed(Duration.zero);
      expect(n.state.location, DocprocProcessLocation.preferCloud);
    });
  });

  testWidgets('hasLocalDocproc=false：优先本机禁用 + 提示语', (tester) async {
    await tester.pumpWidget(_wrap(caps: _caps(localDocproc: false)));
    await tester.pumpAndSettle();

    expect(find.textContaining('当前平台不支持本机处理'), findsOneWidget);

    // 禁用项点击不生效：设置保持默认 auto。
    await tester.tap(find.text('优先本机'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 200));
    await tester.runAsync(() async {
      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('biu.wiki.docproc'), isNull);
    });
  });
}
