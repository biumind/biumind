// StatsClient — 数据统计页的后端读取层。
//
// 两个账户级数据源 (跨设备):
//   - brain   GET /v1/chat/stats        概览: 话题/消息/模型 + 月环比、活跃
//             热力图、streak、模型榜、话题榜 (chat 结构统计)
//   - relay   GET /v1/me/usage?mo=&page= 用量: 今日/本月积分、按天序列、
//             per-model、逐调用明细、全时累计 Token (积分/token 计费视角)
//
// 契约对齐 services/brain/internal/chat/stats.go + api.go (handleStats) 与
// services/model-relay/internal/api/usage.go。

import '../../../data/api/_http_helpers.dart';

// ─── 概览 (brain) ────────────────────────────────────────

/// 当前总数 + 上月底累计 (算月环比 %).
class StatCount {
  final int count;
  final int prev;
  const StatCount(this.count, this.prev);
  factory StatCount.fromJson(Map<String, dynamic> j) =>
      StatCount((j['count'] as num?)?.toInt() ?? 0, (j['prev'] as num?)?.toInt() ?? 0);

  /// 月环比百分比 (null = 无可比基数, 即上月为 0).
  double? get momPercent {
    if (prev <= 0) return null;
    return (count - prev) / prev * 100.0;
  }
}

class HeatmapDay {
  final String date; // YYYY-MM-DD (UTC)
  final int count;
  const HeatmapDay(this.date, this.count);
  factory HeatmapDay.fromJson(Map<String, dynamic> j) =>
      HeatmapDay((j['date'] as String?) ?? '', (j['count'] as num?)?.toInt() ?? 0);
}

class ModelRankItem {
  final String model;
  final int count;
  const ModelRankItem(this.model, this.count);
  factory ModelRankItem.fromJson(Map<String, dynamic> j) =>
      ModelRankItem((j['model'] as String?) ?? '', (j['count'] as num?)?.toInt() ?? 0);
}

class TopicRankItem {
  final String threadId;
  final String title;
  final int count;
  const TopicRankItem(this.threadId, this.title, this.count);
  factory TopicRankItem.fromJson(Map<String, dynamic> j) => TopicRankItem(
        (j['thread_id'] as String?) ?? '',
        (j['title'] as String?) ?? '',
        (j['count'] as num?)?.toInt() ?? 0,
      );
}

class ChatStats {
  final StatCount threads;
  final StatCount messages;
  final StatCount models;
  final List<HeatmapDay> heatmap;
  final List<ModelRankItem> modelRank;
  final List<TopicRankItem> topicRank;
  final int activeDays;
  final int currentStreak;
  final int maxStreak;

  const ChatStats({
    required this.threads,
    required this.messages,
    required this.models,
    required this.heatmap,
    required this.modelRank,
    required this.topicRank,
    required this.activeDays,
    required this.currentStreak,
    required this.maxStreak,
  });

  factory ChatStats.fromJson(Map<String, dynamic> j) {
    final ov = (j['overview'] as Map<String, dynamic>?) ?? const {};
    StatCount c(String k) =>
        StatCount.fromJson((ov[k] as Map<String, dynamic>?) ?? const {});
    List<T> list<T>(String k, T Function(Map<String, dynamic>) f) =>
        ((j[k] as List?) ?? const [])
            .whereType<Map<String, dynamic>>()
            .map(f)
            .toList(growable: false);
    return ChatStats(
      threads: c('threads'),
      messages: c('messages'),
      models: c('models'),
      heatmap: list('heatmap', HeatmapDay.fromJson),
      modelRank: list('model_rank', ModelRankItem.fromJson),
      topicRank: list('topic_rank', TopicRankItem.fromJson),
      activeDays: (j['active_days'] as num?)?.toInt() ?? 0,
      currentStreak: (j['current_streak'] as num?)?.toInt() ?? 0,
      maxStreak: (j['max_streak'] as num?)?.toInt() ?? 0,
    );
  }
}

// ─── 用量 (model-relay) ──────────────────────────────────

class UsageSummary {
  final int todayCredits;
  final int monthCredits;
  final int monthRequests;
  final int activeModels;
  final int totalTokens;
  final int totalTokensPrev;
  const UsageSummary({
    required this.todayCredits,
    required this.monthCredits,
    required this.monthRequests,
    required this.activeModels,
    required this.totalTokens,
    required this.totalTokensPrev,
  });
  factory UsageSummary.fromJson(Map<String, dynamic> j) => UsageSummary(
        todayCredits: (j['today_credits'] as num?)?.toInt() ?? 0,
        monthCredits: (j['month_credits'] as num?)?.toInt() ?? 0,
        monthRequests: (j['month_requests'] as num?)?.toInt() ?? 0,
        activeModels: (j['active_models'] as num?)?.toInt() ?? 0,
        totalTokens: (j['total_tokens'] as num?)?.toInt() ?? 0,
        totalTokensPrev: (j['total_tokens_prev'] as num?)?.toInt() ?? 0,
      );

