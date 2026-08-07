// ChatEventsRealtime — listens on `chat:user:<userId>` and keeps local
// Drift chat data in sync with writes made by this user's other devices.
//
// Server side: services/brain/internal/chat/events.go writes
// chat.message_created / chat.thread_updated / chat.thread_deleted rows
// to brain.events in the
// same tx as the message/thread write (transactional outbox); the events
// Listener/Poller forwards them to realtime, topic = scope =
// `chat:user:<user_id>`. Payloads carry ids only (never content), wrapped
// by the poller as {event_id, event_type, data: {...}} — same envelope the
// sidebar listener unwraps.
//
// Behaviour (tasks_controller 完整范例的同款接线):
//
//   * SseCursors 持久化 last-event-id (scope 'chat.sync') — 重启秒续接;
//     logout 时 auth_logout.purgeUserData → SseCursorsDao.clearAll 统一清。
//   * chat.message_created / chat.thread_updated → per-thread debounce
//     后 ChatSyncService.syncThread（一轮 turn 两条事件合并成一次拉取）。
//   * chat.thread_deleted → 直接本地 repo.deleteThreads（不走 syncThread）。
//   * onDesync (4009) → 清 cursor + 全量 syncThreads()。
//   * 未知 kind 忽略；旧服务端没有 chat 事件时 stream 只是安静 —— 整个
//     listener 退化为纯 hydrate，不报错。
//
// Mirrors features/apps/sync/sidebar_events_realtime.dart in shape and
// lifecycle (started by ChatSyncManager once creds exist, stopped on
// logout / manager dispose).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import '../../../data/sse/realtime_hub.dart';
import '../../../data/sse/sse_cursors_dao.dart';
import '../../../data/wiki_providers.dart' show appDbProvider;
import '../../../services/auth_service.dart';
import '../data/chat_repo.dart';
import '../data/chat_sync.dart';

final _log = Logger('biumind.chat.realtime');

class ChatEventsListener {
  /// [resolveService] 由 ChatSyncManager 注入（读 chatSyncServiceProvider），
  /// 避免本文件反向依赖 manager 的 provider 定义造成循环 import。
  ChatEventsListener(
    this._ref, {
    required ChatSyncService? Function() resolveService,
  }) : _resolveService = resolveService;

  /// SseCursors scope —— 多 RealtimeHub 实例共用一张表，靠 scope 区分。
  static const sseScope = 'chat.sync';

  /// 一轮 turn 会产生 user + assistant 两条 message_created（+ 可能的
  /// thread_updated）；debounce 合并成一次 syncThread。
  static const _debounceWindow = Duration(milliseconds: 500);

  final Ref _ref;
  final ChatSyncService? Function() _resolveService;

  RealtimeHub? _hub;
  StreamSubscription<RealtimeFrame>? _sub;
  String? _topic;
  SseCursorsDao? _cursors;
  final Map<String, Timer> _pendingThreads = {};

  void start() {
    if (_topic != null) return;
    final creds = _ref.read(hubCredentialsProvider);
    if (creds == null) {
      _log.fine('no creds; skipping chat events listener');
      return;
    }
    final userId = decodeJwtUserId(creds.bearerToken);
    if (userId == null) {
      _log.warning('JWT missing sub; chat events listener disabled');
      return;
    }
    _topic = 'chat:user:$userId';
    final cursors = SseCursorsDao(_ref.read(appDbProvider));
    _cursors = cursors;

    _hub = RealtimeHub(
      RealtimeHubConfig(
        endpoint: creds.endpoint.replace(path: '/v1/realtime/stream'),
        auth: () async {
          final c = _ref.read(hubCredentialsProvider);
          return c?.bearerToken ?? '';
        },
      ),
      loadLastEventId: () => cursors.load(sseScope),
      saveLastEventId: (id) => cursors.save(sseScope, id),
      onDesync: _handleDesync,
    );

    _sub = _hub!
        .subscribe(_topic!)
        .listen(
          _onFrame,
          onError: (Object e) => _log.warning('chat events stream error: $e'),
        );
    _log.info('chat events listener subscribed to $_topic');
  }

  Future<void> stop() async {
    for (final t in _pendingThreads.values) {
      t.cancel();
    }
    _pendingThreads.clear();
    await _sub?.cancel();
    _sub = null;
    await _hub?.dispose();
    _hub = null;
    _topic = null;
    _cursors = null;
  }

