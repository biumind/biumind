// ToolsClient — Flutter wrapper for Brain's /v1/tools surface.
//
// W7's ClientAgentLoop calls these to:
//   * fetch the tool catalog (POST /v1/tools)              — what tools
//     are available in the caller's execution mode
//   * proxy-invoke a tool                                   — POST
//     /v1/tools/invoke {name, input, execution_mode}
//
// 设计文档: docs/BiuMind-Chat-Optimization-Design.md §4.6.
//
// Why proxy through Brain instead of Flutter calling brain.search /
// brain.memory directly: tool inputs/outputs are an LLM-facing
// contract, not a public REST API. Going through /v1/tools/invoke
// keeps that contract canonical and the auth boundary single.
//
// In W7 v1 we route ALL tool invocations through the proxy. A future
// pass adds local-execution tools (filesystem, internal MCP) — those
// will short-circuit the HTTP call inside ToolHost without changing
// this client's shape.

import 'dart:async';

import '_http_helpers.dart';
import 'identity_client.dart' show IdentityApiError;

/// Tool descriptor shape mirrors `services/brain/internal/tools.Descriptor`.
class ToolDescriptor {
  final String name;
  final String description;
  final String source;
  /// JSON Schema for the tool input. Anthropic's `input_schema` field
  /// in the chat request takes the same shape.
  final Map<String, dynamic>? inputSchema;
  /// "cloud" | "client" | "both" — hint for the host about where the
  /// tool may run. v1 ignores this and always proxies; later we'll
  /// dispatch client-only tools locally.
  final String runtime;

  const ToolDescriptor({
    required this.name,
    required this.description,
    required this.source,
    this.inputSchema,
    required this.runtime,
  });

  factory ToolDescriptor.fromJson(Map<String, dynamic> j) => ToolDescriptor(
        name: j['name'] as String? ?? '',
        description: j['description'] as String? ?? '',
        source: j['source'] as String? ?? 'builtin',
        inputSchema:
            (j['input_schema'] as Map?)?.cast<String, dynamic>(),
        runtime: j['runtime'] as String? ?? 'cloud',
      );

  /// Convert to the Anthropic `tools[]` shape. Used by ClientAgentLoop
  /// when building the chat request.
  Map<String, dynamic> toAnthropic() {
    final out = <String, dynamic>{
      'name': name,
      'description': description,
    };
    if (inputSchema != null) {
      out['input_schema'] = inputSchema;
    } else {
      // Anthropic requires an input_schema even when the tool takes
      // no args. Empty object is the canonical "no parameters" shape.
      out['input_schema'] = {'type': 'object', 'properties': {}};
    }
    return out;
  }
}

class ToolInvokeResult {
  final String name;
  final dynamic result;
  final int durationMs;
  const ToolInvokeResult({
    required this.name,
    required this.result,
    required this.durationMs,
  });

  factory ToolInvokeResult.fromJson(Map<String, dynamic> j) =>
      ToolInvokeResult(
        name: j['name'] as String? ?? '',
        result: j['result'],
        durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
      );
}

class ToolsClient {
  ToolsClient(this.baseUrl, this.bearerToken);
  final Uri baseUrl;
  final String bearerToken;

  /// List tools available in the given execution mode. Defaults to
  /// `client` since the Flutter caller is by definition the client
  /// side; the server's catalog under that filter is the set of
  /// tools the client agent can use through this proxy.
  ///
  /// Note: server-side filter is "available IN [mode]" — for cloud
  /// mode that's tools the cloud agent can run locally; for client
  /// mode that's tools the client agent can dispatch (which, today,
  /// equals tools the proxy can serve, because every tool with
  /// Runtime>=Both is reachable both ways).
  Future<List<ToolDescriptor>> list({String executionMode = 'client'}) async {
    final raw = await _request('GET', '/v1/tools',
        queryParams: {'execution_mode': executionMode});
    return (raw['tools'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ToolDescriptor.fromJson)
        .toList();
  }

  /// Invoke a tool by name with the JSON input the LLM emitted. The
  /// proxy enforces JWT → user id binding so owner-scoped tools see
  /// the right user. `executionMode` tells the proxy which mode the
  /// caller intends — defaults to `client` to indicate "I'd run this
  /// locally if I could; please serve it for me".
  Future<ToolInvokeResult> invoke({
    required String name,
    required Map<String, dynamic> input,
    String executionMode = 'client',
  }) async {
    final raw = await _request('POST', '/v1/tools/invoke', body: {
      'name': name,
      'input': input,
      'execution_mode': executionMode,
    });
    return ToolInvokeResult.fromJson(raw);
  }

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, String>? queryParams,
    Map<String, dynamic>? body,
  }) async {
    try {
      return await apiRequest(
        method: method,
        url: baseUrl.replace(path: path, queryParameters: queryParams),
        bearerToken: bearerToken,
        body: body,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }
}
