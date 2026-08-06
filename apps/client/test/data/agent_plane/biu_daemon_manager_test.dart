// BiuDaemonManager 单测 —— 用真 Process（spawn `sh -c '<script>'`）
// 模拟 biu serve 行为：stdout 打印 BIU_BRIDGE_URL=... 然后睡着等 SIGTERM。
//
// 不模拟 binary 查找：注入 binaryResolver 直接给 sh 路径。
// 不模拟 brain 注册：daemon 跑的是 sh stub，不真调 brain。

@TestOn('vm')

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:biumind/data/agent_plane/biu_daemon_manager.dart';
import 'package:crypto/crypto.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

Future<Process> _spawnSh(String script) {
  return Process.start('/bin/sh', ['-c', script]);
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  // ensureRootTrusted：选目录后,daemon spawn 应带上 --allowed-roots <dir>;
  // 已覆盖的子目录不重复 / 不重启。
  test('ensureRootTrusted: 选目录后 spawn 带 --allowed-roots', () async {
    final captured = <List<String>>[];
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test-roots.pid',
      processSpawner: (exe, args, {environment}) {
        captured.add(args);
        return _spawnSh('echo "BIU_BRIDGE_URL=http://127.0.0.1:54399"; sleep 30');
      },
    );
    addTearDown(mgr.dispose);

    // daemon 未起时授权 → 只记信任集,不重启
    await mgr.ensureRootTrusted('/Users/didi/Downloads');

    final running = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.running)
        .timeout(const Duration(seconds: 3));
    await mgr.start();
    await running;

    expect(captured, isNotEmpty);
    final args = captured.last;
    final i = args.indexOf('--allowed-roots');
    expect(i, greaterThanOrEqualTo(0), reason: 'spawn 应带 --allowed-roots');
    expect(args[i + 1], '/Users/didi/Downloads');

    // 已覆盖的子目录 → 不应再次 spawn（无重启）
    final before = captured.length;
    await mgr.ensureRootTrusted('/Users/didi/Downloads/sub');
    expect(captured.length, before, reason: '子目录已被覆盖,不该重启');
  });
  // BiuDaemonManager.isSupported 在 dart vm 上跑（macOS / Linux test）应该 true
  test('isSupported on dart vm desktop', () {
    expect(BiuDaemonManager.isSupported, isTrue);
  });

  test('start: spawn sh stub → 解析 BIU_BRIDGE_URL → state=running', () async {
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test.pid',
      processSpawner: (exe, args, {environment}) {
        // 忽略 args（biu serve / --port 0 / 等），跑 sh 脚本：
        //   立刻打印 BIU_BRIDGE_URL=...，再 sleep 让进程不退
        return _spawnSh(
          'echo "BIU_BRIDGE_URL=http://127.0.0.1:54321"; sleep 30',
        );
      },
    );
    addTearDown(mgr.dispose);

    expect(mgr.state.status, BiuDaemonStatus.idle);

    // 监听状态变化
    final running = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.running)
        .timeout(const Duration(seconds: 3));

    await mgr.start();
    final s = await running;
    expect(s.status, BiuDaemonStatus.running);
    expect(s.bridgeUrl, 'http://127.0.0.1:54321');
    expect(s.pid, isNotNull);
  });

  test('start: binary missing → state=binaryMissing', () async {
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => null, // 模拟没找到
      pidFilePath: '/tmp/biu-test-missing.pid',
    );
    addTearDown(mgr.dispose);
    await mgr.start();
    expect(mgr.state.status, BiuDaemonStatus.binaryMissing);
    expect(mgr.state.lastError, contains('not found'));
  });

  test('stop: SIGTERM → state=stopped', () async {
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test.pid',
      processSpawner: (exe, args, {environment}) {
        return _spawnSh(
          // trap 让 SIGTERM 干净退出（exit 0）
          'trap "exit 0" TERM; '
          'echo "BIU_BRIDGE_URL=http://127.0.0.1:54322"; '
          'while true; do sleep 1; done',
        );
      },
    );
    addTearDown(mgr.dispose);
    final running = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.running)
        .timeout(const Duration(seconds: 3));
    await mgr.start();
    await running;

    await mgr.stop();
    expect(mgr.state.status, BiuDaemonStatus.stopped);
  });

  test('start idempotent: 重复调 start 不重复 spawn', () async {
    var spawnCount = 0;
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test.pid',
      processSpawner: (exe, args, {environment}) {
        spawnCount++;
        return _spawnSh(
          'echo "BIU_BRIDGE_URL=http://127.0.0.1:54323"; sleep 30',
        );
      },
    );
    addTearDown(mgr.dispose);
    final running = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.running)
        .timeout(const Duration(seconds: 3));
    await mgr.start();
    await running;

    await mgr.start(); // 第二次
    await mgr.start(); // 第三次

    expect(spawnCount, 1);
  });

  test('process exit non-zero → state=failed', () async {
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test.pid',
      processSpawner: (exe, args, {environment}) {
        return _spawnSh('exit 7'); // 立刻非零退
      },
    );
    addTearDown(mgr.dispose);
    final failed = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.failed)
        .timeout(const Duration(seconds: 3));
    await mgr.start();
    final s = await failed;
    expect(s.status, BiuDaemonStatus.failed);
    expect(s.lastError, contains('code=7'));
  });

  test('auto-respawn: 异常退出后退避自动重启,成功后 attempt 归零', () async {
    var spawnCount = 0;
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test-respawn.pid',
      restartBackoff: (_) => const Duration(milliseconds: 100),
      processSpawner: (exe, args, {environment}) {
        spawnCount++;
        if (spawnCount == 1) {
          return _spawnSh('exit 7'); // 第一次直接挂
        }
        return _spawnSh('echo "BIU_BRIDGE_URL=http://127.0.0.1:54329"; sleep 30');
      },
    );
    addTearDown(mgr.dispose);

    final failed = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.failed)
        .timeout(const Duration(seconds: 3));
    await mgr.start();
    await failed;

    // 100ms 退避后应自动 respawn 并跑起来
    final running = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.running)
        .timeout(const Duration(seconds: 3));
    final s = await running;
    expect(s.bridgeUrl, 'http://127.0.0.1:54329');
    expect(spawnCount, 2, reason: '失败后应自动 respawn 一次');
  });

  test('stop 后不再 respawn', () async {
    var spawnCount = 0;
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test-norespawn.pid',
      restartBackoff: (_) => const Duration(milliseconds: 100),
      processSpawner: (exe, args, {environment}) {
        spawnCount++;
        return _spawnSh('echo "BIU_BRIDGE_URL=http://127.0.0.1:54330"; sleep 30');
      },
    );
    addTearDown(mgr.dispose);
    final running = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.running)
        .timeout(const Duration(seconds: 3));
    await mgr.start();
    await running;
    await mgr.stop();
    // 等超过一个退避周期 —— 不该有第二次 spawn
    await Future<void>.delayed(const Duration(milliseconds: 300));
    expect(spawnCount, 1);
  });

  test('spawn 用 freshTokenProvider 的最新 token', () async {
    String? capturedPat;
    final mgr = BiuDaemonManager(
      brainBaseUrl: 'http://localhost:7003',
      bearerToken: 'stale-tok', modelRelayUrl: 'http://localhost:7001',
      freshTokenProvider: () async => 'fresh-tok',
      binaryResolver: () async => '/bin/sh',
      pidFilePath: '/tmp/biu-test-token.pid',
      processSpawner: (exe, args, {environment}) {
        capturedPat = environment?['BIUMIND_PAT'];
        return _spawnSh('echo "BIU_BRIDGE_URL=http://127.0.0.1:54331"; sleep 30');
      },
    );
    addTearDown(mgr.dispose);
    final running = mgr.stream
        .firstWhere((s) => s.status == BiuDaemonStatus.running)
        .timeout(const Duration(seconds: 3));
    await mgr.start();
    await running;
    expect(capturedPat, 'fresh-tok', reason: 'spawn 应带最新 token 而非构造时的旧 token');
  });

  group('auto-install', () {
    late Directory tmpDir;
    late List<int> binBytes;
    late String binSha;
    final platformKey = Platform.isMacOS ? 'biu-macos-arm64' : 'biu-linux-arm64';

    setUp(() {
      tmpDir = Directory.systemTemp.createTempSync('biu-install-test');
      binBytes = utf8.encode('#!/bin/sh\necho fake biu\n');
      binSha = sha256.convert(binBytes).toString();
    });
    tearDown(() {
      if (tmpDir.existsSync()) tmpDir.deleteSync(recursive: true);
    });

    Map<String, dynamic> manifestWith(Map<String, dynamic> asset) => {
          'version': '0.1.0',
          'releasedAt': '2026-01-01T00:00:00Z',
          'channel': 'stable',
          'assets': [asset],
        };

    Map<String, dynamic> biuAsset({String? sha}) => {
          'platform': platformKey,
          'url': 'https://example.test/biu-binary',
          'filename': 'biumind-0.1.0-$platformKey',
          'size': binBytes.length,
          'sha256': sha ?? binSha,
          'signed': false,
          'arch': 'arm64',
        };

    BiuDaemonManager buildMgr(
      Map<String, dynamic> manifest, {
      required String installPath,
      List<int>? overrideBinBytes,
      void Function(String exe)? onSpawn,
    }) {
      final bytes = overrideBinBytes ?? binBytes;
      return BiuDaemonManager(
        brainBaseUrl: 'https://example.test',
        bearerToken: 'tok', modelRelayUrl: 'http://localhost:7001',
        binaryResolver: () async => null, // 本机没有 → 触发自动安装
        archResolver: () async => 'arm64',
        installPathResolver: () async => installPath,
        pidFilePath: '/tmp/biu-test-autoinstall.pid',
        httpGetter: (url) async {
          if (url.path.endsWith('releases.json')) {
            return Uint8List.fromList(utf8.encode(jsonEncode(manifest)));
          }
          if (url.path == '/biu-binary') return Uint8List.fromList(bytes);
          throw StateError('unexpected url $url');
        },
        processSpawner: (exe, args, {environment}) {
          onSpawn?.call(exe);
          return _spawnSh('echo "BIU_BRIDGE_URL=http://127.0.0.1:54332"; sleep 30');
        },
      );
    }

    test('本机无 binary → 从 releases.json 下载安装并 spawn', () async {
      final installPath = '${tmpDir.path}/bin/biu';
      String? spawnedExe;
      final mgr = buildMgr(
        manifestWith(biuAsset()),
        installPath: installPath,
        onSpawn: (exe) => spawnedExe = exe,
      );
      addTearDown(mgr.dispose);
      final running = mgr.stream
          .firstWhere((s) => s.status == BiuDaemonStatus.running)
          .timeout(const Duration(seconds: 5));
      await mgr.start();
      await running;
      // 装好的文件存在 + 内容一致 + spawn 的就是装好的路径
      final installed = File(installPath);
      expect(await installed.exists(), isTrue);
      expect(await installed.readAsBytes(), equals(binBytes));
      expect(spawnedExe, installPath);
      expect(mgr.state.status, BiuDaemonStatus.running);
    });

    test('sha256 不符 → 拒绝安装,落 binaryMissing', () async {
      final mgr = buildMgr(
        manifestWith(biuAsset(sha: '0' * 64)),
        installPath: '${tmpDir.path}/bin/biu',
      );
      addTearDown(mgr.dispose);
      await mgr.start();
      expect(mgr.state.status, BiuDaemonStatus.binaryMissing);
      expect(File('${tmpDir.path}/bin/biu').existsSync(), isFalse);
    });

    test('releases.json 无 biu 产物 → 落 binaryMissing', () async {
      final mgr = buildMgr(
        manifestWith({
          'platform': 'macos-arm64', // 只有 client DMG,没有 biu CLI
          'url': 'https://example.test/app.dmg',
          'filename': 'biumind-0.1.0-macos-arm64.dmg',
          'size': 1, 'sha256': '0' * 64, 'signed': false, 'arch': 'arm64',
        }),
        installPath: '${tmpDir.path}/bin/biu',
      );
      addTearDown(mgr.dispose);
      await mgr.start();
      expect(mgr.state.status, BiuDaemonStatus.binaryMissing);
    });
  });
}