  /// app resume / token 轮换后主动重连（RealtimeHub.reconnect —— 不断
  /// cursor，只是加速用新 token / 新网络落地重建连接）。
  void kick() => _hub?.reconnect();

  /// 测试钩子 —— 单测经此投递帧走 _onFrame 分支（私有方法无法触达）。
  @visibleForTesting
  void debugHandleFrame(RealtimeFrame frame) => _onFrame(frame);

  void _onFrame(RealtimeFrame frame) {
    switch (frame.kind) {
      case 'chat.message_created':
      case 'chat.thread_updated':
        // poller 包装: {event_id, event_type, data: {thread_id, ...}}。
        final inner =
            (frame.payload['data'] as Map?)?.cast<String, dynamic>() ??
            frame.payload;
        final tid = inner['thread_id']?.toString();
        if (tid == null || tid.isEmpty) return;
        _scheduleThreadSync(tid);
      case 'chat.thread_deleted':
        // 他端删了会话 —— 直接本地级联删, 不走 syncThread(服务端已无此
        // thread, 拉取只会 404)。Drift stream 会自动刷新 UI; 若删的是
        // 正打开的会话, sidebar 的选中守卫(_Sidebar.build)会清 _selectedId。
        final inner =
            (frame.payload['data'] as Map?)?.cast<String, dynamic>() ??
            frame.payload;
        final tid = inner['thread_id']?.toString();
        if (tid == null || tid.isEmpty) return;
        final repo = _resolveService()?.repo ?? ChatRepo(_ref.read(appDbProvider));
        unawaited(repo.deleteThreads([tid]).catchError((Object e) {
          _log.warning('thread_deleted $tid local delete failed: $e');
        }));
      case 'chat.message_deleted':
        // 他端删了单条 message —— 直接本地级联删（blocks + reactions），
        // 与 thread_deleted 同理；服务端为准。desync/漏事件由 syncThread
        // 对账兜底（_pullMessages 删本地孤儿）。
        final inner =
            (frame.payload['data'] as Map?)?.cast<String, dynamic>() ??
            frame.payload;
        final mid = inner['message_id']?.toString();
        if (mid == null || mid.isEmpty) return;
        final repo =
            _resolveService()?.repo ?? ChatRepo(_ref.read(appDbProvider));
        unawaited(repo.deleteMessages([mid]).catchError((Object e) {
          _log.warning('message_deleted $mid local delete failed: $e');
        }));
      default:
        // 未知 kind 忽略 —— 前向兼容（服务端后续加新事件类型不崩）。
        break;
    }
  }

  void _scheduleThreadSync(String threadId) {
    _pendingThreads.remove(threadId)?.cancel();
    _pendingThreads[threadId] = Timer(_debounceWindow, () {
      _pendingThreads.remove(threadId);
      final svc = _resolveService();
      if (svc == null) return;
      unawaited(
        svc.syncThread(threadId).catchError((Object e) {
          _log.warning('syncThread $threadId (realtime) failed: $e');
        }),
      );
    });
  }

  /// desync 4009 兜底 —— 服务端判定 last-event-id 超出 ledger retention。
  /// hub 已清内存 cursor；这里清持久化 cursor + 全量 hydrate 对齐。
  Future<void> _handleDesync(int code, String reason) async {
    _log.warning(
      'chat events desync code=$code reason=$reason — '
      'clearing cursor + full sync',
    );
    try {
      await _cursors?.clear(sseScope);
    } catch (e) {
      _log.warning('clear chat sse cursor on desync: $e');
    }
    try {
      await _resolveService()?.syncThreads();
    } catch (e) {
      _log.warning('full sync on desync failed: $e');
    }
  }
}

/// 从 access_token (JWT) 解 sub —— 跟 sidebar_events_realtime 同款小工具。
/// realtime topic 的 user 段就是 JWT sub（brain events.go scope 约定）。
/// ChatSyncManager 也用它判断「换账号」。
String? decodeJwtUserId(String jwt) {
  try {
    final parts = jwt.split('.');
    if (parts.length != 3) return null;
    var payload = parts[1];
    while (payload.length % 4 != 0) {
      payload += '=';
    }
    final json = utf8.decode(base64Url.decode(payload));
    final m = jsonDecode(json) as Map<String, dynamic>;
    final v = m['sub'];
    if (v is String && v.isNotEmpty) return v;
    return null;
  } catch (_) {
    return null;
  }
}
