// BiuDaemonManager —— S6-3 Flutter 桌面端自动 spawn `biu serve` daemon。
//
// 目的：让 Agent 模式开箱即用 —— 用户启动 biumind app 时，本机自动跑
// 一份 `biu serve` 注册到 brain 成 worker_kind=biu_daemon environment，
// NewThreadDialog 切到 Agent 模式时立刻有可选 worker。
//
// 跨平台：
//   - macOS / Linux / Windows 桌面：spawn `biu serve --port 0 --pid-file ...
//     --register --brain-url ... --token ...`
//   - iOS / Android / Web：noop（手机上 daemon 没意义；web 没 Process）
//
// biu binary 查找优先级：
//   1. env BIU_BIN（dev / 自定义安装）
//   2. PATH 里 `biu`
//   3. macOS app bundle Resources/biu（生产 codesigned binary）
//   4. 自动安装：拉 <origin>/downloads/releases.json 里的 biu CLI 产物，
//      校验 sha256 后装到 ~/.local/bin/biu（macOS / Linux）
//   5. (都不行) → state=binaryMissing，UI 引导用户装
//
// 认证：用 hub bearer token 当 PAT 传给 daemon。SelectVerifier 在 brain
// 端已经接受 RS256 用户 token + HS256 自签 token 双路径，所以 hub
// bearer 走过去能过 requireAuth 注册 environment。每次 (重)spawn 前经
// freshTokenProvider 取最新 access_token —— 不用 manager 构造时的旧 token,
// 避免"token 过期 → daemon register 401 退出"死循环。
//
// 生命周期：app 启动 watch provider → autoStart；app 退出 dispose →
// SIGTERM。Process 异常退出（非 0）时按指数退避自动 respawn（5s→…→5min 上限，
// 成功跑起来后重置）——daemon 因 brain 抖动 / register 失败自杀后能自愈,
// 不必等用户重启 app。
//
// 状态暴露：BiuDaemonStatus + bridgeUrl，UI 可以 watch 显示状态徽章。

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../features/chat/sync/chat_events_realtime.dart'
    show decodeJwtUserId;
import '../../services/auth_service.dart';
import '../../services/login_shell_env.dart';

/// daemon 进程的可观察状态。UI 用 .description 做用户提示。
enum BiuDaemonStatus {
  /// 未启动（初始 / web / mobile / 用户禁用）
  idle,
  /// 找不到 biu binary —— 引导用户装 CLI
  binaryMissing,
  /// 正在 spawn / 等 stdout 报 BIU_BRIDGE_URL
  starting,
  /// 本机无 biu binary,正在从 releases.json 下载安装
  installing,
  /// 进程跑着，bridgeUrl 已知，注册 brain 成功
  running,
  /// spawn 出错 / 进程崩了 / 注册失败
  failed,
  /// app 主动停了
  stopped,
}

class BiuDaemonState {
  final BiuDaemonStatus status;
  /// 解析自 stdout 的 `BIU_BRIDGE_URL=...`，running 时非空。
  final String? bridgeUrl;
  /// 解析自 stdout 的 `BIU_DAEMON_ENV_ID=...`（B2）：本机 daemon 在 brain 注册
  /// 的 environment_id。client-side BYOK 命中时定向投 work 到此 env_id，保证
  /// loopback 推的 key 与 work 投递同机（work 按 env_id 定向投 NATS）。
  final String? daemonEnvId;
  /// failed 状态的最后错误信息，用于 UI 展示。
  final String? lastError;
  /// 进程 PID（debugging 用）。
  final int? pid;

  const BiuDaemonState({
    this.status = BiuDaemonStatus.idle,
    this.bridgeUrl,
    this.daemonEnvId,
    this.lastError,
    this.pid,
  });

  BiuDaemonState copyWith({
    BiuDaemonStatus? status,
    String? bridgeUrl,
    String? daemonEnvId,
    String? lastError,
    int? pid,
    bool clearError = false,
    bool clearBridge = false,
    bool clearEnvId = false,
    bool clearPid = false,
  }) =>
      BiuDaemonState(
        status: status ?? this.status,
        bridgeUrl: clearBridge ? null : (bridgeUrl ?? this.bridgeUrl),
        daemonEnvId: clearEnvId ? null : (daemonEnvId ?? this.daemonEnvId),
        lastError: clearError ? null : (lastError ?? this.lastError),
        pid: clearPid ? null : (pid ?? this.pid),
      );
}

