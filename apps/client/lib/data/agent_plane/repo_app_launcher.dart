// RepoAppLauncher —— 客户端 ↔ 本机 repo-app runner 的通道（Repo Apps M1/M2）。
//
// 技术方案 §3.4：客户端经现有 daemon 管理通道执行
// `biu repo-app ensure <install>`，解析其 stdout 通告的
// `BIU_REPOAPP_URL=http://127.0.0.1:<port>`（通告协议照 `biu serve` 的
// `BIU_BRIDGE_URL` 先例，serve_cmd.go）。M2 复用同一通道执行
// `biu repo-app update <slug>`（一键更新，见 updateRepoApp）。
//
// 对接决策：BiuDaemonManager 管的是长驻 `biu serve` 进程，没有"一次性
// 执行子命令"的通道，bridge（loopback HTTP+WS）是 SDK Protocol 数据面，
// 不适合塞 CLI exec。因此 ensure/update 走独立的一次性进程 spawn，但复用
// daemon 管理代码的两块基建：
//   - binary 查找链：BiuDaemonManager.resolveBiuBinary（BIU_BIN →
//     login-shell PATH → 常见路径 → macOS bundle）
//   - login shell env：GUI app 的 Platform.environment 不含用户 PATH，
//     子进程必须带 shellEnv 才能找到 git/node/uv
//
// 机密 env（D9）：随 ensure 以 `--env NAME=VALUE` 重复 flag 下发，CLI
// 负责写实例目录 .env（明文 0600）。客户端不持久化这些值。
//
// 可测试性：进程执行经 [RepoAppProcessSpawner] 注入，测试喂 fake 进程
// （脚本化 stdout/stderr/exitCode），不起真实子进程。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:logging/logging.dart';

import '../../services/login_shell_env.dart';
import 'biu_daemon_manager.dart';

/// ensure / update 的一次运行结果。
class RepoAppEnsureResult {
  /// 解析自 stdout 的 `BIU_REPOAPP_URL=` 通告。
  final String url;
  const RepoAppEnsureResult(this.url);
}

/// ensure / update 失败：exit code + stderr 末尾几行，给 UI 展示可读原因。
class RepoAppEnsureException implements Exception {
  final int? exitCode;
  final String message;
  const RepoAppEnsureException(this.message, {this.exitCode});

  @override
  String toString() =>
      'RepoAppEnsureException(exit=$exitCode): $message';
}

/// 子进程抽象 —— 测试注入 fake 用。真实实现包装 dart:io Process。
abstract class RepoAppProcess {
  Stream<List<int>> get stdout;
  Stream<List<int>> get stderr;
  Future<int> get exitCode;
  bool kill([ProcessSignal signal]);
}

/// 进程启动器签名：与 Process.start 同参（binary / args / environment）。
typedef RepoAppProcessSpawner = Future<RepoAppProcess> Function(
  String executable,
  List<String> arguments,
  Map<String, String>? environment,
);

class _IoProcess implements RepoAppProcess {
  _IoProcess(this._p);
  final Process _p;

  @override
  Stream<List<int>> get stdout => _p.stdout;
  @override
  Stream<List<int>> get stderr => _p.stderr;
  @override
  Future<int> get exitCode => _p.exitCode;
  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) => _p.kill(signal);
}

class RepoAppLauncher {
  RepoAppLauncher({
    this.shellEnv,
    Logger? logger,
    this.timeout = const Duration(minutes: 10),
    RepoAppProcessSpawner? spawner,
    Future<String?> Function()? binaryResolver,
  })  : _log = logger ?? Logger('biumind.repo_app'),
        _spawner = spawner ?? _defaultSpawner,
        _binaryResolver = binaryResolver;

  /// 用户 login shell 环境（完整 PATH）。null 时退回 Platform.environment。
  final LoginShellEnv? shellEnv;

  /// clone + 装依赖可能很慢（首次安装大型 Node 项目分钟级），给 10min。
  final Duration timeout;
  final Logger _log;
  final RepoAppProcessSpawner _spawner;

  /// binary 查找注入点（测试隔离文件系统）；null 走
  /// BiuDaemonManager.resolveBiuBinary 标准查找链。
  final Future<String?> Function()? _binaryResolver;

  static const _urlPrefix = 'BIU_REPOAPP_URL=';
  static const _missingBinaryMsg = '找不到 biu 命令行工具，请先在「设置 → 编码」安装';

