/// Ingest task 实时进度 controller —— B2.9.y 升级版。
///
/// 原 B2.6 实现连 brain `/ingest/tasks/{tid}/events` SSE 端点；本批升级为
/// 监听 [wikiSyncEventsProvider]：通过 brain syncws 的 catchup + live
/// frame 拉取目标 task 的所有 ingest_task.* 事件，按 op 推动状态机。
///
/// 优势：
///   - SSE 端点废弃（brain stub 当前只发一帧 placeholder，没必要再单独搞）
///   - 与 activity drawer 用同一条 WS（少一条连接）
///   - WS 重连 / catchup 自动补漏（since cursor 持久维护在 sync_provider）
///
/// 状态机（与 brain ingest_tasks.status 一致）：
///   pending → running → partial → done
///                │           │
///                ↓           ↓
///             failed     cancelled
library;

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../application/sync_provider.dart' show wikiSyncEventsProvider;

@immutable
class IngestStreamState {
  const IngestStreamState({
    required this.taskId,
    required this.projectId,
    this.status = 'pending',
    this.title = '',
    this.stage,
    this.percent,
    this.resultPages = const <String>[],
    this.error,
    this.events = const <IngestEventFrame>[],
  });

  final String taskId;
  final String projectId;
  final String status;
  final String title;
  final String? stage;
  final double? percent;
  final List<String> resultPages;
  final String? error;
  final List<IngestEventFrame> events;

  bool get isTerminal =>
      status == 'done' || status == 'failed' || status == 'cancelled';

  /// 兼容老 UI 字段：sync_provider 走通后总是"已连接"。
  bool get connected => true;

  IngestStreamState copyWith({
    String? status,
    String? title,
    Object? stage = _unset,
    Object? percent = _unset,
    List<String>? resultPages,
    Object? error = _unset,
    List<IngestEventFrame>? events,
  }) {
    return IngestStreamState(
      taskId: taskId,
      projectId: projectId,
      status: status ?? this.status,
      title: title ?? this.title,
      stage: stage == _unset ? this.stage : stage as String?,
      percent: percent == _unset ? this.percent : percent as double?,
      resultPages: resultPages ?? this.resultPages,
      error: error == _unset ? this.error : error as String?,
      events: events ?? this.events,
    );
  }
}

const Object _unset = Object();

@immutable
class IngestEventFrame {
  const IngestEventFrame({
    required this.op,
    required this.payload,
    required this.eventId,
    required this.at,
  });
  final String op;
  final Map<String, Object?> payload;
  final int eventId;
  final DateTime at;
}

class IngestStreamArgs {
  const IngestStreamArgs({required this.projectId, required this.taskId});
  final String projectId;
  final String taskId;

  @override
  bool operator ==(Object other) =>
      other is IngestStreamArgs &&
      other.projectId == projectId &&
      other.taskId == taskId;
  @override
  int get hashCode => Object.hash(projectId, taskId);
}

class IngestStreamController
    extends AutoDisposeFamilyAsyncNotifier<IngestStreamState, IngestStreamArgs> {
  final List<IngestEventFrame> _events = <IngestEventFrame>[];
  int _maxEventId = 0;

  @override
  Future<IngestStreamState> build(IngestStreamArgs args) async {
    // 监听项目级实时事件流；只关心 entity=ingest_task && entity_id=taskId。
    ref.listen<AsyncValue<Map<Object?, Object?>>>(
      wikiSyncEventsProvider(args.projectId),
      (_, next) {
        next.whenData((event) => _onEvent(event, args));
      },
    );

    return IngestStreamState(
      taskId: args.taskId,
      projectId: args.projectId,
    );
  }

  void _onEvent(Map<Object?, Object?> event, IngestStreamArgs args) {
    final entity = event['entity'];
    final entityId = event['entity_id'];
    final op = event['op'];
    final eventId = event['event_id'];
    if (entity != 'ingest_task' || entityId != args.taskId) return;
    if (op is! String || eventId is! int) return;
    if (eventId <= _maxEventId) return; // duplicate / out-of-order
    _maxEventId = eventId;

    final payload = event['payload'];
    final p = payload is Map
        ? payload.cast<String, Object?>()
        : const <String, Object?>{};

    _events.add(IngestEventFrame(
      op: op,
      payload: p,
      eventId: eventId,
      at: DateTime.now(),
    ));

    final cur = state.valueOrNull;
    if (cur == null) return;
    var next = cur.copyWith(events: List.unmodifiable(_events));

    switch (op) {
      case 'started':
        next = next.copyWith(
          status: 'running',
          title: (p['title'] as String?) ?? cur.title,
        );
      case 'phase':
      case 'progress':
        final stage = p['phase'] ?? p['stage'];
        final pct = p['percent'];
        next = next.copyWith(
          stage: stage is String ? stage : null,
          percent: (pct is num) ? pct.toDouble() / 100.0 : null,
          status: cur.status == 'pending' ? 'running' : cur.status,
        );
      case 'page_planned':
      case 'page':
        final pid = p['page_id'];
        if (pid is String && pid.isNotEmpty) {
          final pages = List<String>.from(next.resultPages);
          if (!pages.contains(pid)) pages.add(pid);
          next = next.copyWith(
            resultPages: pages,
            status: cur.status == 'pending' ? 'running' : cur.status,
          );
        }
      case 'partial':
        next = next.copyWith(status: 'partial');
      case 'done':
        final pages = (p['result_pages'] as List?)
                ?.map((e) => e.toString())
                .toList() ??
            next.resultPages;
        next = next.copyWith(
          status: 'done',
          resultPages: pages,
          percent: 1.0,
        );
      case 'failed':
        next = next.copyWith(
          status: 'failed',
          error: (p['error'] ?? p['message'])?.toString(),
        );
      case 'cancelled':
        next = next.copyWith(status: 'cancelled');
      default:
        // 未知 op — 仅追加事件日志
        if (kDebugMode) {
          debugPrint('[ingest stream] unknown op: $op');
        }
    }
    state = AsyncData(next);
  }
}

final ingestStreamControllerProvider = AsyncNotifierProvider.autoDispose
    .family<IngestStreamController, IngestStreamState, IngestStreamArgs>(
  IngestStreamController.new,
);
