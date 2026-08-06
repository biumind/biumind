// EmptyThreadViewV2 —— thread 已选但还没消息时的占位（替换"开始你的对话…"）。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 空 thread 起点卡）。
//
// 比 HeroViewV2 紧凑：
//   * 小问候 + 一句副标题
//   * 4 张起点 prompt 卡（grid 2x2）；点击 → composerInjectProvider 注入

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../application/draft_history_controller.dart';
import '../../domain/greeting.dart';

class EmptyThreadViewV2 extends ConsumerWidget {
  const EmptyThreadViewV2({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final hour = DateTime.now().hour;
    final greeting = greetingForHour(hour);
    // 取前 4 张起点；不重复 Hero 全部 6 个（紧凑型）。
    final starters = kStarterPrompts.take(4).toList(growable: false);
    // 键盘顶起 / 矮屏时纵向可滚, 不再硬 Center 导致溢出 (方案 §4.4)。
    return LayoutBuilder(
      builder: (ctx, c) => SingleChildScrollView(
        child: ConstrainedBox(
          constraints: BoxConstraints(minHeight: c.maxHeight),
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 560),
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      greeting,
                      style: theme.textTheme.titleLarge?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      '挑一个起点开始，或者直接输入你想问的',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                    const SizedBox(height: 20),
                    LayoutBuilder(
                      builder: (ctx, c) {
                        final cardW = (c.maxWidth - 12) / 2;
                        return Wrap(
                          spacing: 12,
                          runSpacing: 12,
                          children: [
                            for (final p in starters)
                              SizedBox(
                                width: cardW,
                                child: _StarterCardSmall(
                                  prompt: p,
                                  onTap: () => ref
                                      .read(composerInjectProvider.notifier)
                                      .inject(p.prompt),
                                ),
                              ),
                          ],
                        );
                      },
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _StarterCardSmall extends StatelessWidget {
  const _StarterCardSmall({required this.prompt, required this.onTap});
  final StarterPrompt prompt;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: theme.colorScheme.surface,
      borderRadius: BorderRadius.circular(8),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(8),
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            border: Border.all(color: theme.colorScheme.outlineVariant),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                children: [
                  Icon(prompt.icon, size: 14, color: prompt.tone),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      prompt.title,
                      style: theme.textTheme.labelLarge?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 4),
              Text(
                prompt.prompt,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