/// 桌面端自动起 biu serve daemon 的协调者。
///
/// Riverpod 提供单例（[biuDaemonManagerProvider]）。生命周期：
///   - watch 时若未启动且条件满足（桌面端 + 已登录） → autoStart
///   - app 退出时（ProviderContainer.dispose）→ stop（SIGTERM + 等待）
class BiuDaemonManager {
  BiuDaemonManager({
    required this.brainBaseUrl,
    required this.bearerToken,
    /// model-relay HTTP base URL（http(s)://...:7001）。biu serve 起 cloud
    /// mode SDK 必须 —— 走它调 LLM。空时 daemon 启动直接 exit 1。
    required this.modelRelayUrl,
    /// 用户 login shell 完整环境变量（PATH + LANG + ...）。null 时退回
    /// Platform.environment（GUI app 通常不全）。
    this.shellEnv,
    Logger? logger,
    /// 每次 (重)spawn 前取最新 access_token。null → 恒用 [bearerToken]。
    /// 生产由 provider 注入 ref.read(hubCredentialsProvider) —— token 刷新后
    /// 下一次 spawn / respawn 直接拿到新 token,不再困在过期 token 里。
    Future<String?> Function()? freshTokenProvider,
    /// 测试注入：替换 Process.start。生产留 null 走真 dart:io。
    Future<Process> Function(
      String executable,
      List<String> arguments, {
      Map<String, String>? environment,
    })? processSpawner,
    /// 测试注入：替换 binary 查找。生产留 null 走 _resolveBinary。
    Future<String?> Function()? binaryResolver,
    /// 测试注入：直接给 pid 文件路径。生产留 null 走
    /// getApplicationSupportDirectory（依赖 platform channel）。
    String? pidFilePath,
    /// 测试注入：异常退出后第 attempt 次 respawn 的等待时长。生产留 null
    /// 走默认 5s×2^n（上限 5min）。
    Duration Function(int attempt)? restartBackoff,
    /// 测试注入：HTTP GET 返回字节（自动安装下载 manifest / binary 用）。
    /// 生产留 null 走真 HttpClient。
    Future<Uint8List> Function(Uri url)? httpGetter,
    /// 测试注入：CPU 架构探测（'arm64' / 'x86_64'）。生产留 null 走 uname -m。
    Future<String> Function()? archResolver,
    /// 测试注入：自动安装目标路径。生产留 null 走 ~/.local/bin/biu。
    Future<String> Function()? installPathResolver,
  })  : _log = logger ?? Logger('biumind.biu_daemon'),
        _processSpawner = processSpawner ?? _defaultSpawn,
        _binaryResolver = binaryResolver,
        _freshTokenProvider = freshTokenProvider,
        _pidFilePathOverride = pidFilePath,
        _restartBackoff = restartBackoff ?? _defaultRestartBackoff,
        _httpGetter = httpGetter ?? _defaultHttpGet,
        _archResolver = archResolver ?? _defaultArchResolve,
        _installPathResolver = installPathResolver ?? _defaultInstallPath;

  /// 用户 login shell 环境（含完整 PATH）。GUI 启动 app 时 Platform.environment
  /// 仅含 /usr/bin:/bin... 找不到 ~/.local/bin/biu / /opt/homebrew/bin/biu。
  final LoginShellEnv? shellEnv;

  /// brain HTTP base URL（http(s)://...，无尾斜杠）。daemon `--brain-url` 用。
  final String brainBaseUrl;
  /// hub bearer token，作为 PAT 传给 daemon（BIUMIND_PAT env）让它 register。
  /// 同时作为 BIUMIND_TOKEN 给 biu cloud mode SDK 调 model-relay 用。
  final String bearerToken;
  /// model-relay HTTP base URL —— biu serve 启动 cloud mode SDK 必填。
  final String modelRelayUrl;
  final Logger _log;
  final Future<Process> Function(
    String executable,
    List<String> arguments, {
    Map<String, String>? environment,
  }) _processSpawner;
  final Future<String?> Function()? _binaryResolver;
  final Future<String?> Function()? _freshTokenProvider;
  final String? _pidFilePathOverride;
  final Duration Function(int attempt) _restartBackoff;
  final Future<Uint8List> Function(Uri url) _httpGetter;
  final Future<String> Function() _archResolver;
  final Future<String> Function() _installPathResolver;

  static Future<Process> _defaultSpawn(
    String executable,
    List<String> arguments, {
    Map<String, String>? environment,
  }) =>
      Process.start(
        executable,
        arguments,
        environment: environment,
        runInShell: false,
      );

  /// 默认 respawn 退避：5s × 2^attempt，上限 5min。attempt 到 6 后恒 5min —
  /// daemon 反复死说明有持续故障（brain 宕 / token 长期无效），低频重试即可。
  static Duration _defaultRestartBackoff(int attempt) {
    final shift = attempt > 6 ? 6 : attempt;
    return Duration(seconds: 5 << shift);
  }

  /// 默认 HTTP GET（自动安装用）。manifest 小，给 15s 超时；binary 30MB 级，
  /// 慢网要更久 —— 同一个 getter 按 url 后缀区分太丑,统一给 5min 上限,
  /// 真卡住由用户重启 app 兜底。
  static Future<Uint8List> _defaultHttpGet(Uri url) async {
    final client = HttpClient()..connectionTimeout = const Duration(seconds: 15);
    try {
      final req = await client.getUrl(url);
      final resp = await req.close().timeout(const Duration(minutes: 5));
      if (resp.statusCode != 200) {
        await resp.drain<void>();
        throw HttpException('GET $url → ${resp.statusCode}');
      }
      final builder = BytesBuilder(copy: false);
      await for (final chunk in resp) {
        builder.add(chunk);
      }
      return builder.takeBytes();
    } finally {
      client.close(force: true);
    }
  }

  /// 默认架构探测。Dart 没有内建 API；desktop 上 uname -m 最靠谱
  /// （arm64 → 'arm64'，x86_64 保留原样）。Windows 不走到这（无自动安装）。
  static Future<String> _defaultArchResolve() async {
    final r = await Process.run('uname', ['-m']);
    return (r.stdout as String).trim();
  }

  /// 默认安装目标：~/.local/bin/biu —— 正好是 _resolveBinary 的 fallback
  /// 搜索路径之一,装完下次启动直接命中。
  static Future<String> _defaultInstallPath() async {
    final home = Platform.environment['HOME'] ?? '';
    if (home.isEmpty) throw StateError('HOME unset, cannot pick install dir');
    return '$home/.local/bin/biu';
  }

