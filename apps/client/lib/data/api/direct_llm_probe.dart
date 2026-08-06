// direct_llm_probe.dart — 联通性探测(从客户端本机直连上游,用选中模型实际调用)。
//
// check 的语义:用用户在 UI 选中的那个模型,真发一次最小生成调用(max_tokens=1),
// 验 ① 上游可达 ② key 有效 ③ 选中模型真能生成。这样用户上游后台(如 new-api)
// 必留生成记录(可观测),且绕过 model-relay 的 adaptor 前缀猜测 —— 后者对 new-api
// 的 glm-4.5/bge-m3 等非标准模型名会 fallback 平台池,check 请求到不了用户上游。
//
// 两种探测:
//   * generateProbe(主,_check 用):选中模型发 1-token 生成。三 shape:
//       - openai / custom(OpenAI 兼容):POST {base}/v1/chat/completions
//       - anthropic:                    POST {base}/v1/messages
//       - google:                       POST {base}/v1beta/models/{m}:generateContent
//   * directProbeModels(辅,保留):GET /models 验模型在列表。当前 _check 不用。
//
// key 用完即弃,不落盘(C3)。base 不含 /v1 自动补(与后端 refresh.go 同款容错)。

import 'dart:convert';

import 'package:http/http.dart' as http;

class DirectProbeResult {
  final bool ok;
  final int? latencyMs;
  final int? modelCount;
  /// directProbeModels 专用:选中模型是否在上游 /models 列表。generateProbe 不用。
  final bool? modelIdExists;
  final String? errMsg;

  const DirectProbeResult({
    required this.ok,
    this.latencyMs,
    this.modelCount,
    this.modelIdExists,
    this.errMsg,
  });
}

const _httpTimeout = Duration(seconds: 15);

/// 用选中模型发 1-token 生成请求(_check 主用)。返 ok+latency / fail+errMsg。
Future<DirectProbeResult> generateProbe({
  required String providerId,
  required String apiKey,
  required String model,
  String? baseUrl,
}) async {
  if (apiKey.isEmpty) {
    return const DirectProbeResult(ok: false, errMsg: 'API key 未配置');
  }
  if (model.isEmpty) {
    return const DirectProbeResult(ok: false, errMsg: '未选中模型');
  }
  final pid = providerId.toLowerCase();
  try {
    final sw = Stopwatch()..start();
    switch (pid) {
      case 'anthropic':
        await _genAnthropic(apiKey, baseUrl, model);
        break;
      case 'google':
        await _genGoogle(apiKey, baseUrl, model);
        break;
      default: // openai + custom(OpenAI 兼容)
        await _genOpenAI(apiKey, baseUrl, model);
    }
    sw.stop();
    return DirectProbeResult(ok: true, latencyMs: sw.elapsedMilliseconds);
  } on _UpstreamError catch (e) {
    return DirectProbeResult(ok: false, errMsg: e.message);
  } catch (e) {
    return DirectProbeResult(ok: false, errMsg: '$e');
  }
}

// ─── generate(1-token)三 shape ───────────────────────────

Future<void> _genOpenAI(String key, String? baseUrl, String model) async {
  var base = _base(baseUrl, 'https://api.openai.com/v1');
  if (!base.endsWith('/v1')) base = '$base/v1';
  final resp = await http
      .post(
        Uri.parse('$base/chat/completions'),
        headers: {
          'Authorization': 'Bearer $key',
          'Content-Type': 'application/json',
        },
        body: jsonEncode({
          'model': model,
          'max_tokens': 1,
          'messages': [
            {'role': 'user', 'content': 'ping'}
          ],
        }),
      )
      .timeout(_httpTimeout);
  if (resp.statusCode >= 400) {
    throw _UpstreamError('上游 ${resp.statusCode}: ${_trim(resp.body)}');
  }
}

Future<void> _genAnthropic(String key, String? baseUrl, String model) async {
  final base = _base(baseUrl, 'https://api.anthropic.com');
  final resp = await http
      .post(
        Uri.parse('$base/v1/messages'),
        headers: {
          'x-api-key': key,
          'anthropic-version': '2023-06-01',
          'Content-Type': 'application/json',
        },
        body: jsonEncode({
          'model': model,
          'max_tokens': 1,
          'messages': [
            {'role': 'user', 'content': 'ping'}
          ],
        }),
      )
      .timeout(_httpTimeout);
  if (resp.statusCode >= 400) {
    throw _UpstreamError('上游 ${resp.statusCode}: ${_trim(resp.body)}');
  }
}

