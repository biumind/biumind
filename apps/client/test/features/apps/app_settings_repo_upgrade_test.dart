// AppSettingsPage 升级按钮 repo 分支 widget 测试（Repo Apps M2）。
//
// 不走真实 HTTP（TestWidgetsFlutterBinding 会拦截 HttpClient）：
//   - installationsProvider / upgradeStatusProvider / appsCatalogProvider
//     全部 overrideWith 喂内存数据
//   - appsClientProvider 换内存 fake（记录 redeploy / upgrade 调用）
//   - repoAppLauncherProvider 换 fake（记录 updateRepoApp 调用 / 抛错）
//
// 断言点：
//   - repo 安装（catalog 带 repo_meta）：跳过 UpgradeDialog → 确认框 →
//     redeployRepo → updateRepoApp（slug/ref/buildId/reportUrl 1:1）→
//     toast；client.upgrade 不被调用
//   - updateRepoApp 失败 → humanize toast，不炸页面
//   - 非 repo 安装：仍走 UpgradeDialog（原流程回归）
//   - 点击升级时 upgradeStatusProvider 真重查（伪 fresh 修复，M2.5）

import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/data/agent_plane/repo_app_launcher.dart';
import 'package:biumind/data/api/apps_client.dart';
import 'package:biumind/data/apps_providers.dart';
import 'package:biumind/features/apps/presentation/app_settings_page.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _runnerCaps = PlatformCaps(
  hasLocalPty: true,
  hasFileSystem: true,
  hasNotifications: true,
  supportsBackgroundIsolates: true,
  hasPersistentSqlite: true,
  hasEmbeddedWebView: true,
  hasRepoAppRunner: true,
);

final _repoInstall = Installation(
  id: 'ins-1',
  scope: 'user',
  scopeId: 'u1',
  appId: 'app-1',
  identifier: 'openmontage',
  version: '1.2.0',
  enabled: true,
  installedAt: DateTime(2026, 8, 23),
  updatedAt: DateTime(2026, 8, 23),
);

final _plainInstall = Installation(
  id: 'ins-2',
  scope: 'user',
  scopeId: 'u1',
  appId: 'app-2',
  identifier: 'rss',
  version: '0.1.0',
  enabled: true,
  installedAt: DateTime(2026, 8, 23),
  updatedAt: DateTime(2026, 8, 23),
);

const _repoStatus = UpgradeStatus(
  available: true,
  currentVersion: '1.2.0',
  targetVersion: '1.3.0',
  requiresApproval: false,
);

final _repoEntry = AppCatalogEntry(
  identifier: 'openmontage',
  name: 'OpenMontage',
  description: '',
  version: '1.3.0',
  tier: 'repo',
  repoMeta: const RepoMeta(url: 'https://github.com/acme/openmontage'),
);

const _plainEntry = AppCatalogEntry(
  identifier: 'rss',
  name: 'RSS',
  description: '',
  version: '0.2.0',
);

class _FakeAppsClient extends AppsClient {
  _FakeAppsClient() : super(Uri.parse('http://unused'));

  final List<String> redeployCalls = [];
  final List<String> upgradeCalls = [];

  @override
  Future<RepoRedeployResult> redeployRepo({
    required String installId,
    required String token,
  }) async {
    redeployCalls.add(installId);
    return const RepoRedeployResult(
        buildId: 'b2', ref: 'v1.3.0', sha: 'cafe0123');
  }

  @override
  Future<Installation> upgrade({
    required String installId,
    List<String> acceptedNewPermissions = const [],
    required String token,
  }) async {
    upgradeCalls.add(installId);
    return _plainInstall;
  }
}

class _FakeLauncher extends RepoAppLauncher {
  final List<({String slug, String installId, String buildId, String reportUrl, String ref})>
      updateCalls = [];

  /// 非 null 时 updateRepoApp 抛这个错误。
  Object? updateError;

  @override
  Future<RepoAppEnsureResult> updateRepoApp({
    required String slug,
    required String installId,
    required String buildId,
    required String reportUrl,
    String ref = '',
    void Function(String line)? onLog,
  }) async {
    updateCalls.add((
      slug: slug,
      installId: installId,
      buildId: buildId,
      reportUrl: reportUrl,
      ref: ref,
    ));
    final err = updateError;
    if (err != null) throw err;
    return const RepoAppEnsureResult('http://127.0.0.1:8800');
  }
}

