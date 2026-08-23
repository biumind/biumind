// RepoInstallPage widget 测试（M1.14）—— pin 三态 + D9 机密分流。
//
// 不走真实 HTTP（TestWidgetsFlutterBinding 会拦截 HttpClient）：
//   - repoAnalyzeProvider family 直接 overrideWith 喂分析数据 / 错误
//   - appsClientProvider 换成内存 fake（记录 installRepo 上送的 payload）
// HTTP 契约层的 path/method/解析由 apps_client_test.dart 覆盖。
//
// 断言点：
//   - 平台门控：无 runner 平台 → 降级说明
//   - 分析失败 → 服务端"不支持"原因 + 重试
//   - 确认表单：命令展示 / system 字段不渲染 / secret 标"仅存本机"
//   - 提交：config 只含非机密；secret 进 repoAppPendingEnvProvider 内存
//     接力；跳详情页

import 'dart:convert';

import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/data/api/_http_helpers.dart';
import 'package:biumind/data/api/apps_client.dart';
import 'package:biumind/data/apps_providers.dart';
import 'package:biumind/features/apps/presentation/repo_install_page.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const _runnerCaps = PlatformCaps(
  hasLocalPty: true,
  hasFileSystem: true,
  hasNotifications: true,
  supportsBackgroundIsolates: true,
  hasPersistentSqlite: true,
  hasEmbeddedWebView: true,
  hasRepoAppRunner: true,
);

const _noRunnerCaps = PlatformCaps(
  hasLocalPty: true,
  hasFileSystem: true,
  hasNotifications: true,
  supportsBackgroundIsolates: true,
  hasPersistentSqlite: true,
  hasEmbeddedWebView: false,
  hasRepoAppRunner: false,
);

final _analysis = RepoAnalysis.fromJson(jsonDecode('''
{
  "manifest_draft": {
    "identifier": "openmontage",
    "title": "OpenMontage",
    "description": "video montage service",
    "version": "1.2.0",
    "category": "content"
  },
  "stack": {
    "kind": "python-fastapi",
    "install_cmd": "uv pip install -r requirements.txt",
    "start_cmd": "uvicorn app:app --port 8800",
    "port": 8800,
    "health_path": "/healthz",
    "runtime_reqs": [
      {"name": "python3", "version": ">=3.10", "auto_install": true}
    ]
  },
  "env_schema": [
    {"name": "API_KEY", "label": "API Key", "secret": true, "optional": false, "system": false},
    {"name": "PORT", "label": "端口", "secret": false, "default": "8800", "optional": true, "system": false},
    {"name": "BIU_INSTALL_ID", "label": "", "secret": false, "optional": true, "system": true}
  ],
  "repo_meta": {
    "url": "https://github.com/acme/openmontage",
    "default_branch": "main",
    "latest_ref": "v1.2.0",
    "latest_sha": "deadbeef",
    "stars": 1234,
    "license": "MIT"
  },
  "warnings": []
}
''') as Map<String, dynamic>);

/// 内存 fake：记录 installRepo 上送的 payload，返回固定 Installation。
class _FakeAppsClient extends AppsClient {
  _FakeAppsClient() : super(Uri.parse('http://unused'));

  final List<Map<String, dynamic>> installCalls = [];

