// nightly 更新检测 (checkNightly) 的 run 门控测试。
//
// nightly workflow 以 --build-number=run_number 构建, 客户端 PackageInfo
// .buildNumber 即已装 nightly 号。两级去重:
//   1. manifest.run <= installedRun  → 装的就是这号/更新, 不提示
//   2. manifest.run <= lastNotifiedRun → dismiss 过, 不提示
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
  required int installedRun,
  int? lastNotifiedRun,
  int statusCode = 200,
}) {
  final client = MockClient(
    (_) async => http.Response(jsonEncode(manifest), statusCode),
  );
  return http.runWithClient(
    () => checkNightly(
      origin: 'https://example.com',
      installedRun: installedRun,
      lastNotifiedRun: lastNotifiedRun,
    ),
    () => client,
  );
}

/// assets 留空的最小合法清单 (下载 url 走 releaseUrl fallback)。
Map<String, dynamic> _manifest(int run) => {
      'manifest': 'nightly-canary-v1',
      'channel': 'nightly',
      'version': '0.1.0',
      'run': run,
      'releaseUrl': 'https://github.com/biumind/biumind/releases/tag/client-nightly.$run',
      'notes': 'nightly',
      'assets': <dynamic>[],
    };

void main() {
  group('checkNightly run 门控', () {
    test('已装同号 nightly (run == installedRun) → 不提示', () async {
      final info = await _run(manifest: _manifest(27), installedRun: 27);
      expect(info, isNull);
    });

    test('已装更新 nightly (run < installedRun) → 不提示', () async {
      final info = await _run(manifest: _manifest(26), installedRun: 27);
      expect(info, isNull);
    });

    test('已装更旧 (run > installedRun) → 提示, run/通道/url 正确', () async {
      final info = await _run(manifest: _manifest(28), installedRun: 27);
      expect(info, isNotNull);
      expect(info!.isNightly, isTrue);
      expect(info.nightlyRun, 28);
      expect(info.downloadPageUrl, contains('client-nightly.28'));
    });

    test('旧版夜版 (buildNumber 无 run, 按 0) → 仍提示一次', () async {
      final info = await _run(manifest: _manifest(27), installedRun: 0);
      expect(info, isNotNull);
      expect(info!.nightlyRun, 27);
    });

    test('未装新版但已 dismiss 过该 run → 不提示', () async {
      final info = await _run(
        manifest: _manifest(28),
        installedRun: 27,
        lastNotifiedRun: 28,
      );
      expect(info, isNull);
    });

    test('dismiss 过旧号, 来了更新号 → 提示', () async {
      final info = await _run(
        manifest: _manifest(29),
        installedRun: 27,
        lastNotifiedRun: 28,
      );
      expect(info, isNotNull);
      expect(info!.nightlyRun, 29);
    });

    test('非 200 响应 → null (静默)', () async {
      final info = await _run(
        manifest: _manifest(28),
        installedRun: 27,
        statusCode: 404,
      );
      expect(info, isNull);
    });
  });
}
