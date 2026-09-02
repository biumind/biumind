// ResearchClient — talks to brain's deep-research endpoints.
//
//   POST /v1/wiki/projects/{projectId}/research        kick off
//   GET  /v1/wiki/projects/{projectId}/research        list
//   GET  /v1/wiki/projects/{projectId}/research/{id}   poll one task

import '_http_helpers.dart';

class ResearchTask {
  final String id;
  final String projectId;
  final String topic;
  final List<String> queries;
  final String status; // queued|searching|synthesizing|saving|done|error
  final String? pageId;
  final String synthesis;
  final String? error;
  final List<ResearchHit> webResults;
  /// 来源审阅项 id（review → research 入口创建的任务才有）。研究落页后
  /// 服务端会自动 resolve 该审阅项。
  final String? sourceReviewId;
  final DateTime createdAt;
  final DateTime updatedAt;

  const ResearchTask({
    required this.id,
    required this.projectId,
    required this.topic,
    required this.queries,
    required this.status,
    required this.pageId,
    required this.synthesis,
    required this.error,
    required this.webResults,
    required this.createdAt,
    required this.updatedAt,
    this.sourceReviewId,
  });

  bool get isRunning => switch (status) {
        'queued' || 'searching' || 'synthesizing' || 'saving' => true,
        _ => false,
      };

  bool get isDone => status == 'done';
  bool get isError => status == 'error';

  factory ResearchTask.fromJson(Map<String, dynamic> j) => ResearchTask(
        id: j['id'] as String,
        projectId: j['project_id'] as String,
        topic: (j['topic'] as String?) ?? '',
        queries:
            ((j['queries'] as List?) ?? const []).whereType<String>().toList(),
        status: (j['status'] as String?) ?? 'queued',
        pageId: j['page_id'] as String?,
        synthesis: (j['synthesis'] as String?) ?? '',
        error: (j['error'] as String?)?.isNotEmpty == true
            ? j['error'] as String
            : null,
        webResults: ((j['web_results'] as List?) ?? const [])
            .cast<Map<String, dynamic>>()
            .map(ResearchHit.fromJson)
            .toList(),
        sourceReviewId: (j['source_review_id'] as String?)?.isNotEmpty == true
            ? j['source_review_id'] as String
            : null,
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '') ??
            DateTime.now(),
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '') ??
            DateTime.now(),
      );
}

class ResearchHit {
  final String title;
  final String url;
  final String snippet;
  final String source;

  const ResearchHit({
    required this.title,
    required this.url,
    required this.snippet,
    required this.source,
  });

  factory ResearchHit.fromJson(Map<String, dynamic> j) => ResearchHit(
        title: (j['title'] as String?) ?? '',
        url: (j['url'] as String?) ?? '',
        snippet: (j['snippet'] as String?) ?? '',
        source: (j['source'] as String?) ?? '',
      );
}

class ResearchClient {
  ResearchClient(this.baseUrl, this.bearerToken);

  final Uri baseUrl;
  final String bearerToken;

  Future<ResearchTask> startTask(
    String projectId, {
    required String topic,
    List<String> queries = const [],
    String? sourceReviewId,
  }) async {
    final raw = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(path: '/v1/wiki/projects/$projectId/research'),
      bearerToken: bearerToken,
      body: {
        'topic': topic,
        if (queries.isNotEmpty) 'queries': queries,
        if (sourceReviewId != null && sourceReviewId.isNotEmpty)
          'source_review_id': sourceReviewId,
      },
    );
    return ResearchTask.fromJson(raw);
  }

  Future<List<ResearchTask>> listTasks(String projectId) async {
    final raw = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(path: '/v1/wiki/projects/$projectId/research'),
      bearerToken: bearerToken,
    );
    return ((raw['tasks'] as List?) ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ResearchTask.fromJson)
        .toList();
  }

  Future<ResearchTask> getTask(String projectId, String taskId) async {
    final raw = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(
          path: '/v1/wiki/projects/$projectId/research/$taskId'),
      bearerToken: bearerToken,
    );
    return ResearchTask.fromJson(raw);
  }
}
