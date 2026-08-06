// ChatClient — REST + SSE bindings for Brain's chat endpoints.
//
// Design doc: docs/Chat-Threads-Design.md.
//
// Three operation classes:
//
//  1. Thread CRUD — list / create / update / delete
//  2. Message CRUD — list / create / patch / delete (used by direct
//     mode to upload after-the-fact, and by tool-result injection)
//  3. Stream send — POST /v1/threads/{id}/send returning SSE
//     decoded into typed events (cloud mode)
//
// Stdlib HttpClient (matches memory_client / wiki_client / admin_client).

import 'dart:async';
import 'dart:convert';

import '_http_helpers.dart' as helpers;
import 'identity_client.dart' show IdentityApiError;

// ─── Models ──────────────────────────────────────────────

class ChatThread {
  final String id;
  final String userId;
  final String? projectId;
  final String title;
  final String lastMsgPreview;
  final String? model;
  final String? systemPrompt;
  final bool pinned;
  final bool archived;
  final bool syncEnabled;
  /// Sampling overrides for this thread. Shape:
  ///   {temperature?, top_p?, max_tokens?, stop_sequences?}
  /// null = use provider/server defaults (most threads). Any field
  /// inside the map being absent means the same per-field.
  final Map<String, dynamic>? modelParams;
  final String? agentId;
  final String? parentThreadId;
  final String? summary;
  final DateTime createdAt;
  final DateTime updatedAt;

  const ChatThread({
    required this.id,
    required this.userId,
    this.projectId,
    required this.title,
    required this.lastMsgPreview,
    this.model,
    this.systemPrompt,
    required this.pinned,
    required this.archived,
    required this.syncEnabled,
    this.modelParams,
    this.agentId,
    this.parentThreadId,
    this.summary,
    required this.createdAt,
    required this.updatedAt,
  });