  double? get tokenMomPercent {
    if (totalTokensPrev <= 0) return null;
    return (totalTokens - totalTokensPrev) / totalTokensPrev * 100.0;
  }
}

class UsageDailyBucket {
  final String date;
  final int credits;
  final int tokens;
  final int requests;
  const UsageDailyBucket(this.date, this.credits, this.tokens, this.requests);
  factory UsageDailyBucket.fromJson(Map<String, dynamic> j) => UsageDailyBucket(
        (j['date'] as String?) ?? '',
        (j['credits'] as num?)?.toInt() ?? 0,
        (j['tokens'] as num?)?.toInt() ?? 0,
        (j['requests'] as num?)?.toInt() ?? 0,
      );
}

class UsageModelBucket {
  final String model;
  final int requests;
  final int inputTokens;
  final int outputTokens;
  final int credits;
  const UsageModelBucket(
      this.model, this.requests, this.inputTokens, this.outputTokens, this.credits);
  factory UsageModelBucket.fromJson(Map<String, dynamic> j) => UsageModelBucket(
        (j['model'] as String?) ?? '',
        (j['requests'] as num?)?.toInt() ?? 0,
        (j['input_tokens'] as num?)?.toInt() ?? 0,
        (j['output_tokens'] as num?)?.toInt() ?? 0,
        (j['credits'] as num?)?.toInt() ?? 0,
      );
}

class UsageCall {
  final String model;
  final int inputTokens;
  final int outputTokens;
  final double tps;
  final int latencyMs;
  final int credits;
  final String status;
  final DateTime createdAt;
  const UsageCall({
    required this.model,
    required this.inputTokens,
    required this.outputTokens,
    required this.tps,
    required this.latencyMs,
    required this.credits,
    required this.status,
    required this.createdAt,
  });
  factory UsageCall.fromJson(Map<String, dynamic> j) => UsageCall(
        model: (j['model'] as String?) ?? '',
        inputTokens: (j['input_tokens'] as num?)?.toInt() ?? 0,
        outputTokens: (j['output_tokens'] as num?)?.toInt() ?? 0,
        tps: (j['tps'] as num?)?.toDouble() ?? 0,
        latencyMs: (j['latency_ms'] as num?)?.toInt() ?? 0,
        credits: (j['credits'] as num?)?.toInt() ?? 0,
        status: (j['status'] as String?) ?? '',
        createdAt: DateTime.tryParse((j['created_at'] as String?) ?? '')?.toLocal() ??
            DateTime.now(),
      );
}

class UsageReport {
  final String month; // YYYY-MM
  final UsageSummary summary;
  final List<UsageDailyBucket> daily;
  final List<UsageModelBucket> byModel;
  final List<UsageCall> calls;
  final int page;
  final int pageSize;
  final int total;

  const UsageReport({
    required this.month,
    required this.summary,
    required this.daily,
    required this.byModel,
    required this.calls,
    required this.page,
    required this.pageSize,
    required this.total,
  });

  factory UsageReport.fromJson(Map<String, dynamic> j) {
    List<T> list<T>(String k, T Function(Map<String, dynamic>) f) =>
        ((j[k] as List?) ?? const [])
            .whereType<Map<String, dynamic>>()
            .map(f)
            .toList(growable: false);
    return UsageReport(
      month: (j['month'] as String?) ?? '',
      summary: UsageSummary.fromJson((j['summary'] as Map<String, dynamic>?) ?? const {}),
      daily: list('daily', UsageDailyBucket.fromJson),
      byModel: list('by_model', UsageModelBucket.fromJson),
      calls: list('calls', UsageCall.fromJson),
      page: (j['page'] as num?)?.toInt() ?? 1,
      pageSize: (j['page_size'] as num?)?.toInt() ?? 20,
      total: (j['total'] as num?)?.toInt() ?? 0,
    );
  }
}

// ─── client ──────────────────────────────────────────────

class StatsClient {
  final Uri brainBaseUrl; // brain :7003
  final Uri relayBaseUrl; // model-relay :7001
  final String? Function() bearerProvider;

  StatsClient({
    required this.brainBaseUrl,
    required this.relayBaseUrl,
    required this.bearerProvider,
  });

  static String _strip(Uri u) => u.toString().replaceAll(RegExp(r'/+$'), '');

  Future<ChatStats> fetchChatStats() async {
    final url = Uri.parse('${_strip(brainBaseUrl)}/v1/chat/stats');
    final resp = await apiRequest(
        method: 'GET', url: url, bearerToken: bearerProvider());
    return ChatStats.fromJson(resp);
  }

  Future<UsageReport> fetchUsage({String? mo, int page = 1, int pageSize = 10}) async {
    final base = _strip(relayBaseUrl);
    final qp = <String, String>{
      'page': '$page',
      'page_size': '$pageSize',
      if (mo != null && mo.isNotEmpty) 'mo': mo,
    };
    final url = Uri.parse('$base/v1/me/usage').replace(queryParameters: qp);
    final resp = await apiRequest(
        method: 'GET', url: url, bearerToken: bearerProvider());
    return UsageReport.fromJson(resp);
  }
}
