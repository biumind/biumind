// FollowUpChipsV2 —— assistant 完成后下方推荐追问。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 FollowUp）。
//
// v1 实现：3 个静态 chip 直接 inject 到 composer。背后接低成本模型生成
// 真"针对此回答的"建议是后续工作（需要 brain 端跑廉价模型 / 客户端跑
// 本地小模型）。先把交互层做好。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../application/draft_history_controller.dart';

class FollowUpChipsV2 extends ConsumerWidget {
  const FollowUpChipsV2({super.key});

  static const _suggestions = <_Suggestion>[
    _Suggestion(
      icon: Icons.unfold_more,
      label: '再展开讲讲',
      prompt: '请把上一条回答里你觉得最重要的部分再展开讲讲，给出具体细节和例子。',
    ),
    _Suggestion(
      icon: Icons.lightbulb_outline,
      label: '举个例子',
      prompt: '请围绕上一条回答给一个真实可落地的具体例子，越具体越好。',
    ),
    _Suggestion(
      icon: Icons.compress,
      label: '更简洁',
      prompt: '请把上一条回答压缩成 3 条要点 + 一句总结，去掉冗余。',
    ),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Wrap(
        spacing: 6,
        runSpacing: 6,
        children: [
          for (final s in _suggestions)
            _FollowUpChip(
              suggestion: s,
              onTap: () => ref
                  .read(composerInjectProvider.notifier)
                  .inject(s.prompt),
            ),
        ],
      ),
    );
  }
}

class _FollowUpChip extends StatelessWidget {
  const _FollowUpChip({required this.suggestion, required this.onTap});
  final _Suggestion suggestion;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: theme.colorScheme.surfaceContainerLow,
      borderRadius: BorderRadius.circular(999),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(999),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
          decoration: BoxDecoration(
            border: Border.all(color: theme.colorScheme.outlineVariant),
            borderRadius: BorderRadius.circular(999),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(suggestion.icon,
                  size: 12, color: theme.colorScheme.onSurfaceVariant),
              const SizedBox(width: 6),
              Text(
                suggestion.label,
                style: theme.textTheme.labelSmall?.copyWith(
                  color: theme.colorScheme.onSurface,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Suggestion {
  const _Suggestion({
    required this.icon,
    required this.label,
    required this.prompt,
  });
  final IconData icon;
  final String label;
  final String prompt;
}
