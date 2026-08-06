// BillingEstimateClient — 调 model-relay POST /v1/chat/estimate.
//
// composer 在发送前调一次, 在 send 按钮旁显示「约 N-M 积分」chip 或
// 「0 积分 (BYOK)」. 不阻塞输入, 失败静默 (chip 隐藏).
//
// 与 services/model-relay/internal/api/estimate.go 契约对齐.

import '../../../data/api/_http_helpers.dart';

class ChatEstimate {
  final String provider;
  final String model;
  final bool byokActive;
  final int minCredits;
  final int maxCredits;
  final String warning; // e.g. "pricing not found"

  const ChatEstimate({
    required this.provider,
    required this.model,
    required this.byokActive,
    required this.minCredits,
    required this.maxCredits,
    this.warning = '',
  });

  factory ChatEstimate.fromJson(Map<String, dynamic> j) => ChatEstimate(
        provider: (j['provider'] as String?) ?? '',
        model: (j['model'] as String?) ?? '',
        byokActive: j['byok_active'] == true,
        minCredits: (j['min_credits'] as num?)?.toInt() ?? 0,
        maxCredits: (j['max_credits'] as num?)?.toInt() ?? 0,
        warning: (j['warning'] as String?) ?? '',
      );

  /// 显示文本: 走 BYOK 时高亮 BYOK; 否则约 N-M 积分.
  String displayLabel() {
    if (byokActive) return '0 积分 · BYOK';
    if (warning.isNotEmpty || maxCredits == 0) return ''; // 隐藏 chip
    if (minCredits == maxCredits) return '约 $minCredits 积分';
    return '约 $minCredits-$maxCredits 积分';
  }
}

class BillingEstimateClient {
  final Uri baseUrl; // model-relay :7001
  final String? Function() bearerProvider;

  BillingEstimateClient({required this.baseUrl, required this.bearerProvider});

  Future<ChatEstimate> estimate({
    required String model,
    required List<Map<String, dynamic>> messages,
    int maxTokens = 4096,
  }) async {
    final base = baseUrl.toString().replaceAll(RegExp(r'/+$'), '');
    final url = Uri.parse('$base/v1/chat/estimate');
    final tok = bearerProvider();
    final resp = await apiRequest(
      method: 'POST',
      url: url,
      bearerToken: tok,
      body: {
        'model': model,
        'messages': messages,
        'max_tokens': maxTokens,
      },
    );
    return ChatEstimate.fromJson(resp);
  }
}
