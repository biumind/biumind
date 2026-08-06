// Creation domain models.
//
// 纯数据类 (无 Hive / Drift annotation): MVP 用 SharedPreferences 存 JSON
// 缓存 200 条最近任务. P4b-3 TasksController 在性能压力变大时迁到 drift.
//
// 字段对齐 services/aigc/internal/api projectTask() 投影 (含 outputs).

import 'package:meta/meta.dart';

@immutable
class TaskOutput {
  final int idx;
  final String kind; // image | video | audio | cover
  final String sha256;
  final String url; // "cas:<sha>" 或 https://cdn...
  final String? blurhash;
  final String? coverSha;
  final String? mimeType;
  final int? fileSize;
  final int? width;
  final int? height;
  final int? durationMs;

  /// 结构化产物 (kind=="hotparse": 文案/钩子/分镜/标签/转写)。普通 image/video 为空。
  final Map<String, dynamic>? metadata;

  const TaskOutput({
    required this.idx,
    required this.kind,
    required this.sha256,
    required this.url,
    this.blurhash,
    this.coverSha,
    this.mimeType,
    this.fileSize,
    this.width,
    this.height,
    this.durationMs,
    this.metadata,
  });

  factory TaskOutput.fromJson(Map<String, dynamic> j) => TaskOutput(
        idx: (j['idx'] as num?)?.toInt() ?? 0,
        kind: j['kind'] as String? ?? 'image',
        sha256: j['sha256'] as String? ?? '',
        url: j['url'] as String? ?? '',
        blurhash: j['blurhash'] as String?,
        coverSha: j['cover_sha'] as String?,
        mimeType: j['mime_type'] as String?,
        fileSize: (j['file_size'] as num?)?.toInt(),
        width: (j['width'] as num?)?.toInt(),
        height: (j['height'] as num?)?.toInt(),
        durationMs: (j['duration_ms'] as num?)?.toInt(),
        metadata: (j['metadata'] as Map?)?.cast<String, dynamic>(),
      );

  Map<String, dynamic> toJson() => {
        'idx': idx,
        'kind': kind,
        'sha256': sha256,
        'url': url,
        if (blurhash != null) 'blurhash': blurhash,
        if (coverSha != null) 'cover_sha': coverSha,
        if (mimeType != null) 'mime_type': mimeType,
        if (fileSize != null) 'file_size': fileSize,
        if (width != null) 'width': width,
        if (height != null) 'height': height,
        if (durationMs != null) 'duration_ms': durationMs,
        if (metadata != null) 'metadata': metadata,
      };
}

/// TaskStatus — 任务状态机. 与 services/aigc 统一 (string enum).
enum TaskStatus {
  submitting, // 客户端本地态: POST /v1/generations 在飞
  pending,
  queued,
  running,
  completed,
  failed,
  blocked, // 内容审核拦截
  cancelled;

  static TaskStatus fromWire(String? raw) {
    switch (raw) {
      case 'pending':
        return TaskStatus.pending;
      case 'queued':
        return TaskStatus.queued;
      case 'running':
        return TaskStatus.running;
      case 'completed':
        return TaskStatus.completed;
      case 'failed':
        return TaskStatus.failed;
      case 'blocked':
        return TaskStatus.blocked;
      case 'cancelled':
        return TaskStatus.cancelled;
      default:
        return TaskStatus.pending;
    }
  }

  String get wire {
    switch (this) {
      case TaskStatus.submitting:
        return 'submitting';
      case TaskStatus.pending:
        return 'pending';
      case TaskStatus.queued:
        return 'queued';
      case TaskStatus.running:
        return 'running';
      case TaskStatus.completed:
        return 'completed';
      case TaskStatus.failed:
        return 'failed';
      case TaskStatus.blocked:
        return 'blocked';
      case TaskStatus.cancelled:
        return 'cancelled';
    }
  }

  bool get isActive =>
      this == TaskStatus.submitting ||
      this == TaskStatus.pending ||
      this == TaskStatus.queued ||
      this == TaskStatus.running;

  bool get isTerminal =>
      this == TaskStatus.completed ||
      this == TaskStatus.failed ||
      this == TaskStatus.blocked ||
      this == TaskStatus.cancelled;
}

