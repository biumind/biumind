// ApiKeysClient — BYOK 公开 endpoint 客户端.
//
//   GET    /v1/identity/me/api-keys
//   PUT    /v1/identity/me/api-keys/{provider}
//   DELETE /v1/identity/me/api-keys/{provider}
//   POST   /v1/identity/me/api-keys/{provider}/test
//
// 与 services/identity/internal/api/api_keys.go 契约对齐.

import '../../../data/api/_http_helpers.dart';

/// 12 个支持的 provider — 前 11 个与 identity migrations/00020 CHECK 对齐,
/// 第 12 个 custom 来自 00033 (用户自填代理 new-api/one-api/vLLM).
const supportedByokProviders = <String>[
  'anthropic',
  'openai',
  'deepseek',
  'doubao',
  'dashscope',
  'volcengine',
  'google',
  'azure_openai',
  'moonshot',
  'qwen',
  'baichuan',
  'custom', // 00033: 用户自填代理 (new-api/one-api/vLLM)
];

/// 显示文案 (UI 友好名).
const byokProviderLabels = <String, String>{
  'anthropic': 'Anthropic Claude',
  'openai': 'OpenAI GPT',
  'deepseek': 'DeepSeek',
  'doubao': '字节跳动豆包',
  'dashscope': '阿里灵积 DashScope',
  'volcengine': '火山方舟 (Doubao 视觉)',
  'google': 'Google Gemini',
  'azure_openai': 'Azure OpenAI',
  'moonshot': 'Kimi (Moonshot)',
  'qwen': '阿里通义千问',
  'baichuan': '百川',
  'custom': '自定义 (OpenAI 兼容 / Anthropic / Google)',
};

/// 健康状态.
enum ApiKeyStatus { valid, invalid, revoked, expired }

ApiKeyStatus _parseStatus(String? s) {
  switch (s) {
    case 'valid':
      return ApiKeyStatus.valid;
    case 'invalid':
      return ApiKeyStatus.invalid;
    case 'revoked':
      return ApiKeyStatus.revoked;
    case 'expired':
      return ApiKeyStatus.expired;
    default:
      return ApiKeyStatus.invalid;
  }
}

class ApiKeyEntry {
  final String id;
  final String provider;
  final String label;
  final String last4;
  final ApiKeyStatus status;
  final int failureCount;
  final DateTime? lastValidatedAt;
  final DateTime? lastUsedAt;
  final DateTime createdAt;
  // 00033: custom/代理 endpoint + 协议 (标准 provider 为空).
  final String baseUrl;
  final String protocol;
  // 00034: custom 声明所用模型 (model-relay 按 model 匹配 custom BYOK).
  final List<String> modelGlobs;
  // 00035: client-side BYOK. true = 需本机出口 (relay 连不到的上游, 如内网
  // proxy); key 加密存 identity, 桌面 daemon 取 key 本机直连. 此标记驱动
  // 聊天分流走桌面 daemon.
  final bool isClientSide;

  const ApiKeyEntry({
    required this.id,
    required this.provider,
    required this.label,
    required this.last4,
    required this.status,
    required this.failureCount,
    this.lastValidatedAt,
    this.lastUsedAt,
    required this.createdAt,
    this.baseUrl = '',
    this.protocol = '',
    this.modelGlobs = const [],
    this.isClientSide = false,
  });

  factory ApiKeyEntry.fromJson(Map<String, dynamic> j) => ApiKeyEntry(
    id: (j['id'] as String?) ?? '',
    provider: (j['provider'] as String?) ?? '',
    label: (j['label'] as String?) ?? '',
    last4: (j['last4'] as String?) ?? '',
    status: _parseStatus(j['status'] as String?),
    failureCount: (j['failure_count'] as num?)?.toInt() ?? 0,
    lastValidatedAt: _parseTime(j['last_validated_at']),
    lastUsedAt: _parseTime(j['last_used_at']),
    createdAt: _parseTime(j['created_at']) ?? DateTime.now(),
    baseUrl: (j['base_url'] as String?) ?? '',
    protocol: (j['protocol'] as String?) ?? '',
    modelGlobs:
        (j['model_globs'] as List?)?.map((e) => e.toString()).toList() ??
        const [],
    isClientSide: (j['is_client_side'] as bool?) ?? false,
  );
}

