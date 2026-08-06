/// Sync WS providers ——  实时事件流接入。
///
/// 当前 B2.9 范围：客户端骨架就绪 + 自动重连 + 错误降级。brain 端
/// `/v1/wiki/projects/{pid}/sync` 当前是 ready+ping 心跳骨架（B2.9.x
/// 接通 events_outbox listener 后会真正推 catchup/live 帧）。
///
/// 任何模块都可以 `ref.watch(wikiSyncEventsProvider(projectId))` 监听
/// 实时事件流（每个事件一个 `Map<Object?, Object?>`，schema 与 brain.events
/// 表 row 投影一致）。
library;

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../services/auth_service.dart';
import '../data/sync_ws_client.dart';

/// 实时事件流 — 客户端 reducer 的 input。
///
/// 重连策略：
///   - WebSocket close / error → 等 5s 重连
///   - 重连时 `since` 用上次 ready 帧返回的 event_id（暂未用 — brain
///     当前不发 catchup 帧，永远 since=0；接通后改）
///   - dispose 时关闭 stream
final wikiSyncEventsProvider =
    StreamProvider.family.autoDispose<Map<Object?, Object?>, String>(
  (ref, projectId) async* {
    final creds = ref.watch(hubCredentialsProvider);
    if (creds == null || projectId.isEmpty) {
      // 没凭证或没项目 — 静默无事件流
      return;
    }
    int since = 0;
    while (true) {
      try {
        await for (final msg in connectSync(
          baseUrl: creds.endpoint,
          projectId: projectId,
          token: creds.bearerToken,
          since: since,
        )) {
          switch (msg.type) {
            case 'ready':
              final s = msg.payload['since'];
              if (s is int) since = s;
              if (s is String) since = int.tryParse(s) ?? since;
            case 'catchup':
              final events = msg.payload['events'];
              if (events is List) {
                for (final e in events) {
                  if (e is Map) yield e.cast<Object?, Object?>();
                }
              }
            case 'live':
              final event = msg.payload['event'];
              if (event is Map) yield event.cast<Object?, Object?>();
              // 维护 since
              final eid = (event is Map ? event['event_id'] : null);
              if (eid is int && eid > since) since = eid;
            case 'ping':
              // keepalive — no-op
              break;
            case 'error':
              if (kDebugMode) {
                debugPrint(
                  '[wiki sync] error: ${msg.payload['reason']}',
                );
              }
              break;
            default:
              break;
          }
        }
      } on Exception catch (e) {
        if (kDebugMode) debugPrint('[wiki sync] connection lost: $e');
      }
      // 等一下重连。
      await Future<void>.delayed(const Duration(seconds: 5));
    }
  },
);
