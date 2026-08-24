// RepoAppLauncher 单元测试（Repo Apps M2）。
//
// 不起真实子进程：binary 查找走 binaryResolver 注入，进程执行走
// RepoAppProcessSpawner 注入的 _FakeProcess（脚本化 stdout/stderr/
// exitCode）。pin：
//   1. sanitiseForFS / slugFromRepoUrl 与 CLI repoapp/store.go 同规则
//      （用例照 store_test.go 的 TestSanitiseForFS）
//   2. updateRepoApp 参数列表 1:1（--ref 缺失时省略，兼容老服务端）
//   3. 成功 = stdout 通告 BIU_REPOAPP_URL；失败 = stderr 尾部 + exit code

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/agent_plane/repo_app_launcher.dart';
import 'package:flutter_test/flutter_test.dart';

/// 脚本化假进程：构造时预置 stdout/stderr 行（单订阅 controller 会
/// 缓冲到 listen 时投递）；[exitAfter] 非 null 时延时完成 exitCode，
/// null 表示进程一直不退（成功路径 —— launcher 拿到 URL 后 kill）。
class _FakeProcess implements RepoAppProcess {
  _FakeProcess({
    List<String> stdoutLines = const [],
    List<String> stderrLines = const [],
    this.exitCodeValue = 0,
    Duration? exitAfter,
  }) {
    for (final l in stdoutLines) {
      _out.add(utf8.encode('$l\n'));
    }
    unawaited(_out.close());
    for (final l in stderrLines) {
      _err.add(utf8.encode('$l\n'));
    }
    unawaited(_err.close());
    if (exitAfter != null) {
      // 延时完成，保证流事件先于 exit 投递（顺序敏感）。
      Timer(exitAfter, () {
        if (!_exit.isCompleted) _exit.complete(exitCodeValue);
      });
    }
  }

  final _out = StreamController<List<int>>();
  final _err = StreamController<List<int>>();
  final _exit = Completer<int>();
  final int exitCodeValue;
  final killSignals = <ProcessSignal>[];

  @override
  Stream<List<int>> get stdout => _out.stream;
  @override
  Stream<List<int>> get stderr => _err.stream;
  @override
  Future<int> get exitCode => _exit.future;
  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    killSignals.add(signal);
    if (!_exit.isCompleted) _exit.complete(exitCodeValue);
    return true;
  }
}

