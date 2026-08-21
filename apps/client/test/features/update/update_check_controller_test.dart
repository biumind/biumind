// nightly 更新检测 (checkNightly) 的门控测试。
//
// CI 以 --build-number=epoch秒 构建 (stable/nightly 共用时间轴), 客户端
// PackageInfo.buildNumber 即已装构建时刻; index.json 的 build 字段与产物
// versionCode 同值。两级去重:
//   1. manifest.build <= installedBuild  → 装的就是这版/更新, 不提示
//   2. manifest.run <= lastNotifiedRun   → dismiss 过, 不提示
// 用 http.runWithClient + MockClient 注入清单响应, 不起真 server。
// assets 留空 → 下载 url 走 releaseUrl fallback, 与测试机平台无关。

import 'dart:convert' show jsonEncode;

import 'package:biumind/features/update/application/update_check_controller.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart' show MockClient;

/// 起 MockClient 返回给定 nightly 清单, 在其 zone 内跑 checkNightly。
Future<UpdateInfo?> _run({
  required Map<String, dynamic> manifest,
  required int installedBuild,
  int? lastNotifiedRun,
  int statusCode = 200,
}) {
  final client = MockClient(
    (_) async => http.Response(jsonEncode(manifest), statusCode),
  );
  return http.runWithClient(
    () => checkNightly(
      origin: 'https://example.com',
      installedBuild: installedBuild,
      lastNotifiedRun: lastNotifiedRun,
    ),
    () => client,
  );
}

/// assets 留空的最小合法清单 (下载 url 走 releaseUrl fallback)。
/// build 默认取与 run 同量级的小整数, 语义等价; 真实环境是 epoch 秒。
Map<String, dynamic> _manifest(int run, {int? build, bool omitBuild = false}) => {
      'manifest': 'nightly-canary-v1',
      'channel': 'nightly',
      'version': '0.1.0',
      'run': run,
      if (!omitBuild) 'build': build ?? run,
      'releaseUrl': 'https://github.com/biumind/biumind/releases/tag/client-nightly.$run',
      'notes': 'nightly',
      'assets': <dynamic>[],
    };

void main() {
  group('checkNightly 门控', () {
    test('已装同版 nightly (build == installedBuild) → 不提示', () async {
      final info = await _run(manifest: _manifest(27), installedBuild: 27);
      expect(info, isNull);
    });

    test('已装更新构建 (build < installedBuild, 如 stable 晚于夜版构建) → 不提示', () async {
      final info = await _run(manifest: _manifest(26), installedBuild: 27);
      expect(info, isNull);
    });

    test('已装更旧 (build > installedBuild) → 提示, run/通道/url 正确', () async {
      final info = await _run(manifest: _manifest(28), installedBuild: 27);
      expect(info, isNotNull);
      expect(info!.isNightly, isTrue);
      expect(info.nightlyRun, 28);
      expect(info.downloadPageUrl, contains('client-nightly.28'));
    });

    test('旧版夜版 (buildNumber 非时间戳, 按 0) → 仍提示一次', () async {
      final info = await _run(manifest: _manifest(27), installedBuild: 0);
      expect(info, isNotNull);
      expect(info!.nightlyRun, 27);
    });

    test('旧清单无 build 字段 → 回退 run 比较 (旧夜版 versionCode 即 run)', () async {
      final info = await _run(
        manifest: _manifest(27, omitBuild: true),
        installedBuild: 27,
      );
      expect(info, isNull);
    });

    test('run 与 build 脱钩: build 更旧但 run 更新 → 不提示 (以 build 为准)', () async {
      final info = await _run(
        manifest: _manifest(30, build: 20),
        installedBuild: 27,
      );
      expect(info, isNull);
    });

    test('未装新版但已 dismiss 过该 run → 不提示', () async {
      final info = await _run(
        manifest: _manifest(28),
        installedBuild: 27,
        lastNotifiedRun: 28,
      );
      expect(info, isNull);
    });

    test('dismiss 过旧号, 来了更新号 → 提示', () async {
      final info = await _run(
        manifest: _manifest(29),
        installedBuild: 27,
        lastNotifiedRun: 28,
      );
      expect(info, isNotNull);
      expect(info!.nightlyRun, 29);
    });

    test('非 200 响应 → null (静默)', () async {
      final info = await _run(
        manifest: _manifest(28),
        installedBuild: 27,
        statusCode: 404,
      );
      expect(info, isNull);
    });
  });
}
