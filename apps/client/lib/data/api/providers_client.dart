// ProvidersClient — REST bindings for /v1/providers.
//
// Backend impl: services/brain/internal/chat/providers/.
//
// Two ways the client gets the api key:
//   * GET /v1/providers           → key field is masked ("sk-a••••2345")
//                                    Used by the settings UI for display.
//   * GET /v1/providers/{id}?reveal=1 → key field is plaintext.
//                                       Used by direct-mode chat dispatch
//                                       to actually call Anthropic.
//
// Stdlib HttpClient (matches chat_client / wiki_client / memory_client).

import 'dart:async';

import '_http_helpers.dart';
import 'identity_client.dart' show IdentityApiError;

class ProviderConfig {
  final String id;            // server-issued uuid
  final String providerId;    // 'anthropic' | 'openai' | custom slug
  final String displayName;
  final String? baseUrl;
  final bool enabled;
  /// Either masked ("sk-a••••2345") or plaintext (when fetched with reveal=1).
  /// Empty string when no key is set.
  final String apiKey;
  final bool hasApiKey;
  final String source;        // 'builtin' | 'custom'
  final Map<String, dynamic> config;
  final int sortOrder;
  final DateTime createdAt;
  final DateTime updatedAt;

  const ProviderConfig({
    required this.id,
    required this.providerId,
    required this.displayName,
    this.baseUrl,
    required this.enabled,
    required this.apiKey,
    required this.hasApiKey,
    required this.source,
    required this.config,
    required this.sortOrder,
    required this.createdAt,
    required this.updatedAt,
  });

