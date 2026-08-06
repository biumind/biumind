// AgentPlaneClient —— Flutter 端调 brain Agent Plane HTTP endpoints。
//
// 跟 BiuClient（WS）配合：BiuClient 走会话流；AgentPlaneClient 走 REST
// 控制面（list environments / create session / refresh token / 等）。
//
// 路由对齐 services/brain/internal/agentplane/api.go：
//
//   GET    /v1/agent/environments         列出当前用户的 worker
//   POST   /v1/agent/sessions             创建 session（chat / agent / task）
//   POST   /v1/agent/sessions/{id}/refresh-token   续 session_token

import 'package:flutter/foundation.dart' show debugPrint;
import 'package:http/http.dart' as http;

import '../api/_http_helpers.dart';
import 'environment.dart';

class AgentPlaneClient {
  /// brain HTTP base URL，例 `https://your-biumind.example.com`。末尾不带 `/`。
  final String baseUrl;

  /// 上层 token getter —— 每次请求拿最新 access_token。空返 null 时
  /// HTTP helper 不带 Authorization（实际 brain 会 401，下面再抛）。
  final Future<String?> Function() tokenProvider;

  /// onAuthError —— 401 时 helper 调用，期望返回新 token。直接传给
  /// _http_helpers.apiRequest 即可；nil 时走全局 authErrorHandler。
  final AuthErrorHandler? onAuthError;

  AgentPlaneClient({
    required this.baseUrl,
    required this.tokenProvider,
    this.onAuthError,
  });

  /// 列出当前用户名下所有 environment。state 可选过滤："online" /
  /// "offline" / "" (=不过滤)。
  Future<List<AgentEnvironment>> listEnvironments({String state = ''}) async {
    final tok = await tokenProvider();
    final qp = state.isEmpty ? '' : '?state=$state';
    final sw = Stopwatch()..start();
    final json = await apiRequest(
      method: 'GET',
      url: Uri.parse('$baseUrl/v1/agent/environments$qp'),
      bearerToken: tok,
      onAuthError: onAuthError,
    );
    final envs = json['environments'] as List?;
    final count = envs?.length ?? 0;
    debugPrint('[agent_plane] listEnvironments state=$state count=$count'
        ' latency_ms=${sw.elapsedMilliseconds}');
    if (envs == null) return const [];
    return envs
        .cast<Map<String, dynamic>>()
        .map(AgentEnvironment.fromJson)
        .toList();
  }

  /// 创建 session。mode = 'chat' / 'agent' / 'task'。返回 session_id +
  /// session_token；上层用它构造 BiuClient.connect。
  ///
  /// agent / task mode 跨 brain 端 RouterErrors 时会抛 [ApiError]：
  ///   - 503 no_runtime_available（task pool 空）
  ///   - 409 environment_offline（agent mode 选了离线 env）
  ///   - 404 environment_not_found
  /// 上层根据 [ApiError.body] / status 渲染对应错误。
  Future<CreateSessionResp> createSession({
    required String mode,
    String? environmentId,
    String? threadId,
    String? model,
    String? providerId,
    String? systemPrompt,
    String? prompt,
    String? poolTag,
    String? workdir,
    /// 工具执行环境（Runtime v3 轴 B）：'none' | 'local' | 'cloud'。空 → brain
    /// 按 mode 推默认（agent=local / task=cloud / chat=none）。
    String? runtimeEnvMode,
    /// Agent loop backend（Runtime v3 R3/Q3）：'biumindkit'(默认) | 'claude-cli'
    /// | 'codex-cli'。agent 模式选外部 CLI 时透传给 brain → daemon spawn。
    String? backend,
    /// 图片附件（chat 模式 vision 模型才生效）。每张图都是 base64 编码 +
    /// mime_type；非视觉模型场景上层负责剔除以避免 LLM 400。
    List<ChatImageInput>? images,
    /// client 为本轮预生成的 message uuid（方案3：本地 message.id == brain
    /// chat.messages.id，编辑/删除上行直连）。空 → brain 走 gen_random_uuid。
    String? userMessageId,
    String? assistantMessageId,
    /// client-side BYOK 信号（B2）：命中时透传给 brain → WorkPayload → daemon。
    /// key 不走此通道（经 daemon loopback 注入）；record_id 让 daemon 从内存
    /// store 取 key，base_url/protocol 让 daemon 建 engine 直连上游。
    String? clientSideRecordId,
    String? clientSideBaseUrl,
    String? clientSideProtocol,
  }) async {
    final tok = await tokenProvider();
    final body = <String, dynamic>{
      'mode': mode,
      // 用 if 而非 null-aware (?'key':val) —— 后者跟 ProgressIndicator
      // 等老 SDK 不兼容；info 级 lint 让它存在不影响 build
      'environment_id': ?environmentId,
      'thread_id': ?threadId,
      'model': ?model,
      'provider_id': ?providerId,
      'system_prompt': ?systemPrompt,
      'prompt': ?prompt,
      'pool_tag': ?poolTag,
      'workdir': ?workdir,
      'runtime_env_mode': ?runtimeEnvMode,
      'backend': ?backend,
      if (images != null && images.isNotEmpty)
        'images': images.map((i) => i.toJson()).toList(),
      'user_message_id': ?userMessageId,
      'assistant_message_id': ?assistantMessageId,
      'client_side_record_id': ?clientSideRecordId,
      'client_side_base_url': ?clientSideBaseUrl,
      'client_side_protocol': ?clientSideProtocol,
    };
    final sw = Stopwatch()..start();
    debugPrint('[agent_plane] createSession mode=$mode env=$environmentId'
        ' thread=$threadId model=$model provider=$providerId'
        ' prompt_bytes=${prompt?.length ?? 0}'
        ' images=${images?.length ?? 0}');
    final json = await apiRequest(
      method: 'POST',
      url: Uri.parse('$baseUrl/v1/agent/sessions'),
      bearerToken: tok,
      body: body,
      onAuthError: onAuthError,
    );
    final resp = CreateSessionResp.fromJson(json);
    debugPrint('[agent_plane] createSession ok session=${resp.sessionId}'
        ' latency_ms=${sw.elapsedMilliseconds}');
    return resp;
  }

