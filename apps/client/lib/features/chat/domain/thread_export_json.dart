// Thread 导入/导出 JSON 格式 —— 设计文档
// docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 会话备份）。
//
// 形态：
//   {
//     "schemaVersion": 1,
//     "exportedAt": "2026-06-04T00:00:00Z",
//     "thread": {
//       "id": "...",
//       "title": "...",
//       "mode": "chat",
//       "model": "claude-opus-4-7",
//       "systemPrompt": null,
//       "createdAt": "...",
//       "updatedAt": "..."
//     },
//     "messages": [
//       {
//         "id": "...",
//         "role": "user",
//         "status": "completed",
//         "model": null,
//         "inputTokens": null,
//         "outputTokens": null,
//         "seq": 1,
//         "createdAt": "...",
//         "completedAt": "...",
//         "blocks": [
//           {"type": "text", "text": "..."},
//           {"type": "image", "mimeType": "image/png", "data": "<base64>"},
//           {"type": "tool_use", "toolName": "...", "toolUseId": "...", "input": {...}},
//           {"type": "tool_result", "toolResultId": "...", "isError": false, "content": "..."}
//         ]
//       }
//     ]
//   }
//
// 不导出 sessionId / errorMessage / cancelled 状态等非内容字段 —— 历史回放
// 关注的是"对话内容本身"，运行时元数据导入回去也没意义。

import 'dart:convert';

import 'chat_models.dart';

const int kThreadExportSchemaVersion = 1;

String exportThreadAsJson({
  required Thread thread,
  required List<Message> messages,
  DateTime? exportedAt,
}) {
  final now = exportedAt ?? DateTime.now().toUtc();
  final m = <String, dynamic>{
    'schemaVersion': kThreadExportSchemaVersion,
    'exportedAt': now.toUtc().toIso8601String(),
    'thread': _threadToJson(thread),
    'messages': messages
        .where((m) =>
            m.status == MessageStatus.completed &&
            m.role != MessageRole.toolResult)
        .map(_messageToJson)
        .toList(),
  };
  return const JsonEncoder.withIndent('  ').convert(m);
}

/// 一次性导出多个 thread —— 批量备份用。
/// kind="bulk" 让 import 端能区分（如果以后接 import bulk 路径）。
String exportAllAsJson({
  required List<({Thread thread, List<Message> messages})> entries,
  DateTime? exportedAt,
}) {
  final now = exportedAt ?? DateTime.now().toUtc();
  return const JsonEncoder.withIndent('  ').convert({
    'schemaVersion': kThreadExportSchemaVersion,
    'kind': 'bulk',
    'exportedAt': now.toUtc().toIso8601String(),
    'threads': [
      for (final e in entries)
        {
          'thread': _threadToJson(e.thread),
          'messages': e.messages
              .where((m) =>
                  m.status == MessageStatus.completed &&
                  m.role != MessageRole.toolResult)
              .map(_messageToJson)
              .toList(),
        },
    ],
  });
}

class ParsedThreadExport {
  const ParsedThreadExport({
    required this.thread,
    required this.messages,
  });
  final Thread thread;
  final List<Message> messages;
}

/// 检测 source 是 single thread export 还是 bulk export。
/// kind == 'bulk' 走 parseBulkExportJson；其它走 parseThreadExportJson。
bool isBulkExport(String source) {
  try {
    final dynamic decoded = jsonDecode(source);
    return decoded is Map<String, dynamic> && decoded['kind'] == 'bulk';
  } catch (_) {
    return false;
  }
}

/// 解 bulk export（exportAllAsJson 的反向）。
/// 校验：schemaVersion=1 + threads 数组每项 thread + messages 都有。
List<ParsedThreadExport> parseBulkExportJson(String source) {
  final dynamic decoded = jsonDecode(source);
  if (decoded is! Map<String, dynamic>) {
    throw const FormatException('Bulk export must be a JSON object');
  }
  final version = decoded['schemaVersion'];
  if (version != kThreadExportSchemaVersion) {
    throw FormatException(
        'Unsupported schemaVersion=$version (expected $kThreadExportSchemaVersion)');
  }
  if (decoded['kind'] != 'bulk') {
    throw const FormatException('Not a bulk export (kind != "bulk")');
  }
  final threads = decoded['threads'];
  if (threads is! List) {
    throw const FormatException('Missing threads array');
  }
  final out = <ParsedThreadExport>[];
  for (final raw in threads) {
    if (raw is! Map<String, dynamic>) continue;
    final threadJson = raw['thread'];
    if (threadJson is! Map<String, dynamic>) continue;
    final t = _threadFromJson(threadJson);
    final messagesJson = raw['messages'];
    final messages = <Message>[];
    if (messagesJson is List) {
      for (final m in messagesJson) {
        if (m is Map<String, dynamic>) {
          messages.add(_messageFromJson(m, threadId: t.id));
        }
      }
    }
    out.add(ParsedThreadExport(thread: t, messages: messages));
  }
  return out;
}