DateTime? _parseTime(Object? raw) {
  if (raw is String && raw.isNotEmpty) return DateTime.tryParse(raw);
  return null;
}

class TestResult {
  final String result; // valid / invalid / network / unknown
  const TestResult(this.result);
  bool get isValid => result == 'valid';
  bool get isInvalid => result == 'invalid';
}

class ApiKeysClient {
  final Uri baseUrl; // identity :7004
  final String? Function() bearerProvider;

  ApiKeysClient({required this.baseUrl, required this.bearerProvider});

  Uri _u(String path) {
    final base = baseUrl.toString().replaceAll(RegExp(r'/+$'), '');
    return Uri.parse('$base$path');
  }

  Future<List<ApiKeyEntry>> list() async {
    final resp = await apiRequest(
      method: 'GET',
      url: _u('/v1/identity/me/api-keys'),
      bearerToken: bearerProvider(),
    );
    final items = (resp['items'] as List?) ?? const [];
    return items
        .whereType<Map<String, dynamic>>()
        .map(ApiKeyEntry.fromJson)
        .toList();
  }

  Future<ApiKeyEntry> upsert({
    required String provider,
    required String apiKey,
    String? label,
    Map<String, dynamic>? config,
    String? baseUrl,
    String? protocol,
    List<String>? modelGlobs,
    bool isClientSide = false,
  }) async {
    final resp = await apiRequest(
      method: 'PUT',
      url: _u('/v1/identity/me/api-keys/$provider'),
      bearerToken: bearerProvider(),
      body: {
        // apiKey 空 = 编辑不改 key (identity 保留原加密值); 非空 = 新建/覆盖.
        if (apiKey.isNotEmpty) 'api_key': apiKey,
        if (isClientSide) 'is_client_side': true,
        if (label != null && label.isNotEmpty) 'label': label,
        if (config != null && config.isNotEmpty) 'config': config,
        if (baseUrl != null && baseUrl.isNotEmpty) 'base_url': baseUrl,
        if (protocol != null && protocol.isNotEmpty) 'protocol': protocol,
        if (modelGlobs != null && modelGlobs.isNotEmpty)
          'model_globs': modelGlobs,
      },
    );
    return ApiKeyEntry.fromJson(resp);
  }

  /// 取 client-side 凭据明文 (仅 is_client_side=true 行, owner-scoped). 供端侧
  /// _test 临时取 key 测连通 (测完即弃, 不落 keychain). 返 {api_key, base_url,
  /// protocol, ...}.
  Future<Map<String, dynamic>> fetchCredentials(String id) async {
    return apiRequest(
      method: 'GET',
      url: _u('/v1/identity/me/api-keys/$id/credentials'),
      bearerToken: bearerProvider(),
    );
  }

  Future<void> remove(
    String provider, {
    bool isClientSide = false,
    String? id,
  }) async {
    // id 非空 = 精确删单条 (custom client-side 多 base_url 场景, 同 provider
    // 多行须按 record id 删); 缺省 = 批删该 provider 该模式全部 (向后兼容).
    final qs = <String>[
      if (isClientSide) 'client_side=true',
      if (id != null && id.isNotEmpty) 'id=${Uri.encodeQueryComponent(id)}',
    ].join('&');
    final q = qs.isEmpty ? '' : '?$qs';
    await apiRequest(
      method: 'DELETE',
      url: _u('/v1/identity/me/api-keys/$provider$q'),
      bearerToken: bearerProvider(),
      expectNoBody: true,
    );
  }

  Future<TestResult> test(String provider) async {
    final resp = await apiRequest(
      method: 'POST',
      url: _u('/v1/identity/me/api-keys/$provider/test'),
      bearerToken: bearerProvider(),
    );
    return TestResult((resp['result'] as String?) ?? 'unknown');
  }
}