  factory ChatThread.fromJson(Map<String, dynamic> j) => ChatThread(
        id: j['id'] as String,
        userId: j['user_id'] as String,
        projectId: j['project_id'] as String?,
        title: j['title'] as String? ?? '',
        lastMsgPreview: j['last_msg_preview'] as String? ?? '',
        model: j['model'] as String?,
        systemPrompt: j['system_prompt'] as String?,
        pinned: j['pinned'] as bool? ?? false,
        archived: j['archived'] as bool? ?? false,
        syncEnabled: j['sync_enabled'] as bool? ?? true,
        modelParams:
            (j['model_params'] as Map?)?.cast<String, dynamic>(),
        agentId: j['agent_id'] as String?,
        parentThreadId: j['parent_thread_id'] as String?,
        summary: j['summary'] as String?,
        createdAt:
            DateTime.tryParse(j['created_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
        updatedAt:
            DateTime.tryParse(j['updated_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
      );
}

class ChatMessage {
  final String id;
  final String threadId;
  final String role;
  final String content;
  final List<dynamic> parts;
  final String? toolCallId;
  final String? parentId;
  final String? model;
  final int? promptTokens;
  final int? completionTokens;
  final String status;
  final String? errorMsg;
  final String? clientId;
  final String? agentId;
  final String? messageGroupId;
  final int position;
  final DateTime createdAt;
  final DateTime updatedAt;

  const ChatMessage({
    required this.id,
    required this.threadId,
    required this.role,
    required this.content,
    required this.parts,
    this.toolCallId,
    this.parentId,
    this.model,
    this.promptTokens,
    this.completionTokens,
    required this.status,
    this.errorMsg,
    this.clientId,
    this.agentId,
    this.messageGroupId,
    required this.position,
    required this.createdAt,
    required this.updatedAt,
  });

  factory ChatMessage.fromJson(Map<String, dynamic> j) => ChatMessage(
        id: j['id'] as String,
        threadId: j['thread_id'] as String,
        role: j['role'] as String,
        content: j['content'] as String? ?? '',
        parts: (j['parts'] as List?) ?? const [],
        toolCallId: j['tool_call_id'] as String?,
        parentId: j['parent_id'] as String?,
        model: j['model'] as String?,
        promptTokens: (j['prompt_tokens'] as num?)?.toInt(),
        completionTokens: (j['completion_tokens'] as num?)?.toInt(),
        status: j['status'] as String? ?? 'success',
        errorMsg: j['error'] as String?,
        clientId: j['client_id'] as String?,
        agentId: j['agent_id'] as String?,
        messageGroupId: j['message_group_id'] as String?,
        position: (j['position'] as num?)?.toInt() ?? 0,
        createdAt:
            DateTime.tryParse(j['created_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
        updatedAt:
            DateTime.tryParse(j['updated_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
      );
}

// ─── Stream events ─────────────────────────────────────

/// Discriminated union of frames the server emits during /send.
sealed class ChatStreamEvent {
  const ChatStreamEvent();
}

class ChatUserMessage extends ChatStreamEvent {
  final ChatMessage msg;
  const ChatUserMessage(this.msg);
}

class ChatAssistantPlaceholder extends ChatStreamEvent {
  final ChatMessage msg;
  const ChatAssistantPlaceholder(this.msg);
}

class ChatDelta extends ChatStreamEvent {
  final String text;
  const ChatDelta(this.text);
}

class ChatStop extends ChatStreamEvent {
  final String reason;
  final int promptTokens;
  final int completionTokens;
  const ChatStop({
    required this.reason,
    required this.promptTokens,
    required this.completionTokens,
  });
}

class ChatDone extends ChatStreamEvent {
  final String assistantMessageId;
  final DateTime threadUpdatedAt;
  const ChatDone({
    required this.assistantMessageId,
    required this.threadUpdatedAt,
  });
}

class ChatStreamError extends ChatStreamEvent {
  final String message;
  const ChatStreamError(this.message);
}

// ─── ChunkType v2 events (Chat Optimization §3.2) ───────────────
//
// Brain dual-emits legacy (delta/stop/done) + v2 (block.*/tool.*/
// message.done) so the rollout window is forgiving. send_controller
// drives off the v2 events; the legacy ones are still produced by
// _decodeEvent for any callers that haven't migrated yet.

class ChatBlockCreate extends ChatStreamEvent {
  final String messageId;
  final String blockId;
  final String type; // 'text' | 'thinking' | 'tool_use' | …
  final int index;
  const ChatBlockCreate({
    required this.messageId,
    required this.blockId,
    required this.type,
    required this.index,
  });
}

class ChatBlockDelta extends ChatStreamEvent {
  final String messageId;
  final String blockId;
  final String delta;
  const ChatBlockDelta({
    required this.messageId,
    required this.blockId,
    required this.delta,
  });
}

class ChatBlockComplete extends ChatStreamEvent {
  final String messageId;
  final String blockId;
  const ChatBlockComplete({required this.messageId, required this.blockId});
}

class ChatBlockError extends ChatStreamEvent {
  final String messageId;
  final String? blockId;
  final String code;
  final String message;
  const ChatBlockError({
    required this.messageId,
    this.blockId,
    required this.code,
    required this.message,
  });
}

class ChatToolCreated extends ChatStreamEvent {
  final String messageId;
  final String blockId;
  final String name;
  final Map<String, dynamic> input;
  const ChatToolCreated({
    required this.messageId,
    required this.blockId,
    required this.name,
    required this.input,
  });
}

class ChatToolCompleted extends ChatStreamEvent {
  final String messageId;
  final String blockId;
  final dynamic result;
  final int durationMs;
  const ChatToolCompleted({
    required this.messageId,
    required this.blockId,
    required this.result,
    required this.durationMs,
  });
}

class ChatMessageDone extends ChatStreamEvent {
  final String messageId;
  final String? assistantMessageId;
  final DateTime? threadUpdatedAt;
  const ChatMessageDone({
    required this.messageId,
    this.assistantMessageId,
    this.threadUpdatedAt,
  });
}

// ─── Search DTOs ──────────────────────────────────────

/// 单条搜索命中. snippet 当 highlight=true 时含 `<mark>...</mark>`,
/// 否则纯文本. role/threadTitle 给跨 thread 列表渲染用 (in-thread 模式
/// threadTitle 一般冗余, UI 自决要不要展示)。
class MessageSearchHit {
  const MessageSearchHit({
    required this.messageId,
    required this.threadId,
    required this.threadTitle,
    required this.role,
    required this.snippet,
    required this.score,
    required this.createdAt,
  });

  factory MessageSearchHit.fromJson(Map<String, dynamic> j) =>
      MessageSearchHit(
        messageId: j['message_id']?.toString() ?? '',
        threadId: j['thread_id']?.toString() ?? '',
        threadTitle: j['thread_title']?.toString() ?? '',
        role: j['role']?.toString() ?? '',
        snippet: j['snippet']?.toString() ?? '',
        score: (j['score'] as num?)?.toDouble() ?? 0,
        createdAt: DateTime.tryParse(j['created_at']?.toString() ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
      );

  final String messageId;
  final String threadId;
  final String threadTitle;
  final String role;
  final String snippet;
  final double score;
  final DateTime createdAt;
}

class MessageSearchResult {
  const MessageSearchResult({
    required this.hits,
    required this.total,
    required this.tookMs,
    required this.query,
  });

  factory MessageSearchResult.fromJson(Map<String, dynamic> j) =>
      MessageSearchResult(
        hits: ((j['hits'] as List?) ?? const [])
            .cast<Map<String, dynamic>>()
            .map(MessageSearchHit.fromJson)
            .toList(),
        total: (j['total'] as num?)?.toInt() ?? 0,
        tookMs: (j['took_ms'] as num?)?.toInt() ?? 0,
        query: j['query']?.toString() ?? '',
      );

  final List<MessageSearchHit> hits;
  final int total;
  final int tookMs;
  final String query;
}

// ─── Client ────────────────────────────────────────────

class ChatClient {
  ChatClient(this.baseUrl, this.bearerToken);
  final Uri baseUrl;
  final String bearerToken;

  // ── Threads ──

  Future<List<ChatThread>> listThreads({
    int limit = 50,
    bool? archived,
    DateTime? before,
  }) async {
    final qp = <String, String>{'limit': '$limit'};
    if (archived != null) qp['archived'] = '$archived';
    if (before != null) qp['before'] = before.toUtc().toIso8601String();
    final raw = await _request('GET', '/v1/threads', queryParams: qp);
    return (raw['threads'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ChatThread.fromJson)
        .toList();
  }

  Future<ChatThread> createThread({
    String title = '',
    String? projectId,
    String? model,
    String? systemPrompt,
    String? agentId,
    bool? syncEnabled,
    String? parentThreadId,
  }) async {
    final body = <String, dynamic>{'title': title};
    if (projectId != null) body['project_id'] = projectId;
    if (model != null) body['model'] = model;
    if (systemPrompt != null) body['system_prompt'] = systemPrompt;
    if (agentId != null) body['agent_id'] = agentId;
    if (syncEnabled != null) body['sync_enabled'] = syncEnabled;
    if (parentThreadId != null) body['parent_thread_id'] = parentThreadId;
    final raw = await _request('POST', '/v1/threads', body: body);
    return ChatThread.fromJson(raw);
  }

  Future<ChatThread> getThread(String id) async {
    final raw = await _request('GET', '/v1/threads/$id');
    return ChatThread.fromJson(raw);
  }

  Future<ChatThread> patchThread(
    String id, {
    String? title,
    String? model,
    String? systemPrompt,
    bool? pinned,
    bool? archived,
    String? agentId,
    bool? syncEnabled,
    /// Pass `{}` to clear, a populated map to set, omit to leave alone.
    Map<String, dynamic>? modelParams,
  }) async {
    final body = <String, dynamic>{};
    if (title != null) body['title'] = title;
    if (model != null) body['model'] = model;
    if (systemPrompt != null) body['system_prompt'] = systemPrompt;
    if (pinned != null) body['pinned'] = pinned;
    if (archived != null) body['archived'] = archived;
    if (agentId != null) body['agent_id'] = agentId;
    if (syncEnabled != null) body['sync_enabled'] = syncEnabled;
    if (modelParams != null) body['model_params'] = modelParams;
    final raw = await _request('PATCH', '/v1/threads/$id', body: body);
    return ChatThread.fromJson(raw);
  }

  Future<void> deleteThread(String id) async {
    await _request('DELETE', '/v1/threads/$id', expectNoBody: true);
  }

  // ── Messages ──

  Future<List<ChatMessage>> listMessages(
    String threadId, {
    int? afterPosition,
    int? beforePosition,
    int limit = 50,
  }) async {
    final qp = <String, String>{'limit': '$limit'};
    if (afterPosition != null) qp['after'] = '$afterPosition';
    if (beforePosition != null) qp['before'] = '$beforePosition';
    final raw =
        await _request('GET', '/v1/threads/$threadId/messages', queryParams: qp);
    return (raw['messages'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ChatMessage.fromJson)
        .toList();
  }

  /// Append a message directly (skipping LLM). Used by direct mode
  /// after the client gets its stream from Anthropic, or by injection
  /// of tool results.
  Future<ChatMessage> createMessage(
    String threadId, {
    required String role,
    String content = '',
    List<dynamic>? parts,
    String? clientId,
    String status = 'success',
    String? model,
    String? agentId,
    String? parentId,
  }) async {
    final body = <String, dynamic>{
      'role': role,
      'content': content,
      'status': status,
    };
    if (parts != null) body['parts'] = parts;
    if (clientId != null) body['client_id'] = clientId;
    if (model != null) body['model'] = model;
    if (agentId != null) body['agent_id'] = agentId;
    if (parentId != null) body['parent_id'] = parentId;
    final raw = await _request(
      'POST',
      '/v1/threads/$threadId/messages',
      body: body,
    );
    return ChatMessage.fromJson(raw);
  }

  /// Update a previously-created message. Used by direct mode to upload
  /// the final content + tokens after local LLM stream ends.
  Future<ChatMessage> patchMessage(
    String threadId,
    String messageId, {
    String? content,
    List<dynamic>? parts,
    String? status,
    String? error,
    int? promptTokens,
    int? completionTokens,
  }) async {
    final body = <String, dynamic>{};
    if (content != null) body['content'] = content;
    if (parts != null) body['parts'] = parts;
    if (status != null) body['status'] = status;
    if (error != null) body['error'] = error;
    if (promptTokens != null) body['prompt_tokens'] = promptTokens;
    if (completionTokens != null) body['completion_tokens'] = completionTokens;
    final raw = await _request(
      'PATCH',
      '/v1/threads/$threadId/messages/$messageId',
      body: body,
    );
    return ChatMessage.fromJson(raw);
  }

  Future<void> deleteMessage(String threadId, String messageId) async {
    await _request(
      'DELETE',
      '/v1/threads/$threadId/messages/$messageId',
      expectNoBody: true,
    );
  }

  // ── Streaming send (cloud mode) ──

  /// Stream a chat reply through Brain → model-relay → LLM. Yields typed
  /// events. Caller cancels by breaking out of the iteration; the
  /// underlying HttpClient closes and Brain marks the row paused.
  Stream<ChatStreamEvent> sendStream(
    String threadId, {
    required String content,
    String? clientId,
    /// Multimodal Anthropic-style content blocks. When non-empty,
    /// Brain stores them on chat.messages.parts and model-relay forwards them
    /// to the upstream provider as the user-message content array.
    List<dynamic>? parts,
    String? model,
    String? system,
    int maxTokens = 4096,
  }) async* {
    final url = baseUrl.replace(path: '/v1/threads/$threadId/send');
    final body = <String, dynamic>{
      'content': content,
      'max_tokens': maxTokens,
    };
    if (clientId != null) body['client_id'] = clientId;
    if (parts != null && parts.isNotEmpty) body['parts'] = parts;
    if (model != null) body['model'] = model;
    if (system != null) body['system'] = system;

    // 走共享 sseStream — 自动获得 401 → refresh → 重开流的 retry 能力
    Stream<String> sse() async* {
      try {
        yield* helpers.sseStream(
          url: url,
          bearerToken: bearerToken,
          body: body,
        );
      } on helpers.ApiError catch (e) {
        throw IdentityApiError(
          path: '/v1/threads/$threadId/send',
          status: e.status,
          body: e.body,
        );
      }
    }

    yield* parseSse(sse());
  }

  /// Re-roll the assistant reply for an existing user message. The
  /// stream shape is identical to [sendStream] — same v2 events, same
  /// legacy fallbacks — so SendController can route both through one
  /// handler. The new assistant placeholder arrives as the first
  /// `assistant_message` event; it's a sibling (same parent_id) of
  /// any prior assistant for [userMsgId].
  Stream<ChatStreamEvent> regenerateStream(
    String threadId,
    String userMsgId, {
    String? model,
    String? system,
    int maxTokens = 4096,
  }) async* {
    final url = baseUrl.replace(
        path: '/v1/threads/$threadId/messages/$userMsgId/regenerate');
    final body = <String, dynamic>{'max_tokens': maxTokens};
    if (model != null) body['model'] = model;
    if (system != null) body['system'] = system;

    Stream<String> sse() async* {
      try {
        yield* helpers.sseStream(
          url: url,
          bearerToken: bearerToken,
          body: body,
        );
      } on helpers.ApiError catch (e) {
        throw IdentityApiError(
          path: '/v1/threads/$threadId/messages/$userMsgId/regenerate',
          status: e.status,
          body: e.body,
        );
      }
    }

    yield* parseSse(sse());
  }

  static Stream<ChatStreamEvent> parseSse(Stream<String> lines) async* {
    String event = '';
    String data = '';
    await for (final line in lines) {
      if (line.startsWith('event: ')) {
        event = line.substring('event: '.length);
      } else if (line.startsWith('data: ')) {
        data = line.substring('data: '.length);
      } else if (line.isEmpty) {
        if (event.isEmpty) continue;
        final ev = _decodeEvent(event, data);
        if (ev != null) yield ev;
        event = '';
        data = '';
      }
    }
  }

  static ChatStreamEvent? _decodeEvent(String name, String raw) {
    try {
      final j = raw.isEmpty ? <String, dynamic>{} : jsonDecode(raw);
      switch (name) {
        case 'user_message':
          return ChatUserMessage(
              ChatMessage.fromJson(j as Map<String, dynamic>));
        case 'assistant_message':
          return ChatAssistantPlaceholder(
              ChatMessage.fromJson(j as Map<String, dynamic>));
        case 'delta':
          return ChatDelta((j as Map<String, dynamic>)['text'] as String? ?? '');
        case 'stop':
          final m = j as Map<String, dynamic>;
          final usage = (m['usage'] as Map?)?.cast<String, dynamic>() ??
              const <String, dynamic>{};
          return ChatStop(
            reason: m['reason'] as String? ?? '',
            promptTokens:
                (usage['prompt_tokens'] as num?)?.toInt() ?? 0,
            completionTokens:
                (usage['completion_tokens'] as num?)?.toInt() ?? 0,
          );
        case 'done':
          final m = j as Map<String, dynamic>;
          return ChatDone(
            assistantMessageId: m['assistant_message_id'] as String? ?? '',
            threadUpdatedAt: DateTime.tryParse(
                    m['thread_updated_at'] as String? ?? '')
                    ?.toUtc() ??
                DateTime.now().toUtc(),
          );
        case 'error':
          return ChatStreamError(
              (j as Map<String, dynamic>)['message'] as String? ?? '');

        // ─── ChunkType v2 ─────────────────────────────────────
        case 'block.create':
          final m = j as Map<String, dynamic>;
          return ChatBlockCreate(
            messageId: m['message_id'] as String? ?? '',
            blockId: m['block_id'] as String? ?? '',
            type: m['type'] as String? ?? 'text',
            index: (m['index'] as num?)?.toInt() ?? 0,
          );
        case 'block.delta':
          final m = j as Map<String, dynamic>;
          return ChatBlockDelta(
            messageId: m['message_id'] as String? ?? '',
            blockId: m['block_id'] as String? ?? '',
            delta: m['delta'] as String? ?? '',
          );
        case 'block.complete':
          final m = j as Map<String, dynamic>;
          return ChatBlockComplete(
            messageId: m['message_id'] as String? ?? '',
            blockId: m['block_id'] as String? ?? '',
          );
        case 'block.error':
          final m = j as Map<String, dynamic>;
          return ChatBlockError(
            messageId: m['message_id'] as String? ?? '',
            blockId: m['block_id'] as String?,
            code: m['code'] as String? ?? 'unknown',
            message: m['message'] as String? ?? '',
          );
        case 'tool.created':
          final m = j as Map<String, dynamic>;
          return ChatToolCreated(
            messageId: m['message_id'] as String? ?? '',
            blockId: m['block_id'] as String? ?? '',
            name: m['name'] as String? ?? '',
            input: ((m['input'] as Map?) ?? const {})
                .cast<String, dynamic>(),
          );
        case 'tool.completed':
          final m = j as Map<String, dynamic>;
          return ChatToolCompleted(
            messageId: m['message_id'] as String? ?? '',
            blockId: m['block_id'] as String? ?? '',
            result: m['result'],
            durationMs: (m['duration_ms'] as num?)?.toInt() ?? 0,
          );
        case 'message.done':
          final m = j as Map<String, dynamic>;
          return ChatMessageDone(
            messageId: m['message_id'] as String? ?? '',
            assistantMessageId: m['assistant_message_id'] as String?,
            threadUpdatedAt: DateTime.tryParse(
                    m['thread_updated_at'] as String? ?? '')
                ?.toUtc(),
          );

        default:
          return null;
      }
    } catch (e) {
      return ChatStreamError('decode $name: $e');
    }
  }

  /// Politely ask the server to cancel a streaming assistant message.
  /// 202 on success even if there's no in-flight stream (server marks
  /// status=paused defensively).
  Future<void> cancel(String threadId, String messageId) async {
    await _request(
      'POST',
      '/v1/threads/$threadId/messages/$messageId/cancel',
      expectNoBody: true,
    );
  }

  // ─── HTTP plumbing ────────────────────────────────────
  //
  // 走共享 helpers.apiRequest — 自动获得 401 → tokenManager.handle401 →
  // refresh → retry 一次的能力。错误转成 IdentityApiError 保持兼容。

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, String>? queryParams,
    Map<String, dynamic>? body,
    bool expectNoBody = false,
  }) async {
    final url = baseUrl.replace(path: path, queryParameters: queryParams);
    try {
      return await helpers.apiRequest(
        method: method,
        url: url,
        bearerToken: bearerToken,
        body: body,
        expectNoBody: expectNoBody,
      );
    } on helpers.ApiError catch (e) {
      throw IdentityApiError(path: path, status: e.status, body: e.body);
    }
  }

  // ── Search ──
  // 设计文档: docs/BiuMind-Chat-Search-Design.md

  /// 搜索消息. threadId=null → 跨 thread; 指定即缩到当前 thread。
  /// role / fromTime / toTime / highlight 全部 server-side 过滤。
  Future<MessageSearchResult> searchMessages({
    required String query,
    String? threadId,
    String? role,
    DateTime? fromTime,
    DateTime? toTime,
    int limit = 20,
    int offset = 0,
    bool highlight = true,
  }) async {
    final body = <String, dynamic>{
      'query': query,
      'limit': limit,
      'offset': offset,
      'highlight': highlight,
    };
    if (threadId != null) body['thread_id'] = threadId;
    if (role != null) body['role'] = role;
    if (fromTime != null) body['from'] = fromTime.toUtc().toIso8601String();
    if (toTime != null) body['to'] = toTime.toUtc().toIso8601String();
    final raw = await _request('POST', '/v1/chat/search', body: body);
    return MessageSearchResult.fromJson(raw);
  }
}