  factory ProviderConfig.fromJson(Map<String, dynamic> j) => ProviderConfig(
        id: j['id'] as String,
        providerId: j['provider_id'] as String,
        displayName: j['display_name'] as String? ?? '',
        baseUrl: j['base_url'] as String?,
        enabled: j['enabled'] as bool? ?? true,
        apiKey: j['api_key'] as String? ?? '',
        hasApiKey: j['has_api_key'] as bool? ?? false,
        source: j['source'] as String? ?? 'builtin',
        config: ((j['config'] as Map?) ?? const {})
            .cast<String, dynamic>(),
        sortOrder: (j['sort_order'] as num?)?.toInt() ?? 0,
        createdAt:
            DateTime.tryParse(j['created_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
        updatedAt:
            DateTime.tryParse(j['updated_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
      );
  bool get isCustom => source == 'custom';
  bool get isOfficial => source == 'official';
}

class ChatModel {
  final String id;
  final String providerId;
  final String modelId;
  final String displayName;
  final String type;          // 'chat' | 'image' | 'video' | 'embedding' | 'stt' | 'tts'
  final Map<String, bool> abilities;
  final int? contextWindow;
  final Map<String, dynamic>? pricing;
  final DateTime? releasedAt;
  final bool enabled;
  final int sortOrder;
  final String source;        // 'builtin' | 'remote' | 'custom'
  final DateTime updatedAt;

  const ChatModel({
    required this.id,
    required this.providerId,
    required this.modelId,
    required this.displayName,
    required this.type,
    required this.abilities,
    this.contextWindow,
    this.pricing,
    this.releasedAt,
    required this.enabled,
    required this.sortOrder,
    required this.source,
    required this.updatedAt,
  });

  factory ChatModel.fromJson(Map<String, dynamic> j) => ChatModel(
        id: j['id'] as String,
        providerId: j['provider_id'] as String,
        modelId: j['model_id'] as String,
        displayName: j['display_name'] as String? ?? '',
        type: j['type'] as String? ?? 'chat',
        abilities: ((j['abilities'] as Map?) ?? const {})
            .map((k, v) => MapEntry(k as String, v as bool)),
        contextWindow: (j['context_window'] as num?)?.toInt(),
        pricing: (j['pricing'] as Map?)?.cast<String, dynamic>(),
        releasedAt: DateTime.tryParse(j['released_at'] as String? ?? ''),
        enabled: j['enabled'] as bool? ?? true,
        sortOrder: (j['sort_order'] as num?)?.toInt() ?? 0,
        source: j['source'] as String? ?? 'builtin',
        updatedAt:
            DateTime.tryParse(j['updated_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
      );

  // ── Display helpers ──
  bool get hasVision => abilities['vision'] == true;
  bool get hasAudio => abilities['audio'] == true;
  bool get hasFunctions => abilities['functions'] == true;
  bool get hasReasoning => abilities['reasoning'] == true;

  /// Pricing chip text like "$5/M". Null when pricing not set.
  String? get inputPriceLabel {
    final p = pricing?['input_per_m_usd'];
    if (p is num && p > 0) return '\$${p.toString()}/M';
    return null;
  }

  String? get outputPriceLabel {
    final p = pricing?['output_per_m_usd'];
    if (p is num && p > 0) return '\$${p.toString()}/M';
    return null;
  }
}

class ProvidersClient {
  ProvidersClient(this.baseUrl, this.bearerToken);
  final Uri baseUrl;
  final String bearerToken;

  Future<List<ProviderConfig>> list() async {
    final raw = await _request('GET', '/v1/providers');
    return (raw['providers'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ProviderConfig.fromJson)
        .toList();
  }

  Future<ProviderConfig> create({
    required String providerId,
    String displayName = '',
    String? baseUrl,
    bool enabled = true,
    String? apiKey,
    String source = 'builtin',
    Map<String, dynamic>? config,
  }) async {
    final body = <String, dynamic>{
      'provider_id': providerId,
      'display_name': displayName,
      'enabled': enabled,
      'source': source,
    };
    if (baseUrl != null) body['base_url'] = baseUrl;
    if (apiKey != null) body['api_key'] = apiKey;
    if (config != null) body['config'] = config;
    final raw = await _request('POST', '/v1/providers', body: body);
    return ProviderConfig.fromJson(raw);
  }

  /// Fetch a single provider. [reveal] toggles plaintext api_key in
  /// the response — call with reveal=true only when you actually need
  /// the key for an LLM dispatch, never for UI rendering.
  Future<ProviderConfig> get(String id, {bool reveal = false}) async {
    final qp = reveal ? const {'reveal': '1'} : null;
    final raw = await _request('GET', '/v1/providers/$id', queryParams: qp);
    return ProviderConfig.fromJson(raw);
  }

  Future<ProviderConfig> patch(
    String id, {
    String? displayName,
    String? baseUrl,
    bool? enabled,
    String? apiKey,
    Map<String, dynamic>? config,
  }) async {
    final body = <String, dynamic>{};
    if (displayName != null) body['display_name'] = displayName;
    if (baseUrl != null) body['base_url'] = baseUrl;
    if (enabled != null) body['enabled'] = enabled;
    if (apiKey != null) body['api_key'] = apiKey;
    if (config != null) body['config'] = config;
    final raw = await _request('PATCH', '/v1/providers/$id', body: body);
    return ProviderConfig.fromJson(raw);
  }

  Future<void> delete(String id) async {
    await _request('DELETE', '/v1/providers/$id', expectNoBody: true);
  }

  // ── Models ──

  Future<List<ChatModel>> listModels(String providerId, {String? type}) async {
    final qp = type == null ? null : {'type': type};
    final raw = await _request('GET', '/v1/providers/$providerId/models',
        queryParams: qp);
    return (raw['models'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ChatModel.fromJson)
        .toList();
  }

  Future<int> refreshModels(String providerId) async {
    final raw = await _request(
        'POST', '/v1/providers/$providerId/models/refresh', body: const {});
    return (raw['refreshed'] as num?)?.toInt() ?? 0;
  }

  Future<ChatModel> patchModel(
    String providerId,
    String modelId, {
    bool? enabled,
    int? sortOrder,
  }) async {
    final body = <String, dynamic>{};
    if (enabled != null) body['enabled'] = enabled;
    if (sortOrder != null) body['sort_order'] = sortOrder;
    final raw = await _request(
        'PATCH', '/v1/providers/$providerId/models/$modelId',
        body: body);
    return ChatModel.fromJson(raw);
  }

  Future<void> deleteModel(String providerId, String modelId) async {
    await _request('DELETE', '/v1/providers/$providerId/models/$modelId',
        expectNoBody: true);
  }

  // ─── HTTP plumbing ────────────────────────────────────

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, String>? queryParams,
    Map<String, dynamic>? body,
    bool expectNoBody = false,
  }) async {
    try {
      return await apiRequest(
        method: method,
        url: baseUrl.replace(path: path, queryParameters: queryParams),
        bearerToken: bearerToken,
        body: body,
        expectNoBody: expectNoBody,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }
}