  /// spawn 前取最新 token：freshTokenProvider 给到非空值就用它,否则退回
  /// 构造时的 bearerToken。
  Future<String> _resolveToken() async {
    try {
      final t = await _freshTokenProvider?.call();
      if (t != null && t.isNotEmpty) return t;
    } catch (e) {
      _log.fine('freshTokenProvider failed, fall back to ctor token: $e');
    }
    return bearerToken;
  }

  Process? _proc;
  StreamSubscription<String>? _stdoutSub;
  StreamSubscription<String>? _stderrSub;
  Future<void>? _exitFuture;
  String? _pidFilePath;

  /// 异常退出后的自动 respawn：timer + 已尝试次数（决定退避时长）。
  /// 成功 running 一次就归零。
  Timer? _restartTimer;
  int _restartAttempts = 0;

  /// 采用(adopt)了一个**幸存** daemon 时,记下它的 pid(我们没 spawn 它,故无
  /// Process 句柄)。用于 stop() 时按 pid 杀 —— 保持 B 语义:正常退出仍杀 daemon,
  /// 不让"采用"漂移成常驻进程。
  int? _adoptedPid;

  /// 上次成功就绪的 daemon 信息 {pid, bridge_url},持久化到 prefs;下次启动据此
  /// 探活并采用幸存 daemon(热重启 / 崩溃重连场景,避免起第二个 daemon、且能重连
  /// 仍在跑的 PTY)。
  static const _daemonInfoKey = 'biu.daemon.last';

  /// 用户在 composer 显式选过的工作目录集（动态授权）。daemon 的路径安全
  /// 地板 D7 默认只信任启动 cwd —— 用户选 Downloads 等目录会被拒。这里把
  /// "用户经系统弹窗显式选过的目录"作为本机信任来源(不是 brain 投下来的,
  /// 符合 D7 不盲信 brain 的前提),spawn 时经 --allowed-roots 传给 daemon。
  /// SharedPreferences 持久化,跨重启保留(选过一次不必再授权)。
  final Set<String> _trustedRoots = {};
  bool _trustedLoaded = false;
  static const _trustedRootsKey = 'biu.daemon.trusted_roots';