void main() {
  group('sanitiseForFS（与 CLI store.go 同规则）', () {
    // 用例 1:1 照 apps/cli/biu/internal/repoapp/store_test.go。
    const cases = {
      'owner/repo': 'owner-repo',
      'a//b///c': 'a-b-c',
      '--lead-trail--': 'lead-trail',
      'under_score-ok': 'under_score-ok',
      'spaces and.dots': 'spaces-and-dots',
      // 大小写不折叠（Go 实现原样保留 A-Z，与 CLI 建目录一致）。
      'Owner/Repo': 'Owner-Repo',
    };
    for (final e in cases.entries) {
      test('${e.key} → ${e.value}', () {
        expect(RepoAppLauncher.sanitiseForFS(e.key), e.value);
      });
    }
  });

  group('slugFromRepoUrl', () {
    const cases = {
      'https://github.com/acme/openmontage': 'acme-openmontage',
      'https://github.com/acme/openmontage.git': 'acme-openmontage',
      'https://www.github.com/acme/openmontage': 'acme-openmontage',
      'git@github.com:acme/openmontage.git': 'acme-openmontage',
      'acme/openmontage': 'acme-openmontage',
    };
    for (final e in cases.entries) {
      test('${e.key} → ${e.value}', () {
        expect(RepoAppLauncher.slugFromRepoUrl(e.key), e.value);
      });
    }

    test('非 github / 多层路径 / 空串 → null', () {
      expect(RepoAppLauncher.slugFromRepoUrl('https://gitlab.com/a/b'), isNull);
      expect(RepoAppLauncher.slugFromRepoUrl(
          'https://github.com/a/b/tree/main'), isNull);
      expect(RepoAppLauncher.slugFromRepoUrl(''), isNull);
      expect(RepoAppLauncher.slugFromRepoUrl('not a url at all'), isNull);
    });
  });

  group('updateRepoApp', () {
    late List<(String, List<String>, Map<String, String>?)> spawned;
    late _FakeProcess fake;
    late RepoAppLauncher launcher;

    setUp(() {
      spawned = [];
      launcher = RepoAppLauncher(
        binaryResolver: () async => '/fake/biu',
        spawner: (bin, args, env) async {
          spawned.add((bin, args, env));
          return fake;
        },
      );
    });

    test('成功：参数 1:1 + 解析 BIU_REPOAPP_URL + 拿到 URL 后 kill',
        () async {
      fake = _FakeProcess(stdoutLines: [
        '[repo-app] updating to ref=v1.3.0 ...',
        'BIU_REPOAPP_URL=http://127.0.0.1:8800',
      ]);

      final res = await launcher.updateRepoApp(
        slug: 'acme-openmontage',
        installId: 'ins-1',
        buildId: 'b2',
        reportUrl: 'http://localhost:8088',
        ref: 'v1.3.0',
      );

      expect(res.url, 'http://127.0.0.1:8800');
      final (bin, args, env) = spawned.single;
      expect(bin, '/fake/biu');
      expect(args, [
        'repo-app', 'update', 'acme-openmontage',
        '--ref', 'v1.3.0',
        '--install-id', 'ins-1',
        '--build-id', 'b2',
        '--report-url', 'http://localhost:8088',
      ]);
      expect(env, isNull); // shellEnv 未注入 → 不加 environment
      expect(fake.killSignals, isNotEmpty);
    });

    test('ref 空 → 省略 --ref（兼容未返回 ref/sha 的老服务端）', () async {
      fake = _FakeProcess(
          stdoutLines: ['BIU_REPOAPP_URL=http://127.0.0.1:9000']);

      await launcher.updateRepoApp(
        slug: 'acme-openmontage',
        installId: 'ins-1',
        buildId: 'b2',
        reportUrl: 'http://localhost:8088',
      );

      final (_, args, _) = spawned.single;
      expect(args, isNot(contains('--ref')));
      expect(args, [
        'repo-app', 'update', 'acme-openmontage',
        '--install-id', 'ins-1',
        '--build-id', 'b2',
        '--report-url', 'http://localhost:8088',
      ]);
    });

    test('失败：进程先于 URL 通告退出 → 带 stderr 尾部 + exit code',
        () async {
      fake = _FakeProcess(
        stderrLines: ['[repo-app] updating to ref=v9 ...', 'git fetch: fatal: not found'],
        exitCodeValue: 3,
        exitAfter: const Duration(milliseconds: 20),
      );

      await expectLater(
        launcher.updateRepoApp(
          slug: 'acme-openmontage',
          installId: 'ins-1',
          buildId: 'b2',
          reportUrl: 'http://localhost:8088',
          ref: 'v9',
        ),
        throwsA(isA<RepoAppEnsureException>()
            .having((e) => e.exitCode, 'exitCode', 3)
            .having((e) => e.message, 'message',
                allOf(contains('git fetch: fatal'), contains('updating')))),
      );
    });

    test('ensure 回归：--env 重复 flag + URL 解析不变', () async {
      fake = _FakeProcess(
          stdoutLines: ['BIU_REPOAPP_URL=http://127.0.0.1:7000']);

      final res = await launcher.ensure(
        'ins-1',
        env: {'API_KEY': 'sekret', 'PORT': '8800'},
      );

      expect(res.url, 'http://127.0.0.1:7000');
      final (_, args, _) = spawned.single;
      expect(args, [
        'repo-app', 'ensure', 'ins-1',
        '--env', 'API_KEY=sekret',
        '--env', 'PORT=8800',
      ]);
    });

    test('找不到 biu binary → 中文引导文案', () async {
      final noBin = RepoAppLauncher(binaryResolver: () async => null);
      await expectLater(
        noBin.updateRepoApp(
          slug: 'x',
          installId: 'i',
          buildId: 'b',
          reportUrl: 'http://x',
        ),
        throwsA(isA<RepoAppEnsureException>().having(
            (e) => e.message, 'message', contains('找不到 biu 命令行工具'))),
      );
      expect(spawned, isEmpty);
    });
  });
}