  @override
  Future<Installation> installRepo({
    required String repoUrl,
    required String refType,
    Map<String, dynamic> config = const {},
    required String token,
  }) async {
    installCalls.add({
      'repo_url': repoUrl,
      'ref_type': refType,
      'config': config,
    });
    return Installation(
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
  }
}

void main() {
  late _FakeAppsClient fakeClient;

  /// 非 null 时 analyze 抛这个错误（模拟服务端"不支持"）。
  Object? analyzeError;

  setUp(() {
    fakeClient = _FakeAppsClient();
    analyzeError = null;
  });

  Future<ProviderContainer> pumpPage(
    WidgetTester tester, {
    required PlatformCaps caps,
    String initialUrl = '',
  }) async {
    // 内容比默认 800x600 测试窗口高 —— 放大 surface 让 ListView 一次
    // 性把所有表单项 build 出来。
    tester.view.physicalSize = const Size(1400, 2400);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final container = ProviderContainer(overrides: [
      appsClientProvider.overrideWithValue(fakeClient),
      appsBearerProvider.overrideWithValue('t'),
      platformCapsProvider.overrideWithValue(caps),
      repoAnalyzeProvider.overrideWith((ref, url) async {
        final err = analyzeError;
        if (err != null) throw err;
        return _analysis;
      }),
    ]);
    addTearDown(container.dispose);

    final router = GoRouter(
      initialLocation: initialUrl.isEmpty
          ? '/apps/repo-install'
          : '/apps/repo-install?url=${Uri.encodeQueryComponent(initialUrl)}',
      routes: [
        GoRoute(
          path: '/apps/repo-install',
          // 生产由 ShellRoute 提供 Scaffold/Material；测试里自己包。
          builder: (_, state) => Scaffold(
            body: RepoInstallPage(
              repoUrl: state.uri.queryParameters['url'] ?? '',
            ),
          ),
        ),
        GoRoute(
          path: '/apps/detail/:slug',
          builder: (_, state) => Scaffold(
            body: Text('detail:${state.pathParameters['slug']}'),
          ),
        ),
      ],
    );
    addTearDown(router.dispose);

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(
        routerConfig: router,
        localizationsDelegates: const [
          AppLocalizations.delegate,
          GlobalMaterialLocalizations.delegate,
          GlobalWidgetsLocalizations.delegate,
          GlobalCupertinoLocalizations.delegate,
        ],
        supportedLocales: AppLocalizations.supportedLocales,
      ),
    ));
    await tester.pump();
    return container;
  }

  /// provider future 走 microtask —— 轮询 pump 直到 finder 出现。
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

  testWidgets('无 runner 平台 → 降级说明', (tester) async {
    await pumpPage(tester,
        caps: _noRunnerCaps,
        initialUrl: 'https://github.com/acme/openmontage');
    expect(find.textContaining('当前平台暂不支持安装 GitHub 应用'), findsOneWidget);
    expect(find.textContaining('macOS / Linux'), findsOneWidget);
  });

  testWidgets('空 url → URL 输入态；输入后进入确认表单', (tester) async {
    await pumpPage(tester, caps: _runnerCaps);
    expect(find.text('GitHub 仓库地址'), findsOneWidget);

    await tester.enterText(
        find.byType(TextField), 'https://github.com/acme/openmontage');
    await tester.tap(find.text('分析仓库'));
    await pumpUntil(tester, find.text('OpenMontage'));

    // 摘要 + 命令 + 运行时要求
    expect(find.textContaining('uv pip install'), findsOneWidget);
    expect(find.textContaining('uvicorn app:app'), findsOneWidget);
    expect(find.textContaining('python3 >=3.10'), findsOneWidget);
    // env：secret 标注 + system 字段不渲染
    expect(find.text('API Key'), findsOneWidget);
    expect(find.textContaining('仅存本机'), findsOneWidget);
    expect(find.text('BIU_INSTALL_ID'), findsNothing);
  });

  testWidgets('分析失败 → 展示服务端"不支持"原因 + 重试按钮', (tester) async {
    analyzeError = const ApiError(
      path: '/v1/apps/repo/analyze',
      status: 400,
      body: '{"error":{"code":"unsupported_repo",'
          '"message":"不支持的项目类型：未发现可运行的 Web 服务"}}',
    );
    await pumpPage(tester,
        caps: _runnerCaps, initialUrl: 'https://github.com/acme/static-site');
    await pumpUntil(tester, find.text('分析失败'));
    expect(find.textContaining('不支持的项目类型'), findsOneWidget);
    expect(find.text('重试'), findsOneWidget);
  });

  testWidgets('提交：config 只含非机密，secret 进内存接力，跳详情页',
      (tester) async {
    final container = await pumpPage(tester,
        caps: _runnerCaps, initialUrl: 'https://github.com/acme/openmontage');
    await pumpUntil(tester, find.text('OpenMontage'));

    await tester.enterText(
        find.widgetWithText(TextFormField, 'API Key'), 'sekret');
    await tester.tap(find.text('安装'));
    await pumpUntil(tester, find.text('detail:openmontage'));

    // 服务端收到的 install payload：ref_type + 非机密 config，无 API_KEY。
    expect(fakeClient.installCalls, hasLength(1));
    final sent = fakeClient.installCalls.single;
    expect(sent['repo_url'], 'https://github.com/acme/openmontage');
    expect(sent['ref_type'], 'release');
    expect(sent['config'], {'PORT': '8800'});
    expect(jsonEncode(sent), isNot(contains('API_KEY')));

    // D9：secret 走内存接力，key = installId。
    final pending = container.read(repoAppPendingEnvProvider);
    expect(pending['ins-1'], {'API_KEY': 'sekret'});
  });
}
