// RelayCatalogClient — GET /v1/me/models (model-relay 公开 catalog).
//
// P6: client picker 官方模型直读 model-relay (跳 brain 一跳)。返 markup 后
// 实际计费价 + min_plan + max_output, 不含 markup_ratio/min_charge 等。
// 后端: services/model-relay/internal/api/publicmodels.go。
// site nginx: /v1/me/models → model-relay:7001。
//
// 与 providers_client (brain per-user BYOK) 区别: 此 client 读 global 平台
// catalog, 不带 user 维度, 普通用户 JWT 即可 (无需 admin perm)。

import '_http_helpers.dart';
import 'identity_client.dart' show IdentityApiError;

class RelayPricing {
  final String currency; // 'USD' | 'CNY'
  final double inputPerMTok; // markup 后实际计费单价 (per_mtok, 原币种)
  final double outputPerMTok;
  const RelayPricing({
    required this.currency,
    required this.inputPerMTok,
    required this.outputPerMTok,
  });
  factory RelayPricing.fromJson(Map<String, dynamic> j) => RelayPricing(
        currency: j['currency'] as String? ?? 'USD',
        inputPerMTok: (j['input_per_mtok'] as num?)?.toDouble() ?? 0,
        outputPerMTok: (j['output_per_mtok'] as num?)?.toDouble() ?? 0,
      );
}

class RelayCatalogModel {
  final String code; // wire model id
  final String displayName;
  final String family; // 'claude' / 'gpt' / ... (picker 副标)
  final int? contextWindow;
  final String mode; // chat / embedding / audio_speech / image_generation / ...
  final String? minPlan; // pro / team; null = free (所有人可用)
  final int? maxOutput;
  final RelayPricing? pricing;
  const RelayCatalogModel({
    required this.code,
    required this.displayName,
    required this.family,
    this.contextWindow,
    required this.mode,
    this.minPlan,
    this.maxOutput,
    this.pricing,
  });
  factory RelayCatalogModel.fromJson(Map<String, dynamic> j) {
    final mp = j['min_plan'] as String?;
    final pj = j['pricing'] as Map?;
    return RelayCatalogModel(
      code: j['code'] as String,
      displayName: j['display_name'] as String? ?? '',
      family: j['family'] as String? ?? '',
      contextWindow: (j['context_window'] as num?)?.toInt(),
      mode: j['mode'] as String? ?? 'chat',
      minPlan: (mp == null || mp.isEmpty) ? null : mp,
      maxOutput: (j['max_output'] as num?)?.toInt(),
      pricing: pj == null ? null : RelayPricing.fromJson(pj.cast<String, dynamic>()),
    );
  }

  /// Pricing chip "$X/M" (markup 后实际计费单价; 原币种非 USD 时附 currency).
  String? get inputPriceLabel {
    final p = pricing;
    if (p == null || p.inputPerMTok <= 0) return null;
    final sym = p.currency == 'CNY' ? '¥' : '\$';
    return '$sym${_fmt(p.inputPerMTok)}/M';
  }
}

String _fmt(double v) =>
    v == v.roundToDouble() ? v.toStringAsFixed(0) : v.toStringAsFixed(2);

class RelayCatalogClient {
  RelayCatalogClient(this.baseUrl, this.bearerToken);
  final Uri baseUrl;
  final String bearerToken;

  /// GET /v1/me/models?status=active — 全平台 official catalog (global)。
  Future<List<RelayCatalogModel>> list({String status = 'active'}) async {
    final raw = await _request('GET', '/v1/me/models',
        queryParams: {'status': status});
    return (raw['items'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(RelayCatalogModel.fromJson)
        .toList();
  }

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, String>? queryParams,
  }) async {
    try {
      return await apiRequest(
        method: method,
        url: baseUrl.replace(path: path, queryParameters: queryParams),
        bearerToken: bearerToken,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }
}