  Future<void> _loadTrustedRoots() async {
    if (_trustedLoaded) return;
    _trustedLoaded = true;
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_trustedRootsKey);
      if (raw != null && raw.isNotEmpty) {
        final list = (jsonDecode(raw) as List).cast<String>();
        _trustedRoots.addAll(list);
      }
    } catch (e) {
      _log.warning('load trusted roots failed: $e');
    }
  }

  Future<void> _persistTrustedRoots() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_trustedRootsKey, jsonEncode(_trustedRoots.toList()));
    } catch (e) {
      _log.warning('persist trusted roots failed: $e');
    }
  }

  /// 记下当前就绪 daemon 的 {pid, bridge_url},供下次启动探活采用。
  /// 持久化 daemon 信息（合并写：任一非 null 字段更新，其余保留）。BIU_BRIDGE_URL
  /// 先到（HTTP up 即打印），BIU_DAEMON_ENV_ID 后到（注册异步），两次各更新自己
  /// 字段；同一 prefs key 覆盖会丢另一字段，故合并。
  Future<void> _persistDaemonInfo({int? pid, String? bridgeUrl, String? envId}) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final existing = prefs.getString(_daemonInfoKey);
      final m = <String, dynamic>{};
      if (existing != null) {
        final decoded = jsonDecode(existing);
        if (decoded is Map) m.addAll(Map<String, dynamic>.from(decoded));
      }
      if (pid != null) m['pid'] = pid;
      if (bridgeUrl != null) m['bridge_url'] = bridgeUrl;
      if (envId != null) m['env_id'] = envId;
      await prefs.setString(_daemonInfoKey, jsonEncode(m));
    } catch (e) {
      _log.warning('persist daemon info failed: $e');
    }
  }

  Future<void> _clearDaemonInfo() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.remove(_daemonInfoKey);
    } catch (_) {/* ignore */}
  }

  /// 探活并采用一个**幸存** daemon。读上次持久化的 bridge_url,GET /healthz(短
  /// 超时);200 → 采用(setState running + bridgeUrl,记 pid),返回 true,start()
  /// 据此跳过 spawn。任何失败 → false(照常 spawn)。
  ///
  /// 触发场景:开发热重启 / app 崩溃后 daemon 成孤儿仍在跑 ——
  /// 采用它而非起第二个,从而能重连仍在跑的 PTY(detached → reattach)。
  Future<bool> _tryAdoptExistingDaemon() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_daemonInfoKey);
      if (raw == null || raw.isEmpty) return false;
      final m = jsonDecode(raw) as Map<String, dynamic>;
      final url = (m['bridge_url'] as String?)?.trim() ?? '';
      final pid = m['pid'] as int?;
      final envId = (m['env_id'] as String?)?.trim() ?? '';
      if (url.isEmpty) return false;

      final healthy = await _probeHealthz(url);
      if (!healthy) return false;

      _adoptedPid = pid;
      _proc = null;
      _restartAttempts = 0; // 采用成功也算跑起来了,respawn 退避归零
      _setState(_state.copyWith(
        status: BiuDaemonStatus.running,
        bridgeUrl: url,
        pid: pid,
        // B2: 采用幸存 daemon 时恢复 env_id（daemon 未重启则有效；重启后
        // re-register 新 env_id 会经 stdout 重报，这里临时值被覆盖）。
        daemonEnvId: envId.isEmpty ? null : envId,
        clearError: true,
      ));
      _log.info('adopted surviving daemon at $url (pid=$pid)');
      return true;
    } catch (e) {
      _log.fine('adopt existing daemon failed: $e');
      return false;
    }
  }

  /// GET {bridgeUrl}/healthz,200 即视为有 biu daemon 在跑(serve_cmd.go 的
  /// healthzHandler)。短超时,失败/超时返回 false。
  Future<bool> _probeHealthz(String bridgeUrl) async {
    final base = bridgeUrl.endsWith('/')
        ? bridgeUrl.substring(0, bridgeUrl.length - 1)
        : bridgeUrl;
    final client = HttpClient()
      ..connectionTimeout = const Duration(milliseconds: 600);
    try {
      final req = await client
          .getUrl(Uri.parse('$base/healthz'))
          .timeout(const Duration(milliseconds: 600));
      final resp = await req.close().timeout(const Duration(milliseconds: 600));
      await resp.drain<void>();
      return resp.statusCode == 200;
    } catch (_) {
      return false;
    } finally {
      client.close(force: true);
    }
  }

  /// dir 是否已被某个信任根覆盖（== 根 或 在根之下）。镜像 daemon 侧
  /// floor.withinRoots 的语义,避免对已覆盖的目录做无谓重启。
  bool _coveredByTrusted(String dir) {
    final d = _normDir(dir);
    for (final r in _trustedRoots) {
      final rn = _normDir(r);
      if (d == rn || d.startsWith('$rn${Platform.pathSeparator}')) return true;
    }
    return false;
  }

  static String _normDir(String p) {
    var s = p.trim();
    while (s.length > 1 && s.endsWith(Platform.pathSeparator)) {
      s = s.substring(0, s.length - 1);
    }
    return s;
  }

  /// ensureRootTrusted —— composer 选定工作目录后调。把该目录加入信任集
  /// （若尚未被覆盖）并让 daemon 生效：daemon 在跑 → 重启带上新 --allowed-roots
  /// （选目录是任务前动作,重启可接受;且离线期间 janitor 会推失败帧不会卡死）。
  /// 已覆盖 → no-op,不重启。返回时保证(尽力)daemon 已带新根重新就绪。
  Future<void> ensureRootTrusted(String dir) async {
    if (!isSupported || dir.trim().isEmpty) return;
    await _loadTrustedRoots();
    if (_coveredByTrusted(dir)) return;
    _trustedRoots.add(_normDir(dir));
    await _persistTrustedRoots();
    _log.info('trusted root added: ${_normDir(dir)} → 重启 daemon 应用新允许根');
    // daemon 在跑(或正在起)→ 重启带上新根;否则下次 start() 自然带上。
    if (_state.status == BiuDaemonStatus.running ||
        _state.status == BiuDaemonStatus.starting) {
      await stop();
      await start();
    }
  }

  /// pushToken — 把 fresh access_token POST 给运行中的 daemon bridge
  /// (/internal/token), daemon 热更 worker.client.token, 不重启、不断会话。
  /// 由 biuDaemonTokenPusherProvider 在 token_refresher 刷新 access_token 后触发
  /// (生产 TTL 1h, 每小时刷一次)。非 running 状态跳过 —— daemon 下次 start 用
  /// 最新 bearerToken 已对。失败仅 log, 不抛 (下次 token 变化再推; daemon 真挂
  /// 了由 exitFuture 兜底)。
  Future<void> pushToken(String token) async {
    final url = _state.bridgeUrl;
    if (_state.status != BiuDaemonStatus.running || url == null) return;
    final base = url.endsWith('/')
        ? url.substring(0, url.length - 1)
        : url;
    final client = HttpClient()..connectionTimeout = const Duration(seconds: 2);
    try {
      final req = await client
          .postUrl(Uri.parse('$base/internal/token'))
          .timeout(const Duration(seconds: 3));
      req.headers.contentType = ContentType.json;
      req.write('{"token":${jsonEncode(token)}}');
      final resp = await req.close().timeout(const Duration(seconds: 3));
      await resp.drain<void>();
      if (resp.statusCode == 200) {
        _log.info('pushed fresh access_token to daemon (bridge=$base)');
      } else {
        _log.warning('push token to daemon failed: HTTP ${resp.statusCode}');
      }
    } catch (e) {
      _log.warning('push token to daemon error: $e');
    } finally {
      client.close(force: true);
    }
  }

  final _stateCtrl = StreamController<BiuDaemonState>.broadcast();
  BiuDaemonState _state = const BiuDaemonState();

  Stream<BiuDaemonState> get stream => _stateCtrl.stream;
  BiuDaemonState get state => _state;

  void _setState(BiuDaemonState next) {
    _state = next;
    if (!_stateCtrl.isClosed) _stateCtrl.add(next);
  }

  /// 是否在桌面平台（spawn child process 受支持）。
  static bool get isSupported {
    if (kIsWeb) return false;
    return Platform.isMacOS || Platform.isLinux || Platform.isWindows;
  }

  /// 找 biu binary 路径。返 null 表示找不到。
  ///
  /// 顺序：
  ///   1. BIU_BIN env（用户 / dev 显式指定）
  ///   2. shellEnv.path 拆开手工搜（含 ~/.local/bin / /opt/homebrew/bin / 等）
  ///   3. 直接试常见路径（fallback，shellEnv 也可能没加载好）
  ///   4. macOS app bundle Resources/biu（生产 codesigned binary）
  ///
  /// 不再用 `Process.run('which', ['biu'])` —— GUI app 的 spawn 子进程
  /// 仍然继承短 PATH，which 永远查不到 ~/.local/bin/biu。
  Future<String?> _resolveBinary() async {
    final envPath = Platform.environment['BIU_BIN'];
    if (envPath != null && envPath.isNotEmpty && await File(envPath).exists()) {
      return envPath;
    }
    // 用 login shell PATH 手工搜
    final pathStr = shellEnv?.path ?? Platform.environment['PATH'] ?? '';
    final binName = Platform.isWindows ? 'biu.exe' : 'biu';
    for (final dir in pathStr.split(Platform.isWindows ? ';' : ':')) {
      if (dir.isEmpty) continue;
      // 展开 ~ 到 HOME
      var d = dir;
      if (d.startsWith('~')) {
        final home = Platform.environment['HOME'];
        if (home != null) d = home + d.substring(1);
      }
      final candidate = '$d/$binName';
      if (await File(candidate).exists()) {
        return candidate;
      }
    }
    // 兜底常见位置（用户 PATH 没加载到时）
    final home = Platform.environment['HOME'] ?? '';
    final fallbacks = <String>[
      if (home.isNotEmpty) '$home/.local/bin/biu',
      if (home.isNotEmpty) '$home/go/bin/biu',
      '/usr/local/bin/biu',
      '/opt/homebrew/bin/biu',
      '/opt/local/bin/biu',
    ];
    for (final p in fallbacks) {
      if (await File(p).exists()) return p;
    }
    // macOS app bundle Resources/biu
    if (Platform.isMacOS) {
      final exe = Platform.resolvedExecutable;
      final bundle = File(exe).parent.parent;
      final candidate = '${bundle.path}/Resources/biu';
      if (await File(candidate).exists()) return candidate;
    }
    return null;
  }

  /// 启动 daemon。idempotent：已 running / starting 时直接返。
  Future<void> start() async {
    if (!isSupported) {
      _setState(const BiuDaemonState(status: BiuDaemonStatus.idle));
      return;
    }
    // 手动 start（含 respawn timer 触发）优先于任何挂起的重启计划。
    _restartTimer?.cancel();
    _restartTimer = null;
    if (_state.status == BiuDaemonStatus.starting ||
        _state.status == BiuDaemonStatus.installing ||
        _state.status == BiuDaemonStatus.running) {
      return;
    }
    _setState(_state.copyWith(
      status: BiuDaemonStatus.starting,
      clearError: true,
      clearBridge: true,
      clearEnvId: true,
    ));

    // 先尝试采用一个仍在跑的 daemon(热重启/崩溃重连)——避免起第二个,且能重连
    // 仍在跑的 PTY。采用成功即就绪,跳过 spawn。
    if (await _tryAdoptExistingDaemon()) return;

    var bin = await (_binaryResolver?.call() ?? _resolveBinary());
    // 本机没找到 biu → 尝试从 releases.json 自动安装到 ~/.local/bin。
    // 装不上(release 还没发 biu 产物 / 网络问题)才落 binaryMissing 引导手动装。
    bin ??= await _tryAutoInstall();
    if (bin == null) {
      _log.warning('biu binary 未找到（BIU_BIN / PATH / app bundle / 自动安装 都没有）');
      _setState(_state.copyWith(
        status: BiuDaemonStatus.binaryMissing,
        lastError: 'biu binary not found in BIU_BIN / PATH / app bundle / releases',
      ));
      return;
    }
    // stop() / dispose() 在 binary 查找 + 下载期间被调 → 放弃本次启动,
    // 不把已停状态又拉起。
    if (_state.status == BiuDaemonStatus.stopped) return;

    if (_pidFilePathOverride != null) {
      _pidFilePath = _pidFilePathOverride;
    } else {
      final supportDir = await getApplicationSupportDirectory();
      final pidDir = Directory('${supportDir.path}/biumind');
      if (!await pidDir.exists()) {
        await pidDir.create(recursive: true);
      }
      _pidFilePath = '${pidDir.path}/biu.pid';
    }

    await _loadTrustedRoots();
    final args = [
      'serve',
      '--port', '0',
      '--pid-file', _pidFilePath!,
      '--register',
      '--brain-url', brainBaseUrl,
    ];
    // 用户显式授权过的工作目录 → 传给 daemon 的路径安全地板(D7)。重复
    // --allowed-roots 而非逗号拼接,避免路径含逗号被 cobra 切断。空集时不传,
    // daemon 退回默认(cwd),行为同旧版。
    for (final root in _trustedRoots) {
      args.add('--allowed-roots');
      args.add(root);
    }
    _log.info('spawn $bin ${args.join(' ')}');
    try {
      // 合并：login shell env (PATH/LANG/HOME/...) + 注入 biu serve 必需
      // 三件套：
      //   - BIUMIND_PAT          —— register environment 走 brain auth
      //   - BIUMIND_MODEL_RELAY_URL + BIUMIND_TOKEN —— cloud mode SDK 调
      //                              model-relay 必填，缺则 biu serve 直接
      //                              exit 1 with "cloud mode SDK requires
      //                              model-relay URL + bearer token"
      // token 每次 spawn 前取最新（respawn 场景尤其重要：旧 token 过期导致
      // daemon 死掉后,这里拿刷新后的 token 重启才能真自愈）。
      final token = await _resolveToken();
      final mergedEnv = <String, String>{
        ...?shellEnv?.env,
        'BIUMIND_PAT': token,
        'BIUMIND_MODEL_RELAY_URL': modelRelayUrl,
        'BIUMIND_TOKEN': token,
      };
      final proc = await _processSpawner(
        bin,
        args,
        environment: mergedEnv.isEmpty ? null : mergedEnv,
      );
      _proc = proc;
      _stdoutSub = const LineSplitter()
          .bind(utf8.decoder.bind(proc.stdout))
          .listen(_onStdoutLine);
      _stderrSub = const LineSplitter()
          .bind(utf8.decoder.bind(proc.stderr))
          .listen((line) {
        // 提到 info 级 —— biu serve --register 失败(register HTTP 401 /
        // PAT 过期 / panic 等)只往 stderr 写一行 `[biu] serve: agent
        // worker exited: ...`,daemon HTTP 仍存活让上层无感。压到 fine
        // 等于"silent fail",用户体验是 UI 一直显示无 daemon 在线。
        _log.info('[biu serve stderr] $line');
      });
      _exitFuture = proc.exitCode.then((code) {
        _onExit(code);
      });
      _setState(_state.copyWith(pid: proc.pid));
    } catch (e, stack) {
      _log.severe('spawn biu serve failed', e, stack);
      _setState(_state.copyWith(
        status: BiuDaemonStatus.failed,
        lastError: 'spawn failed: $e',
      ));
    }
  }

  void _onStdoutLine(String line) {
    _log.fine('[biu serve stdout] $line');
    if (line.startsWith('BIU_BRIDGE_URL=')) {
      final url = line.substring('BIU_BRIDGE_URL='.length).trim();
      _restartAttempts = 0; // 跑起来了 → respawn 退避归零
      _setState(_state.copyWith(
        status: BiuDaemonStatus.running,
        bridgeUrl: url,
      ));
      _log.info('biu daemon up at $url');
      // 记下 {pid, bridge_url} 供下次启动探活采用(热重启/崩溃重连)。
      final pid = _proc?.pid;
      if (pid != null) unawaited(_persistDaemonInfo(pid: pid, bridgeUrl: url));
    } else if (line.startsWith('BIU_DAEMON_ENV_ID=')) {
      // B2: daemon 注册成功后上报 env_id（晚于 BIU_BRIDGE_URL，注册异步）。
      // 持有它 → client-side BYOK 命中时定向投 work 到本机 daemon。
      final envId = line.substring('BIU_DAEMON_ENV_ID='.length).trim();
      if (envId.isNotEmpty) {
        _setState(_state.copyWith(daemonEnvId: envId));
        unawaited(_persistDaemonInfo(envId: envId));
      }
    }
  }

  void _onExit(int code) {
    _log.warning('biu daemon exited code=$code');
    _proc = null;
    if (_state.status == BiuDaemonStatus.stopped) {
      // 用户主动停的，不标 failed 也不 respawn
      return;
    }
    if (code == 0) {
      // 干净退出（自己收到信号 / graceful）—— 不 respawn。
      _setState(_state.copyWith(
        status: BiuDaemonStatus.stopped,
        clearBridge: true,
        clearPid: true,
        clearEnvId: true,
      ));
      return;
    }
    _setState(_state.copyWith(
      status: BiuDaemonStatus.failed,
      lastError: 'biu serve exited code=$code',
      clearBridge: true,
      clearPid: true,
      clearEnvId: true,
    ));
    _scheduleRestart();
  }

  /// 异常退出（非 0）后按指数退避自动 respawn。daemon 常死法:register 401
  /// (token 过期) / brain 抖动 re-register 失败 / 崩溃 —— 全是可自愈场景,
  /// 不该等用户重启 app 才发现"agent 模式没 worker"。跑起来一次
  /// (BIU_BRIDGE_URL) 就把 attempt 归零。
  void _scheduleRestart() {
    _restartTimer?.cancel();
    final delay = _restartBackoff(_restartAttempts);
    _restartAttempts++;
    _log.info('biu daemon auto-respawn in ${delay.inSeconds}s '
        '(attempt $_restartAttempts)');
    _restartTimer = Timer(delay, () {
      _restartTimer = null;
      // 只从 failed 状态 respawn —— stop()/dispose/手动 start 已经改状态
      // 的话不抢。start() 自己会再查一遍 starting/running 幂等。
      if (_state.status == BiuDaemonStatus.failed) {
        // ignore: discarded_futures
        start();
      }
    });
  }

  /// 停 daemon。SIGTERM + 等 5s graceful，超时 SIGKILL。idempotent。
  Future<void> stop() async {
    // 用户主动停 → 取消任何挂起的自动 respawn。
    _restartTimer?.cancel();
    _restartTimer = null;
    final p = _proc;
    if (p == null) {
      // 采用来的幸存 daemon(无 Process 句柄):按 pid 杀,保持 B 语义 —— 正常退出
      // 仍终止 daemon,不让"采用"漂移成常驻进程。
      final apid = _adoptedPid;
      if (apid != null) {
        try {
          Process.killPid(apid, ProcessSignal.sigterm);
        } catch (_) {/* ignore */}
        _adoptedPid = null;
        unawaited(_clearDaemonInfo());
      }
      _setState(_state.copyWith(status: BiuDaemonStatus.stopped));
      return;
    }
    _setState(_state.copyWith(status: BiuDaemonStatus.stopped));
    try {
      // Windows 不支持 SIGTERM；用 kill() 默认 SIGTERM on POSIX，平台兜底
      p.kill(ProcessSignal.sigterm);
    } catch (_) {/* fall through */}
    try {
      await _exitFuture?.timeout(const Duration(seconds: 5));
    } catch (_) {
      // 超时 → 强杀
      try {
        p.kill(ProcessSignal.sigkill);
      } catch (_) {/* ignore */}
    }
    await _stdoutSub?.cancel();
    await _stderrSub?.cancel();
    _stdoutSub = null;
    _stderrSub = null;
    _proc = null;
  }

  /// 自动安装 biu CLI —— 本机找不到 binary 时由 [start] 调。从
  /// `<brainBaseUrl>/downloads/releases.json` 找当前平台对应的 biu 产物
  /// （platform = biu-macos-arm64 / biu-macos-x64 / biu-linux-x64 /
  /// biu-linux-arm64，CI 发版时挂上），下载、校验 sha256、写到安装路径
  /// （默认 ~/.local/bin/biu）+ chmod 0755。
  ///
  /// 返回装好的 binary 路径；任何一步失败（release 里还没 biu 产物 / 网络 /
  /// 校验和不符）都返 null，[start] 落 binaryMissing 走手动安装引导。
  /// Windows 暂无产物直接 null。
  Future<String?> _tryAutoInstall() async {
    if (!Platform.isMacOS && !Platform.isLinux) return null;
    _setState(_state.copyWith(status: BiuDaemonStatus.installing));
    try {
      final manifestBytes =
          await _httpGetter(Uri.parse('$brainBaseUrl/downloads/releases.json'));
      final decoded = jsonDecode(utf8.decode(manifestBytes));
      if (decoded is! Map<String, dynamic>) {
        throw StateError('releases.json is not a JSON object');
      }
      final platformKey = await _biuAssetPlatformKey();
      if (platformKey == null) {
        throw StateError('unsupported cpu arch for auto-install');
      }
      Map<String, dynamic>? asset;
      for (final a in (decoded['assets'] as List?) ?? const []) {
        if (a is Map<String, dynamic> && a['platform'] == platformKey) {
          asset = a;
          break;
        }
      }
      if (asset == null) {
        // releases.json 还没带 biu 产物（老版本 release）—— 不是错误,
        // 安静回退 binaryMissing 让用户手动装。
        _log.warning('releases.json 无 $platformKey 产物,无法自动安装 biu CLI');
        return null;
      }
      final url = (asset['url'] as String?) ?? '';
      if (url.isEmpty) throw StateError('biu asset url empty');
      final expectedSha =
          ((asset['sha256'] as String?) ?? '').toLowerCase();
      _log.info('auto-install biu CLI: $url → ${await _installPathResolver()}');
      final bytes = await _httpGetter(Uri.parse(url));
      if (expectedSha.isNotEmpty &&
          sha256.convert(bytes).toString() != expectedSha) {
        throw StateError('sha256 mismatch for $url');
      }
      final targetPath = await _installPathResolver();
      final target = File(targetPath);
      await target.parent.create(recursive: true);
      await target.writeAsBytes(bytes, flush: true);
      // HttpClient 写出的文件默认 0644 —— 补可执行位。
      final chmod = await Process.run('chmod', ['0755', target.path]);
      if (chmod.exitCode != 0) {
        throw StateError('chmod 0755 ${target.path} failed: ${chmod.stderr}');
      }
      _log.info('biu CLI 自动安装完成 → $targetPath '
          '(${bytes.length} bytes, $platformKey)');
      return target.path;
    } catch (e) {
      _log.warning('biu CLI 自动安装失败: $e');
      return null;
    }
  }

  /// 当前平台对应的 biu 产物 platform key。架构不识别（除 arm64/x86_64 外）
  /// 返 null。macOS intel → biu-macos-x64，Apple Silicon → biu-macos-arm64。
  Future<String?> _biuAssetPlatformKey() async {
    final arch = (await _archResolver()).trim();
    final suffix = switch (arch) {
      'arm64' || 'aarch64' => 'arm64',
      'x86_64' || 'amd64' => 'x64',
      _ => null,
    };
    if (suffix == null) return null;
    if (Platform.isMacOS) return 'biu-macos-$suffix';
    if (Platform.isLinux) return 'biu-linux-$suffix';
    return null;
  }

  /// container.dispose 时调；不抛异常。
  Future<void> dispose() async {
    await stop();
    if (!_stateCtrl.isClosed) await _stateCtrl.close();
  }
}