  /// 续 session_token。BiuClient.refreshToken 用。
  Future<RefreshTokenResp> refreshSessionToken(String sessionId) async {
    final tok = await tokenProvider();
    final sw = Stopwatch()..start();
    final json = await apiRequest(
      method: 'POST',
      url: Uri.parse('$baseUrl/v1/agent/sessions/$sessionId/refresh-token'),
      bearerToken: tok,
      onAuthError: onAuthError,
    );
    final resp = RefreshTokenResp.fromJson(json);
    debugPrint('[agent_plane] refreshSessionToken session=$sessionId'
        ' latency_ms=${sw.elapsedMilliseconds}');
    return resp;
  }

  /// S9-1 跨设备 resume —— 给定一条已经存在的 sessionId（另一台设备已经
  /// 创了），refresh 拿新 session_token 然后构造一个 BiuClient 用 sinceSeq
  /// 重放历史。
  ///
  /// 调用方：mac 上写到一半，手机打开同一 thread → 客户端拿到 session_id
  /// 就调这个方法，返回 [ResumeResp] 带 token + 推荐 sinceSeq=0（从头
  /// 重放；ingress 走 OrderedConsumer DeliverByStartSequence）。
  ///
  /// 当前实现：sinceSeq 默认 0（全量重放）；上层有持久化 lastSeenSeq
  /// 的话可以传更高值跳过已看过的部分。
  Future<ResumeResp> resumeSession(
    String sessionId, {
    int sinceSeq = 0,
  }) async {
    final refresh = await refreshSessionToken(sessionId);
    return ResumeResp(
      sessionId: sessionId,
      sessionToken: refresh.sessionToken,
      expiresAt: refresh.expiresAt,
      sinceSeq: sinceSeq,
    );
  }
}

/// ChatImageInput —— createSession 携带的单张图片附件。跟 brain 端
/// `agentplane.ChatImageInput` 字段名一一对应（mime_type / data）。
/// data 是 base64 编码字节，不带 `data:image/...;base64,` 前缀。
class ChatImageInput {
  final String mimeType;
  final String data;
  const ChatImageInput({required this.mimeType, required this.data});

  Map<String, dynamic> toJson() => {
        'mime_type': mimeType,
        'data': data,
      };
}

/// ResumeResp —— resumeSession 返回值，跟 CreateSessionResp 字段子集对齐
/// 让上层用同一形态喂给 BiuClient.connect。
class ResumeResp {
  final String sessionId;
  final String sessionToken;
  final DateTime? expiresAt;
  final int sinceSeq;

  ResumeResp({
    required this.sessionId,
    required this.sessionToken,
    this.expiresAt,
    required this.sinceSeq,
  });
}

class CreateSessionResp {
  final String sessionId;
  final String sessionToken;
  final DateTime? expiresAt;
  final String mode;
  final String? environmentId;
  final String jetstreamSubjectIn;
  final String jetstreamSubjectOut;

  /// 会话生命周期态。Runtime v3 R7：agent 模式投给离线设备时 brain 返
  /// 'pending'（已排队，等设备上线派发）；此时客户端不应连 WS。空 → 'active'。
  final String state;

  CreateSessionResp({
    required this.sessionId,
    required this.sessionToken,
    this.expiresAt,
    required this.mode,
    this.environmentId,
    required this.jetstreamSubjectIn,
    required this.jetstreamSubjectOut,
    this.state = 'active',
  });

  factory CreateSessionResp.fromJson(Map<String, dynamic> json) {
    return CreateSessionResp(
      sessionId: json['session_id'] as String,
      sessionToken: json['session_token'] as String,
      expiresAt: _parseTime(json['expires_at']),
      mode: json['mode'] as String? ?? '',
      environmentId: json['environment_id'] as String?,
      jetstreamSubjectIn: json['jetstream_subject_in'] as String? ?? '',
      jetstreamSubjectOut: json['jetstream_subject_out'] as String? ?? '',
      state: json['state'] as String? ?? 'active',
    );
  }
}

class RefreshTokenResp {
  final String sessionToken;
  final DateTime? expiresAt;

  RefreshTokenResp({required this.sessionToken, this.expiresAt});

  factory RefreshTokenResp.fromJson(Map<String, dynamic> json) {
    return RefreshTokenResp(
      sessionToken: json['session_token'] as String,
      expiresAt: _parseTime(json['expires_at']),
    );
  }
}

DateTime? _parseTime(Object? raw) {
  if (raw is String) {
    try {
      return DateTime.parse(raw);
    } catch (_) {
      return null;
    }
  }
  return null;
}

// 让 lints 不抓"unused import http" —— transitively 用 ApiError。
// ignore: unused_element
final _ = http.Client; // dart 编译器 keep 引用
