// 工具调用专用 widget — 不同工具不同视觉。
// P0：实现 Read / Edit / Bash / 通用 fallback。
// P1：TodoWrite / Grep / Glob / Task 等专用 widget。

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import '../../../../core/ui/biu_card.dart';
import '../../domain/code_task.dart';

class ToolCallCard extends StatelessWidget {
  const ToolCallCard({super.key, required this.start, required this.result});

  final ToolUseStart start;
  final ToolUseResult? result;

  @override
  Widget build(BuildContext context) {
    final isRunning = result == null;
    final isError = result?.isError ?? false;

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      // 错误态用 selected=true 借 BiuCard 的 1.5px 边框 + 把边框色换成 error;
      // 正常态默认 hairline + shadow-sm + brand 微染,工具卡有"温度"。
      // disableTint=true 避免内部 surfaceMuted body 被叠层渐变染色变浑浊。
      child: BiuCard(
        lift: 0,
        disableTint: true,
        padding: EdgeInsets.zero,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          child: Container(
            decoration: isError
                ? BoxDecoration(
                    border: Border.all(color: SemanticTokens.error.withValues(alpha: 0.3)),
                    borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                  )
                : null,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // Header
                Padding(
                  padding: const EdgeInsets.fromLTRB(10, 8, 10, 8),
                  child: Row(
                    children: [
                      _toolIcon(start.name),
                      const SizedBox(width: 8),
                      Text(
                        start.name,
                        style: const TextStyle(
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                          fontFamily: 'SF Mono',
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          _argSummary(start),
                          style: TextStyle(
                            fontSize: 11.5,
                            color: BiuTokens.textSecondary,
                            fontFamily: 'SF Mono',
                          ),
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      if (isRunning)
                        const SizedBox(
                          width: 12,
                          height: 12,
                          child: CircularProgressIndicator(strokeWidth: 1.5),
                        )
                      else if (isError)
                        Icon(Icons.close_rounded,
                            size: 14, color: SemanticTokens.error)
                      else
                        Icon(Icons.check_rounded,
                            size: 14, color: BiuTokens.green),
                    ],
                  ),
                ),
                // Body
                if (result != null) _buildBody(context, start, result!),
              ],
            ),
          ),
        ),
      ),
    );
  }

  static Widget _toolIcon(String name) {
    final (icon, color) = switch (name) {
      'Read' => (Icons.description_outlined, BiuTokens.purple),
      'Edit' || 'MultiEdit' => (Icons.edit_outlined, BiuTokens.purple),
      'Write' => (Icons.note_add_outlined, BiuTokens.purple),
      'Bash' => (Icons.terminal_rounded, Colors.orange),
      'Grep' => (Icons.search_rounded, BiuTokens.purple),
      'Glob' => (Icons.folder_open_outlined, BiuTokens.purple),
      'WebFetch' || 'WebSearch' => (Icons.public_rounded, Colors.teal),
      _ => (Icons.build_rounded, BiuTokens.textSecondary),
    };
    return Icon(icon, size: 14, color: color);
  }

  static String _argSummary(ToolUseStart s) {
    return switch (s.name) {
      'Read' || 'Write' => (s.args['path'] ?? s.args['file_path'] ?? '').toString(),
      'Edit' || 'MultiEdit' => (s.args['file_path'] ?? '').toString(),
      'Bash' => (s.args['command'] ?? '').toString(),
      'Grep' => '${s.args['pattern'] ?? ''} ${s.args['path'] ?? ''}',
      'Glob' => (s.args['pattern'] ?? '').toString(),
      _ => s.args.isEmpty ? '' : s.args.toString(),
    };
  }

  Widget _buildBody(BuildContext ctx, ToolUseStart start, ToolUseResult result) {
    final body = switch (start.name) {
      'Edit' || 'MultiEdit' => _DiffPreview(
          oldString: start.args['old_string']?.toString() ?? '',
          newString: start.args['new_string']?.toString() ?? '',
          summary: result.result,
        ),
      'Bash' => _BashOutput(
          command: start.args['command']?.toString() ?? '',
          output: result.result,
        ),
      _ => _GenericOutput(text: result.result, isError: result.isError),
    };
    return Container(
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: const BorderRadius.only(
          bottomLeft: Radius.circular(BiuTokens.radiusSm),
          bottomRight: Radius.circular(BiuTokens.radiusSm),
        ),
      ),
      padding: const EdgeInsets.all(10),
      child: body,
    );
  }
}

class _GenericOutput extends StatelessWidget {
  const _GenericOutput({required this.text, required this.isError});
  final String text;
  final bool isError;

  @override
  Widget build(BuildContext context) {
    final preview = text.length > 800 ? '${text.substring(0, 800)}…' : text;
    return SelectableText(
      preview,
      style: TextStyle(
        fontSize: 11.5,
        fontFamily: 'SF Mono',
        height: 1.5,
        color: isError ? Colors.red.shade700 : BiuTokens.textSecondary,
      ),
    );
  }
}

class _BashOutput extends StatelessWidget {
  const _BashOutput({required this.command, required this.output});
  final String command;
  final String output;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '\$ $command',
          style: TextStyle(
            fontSize: 11.5,
            fontFamily: 'SF Mono',
            color: BiuTokens.purple,
            fontWeight: FontWeight.w600,
          ),
        ),
        if (output.trim().isNotEmpty) ...[
          const SizedBox(height: 6),
          SelectableText(
            output.length > 800 ? '${output.substring(0, 800)}…' : output,
            style: const TextStyle(fontSize: 11.5, fontFamily: 'SF Mono', height: 1.4),
          ),
        ],
      ],
    );
  }
}

/// 简单 diff 预览：找 old → new 的差异行高亮（P0 行级，P2 升级 word-level）
class _DiffPreview extends StatelessWidget {
  const _DiffPreview({
    required this.oldString,
    required this.newString,
    required this.summary,
  });
  final String oldString;
  final String newString;
  final String summary;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // 删除行
        if (oldString.isNotEmpty)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            color: Colors.red.shade50,
            child: SelectableText(
              oldString.split('\n').map((l) => '- $l').join('\n'),
              style: TextStyle(
                fontSize: 11.5,
                fontFamily: 'SF Mono',
                color: Colors.red.shade900,
                height: 1.4,
              ),
            ),
          ),
        // 新增行
        if (newString.isNotEmpty)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            color: Colors.green.shade50,
            child: SelectableText(
              newString.split('\n').map((l) => '+ $l').join('\n'),
              style: TextStyle(
                fontSize: 11.5,
                fontFamily: 'SF Mono',
                color: Colors.green.shade900,
                height: 1.4,
              ),
            ),
          ),
        const SizedBox(height: 4),
        Text(
          summary,
          style: TextStyle(fontSize: 11, color: BiuTokens.textSecondary),
        ),
      ],
    );
  }
}
