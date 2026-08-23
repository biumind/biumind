// RepoAppLauncher —— 客户端 ↔ 本机 repo-app runner 的通道（Repo Apps M1）。
//
// 技术方案 §3.4：客户端经现有 daemon 管理通道执行
// `biu repo-app ensure <install>`，解析其 stdout 通告的
// `BIU_REPOAPP_URL=http://127.0.0.1:<port>`（通告协议照 `biu serve` 的
// `BIU_BRIDGE_URL` 先例，serve_cmd.go）。
//
// 对接决策：BiuDaemonManager 管的是长驻 `biu serve` 进程，没有"一次性
// 执行子命令"的通道，bridge（loopback HTTP+WS）是 SDK Protocol 数据面，
// 不适合塞 CLI exec。因此 ensure 走独立的一次性进程 spawn，但复用
// daemon 管理代码的两块基建：
//   - binary 查找链：BiuDaemonManager.resolveBiuBinary（BIU_BIN →
//     login-shell PATH → 常见路径 → macOS bundle）
//   - login shell env：GUI app 的 Platform.environment 不含用户 PATH，
//     子进程必须带 shellEnv 才能找到 git/node/uv
//
// 机密 env（D9）：随 ensure 以 `--env NAME=VALUE` 重复 flag 下发，CLI
// 负责写实例目录 .env（明文 0600）。客户端不持久化这些值。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:logging/logging.dart';

import '../../services/login_shell_env.dart';
import 'biu_daemon_manager.dart';

/// ensure 的一次运行结果。
class RepoAppEnsureResult {
  /// 解析自 stdout 的 `BIU_REPOAPP_URL=` 通告。
  final String url;
  const RepoAppEnsureResult(this.url);
}

/// ensure 失败：exit code + stderr 末尾几行，给 UI 展示可读原因。
class RepoAppEnsureException implements Exception {
  final int? exitCode;
  final String message;
  const RepoAppEnsureException(this.message, {this.exitCode});

  @override
  String toString() =>
      'RepoAppEnsureException(exit=$exitCode): $message';
}

class RepoAppLauncher {
  RepoAppLauncher({
    this.shellEnv,
    Logger? logger,
    this.timeout = const Duration(minutes: 10),
  }) : _log = logger ?? Logger('biumind.repo_app');

  /// 用户 login shell 环境（完整 PATH）。null 时退回 Platform.environment。
  final LoginShellEnv? shellEnv;

  /// clone + 装依赖可能很慢（首次安装大型 Node 项目分钟级），给 10min。
  final Duration timeout;
  final Logger _log;

  static const _urlPrefix = 'BIU_REPOAPP_URL=';

  /// 执行 `biu repo-app ensure <installId>` 并等待 URL 通告。
  ///
  /// 平台门控在调用方（PlatformCaps.hasRepoAppRunner，C5：这里不许再
  /// 出现裸 Platform.isXXX）。本类 import dart:io，web 本就无法加载。
  ///
  /// [env] 为随 ensure 下发的配置（含机密），以 `--env NAME=VALUE` 传
  /// 给 CLI；[onLog] 每收到一行 stdout/stderr 回调一次（等待页展示进
  /// 度用）。
  ///
  /// 成功条件：stdout 出现 `BIU_REPOAPP_URL=` 行（此后进程应自行退
  /// 出；不等 exit，拿到 URL 即返回，避免 runner 前台挂住拖死 UI）。
  /// 进程先于 URL 通告退出 → 抛 [RepoAppEnsureException]（带 stderr 尾
  /// 部 + exit code）。
  Future<RepoAppEnsureResult> ensure(
    String installId, {
    Map<String, String> env = const {},
    void Function(String line)? onLog,
  }) async {
    final bin = await BiuDaemonManager.resolveBiuBinary(shellEnv: shellEnv);
    if (bin == null) {
      throw const RepoAppEnsureException(
          '找不到 biu 命令行工具，请先在「设置 → 编码」安装');
    }

    final args = ['repo-app', 'ensure', installId];
    env.forEach((k, v) {
      if (k.isNotEmpty) args.addAll(['--env', '$k=$v']);
    });
    _log.info('spawn $bin ${args.join(' ')}');

    final Process proc;
    try {
      proc = await Process.start(
        bin,
        args,
        environment: shellEnv?.env,
        runInShell: false,
      );
    } catch (e) {
      throw RepoAppEnsureException('启动 biu 失败: $e');
    }

    final url = Completer<String>();
    final stderrTail = <String>[];
    var exited = false;

    void onLine(String line, {required bool isErr}) {
      onLog?.call(line);
      if (isErr) {
        stderrTail.add(line);
        if (stderrTail.length > 20) stderrTail.removeAt(0);
        return;
      }
      if (line.startsWith(_urlPrefix)) {
        final u = line.substring(_urlPrefix.length).trim();
        if (u.isNotEmpty && !url.isCompleted) url.complete(u);
      }
    }

    final subs = <StreamSubscription>[
      const LineSplitter()
          .bind(utf8.decoder.bind(proc.stdout))
          .listen((l) => onLine(l, isErr: false)),
      const LineSplitter()
          .bind(utf8.decoder.bind(proc.stderr))
          .listen((l) => onLine(l, isErr: true)),
    ];
    unawaited(proc.exitCode.then((c) {
      exited = true;
      if (!url.isCompleted) {
        url.completeError(RepoAppEnsureException(
          stderrTail.isEmpty ? 'biu repo-app ensure 异常退出' : stderrTail.join('\n'),
          exitCode: c,
        ));
      }
    }));

    try {
      final u = await url.future.timeout(timeout, onTimeout: () {
        throw RepoAppEnsureException(
            '启动超时（${timeout.inMinutes} 分钟）——依赖安装可能卡住，'
            '可执行 `biu repo-app doctor` 排查');
      });
      // 拿到 URL 后进程大概率自己退出；没退就杀掉（ensure 是一次性命令，
      // runner 本体由 CLI detached 托管，不在这个进程里）。
      if (!exited) {
        try {
          proc.kill(ProcessSignal.sigterm);
        } catch (_) {/* already gone */}
      }
      return RepoAppEnsureResult(u);
    } finally {
      for (final s in subs) {
        await s.cancel();
      }
      // 上面对 exitCode 的 then 回调不再有人等 —— 但没 await 它不会泄
      // 漏；进程已 kill 或已退出。
      if (!exited) {
        try {
          proc.kill(ProcessSignal.sigkill);
        } catch (_) {/* ignore */}
      }
    }
  }
}