/// CreationTask — 一条作品 / 任务的完整快照.
@immutable
class CreationTask {
  final String id;
  final String userId;
  final String type; // image | video | digital_human | hotparse
  final String modelCode;
  final String? providerCode;
  final String prompt;
  final String? negativePrompt;
  final Map<String, dynamic> params;

  final TaskStatus status;
  final int progress; // 0..100
  final String? errorCode;
  final String? errorMessage;

  final int costCredits;
  final int refundedCredits;
  final bool isPublic;

  final List<TaskOutput> outputs;

  final DateTime createdAt;
  final DateTime? queuedAt;
  final DateTime? startedAt;
  final DateTime? completedAt;
  final DateTime updatedAt; // 客户端补的: 最后一次 SSE/poll 触达

  /// 客户端本地占位 ID (submitting 状态用). 真 id 拿到后替换, 此字段保留便于 UI 追溯.
  final String? localTempId;

  const CreationTask({
    required this.id,
    required this.userId,
    required this.type,
    required this.modelCode,
    this.providerCode,
    required this.prompt,
    this.negativePrompt,
    this.params = const {},
    required this.status,
    this.progress = 0,
    this.errorCode,
    this.errorMessage,
    this.costCredits = 0,
    this.refundedCredits = 0,
    this.isPublic = false,
    this.outputs = const [],
    required this.createdAt,
    this.queuedAt,
    this.startedAt,
    this.completedAt,
    required this.updatedAt,
    this.localTempId,
  });

  /// 构造一个客户端本地"提交中"占位; 服务端 POST /v1/generations 返回前用.
  factory CreationTask.localSubmitting({
    required String tempId,
    required String userId,
    required String type,
    required String modelCode,
    required String prompt,
    Map<String, dynamic> params = const {},
  }) {
    final now = DateTime.now().toUtc();
    return CreationTask(
      id: tempId,
      userId: userId,
      type: type,
      modelCode: modelCode,
      prompt: prompt,
      params: params,
      status: TaskStatus.submitting,
      createdAt: now,
      updatedAt: now,
      localTempId: tempId,
    );
  }

