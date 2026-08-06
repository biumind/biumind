// AigcClient — services/aigc REST endpoint thin wrapper.
//
// 与 services/aigc/internal/api 契约对齐:
//
//   GET    /v1/models?type=image|video|digital_human|hotparse
//   GET    /v1/gallery?type=&keyword=&limit=&offset=
//   POST   /v1/generations               (Bearer)
//   GET    /v1/generations/mine          (Bearer)
//   GET    /v1/generations/{id}          (Bearer)
//   PATCH  /v1/generations/{id}/visibility  (Bearer)
//   DELETE /v1/generations/{id}          (Bearer)
//   POST   /v1/generations/{id}/cancel   (Bearer)
//
// 与 chat/wiki/admin client 一致, 走 _http_helpers.apiRequest, 自动 401 retry.

import '../../../data/api/_http_helpers.dart';
import '../domain/ai_model.dart';
import '../domain/character.dart';
import '../domain/creation_task.dart';

class AigcSubmitResult {
  final CreationTask task;
  final int estimatedSeconds;
  final int costCredits;
  final int balanceAfter;

  const AigcSubmitResult({
    required this.task,
    required this.estimatedSeconds,
    required this.costCredits,
    required this.balanceAfter,
  });
}

class AigcClient {
  final Uri baseUrl;
  final String? Function() bearerProvider;

  AigcClient({required this.baseUrl, required this.bearerProvider});

  String? get _token => bearerProvider();

  Uri _uri(String path, [Map<String, String>? query]) {
    final base = baseUrl.toString().replaceAll(RegExp(r'/+$'), '');
    return Uri.parse('$base$path').replace(
      queryParameters: query == null || query.isEmpty ? null : query,
    );
  }

  // ─── Models / Gallery (公开, 不需 token) ────────────

