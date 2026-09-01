/// 本机解析任务的服务端镜像（设计文档 §3.5 / W2）：
/// 把 docproc-web 本机解析的生命周期镜像进服务端 `ingest_tasks`
/// （processor=client）——进度进 activity 管道、客户端进程死亡后云端
/// reaper 可接管续跑（服务端 >10min 无 PATCH 即接管，updated_at 即
/// 惰性心跳，客户端无需为「进程被杀」做任何事）。
///
/// **镜像是可见性/接管机制，不是正确性依赖**：所有方法 best-effort，
/// 失败仅 debugPrint，绝不抛出阻断解析主流程。
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../data/api/wiki_client.dart';

class DocprocTaskMirror {
  DocprocTaskMirror({
    required WikiClient client,
    required String projectId,
    this.progressInterval = const Duration(milliseconds: 500),
  })  : _client = client,
        _projectId = projectId;

  final WikiClient _client;
  final String _projectId;

  /// progress PATCH 节流间隔：距上次成功发送 ≥ 该值才再发。
  final Duration progressInterval;

  /// 镜像任务 id；null = 未建（建任务失败时后续全是 no-op）。
  String? taskId;

  DateTime? _lastProgressAt;

  /// 终态标记：done/failed/cancelled 后不再发任何 PATCH（服务端对
  /// 已终态任务回 409 already_terminal，没必要求 409）。
  bool _terminal = false;

  /// 建镜像任务（processor=client，不 publish 给 wiki-llm）并立即
  /// PATCH running。[sourceId] 是占位 source 的 id。
  Future<void> start({
    required String sourceId,
    required String title,
  }) async {
    try {
      final task = await _client.createIngestTask(
        _projectId,
        sourceId: sourceId,
        title: title,
        processor: 'client',
      );
      taskId = task.taskId;
      await _patch(status: 'running');
    } on Exception catch (e) {
      _log('start 镜像失败: $e');
    }
  }

  /// 解析进度（节流）：progress 是**整体替换**对象，每次发完整的
  /// {phase, percent}。[now] 可注入便于测试节流逻辑。
  Future<void> progress(String phase, int percent, {DateTime? now}) async {
    if (_terminal) return;
    final t = now ?? DateTime.now();
    final last = _lastProgressAt;
    if (last != null && t.difference(last) < progressInterval) return;
    _lastProgressAt = t;
    await _patch(progress: {'phase': phase, 'percent': percent});
  }

  Future<void> done() async {
    _terminal = true;
    await _patch(status: 'done');
  }

  Future<void> failed(Object error) async {
    _terminal = true;
    await _patch(status: 'failed', error: error.toString());
  }

  /// 用户主动取消时必须 PATCH cancelled（区别于进程死亡：死亡靠
  /// 服务端 reaper 检测，无需任何调用）。
  Future<void> cancelled() async {
    _terminal = true;
    await _patch(status: 'cancelled');
  }

  Future<void> _patch({
    String? status,
    Map<String, dynamic>? progress,
    String? error,
  }) async {
    final id = taskId;
    if (id == null) return;
    try {
      await _client.patchIngestTask(
        _projectId,
        id,
        status: status,
        progress: progress,
        error: error,
      );
    } on Exception catch (e) {
      _log('PATCH ${status ?? 'progress'} 失败: $e');
    }
  }

  void _log(String msg) => debugPrint('[docproc-mirror] $msg');
}