ParsedThreadExport parseThreadExportJson(String source) {
  final dynamic decoded = jsonDecode(source);
  if (decoded is! Map<String, dynamic>) {
    throw const FormatException('Thread export must be a JSON object');
  }
  final version = decoded['schemaVersion'];
  if (version != kThreadExportSchemaVersion) {
    throw FormatException(
        'Unsupported schemaVersion=$version (expected $kThreadExportSchemaVersion)');
  }
  final threadJson = decoded['thread'];
  if (threadJson is! Map<String, dynamic>) {
    throw const FormatException('Missing thread object');
  }
  final thread = _threadFromJson(threadJson);
  final messagesJson = decoded['messages'];
  if (messagesJson is! List) {
    throw const FormatException('Missing messages array');
  }
  final messages = <Message>[];
  for (final raw in messagesJson) {
    if (raw is! Map<String, dynamic>) continue;
    messages.add(_messageFromJson(raw, threadId: thread.id));
  }
  return ParsedThreadExport(thread: thread, messages: messages);
}

Map<String, dynamic> _threadToJson(Thread t) => {
      'id': t.id,
      'title': t.title,
      'mode': t.mode.name,
      'model': t.model,
      'systemPrompt': t.systemPrompt,
      'createdAt': t.createdAt.toUtc().toIso8601String(),
      'updatedAt': t.updatedAt.toUtc().toIso8601String(),
    };

Thread _threadFromJson(Map<String, dynamic> j) => Thread(
      id: j['id'] as String,
      title: (j['title'] as String?) ?? '',
      mode: ThreadMode.fromName((j['mode'] as String?) ?? 'chat'),
      model: j['model'] as String?,
      systemPrompt: j['systemPrompt'] as String?,
      createdAt: DateTime.parse(j['createdAt'] as String),
      updatedAt: DateTime.parse(j['updatedAt'] as String),
    );

Map<String, dynamic> _messageToJson(Message m) => {
      'id': m.id,
      'role': m.role.name,
      'status': m.status.name,
      'model': m.model,
      'inputTokens': m.inputTokens,
      'outputTokens': m.outputTokens,
      'seq': m.seq,
      'createdAt': m.createdAt.toUtc().toIso8601String(),
      'completedAt': m.completedAt?.toUtc().toIso8601String(),
      'blocks': m.blocks.map(_blockToJson).toList(),
    };

Message _messageFromJson(Map<String, dynamic> j,
    {required String threadId}) {
  final blocks = <Block>[];
  final raw = j['blocks'];
  if (raw is List) {
    for (var i = 0; i < raw.length; i++) {
      final b = raw[i];
      if (b is! Map<String, dynamic>) continue;
      final block = _blockFromJson(b, index: i);
      if (block != null) blocks.add(block);
    }
  }
  return Message(
    id: j['id'] as String,
    threadId: threadId,
    role: MessageRole.fromName((j['role'] as String?) ?? 'user'),
    status: MessageStatus.fromName((j['status'] as String?) ?? 'completed'),
    model: j['model'] as String?,
    inputTokens: j['inputTokens'] as int?,
    outputTokens: j['outputTokens'] as int?,
    seq: (j['seq'] as int?) ?? 0,
    createdAt: DateTime.parse(j['createdAt'] as String),
    completedAt: j['completedAt'] == null
        ? null
        : DateTime.parse(j['completedAt'] as String),
    blocks: blocks,
  );
}

Map<String, dynamic> _blockToJson(Block b) {
  return switch (b) {
    TextBlock(:final text) => {'type': 'text', 'text': text},
    ImageBlock(:final mimeType, :final data) => {
        'type': 'image',
        'mimeType': mimeType,
        'data': data,
      },
    ToolUseBlock(:final toolUseId, :final toolName, :final input) => {
        'type': 'tool_use',
        'toolUseId': toolUseId,
        'toolName': toolName,
        // 仅 input 非空时入 JSON，避免噪音键。
        // ignore: use_null_aware_elements
        if (input != null) 'input': input,
      },
    ToolResultBlock(:final toolResultId, :final isError, :final content) => {
        'type': 'tool_result',
        'toolResultId': toolResultId,
        'isError': isError,
        'content': content,
      },
  };
}

Block? _blockFromJson(Map<String, dynamic> j, {required int index}) {
  final type = j['type'] as String?;
  // 用 message_id + index 派生 block id，省得 export 还要塞唯一 id。
  // 真正落库时调用方会重新生成 id —— 见 ChatRepo.importThreadJson。
  final id = 'imported-$index';
  switch (type) {
    case 'text':
      return TextBlock(
        id: id,
        index: index,
        state: BlockState.closed,
        text: (j['text'] as String?) ?? '',
      );
    case 'image':
      return ImageBlock(
        id: id,
        index: index,
        state: BlockState.closed,
        mimeType: (j['mimeType'] as String?) ?? 'image/png',
        data: (j['data'] as String?) ?? '',
      );
    case 'tool_use':
      return ToolUseBlock(
        id: id,
        index: index,
        state: BlockState.closed,
        toolUseId: (j['toolUseId'] as String?) ?? '',
        toolName: (j['toolName'] as String?) ?? '',
        input: j['input'] is Map
            ? Map<String, dynamic>.from(j['input'] as Map)
            : null,
      );
    case 'tool_result':
      return ToolResultBlock(
        id: id,
        index: index,
        state: BlockState.closed,
        toolResultId: (j['toolResultId'] as String?) ?? '',
        isError: (j['isError'] as bool?) ?? false,
        content: (j['content'] as String?) ?? '',
      );
    default:
      return null;
  }
}