/// biuDaemonWatchKeyProvider —— daemon 重建的 watch key: endpoint + 账号
/// 身份 (JWT sub)。
///
/// 关键设计: **不能 watch 整个 HubCredentials / bearerToken**。token_manager
/// 的后台 timer 每个 ttl × 0.05 触发一次 refresh, refresh 后 settings 写入
/// 新 bearerToken → hubCredentialsProvider 重 build。如果 watch 整个 token,
/// 每次 refresh 都会 dispose 旧 daemon + spawn 新 daemon, 进入死循环(实际
/// 曾经 22 分钟内重启了 30+ 次)。同人换 token 由 biuDaemonTokenPusherProvider
/// 推新 token 热更, 不重启进程。
///
/// P2 多账号: key 里加 userId —— 同 endpoint 换账号 (switchAccount) 时
/// key 变化 → provider 重建 → 旧 daemon SIGTERM + respawn。不重启的话旧
/// 进程还挂着旧账号在 brain 注册的 environment_id, tokenPusher 还会把新
/// 账号 token 推给旧 daemon。
final biuDaemonWatchKeyProvider = Provider<String?>((ref) {
  return ref.watch(
    hubCredentialsProvider.select((c) {
      if (c == null) return null;
      return '${c.endpoint}|${decodeJwtUserId(c.bearerToken) ?? ''}';
    }),
  );
});