Future<void> _genGoogle(String key, String? baseUrl, String model) async {
  final base =
      _base(baseUrl, 'https://generativelanguage.googleapis.com/v1beta');
  final resp = await http
      .post(
        Uri.parse('$base/models/${Uri.encodeComponent(model)}:generateContent'
            '?key=${Uri.encodeComponent(key)}'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({
          'contents': [
            {
              'parts': [
                {'text': 'ping'}
              ]
            }
          ],
          'generationConfig': {'maxOutputTokens': 1},
        }),
      )
      .timeout(_httpTimeout);
  if (resp.statusCode >= 400) {
    throw _UpstreamError('上游 ${resp.statusCode}: ${_trim(resp.body)}');
  }
}

// ─── 辅助:GET /models 验模型在列表(当前 _check 未用,保留)──────────

/// 直连上游 GET /models,返该上游可见的模型 id 清单. 抛 _UpstreamError/
/// 网络异常给 caller 处理. 被 directProbeModels + API Keys 测试路径
/// (通配 globs 解析具体模型) 共用.
Future<List<String>> listUpstreamModels({
  required String providerId,
  required String apiKey,
  String? baseUrl,
}) async {
  if (apiKey.isEmpty) {
    throw _UpstreamError('API key 未配置');
  }
  final pid = providerId.toLowerCase();
  switch (pid) {
    case 'anthropic':
      return _fetchAnthropicList(apiKey, baseUrl);
    case 'google':
      return _fetchGoogleList(apiKey, baseUrl);
    default:
      return _fetchOpenAIList(apiKey, baseUrl);
  }
}

/// 直连上游 GET /models,校验选中 modelId 是否在清单。返 ok+modelCount+
/// modelIdExists。generateProbe 发生成更直接,_check 主用 generateProbe。
Future<DirectProbeResult> directProbeModels({
  required String providerId,
  required String apiKey,
  String? baseUrl,
  String? modelId,
}) async {
  try {
    final sw = Stopwatch()..start();
    final ids = await listUpstreamModels(
        providerId: providerId, apiKey: apiKey, baseUrl: baseUrl);
    sw.stop();
    return DirectProbeResult(
      ok: true,
      latencyMs: sw.elapsedMilliseconds,
      modelCount: ids.length,
      modelIdExists: modelId == null ? null : ids.contains(modelId),
    );
  } on _UpstreamError catch (e) {
    return DirectProbeResult(ok: false, errMsg: e.message);
  } catch (e) {
    return DirectProbeResult(ok: false, errMsg: '$e');
  }
}

class _UpstreamError implements Exception {
  final String message;
  _UpstreamError(this.message);
}

String _base(String? baseUrl, String def) {
  final b =
      (baseUrl == null || baseUrl.trim().isEmpty) ? def : baseUrl.trim();
  return b.endsWith('/') ? b.substring(0, b.length - 1) : b;
}

String _trim(String s) => s.length > 300 ? '${s.substring(0, 300)}…' : s;

Future<List<String>> _fetchOpenAIList(String key, String? baseUrl) async {
  var base = _base(baseUrl, 'https://api.openai.com/v1');
  if (!base.endsWith('/v1')) base = '$base/v1';
  final resp = await http
      .get(Uri.parse('$base/models'), headers: {
        'Authorization': 'Bearer $key',
        'Accept': 'application/json',
      })
      .timeout(_httpTimeout);
  if (resp.statusCode >= 400) {
    throw _UpstreamError('上游 ${resp.statusCode}: ${_trim(resp.body)}');
  }
  final list = jsonDecode(resp.body) as Map<String, dynamic>;
  return ((list['data'] as List? ?? const [])
      .map((e) => ((e as Map)['id'] ?? '').toString())
      .where((s) => s.isNotEmpty)
      .toList());
}

Future<List<String>> _fetchAnthropicList(String key, String? baseUrl) async {
  final base = _base(baseUrl, 'https://api.anthropic.com');
  final resp = await http
      .get(Uri.parse('$base/v1/models?limit=1000'), headers: {
        'x-api-key': key,
        'anthropic-version': '2023-06-01',
        'Accept': 'application/json',
      })
      .timeout(_httpTimeout);
  if (resp.statusCode >= 400) {
    throw _UpstreamError('上游 ${resp.statusCode}: ${_trim(resp.body)}');
  }
  final list = jsonDecode(resp.body) as Map<String, dynamic>;
  return ((list['data'] as List? ?? const [])
      .map((e) => ((e as Map)['id'] ?? '').toString())
      .where((s) => s.isNotEmpty)
      .toList());
}

Future<List<String>> _fetchGoogleList(String key, String? baseUrl) async {
  final base =
      _base(baseUrl, 'https://generativelanguage.googleapis.com/v1beta');
  final resp = await http
      .get(Uri.parse(
          '$base/models?pageSize=1000&key=${Uri.encodeComponent(key)}'),
      headers: {'Accept': 'application/json'}).timeout(_httpTimeout);
  if (resp.statusCode >= 400) {
    throw _UpstreamError('上游 ${resp.statusCode}: ${_trim(resp.body)}');
  }
  final list = jsonDecode(resp.body) as Map<String, dynamic>;
  return ((list['models'] as List? ?? const [])
      .map((e) {
        final name = ((e as Map)['name'] ?? '').toString();
        return name.startsWith('models/') ? name.substring(7) : name;
      })
      .where((s) => s.isNotEmpty)
      .toList());
}
