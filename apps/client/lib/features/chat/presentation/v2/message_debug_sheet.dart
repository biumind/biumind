// MessageDebugSheetV2 —— bubble 长按弹出，显原始 message + blocks JSON。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 bubble debug）。
//
// 给开发者 / 高级用户：看模型实际返了哪些 block / metadata。
// content 一栏可复制；右上 close 关。

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../domain/chat_models.dart';

Future<void> showMessageDebugSheet(
  BuildContext context,
  Message message,
) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (_) => _MessageDebugSheet(message: message),
  );
}

class _MessageDebugSheet extends StatelessWidget {
  const _MessageDebugSheet({required this.message});
  final Message message;

  Map<String, dynamic> _toJson() => {
        'id': message.id,
        'threadId': message.threadId,
        'role': message.role.name,
        'status': message.status.name,
        'sessionId': message.sessionId,
        'stopReason': message.stopReason,
        'model': message.model,
        'inputTokens': message.inputTokens,
        'outputTokens': message.outputTokens,
        'seq': message.seq,
        'errorMessage': message.errorMessage,
        'createdAt': message.createdAt.toUtc().toIso8601String(),
        'completedAt': message.completedAt?.toUtc().toIso8601String(),
        'blocks': message.blocks.map(_blockSummary).toList(),
      };

  Map<String, dynamic> _blockSummary(Block b) {
    return switch (b) {
      TextBlock(:final id, :final index, :final state, :final text) => {
          'kind': 'text',
          'id': id,
          'index': index,
          'state': state.name,
          'length': text.length,
          'text': text.length > 200 ? '${text.substring(0, 200)}…' : text,
        },
      ToolUseBlock(
        :final id,
        :final index,
        :final state,
        :final toolUseId,
        :final toolName,
        :final input,
      ) =>
        {
          'kind': 'tool_use',
          'id': id,
          'index': index,
          'state': state.name,
          'toolUseId': toolUseId,
          'toolName': toolName,
          'input': input,
        },
      ToolResultBlock(
        :final id,
        :final index,
        :final state,
        :final toolResultId,
        :final isError,
        :final content,
      ) =>
        {
          'kind': 'tool_result',
          'id': id,
          'index': index,
          'state': state.name,
          'toolResultId': toolResultId,
          'isError': isError,
          'contentLength': content.length,
        },
      ImageBlock(
        :final id,
        :final index,
        :final state,
        :final mimeType,
        :final data,
      ) =>
        {
          'kind': 'image',
          'id': id,
          'index': index,
          'state': state.name,
          'mimeType': mimeType,
          'dataLength': data.length,
        },
    };
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final json = const JsonEncoder.withIndent('  ').convert(_toJson());
    return Padding(
      padding: EdgeInsets.fromLTRB(
        16, 0, 16, MediaQuery.of(context).viewInsets.bottom + 16,
      ),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxHeight: 600),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.bug_report_outlined,
                    size: 16, color: theme.colorScheme.onSurfaceVariant),
                const SizedBox(width: 8),
                Text(
                  '消息原始结构',
                  style: theme.textTheme.titleSmall?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const Spacer(),
                TextButton.icon(
                  icon: const Icon(Icons.copy_outlined, size: 14),
                  label: const Text('复制 JSON'),
                  onPressed: () async {
                    await Clipboard.setData(ClipboardData(text: json));
                    if (!context.mounted) return;
                    ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('已复制 JSON'),
                            duration: Duration(seconds: 1)));
                  },
                ),
              ],
            ),
            const SizedBox(height: 8),
            Flexible(
              child: SingleChildScrollView(
                child: Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: theme.colorScheme.surfaceContainerHighest,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: SelectableText(
                    json,
                    style: theme.textTheme.bodySmall?.copyWith(
                      fontFamily: 'monospace',
                      height: 1.5,
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