void main() {
  late _FakeAppsClient fakeClient;
  late _FakeLauncher fakeLauncher;
  late int statusFetches;

  setUp(() {
    fakeClient = _FakeAppsClient();
    fakeLauncher = _FakeLauncher();
    statusFetches = 0;
  });

  Future<void> pumpPage(
    WidgetTester tester, {
    required List<Installation> installs,
    required List<AppCatalogEntry> catalog,
  }) async {
    tester.view.physicalSize = const Size(1400, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final container = ProviderContainer(overrides: [
      appsClientProvider.overrideWithValue(fakeClient),
      appsBearerProvider.overrideWithValue('t'),
      platformCapsProvider.overrideWithValue(_runnerCaps),
      repoAppLauncherProvider.overrideWithValue(fakeLauncher),
      installationsProvider.overrideWith(
          (ref, scope) async => scope == 'user' ? installs : const []),
      upgradeStatusProvider.overrideWith((ref, id) async {
        statusFetches++;
        return _repoStatus;
      }),
      appsCatalogProvider.overrideWith((ref) async => catalog),
      // _Row 懒加载 manifest 决定"打开"按钮 —— 喂空 map 避免打 HTTP。
      manifestProvider.overrideWith((ref, identifier) async => const {}),
    ]);
    addTearDown(container.dispose);

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        localizationsDelegates: const [
          AppLocalizations.delegate,
          GlobalMaterialLocalizations.delegate,
          GlobalWidgetsLocalizations.delegate,
          GlobalCupertinoLocalizations.delegate,
        ],
        supportedLocales: AppLocalizations.supportedLocales,
        // 生产由 ShellRoute 提供 Scaffold/Material；测试里自己包。
        home: const Scaffold(body: AppSettingsPage()),
      ),
    ));
    await tester.pump();
  }

  Future<void> pumpUntil(
    WidgetTester tester,
    Finder finder, {
    int maxRounds = 50,
  }) async {
    for (var i = 0; i < maxRounds; i++) {
      await tester.pump(const Duration(milliseconds: 20));
      if (finder.evaluate().isNotEmpty) return;
    }
    fail('pumpUntil timeout: $finder');
  }

  testWidgets('repo 安装：跳过 UpgradeDialog，确认后 redeploy + 本机 update',
      (tester) async {
    await pumpPage(tester,
        installs: [_repoInstall], catalog: [_repoEntry]);
    await pumpUntil(tester, find.widgetWithText(FilledButton, 'Upgrade'));

    final fetchesBeforeTap = statusFetches;
    await tester.tap(find.widgetWithText(FilledButton, 'Upgrade'));
    // 确认框（不是权限 diff 弹窗）。
    await pumpUntil(tester, find.text('Update GitHub app'));
    expect(find.text('Will update to v1.3.0. The app will be briefly '
        'unavailable while updating.'), findsOneWidget);
    // 伪 fresh 修复：点击后 status 真重查了一次。
    expect(statusFetches, greaterThan(fetchesBeforeTap));

    await tester.tap(find.widgetWithText(FilledButton, 'Upgrade').last);
    await pumpUntil(tester, find.text('Upgraded.'));

    // 服务端 redeploy + 本机 update，client.upgrade 不被调用。
    expect(fakeClient.redeployCalls, ['ins-1']);
    expect(fakeClient.upgradeCalls, isEmpty);
    final call = fakeLauncher.updateCalls.single;
    expect(call.slug, 'acme-openmontage');
    expect(call.installId, 'ins-1');
    expect(call.buildId, 'b2');
    expect(call.reportUrl, 'http://unused');
    expect(call.ref, 'v1.3.0');
  });

  testWidgets('repo 安装：取消确认框 → 不发 redeploy', (tester) async {
    await pumpPage(tester,
        installs: [_repoInstall], catalog: [_repoEntry]);
    await pumpUntil(tester, find.widgetWithText(FilledButton, 'Upgrade'));

    await tester.tap(find.widgetWithText(FilledButton, 'Upgrade'));
    await pumpUntil(tester, find.text('Update GitHub app'));
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();

    expect(fakeClient.redeployCalls, isEmpty);
    expect(fakeLauncher.updateCalls, isEmpty);
  });

  testWidgets('repo 安装：本机 update 失败 → humanize toast', (tester) async {
    fakeLauncher.updateError =
        const RepoAppEnsureException('git fetch: fatal: not found', exitCode: 3);
    await pumpPage(tester,
        installs: [_repoInstall], catalog: [_repoEntry]);
    await pumpUntil(tester, find.widgetWithText(FilledButton, 'Upgrade'));

    await tester.tap(find.widgetWithText(FilledButton, 'Upgrade'));
    await pumpUntil(tester, find.text('Update GitHub app'));
    await tester.tap(find.widgetWithText(FilledButton, 'Upgrade').last);
    await pumpUntil(tester, find.textContaining('Operation failed'));

    expect(find.textContaining('git fetch: fatal'), findsOneWidget);
    // 失败不 toast 成功。
    expect(find.text('Upgraded.'), findsNothing);
  });

  testWidgets('非 repo 安装：仍走 UpgradeDialog（原流程回归）', (tester) async {
    await pumpPage(tester,
        installs: [_plainInstall], catalog: [_plainEntry]);
    await pumpUntil(tester, find.widgetWithText(FilledButton, 'Upgrade'));

    await tester.tap(find.widgetWithText(FilledButton, 'Upgrade'));
    // 权限 diff 弹窗（upgradeTitle：Upgrade {name}: v{from} → v{to}）。
    await pumpUntil(tester, find.text('Upgrade rss: v1.2.0 → v1.3.0'));
    expect(find.text('Update GitHub app'), findsNothing);
    expect(fakeClient.redeployCalls, isEmpty);
    expect(fakeLauncher.updateCalls, isEmpty);
  });
}