  static Future<RepoAppProcess> _defaultSpawner(
    String executable,
    List<String> arguments,
    Map<String, String>? environment,
  ) async {
    final p = await Process.start(
      executable,
      arguments,
      environment: environment,
      runInShell: false,
    );
    return _IoProcess(p);
  }

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
  }) {
    final args = ['repo-app', 'ensure', installId];
    env.forEach((k, v) {
      if (k.isNotEmpty) args.addAll(['--env', '$k=$v']);
    });
    return _runToUrl(args, 'biu repo-app ensure', onLog: onLog);
  }

  /// 执行 `biu repo-app update <slug>` 并等待 URL 通告（Repo Apps M2
  /// 一键更新）。CLI 侧流程：stop → fetch+checkout → 装依赖 → start →
  /// 健康检查 → 失败回切，结束同样通告 `BIU_REPOAPP_URL=`。
  ///
  /// [slug] 由 [slugFromRepoUrl] 从 repo_meta.url 推导（与 CLI
  /// repoapp.sanitiseForFS 同规则）。 [ref] 为 redeploy 锁定的 git 引
  /// 用，空时省略 --ref（CLI 回退已安装的 ref —— 兼容尚未返回 ref/sha
  /// 的老服务端）。[installId] / [buildId] / [reportUrl] 让 CLI 把更新
  /// 结果回报服务端 builds 行。
  Future<RepoAppEnsureResult> updateRepoApp({
    required String slug,
    required String installId,
    required String buildId,
    required String reportUrl,
    String ref = '',
    void Function(String line)? onLog,
  }) {
    final args = ['repo-app', 'update', slug];
    if (ref.isNotEmpty) args.addAll(['--ref', ref]);
    args.addAll([
      '--install-id', installId,
      '--build-id', buildId,
      '--report-url', reportUrl,
    ]);
    return _runToUrl(args, 'biu repo-app update', onLog: onLog);
  }

  /// 从 repo_meta.url 推导 CLI 侧实例 slug。接受：
  ///
  ///   owner/repo
  ///   https://github.com/owner/repo(.git)
  ///   git@github.com:owner/repo(.git)
  ///
  /// 规则与 CLI `repoapp.ParseRepoArg` + `sanitiseForFS`
  ///（apps/cli/biu/internal/repoapp/store.go）1:1 —— slug 必须与 CLI
  /// 建实例目录时算出的完全一致，否则 update 找不到实例。
  static String? slugFromRepoUrl(String repoUrl) {
    var arg = repoUrl.trim();
    if (arg.isEmpty) return null;
    final ownerRepoRe = RegExp(r'^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(\.git)?$');
    if (ownerRepoRe.hasMatch(arg)) {
      arg = 'https://github.com/${_stripGitSuffix(arg)}';
    }
    const sshPrefix = 'git@github.com:';
    if (arg.startsWith(sshPrefix)) {
      arg = 'https://github.com/${arg.substring(sshPrefix.length)}';
    }
    final u = Uri.tryParse(arg);
    if (u == null || (u.host != 'github.com' && u.host != 'www.github.com')) {
      return null;
    }
    final parts = _stripGitSuffix(u.path)
        .split('/')
        .where((s) => s.isNotEmpty)
        .toList(growable: false);
    if (parts.length != 2) return null;
    final slug = sanitiseForFS('${parts[0]}/${parts[1]}');
    return slug.isEmpty ? null : slug;
  }

  static String _stripGitSuffix(String s) =>
      s.endsWith('.git') ? s.substring(0, s.length - 4) : s;

  /// sanitiseForFS 的 Dart 镜像（CLI store.go）：字母 / 数字 / `_` 原样
  /// 保留（**大小写不折叠**，与 Go 实现一致），其余字符的每个连续段折
  /// 成一个 `-`，首尾 `-` 裁掉。
  static String sanitiseForFS(String s) {
    final b = StringBuffer();
    var prevHyphen = true;
    for (final r in s.runes) {
      final keep = (r >= 0x61 && r <= 0x7a) || // a-z
          (r >= 0x41 && r <= 0x5a) || // A-Z
          (r >= 0x30 && r <= 0x39) || // 0-9
          r == 0x5f; // _
      if (keep) {
        b.writeCharCode(r);
        prevHyphen = false;
      } else if (!prevHyphen) {
        b.write('-');
        prevHyphen = true;
      }
    }
    return b.toString().replaceAll(RegExp(r'^-+|-+$'), '');
  }

  /// 一次性命令公共骨架：解析 binary → spawn → 等 stdout 的
  /// `BIU_REPOAPP_URL=` 通告。进程先于通告退出 / 超时 →
  /// [RepoAppEnsureException]（带 stderr 尾部 + exit code）。
  Future<RepoAppEnsureResult> _runToUrl(
    List<String> args,
    String cmdLabel, {
    void Function(String line)? onLog,
  }) async {
    final resolver = _binaryResolver;
    final bin = await (resolver != null
        ? resolver()
        : BiuDaemonManager.resolveBiuBinary(shellEnv: shellEnv));
    if (bin == null) {
      throw const RepoAppEnsureException(_missingBinaryMsg);
    }
    _log.info('spawn $bin ${args.join(' ')}');

    final RepoAppProcess proc;
    try {
      proc = await _spawner(bin, args, shellEnv?.env);
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
          stderrTail.isEmpty ? '$cmdLabel 异常退出' : stderrTail.join('\n'),
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
      // 拿到 URL 后进程大概率自己退出；没退就杀掉（ensure/update 是一次
      // 性命令，runner 本体由 CLI detached 托管，不在这个进程里）。
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
