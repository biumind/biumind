// ChatSyncManager —— 聊天跨设备下行同步的生命周期 + 触发编排。
//
// 触发点汇总（全部 fire-and-forget，不阻塞 UI）：
//
//   * 冷启动已登录 / 登录成功：hubCredentialsProvider 由 null 变有值 →
//     全量 syncThreads() + 启动 chat realtime 监听。main.dart build 里
//     ref.watch(chatSyncManagerProvider) 一次即挂上（login 走的是
//     settings 变化 → creds 重建，无需侵入 settings_controller.login）。
//   * 进入会话列表（ThreadsShellPage initState）：syncIfStale(30s)。
//   * app resume（main.dart didChangeAppLifecycleState）：onAppResumed —
//     SSE kick + syncIfStale(5s)。
//   * realtime desync：listener 内部清 cursor + 全量 syncThreads()。
//   * logout：creds 变 null → 停监听；SseCursors 由
//     auth_logout.purgeUserData → clearAll 统一清（含 'chat.sync' scope）。
//
// token 轮换（后台 refresher 每小时）：只 kick SSE 重连，不重复全量
// hydrate —— 增量由 realtime 事件覆盖，hydrate 由各 30s 节流触发点覆盖。

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import '../../../data/wiki_providers.dart' show appDbProvider;
import '../../../services/auth_service.dart';
import '../data/chat_repo.dart';
import '../data/chat_scope.dart';
import '../data/chat_sync.dart';
import 'chat_events_realtime.dart';

final _log = Logger('biumind.chat.sync.manager');

class ChatSyncManager {
  ChatSyncManager(this._ref);
  final Ref _ref;

  /// 会话列表进入时的增量补拉节流阈值。
  static const staleThreshold = Duration(seconds: 30);

  ChatEventsListener? _listener;
  String? _lastUserId;
  DateTime? _lastSyncAt;
  Future<ChatSyncResult>? _inFlight;

  /// 由 provider 在构建时 + creds 变化时调用。未登录 noop。
  void onCredentialsChanged() {
    final creds = _ref.read(hubCredentialsProvider);
    if (creds == null) {
      // logout —— 停监听；本地 chat 数据保留但已按 ownerKey scope 隔离
      // （P0 数据隔离：下个账号的 ChatRepo 查询强制 ownerKey 过滤，旧数据
      // 天然不可见，无需清库），cursor 由 purgeUserData 统一清。
      final l = _listener;
      _listener = null;
      if (l != null) unawaited(l.stop());
      _lastUserId = null;
      _lastSyncAt = null;
      return;
    }
    final uid = decodeJwtUserId(creds.bearerToken);
    final isNewLogin = _lastUserId == null;
    // 换账号但没过 logout 空档（防御：正常流程 clearTokens 会先置 null）——
    // topic 里带旧 uid，必须重建监听；数据也按新账号重新 hydrate。
    final userChanged =
        uid != null && _lastUserId != null && uid != _lastUserId;
    if (uid != null) _lastUserId = uid;
    if (userChanged) {
      final l = _listener;
      _listener = null;
      if (l != null) unawaited(l.stop());
    } else if (!isNewLogin) {
      // 同账号 token 轮换：SSE 长连接还挂着旧 token，主动 kick 让它用新
      // token 重连（auth 闭包每次 connect 读最新 creds）。不重复全量
      // hydrate —— 增量由 realtime 覆盖，兜底由各节流触发点覆盖。
      _listener?.kick();
    }
    _listener ??= ChatEventsListener(
      _ref,
      resolveService: () => _ref.read(chatSyncServiceProvider),
    )..start();
    if (isNewLogin || userChanged) unawaited(syncNow());
  }

  /// 全量 hydrate。单实例 in-flight —— 多触发点同时调用合并成一次。
  /// syncThreads 整体失败（网络）只记日志，下个触发点自然重试。
  Future<ChatSyncResult> syncNow() {
    final running = _inFlight;
    if (running != null) return running;
    final svc = _ref.read(chatSyncServiceProvider);
    if (svc == null) return Future.value(ChatSyncResult());
    final f = svc
        .syncThreads()
        .then(
          (r) {
            _lastSyncAt = DateTime.now();
            return r;
          },
          onError: (Object e) {
            _log.warning('syncThreads failed: $e');
            return ChatSyncResult()..errors.add('$e');
          },
        )
        .whenComplete(() {
          _inFlight = null;
        });
    return _inFlight = f;
  }

  /// 距上次成功同步超过 [maxAge] 才补拉 —— ThreadsShellPage / resume 用。
  Future<void> syncIfStale([Duration maxAge = staleThreshold]) async {
    final last = _lastSyncAt;
    if (last != null && DateTime.now().difference(last) < maxAge) return;
    await syncNow();
  }

  /// app 从后台回前台：SSE 主动重连 + 轻量补拉（5s 内刚拉过就跳过）。
  void onAppResumed() {
    _listener?.kick();
    unawaited(syncIfStale(const Duration(seconds: 5)));
  }

  Future<void> dispose() async {
    await _listener?.stop();
    _listener = null;
  }
}

/// 当前登录态的 ChatSyncService；未登录 null（各调用方据此退化 noop）。
/// tokenProvider 每次请求读最新 creds —— token 轮换后不留陈 token。
final chatSyncServiceProvider = Provider<ChatSyncService?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  // P0 数据隔离：scope 派生不出来（token 非 JWT）时同步整体退化为 noop ——
  // 没有隔离键宁可不同步，也不能写无归属行。
  final scope = ref.watch(chatOwnerScopeProvider);
  if (scope == null) return null;
  final db = ref.watch(appDbProvider);
  final base = creds.endpoint.toString();
  return ChatSyncService(
    repo: ChatRepo(db, scope: scope),
    baseUrl: base.endsWith('/') ? base.substring(0, base.length - 1) : base,
    tokenProvider: () async => ref.read(hubCredentialsProvider)?.bearerToken,
  );
});

/// main.dart build 里 ref.watch 一次即完成挂载（冷启动 hydrate + realtime
/// 监听 + login/logout 响应）。
final chatSyncManagerProvider = Provider<ChatSyncManager>((ref) {
  final m = ChatSyncManager(ref);
  ref.listen<HubCredentials?>(hubCredentialsProvider, (prev, next) {
    m.onCredentialsChanged();
  });
  ref.onDispose(() => m.dispose());
  // 已登录（冷启动）立即 kick；未登录 noop，等 login 后 listen 触发。
  m.onCredentialsChanged();
  return m;
});