/// biuDaemonManagerProvider —— null 表示未登录或不支持平台。
///
/// 自动 start：watch 时如果未启动且 creds 已就位，会触发 start()。app
/// 退出时 ref.onDispose → manager.dispose() → SIGTERM。
///
/// 重建时机见 [biuDaemonWatchKeyProvider]: endpoint 或账号身份变化才
/// dispose + respawn; token 轮换 (同人) 不触发。
final biuDaemonManagerProvider = Provider<BiuDaemonManager?>((ref) {
  if (!BiuDaemonManager.isSupported) return null;
  final watchKey = ref.watch(biuDaemonWatchKeyProvider);
  if (watchKey == null) return null;
  // 拿当前 token 一次塞进环境变量;后续 token 变了不响应。
  final creds = ref.read(hubCredentialsProvider);
  if (creds == null) return null;
  final endpoint = creds.endpoint;
  // login shell env 是 FutureProvider；watch.valueOrNull 拿当前快照，加载
  // 完会重 build 这个 provider 给到 manager。manager 自己 idempotent。
  // 用 select 避免 FutureProvider 中间态(loading → data)也触发 rebuild。
  final shellEnv = ref.watch(
    loginShellEnvProvider.select((async) => async.valueOrNull),
  );

  // brain HTTP base：单 origin, 跟 chat_controller.dart 一致 —— 由 site nginx
  // 按路径反代到 brain, 不换端口。
  final brainHttpBase = endpoint.toString();
  final stripped = brainHttpBase.endsWith('/')
      ? brainHttpBase.substring(0, brainHttpBase.length - 1)
      : brainHttpBase;

  // model-relay 同 origin —— endpoint 的 toString。strip 末尾 /。
  final relayBase = endpoint.toString();
  final relayStripped = relayBase.endsWith('/')
      ? relayBase.substring(0, relayBase.length - 1)
      : relayBase;
  final mgr = BiuDaemonManager(
    brainBaseUrl: stripped,
    bearerToken: creds.bearerToken,
    modelRelayUrl: relayStripped,
    shellEnv: shellEnv,
    // 每次 (重)spawn 前读最新 token —— token_refresher 刷新后,daemon
    // 异常退出再 respawn 时直接带新 token,不再困在过期 token 里。
    freshTokenProvider: () async =>
        ref.read(hubCredentialsProvider)?.bearerToken,
  );
  ref.onDispose(mgr.dispose);
  // shellEnv 是 nice-to-have（更全的 PATH），没加载到也没事 —— manager
  // 自己有 fallback 路径（~/.local/bin / /usr/local/bin / /opt/homebrew/bin
  // / app bundle Resources）。所以即使 valueOrNull == null 也立即 start。
  // ignore: discarded_futures
  mgr.start();
  return mgr;
});

