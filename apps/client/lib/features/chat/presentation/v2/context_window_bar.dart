// ContextWindowBarV2 —— thread 累计 token 进度条。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P1-8。
//
// 显示位置：AppBar 下方一条细 progress；hover 显示具体数字。
// 累计：所有 message 的 input + output tokens 之和（已落库的 prompt/completion）。
// model 未知 → contextWindowFor 兜底 8k。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/token_estimate.dart';

class ContextWindowBarV2 extends ConsumerWidget {
  const ContextWindowBarV2({
    super.key,
    required this.threadId,
    required this.model,
  });

  final String threadId;
  final String? model;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final messagesAsync = ref.watch(messagesProvider(threadId));
    final used = messagesAsync.valueOrNull == null
        ? 0
        : _sumTokens(messagesAsync.value!);
    final ctx = contextWindowFor(model);
    if (used == 0) return const SizedBox.shrink();
    final ratio = (used / ctx).clamp(0.0, 1.0);
    final theme = Theme.of(context);
    final color = ratio > 0.85
        ? theme.colorScheme.error
        : ratio > 0.6
            ? Colors.orange
            : theme.colorScheme.primary;
    final pct = (ratio * 100).round();
    final l = AppLocalizations.of(context)!;
    return Tooltip(
      message: l.chatV2CtxBarTooltip(_fmt(used), _fmt(ctx), pct),
      child: Container(
        padding: const EdgeInsets.symmetric(
            horizontal: 12, vertical: 4),
        decoration: BoxDecoration(
          border: Border(
            bottom: BorderSide(
              color: theme.colorScheme.outlineVariant.withValues(alpha: 0.5),
            ),
          ),
        ),
        child: Row(
          children: [
            Expanded(
              child: ClipRRect(
                borderRadius: BorderRadius.circular(2),
                child: LinearProgressIndicator(
                  value: ratio,
                  minHeight: 3,
                  backgroundColor:
                      theme.colorScheme.surfaceContainerHighest,
                  valueColor: AlwaysStoppedAnimation<Color>(color),
                ),
              ),
            ),
            const SizedBox(width: 10),
            Text(
              '${_fmt(used)} / ${_fmt(ctx)} · $pct%',
              style: theme.textTheme.labelSmall?.copyWith(
                color: ratio > 0.85
                    ? theme.colorScheme.error
                    : theme.colorScheme.onSurfaceVariant,
                fontFeatures: const [FontFeature.tabularFigures()],
              ),
            ),
          ],
        ),
      ),
    );
  }

  static int _sumTokens(List<Message> messages) {
    var total = 0;
    for (final m in messages) {
      total += (m.inputTokens ?? 0) + (m.outputTokens ?? 0);
    }
    return total;
  }

  static String _fmt(int n) {
    if (n < 1000) return '$n';
    if (n < 100000) return '${(n / 1000).toStringAsFixed(1)}k';
    return '${(n / 1000).round()}k';
  }
}