  factory CreationTask.fromJson(Map<String, dynamic> j) {
    final outputsRaw = j['outputs'];
    final outputs = outputsRaw is List
        ? outputsRaw
            .whereType<Map<String, dynamic>>()
            .map(TaskOutput.fromJson)
            .toList()
        : <TaskOutput>[];
    return CreationTask(
      id: j['id'] as String? ?? '',
      userId: j['user_id'] as String? ?? '',
      type: j['type'] as String? ?? 'image',
      modelCode: j['model_code'] as String? ?? '',
      providerCode: j['provider_code'] as String?,
      prompt: j['prompt'] as String? ?? '',
      negativePrompt: j['negative_prompt'] as String?,
      params: (j['params'] as Map?)?.cast<String, dynamic>() ?? const {},
      status: TaskStatus.fromWire(j['status'] as String?),
      progress: (j['progress'] as num?)?.toInt() ?? 0,
      errorCode: j['error_code'] as String?,
      errorMessage: j['error_message'] as String?,
      costCredits: (j['cost_credits'] as num?)?.toInt() ?? 0,
      refundedCredits: (j['refunded_credits'] as num?)?.toInt() ?? 0,
      isPublic: j['is_public'] as bool? ?? false,
      outputs: outputs,
      createdAt: _parseDate(j['created_at']) ?? DateTime.now().toUtc(),
      queuedAt: _parseDate(j['queued_at']),
      startedAt: _parseDate(j['started_at']),
      completedAt: _parseDate(j['completed_at']),
      updatedAt: _parseDate(j['updated_at']) ??
          _parseDate(j['completed_at']) ??
          _parseDate(j['created_at']) ??
          DateTime.now().toUtc(),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'user_id': userId,
        'type': type,
        'model_code': modelCode,
        if (providerCode != null) 'provider_code': providerCode,
        'prompt': prompt,
        if (negativePrompt != null) 'negative_prompt': negativePrompt,
        'params': params,
        'status': status.wire,
        'progress': progress,
        if (errorCode != null) 'error_code': errorCode,
        if (errorMessage != null) 'error_message': errorMessage,
        'cost_credits': costCredits,
        'refunded_credits': refundedCredits,
        'is_public': isPublic,
        'outputs': outputs.map((o) => o.toJson()).toList(),
        'created_at': createdAt.toIso8601String(),
        if (queuedAt != null) 'queued_at': queuedAt!.toIso8601String(),
        if (startedAt != null) 'started_at': startedAt!.toIso8601String(),
        if (completedAt != null)
          'completed_at': completedAt!.toIso8601String(),
        'updated_at': updatedAt.toIso8601String(),
        if (localTempId != null) 'local_temp_id': localTempId,
      };

  /// merge 把进度更新合并到现有 task. 不变量:
  /// - progress 单调递增 (max)
  /// - status 严格按状态机推进; 只能往前不能回退到不那么 terminal 的状态
  /// - outputs 非空时覆盖 (服务端 completed 一次性带回完整列表)
  CreationTask merge({
    TaskStatus? status,
    int? progress,
    List<TaskOutput>? outputs,
    String? errorCode,
    String? errorMessage,
    int? refundedCredits,
    DateTime? queuedAt,
    DateTime? startedAt,
    DateTime? completedAt,
    DateTime? updatedAt,
  }) {
    final newStatus = _advanceStatus(this.status, status);
    final newProgress = progress != null && progress > this.progress
        ? progress
        : this.progress;
    final newOutputs =
        (outputs != null && outputs.isNotEmpty) ? outputs : this.outputs;
    return CreationTask(
      id: id,
      userId: userId,
      type: type,
      modelCode: modelCode,
      providerCode: providerCode,
      prompt: prompt,
      negativePrompt: negativePrompt,
      params: params,
      status: newStatus,
      progress: newProgress,
      errorCode: errorCode ?? this.errorCode,
      errorMessage: errorMessage ?? this.errorMessage,
      costCredits: costCredits,
      refundedCredits: refundedCredits ?? this.refundedCredits,
      isPublic: isPublic,
      outputs: newOutputs,
      createdAt: createdAt,
      queuedAt: queuedAt ?? this.queuedAt,
      startedAt: startedAt ?? this.startedAt,
      completedAt: completedAt ?? this.completedAt,
      updatedAt: updatedAt ?? DateTime.now().toUtc(),
      localTempId: localTempId,
    );
  }

  CreationTask copyWith({
    String? id,
    String? userId,
    String? localTempId,
  }) =>
      CreationTask(
        id: id ?? this.id,
        userId: userId ?? this.userId,
        type: type,
        modelCode: modelCode,
        providerCode: providerCode,
        prompt: prompt,
        negativePrompt: negativePrompt,
        params: params,
        status: status,
        progress: progress,
        errorCode: errorCode,
        errorMessage: errorMessage,
        costCredits: costCredits,
        refundedCredits: refundedCredits,
        isPublic: isPublic,
        outputs: outputs,
        createdAt: createdAt,
        queuedAt: queuedAt,
        startedAt: startedAt,
        completedAt: completedAt,
        updatedAt: updatedAt,
        localTempId: localTempId ?? this.localTempId,
      );
}

DateTime? _parseDate(dynamic v) {
  if (v is String && v.isNotEmpty) {
    try {
      return DateTime.parse(v);
    } catch (_) {
      return null;
    }
  }
  return null;
}

/// _advanceStatus: 严格状态机推进, 防回退.
/// 优先级: terminal > running > queued > pending > submitting.
TaskStatus _advanceStatus(TaskStatus cur, TaskStatus? incoming) {
  if (incoming == null) return cur;
  // terminal 不可被覆盖 (一旦 completed 就不能回到 running)
  if (cur.isTerminal) return cur;
  // 其他情况按 rank 升序推进
  final ranks = {
    TaskStatus.submitting: 0,
    TaskStatus.pending: 1,
    TaskStatus.queued: 2,
    TaskStatus.running: 3,
    TaskStatus.completed: 4,
    TaskStatus.failed: 4,
    TaskStatus.blocked: 4,
    TaskStatus.cancelled: 4,
  };
  return (ranks[incoming]! >= ranks[cur]!) ? incoming : cur;
}