/// daemonStateProvider —— UI watch 当前 daemon 状态徽章。
final biuDaemonStateProvider = StreamProvider<BiuDaemonState>((ref) {
  final mgr = ref.watch(biuDaemonManagerProvider);
  if (mgr == null) {
    return Stream.value(const BiuDaemonState(status: BiuDaemonStatus.idle));
  }
  return Stream.multi((controller) {
    controller.add(mgr.state);
    final sub = mgr.stream.listen(controller.add);
    controller.onCancel = sub.cancel;
  });
});

/// biuDaemonTokenPusherProvider — 监听 access_token 变化, 把新 token 推给运行中
/// 的 daemon。token_refresher 每小时 (生产 TTL 1h) 刷一次 access_token, 这里
/// side-effect POST 到 daemon bridge /internal/token, daemon 热更 worker.client.token,
/// 不重启、不断 agent 会话。解决 token 过期 → daemon 401 退出 → brain GC env →
/// environment_id required 报错链。
///
/// 关键设计: 用 ref.listen (side-effect), **不改 biuDaemonManagerProvider**
/// (它只 watch endpoint+userId, 避免 token refresh 触发 daemon 重启死循环,
/// 见 biuDaemonWatchKeyProvider)。token 变化只 POST, 不 rebuild manager。
/// daemon 非 running 时 pushToken 自身 no-op (daemon 下次 start 用最新
/// bearerToken 已对)。
final biuDaemonTokenPusherProvider = Provider<void>((ref) {
  final mgr = ref.watch(biuDaemonManagerProvider);
  if (mgr == null) return;
  ref.listen<String?>(
    hubCredentialsProvider.select((c) => c?.bearerToken),
    (previous, next) {
      if (next == null || next.isEmpty || next == previous) return;
      // ignore: discarded_futures
      mgr.pushToken(next);
    },
  );
});
