// DocprocPane（设置 > 通用 > 文档处理）widget 冒烟测试（P2 W3）：
// 三态渲染、点击持久化到 SharedPreferences、hasLocalDocproc=false 时
// 「优先本机」禁用 + 提示语出现。
//
// B2：pane 末尾的「Wiki 生成模型」区块走服务端偏好（ingestModelProvider
// → WikiSettingsClient）。测试里 override 成内存假 client，不发真实 HTTP。

import 'package:biumind/app/theme.dart';
import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/features/chat/data/chat_model_groups.dart';
import 'package:biumind/features/settings/application/wiki_settings_providers.dart';
import 'package:biumind/features/settings/data/wiki_settings_client.dart';
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

/// 内存假 client —— 覆盖 HTTP 层，直接记录 set 值（不发网络请求）。
class _RecordingWikiClient extends WikiSettingsClient {
  _RecordingWikiClient()
    : super(baseUrl: Uri.parse('http://localhost:1'), bearerProvider: () => null);

  String? stored;

  @override
  Future<String?> getIngestModel() async => stored;

  @override
  Future<void> putIngestModel(String? model) async {
    stored = (model == null || model.isEmpty) ? null : model;
  }
}

const _officialGroups = <ChatModelGroup>[
  ChatModelGroup(
    providerId: 'biumind-official',
    displayName: 'BiuMind Cloud',
    isOfficial: true,
    models: [
      ChatModelEntry(code: 'claude-sonnet-4', displayName: 'Claude Sonnet 4'),
      ChatModelEntry(code: 'kimi-k2', displayName: 'Kimi K2'),
    ],
  ),
  // 非 official 组不应出现在 Wiki 生成模型下拉里。
  ChatModelGroup(
    providerId: 'openai',
    displayName: 'OpenAI GPT',
    isOfficial: false,
    models: [ChatModelEntry(code: 'gpt-5', displayName: 'GPT-5')],
  ),
];

Widget _wrap({PlatformCaps? caps, _RecordingWikiClient? wikiClient}) {
  return ProviderScope(
    overrides: [
      platformCapsProvider.overrideWithValue(caps ?? _caps()),
      // B2 区块：不发真实 HTTP，保持测试封闭。
      ingestModelProvider.overrideWith(
        (ref) => IngestModelNotifier(() => wikiClient),
      ),
      chatModelGroupsProvider.overrideWith((ref) async => _officialGroups),
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

  testWidgets('Wiki 生成模型：默认「跟随平台默认」，只列官方 chat 模型',
      (tester) async {
    final client = _RecordingWikiClient();
    await tester.pumpWidget(_wrap(wikiClient: client));
    await tester.pump();
    await tester.pump();

    await tester.scrollUntilVisible(
      find.text('Wiki 生成模型'),
      100,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pump();
    await tester.pump();
    // 初始未设置 → 显示「跟随平台默认」。
    expect(find.text('跟随平台默认'), findsOneWidget);

    // 展开下拉：官方组两项在，BYOK 组（GPT-5）不在。
    await tester.tap(find.text('跟随平台默认'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('Claude Sonnet 4'), findsOneWidget);
    expect(find.text('Kimi K2'), findsOneWidget);
    expect(find.text('GPT-5'), findsNothing);
  });

  testWidgets('Wiki 生成模型：选择官方模型后 PUT 到服务端', (tester) async {
    final client = _RecordingWikiClient();
    await tester.pumpWidget(_wrap(wikiClient: client));
    await tester.pump();
    await tester.pump();

    await tester.scrollUntilVisible(
      find.text('跟随平台默认'),
      100,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pump();
    await tester.pump();
    await tester.tap(find.text('跟随平台默认'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    await tester.tap(find.text('Claude Sonnet 4'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));

    expect(client.stored, 'claude-sonnet-4');
    // 选中项回填到下拉按钮上（overlay 关闭动画期间可能短暂双份）。
    expect(find.text('Claude Sonnet 4'), findsWidgets);
  });
}