  Future<List<AiModel>> fetchModels({String? type}) async {
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/models', {if (type != null && type.isNotEmpty) 'type': type}),
      bearerToken: null,
    );
    final list = (resp['models'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(AiModel.fromJson)
        .toList();
  }

  Future<List<CreationTask>> fetchGallery({
    String? type,
    String? keyword,
    int limit = 50,
    int offset = 0,
  }) async {
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/gallery', {
        if (type != null && type.isNotEmpty) 'type': type,
        if (keyword != null && keyword.isNotEmpty) 'keyword': keyword,
        'limit': '$limit',
        'offset': '$offset',
      }),
      bearerToken: null,
    );
    final items = (resp['items'] as List?) ?? const [];
    return items
        .whereType<Map<String, dynamic>>()
        .map(_galleryItemToTask)
        .toList();
  }

  // ─── 用户态 ─────────────────────────────────────────

  /// 提交生成. cost_credits 由服务端按模型 + 参数计算; client 不预测.
  Future<AigcSubmitResult> submit({
    required String type,
    required String modelCode,
    required String prompt,
    String? negativePrompt,
    Map<String, dynamic> params = const {},
    bool isPublic = false,
    String? parentSha,
    String? lineageOp,
    String? idempotencyKey,
  }) async {
    final body = <String, dynamic>{
      'type': type,
      'model_code': modelCode,
      'prompt': prompt,
      'params': params,
      'is_public': isPublic,
    };
    if (negativePrompt != null && negativePrompt.isNotEmpty) {
      body['negative_prompt'] = negativePrompt;
    }
    if (parentSha != null && parentSha.isNotEmpty) {
      body['parent_sha'] = parentSha;
    }
    if (lineageOp != null && lineageOp.isNotEmpty) {
      body['lineage_op'] = lineageOp;
    }
    if (idempotencyKey != null) {
      body['idempotency_key'] = idempotencyKey;
    }
    final resp = await apiRequest(
      method: 'POST',
      url: _uri('/v1/generations'),
      bearerToken: _token,
      body: body,
    );
    final task = CreationTask.fromJson(
      (resp['task'] as Map?)?.cast<String, dynamic>() ?? const {},
    );
    return AigcSubmitResult(
      task: task,
      estimatedSeconds: (resp['estimated_seconds'] as num?)?.toInt() ?? 0,
      costCredits: (resp['cost_credits'] as num?)?.toInt() ?? 0,
      balanceAfter: (resp['balance_after'] as num?)?.toInt() ?? 0,
    );
  }

  Future<List<CreationTask>> fetchMyTasks({
    List<TaskStatus> statuses = const [],
    String? type,
    int limit = 50,
    int offset = 0,
  }) async {
    final query = <String, String>{
      'limit': '$limit',
      'offset': '$offset',
    };
    if (statuses.isNotEmpty) {
      query['statuses'] = statuses.map((s) => s.wire).join(',');
    }
    if (type != null && type.isNotEmpty) {
      query['type'] = type;
    }
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/generations/mine', query),
      bearerToken: _token,
    );
    final list = (resp['tasks'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(CreationTask.fromJson)
        .toList();
  }

  Future<CreationTask> getTask(String id, {bool includeLineage = false}) async {
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/generations/$id', {
        if (includeLineage) 'include_lineage': '1',
      }),
      bearerToken: _token,
    );
    return CreationTask.fromJson(
      (resp['task'] as Map?)?.cast<String, dynamic>() ?? const {},
    );
  }

  Future<void> setVisibility(String id, {required bool isPublic}) async {
    await apiRequest(
      method: 'PATCH',
      url: _uri('/v1/generations/$id/visibility'),
      bearerToken: _token,
      body: {'is_public': isPublic},
    );
  }

  Future<void> deleteTask(String id) async {
    await apiRequest(
      method: 'DELETE',
      url: _uri('/v1/generations/$id'),
      bearerToken: _token,
      expectNoBody: false,
    );
  }

  Future<void> cancelTask(String id) async {
    await apiRequest(
      method: 'POST',
      url: _uri('/v1/generations/$id/cancel'),
      bearerToken: _token,
    );
  }

  // ─── Characters / Voices (P5 数字人) ────────────────

  /// 列出数字人角色 — includePublic=true 时同时返回系统内置 + 公开角色.
  Future<List<CharacterEntry>> fetchCharacters({bool includePublic = true}) async {
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/characters', {if (includePublic) 'include_public': '1'}),
      bearerToken: _token,
    );
    final list = (resp['characters'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(CharacterEntry.fromJson)
        .toList();
  }

  /// 创建私有角色. config 可空; avatarUrl 走 cas: 协议或 https.
  Future<CharacterEntry> createCharacter({
    required String name,
    String avatarUrl = '',
    String voiceDefault = '',
    Map<String, dynamic>? config,
    bool isPublic = false,
  }) async {
    final body = <String, dynamic>{
      'name': name,
      'avatar_url': avatarUrl,
      'voice_default': voiceDefault,
      'is_public': isPublic,
    };
    if (config != null && config.isNotEmpty) {
      body['config'] = config;
    }
    final resp = await apiRequest(
      method: 'POST',
      url: _uri('/v1/characters'),
      bearerToken: _token,
      body: body,
    );
    return CharacterEntry.fromJson(
      (resp['character'] as Map?)?.cast<String, dynamic>() ?? const {},
    );
  }

  Future<void> deleteCharacter(String id) async {
    await apiRequest(
      method: 'DELETE',
      url: _uri('/v1/characters/$id'),
      bearerToken: _token,
      expectNoBody: false,
    );
  }

  /// 列音色字典 — provider 为空时返全部.
  Future<List<VoiceEntry>> fetchVoices({String? provider}) async {
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/voices',
          {if (provider != null && provider.isNotEmpty) 'provider': provider}),
      bearerToken: _token,
    );
    final list = (resp['voices'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(VoiceEntry.fromJson)
        .toList();
  }
}

/// _galleryItemToTask: gallery items 字段名略不同 (creator_id 而非 user_id).
/// 把它对齐成 CreationTask 让 UI 复用同一渲染.
CreationTask _galleryItemToTask(Map<String, dynamic> j) {
  final mapped = {
    'id': j['task_id'],
    'user_id': j['creator_id'],
    'type': j['type'],
    'model_code': j['model_code'],
    'prompt': j['prompt'],
    'status': 'completed',
    'progress': 100,
    'is_public': true,
    'outputs': j['outputs'],
    'created_at': j['created_at'],
    'completed_at': j['created_at'],
  };
  return CreationTask.fromJson(mapped);
}
