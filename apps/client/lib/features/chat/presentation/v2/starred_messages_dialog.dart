// StarredMessagesDialogV2 —— 跨 thread 看所有"⭐ 收藏"消息。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 收藏侧栏）。
//
// 行为：
//   * watch ChatRepo.watchStarredMessageHits()
//   * 每行：thread title + role icon + 消息 snippet + starredAt 相对时间
//   * 点击 → 关 dialog，调 onPick(threadId, messageId) 让父级切 thread +
//     pendingScroll 锚到目标消息

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme/category_colors.dart';
import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/greeting.dart';

Future<void> showStarredMessagesDialog(
  BuildContext context, {
  required void Function(String threadId, String messageId) onPick,
}) {
  return showAdaptiveDialog<void>(
    context: context,
    builder: (_) => _StarredMessagesDialog(onPick: onPick),
  );
}

class _StarredMessagesDialog extends ConsumerWidget {
  const _StarredMessagesDialog({required this.onPick});
  final void Function(String threadId, String messageId) onPick;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final repo = ref.watch(chatControllerDepsProvider).repo;
    return AdaptiveDialogFrame(
      maxWidth: 720,
      maxHeight: 600,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            decoration: BoxDecoration(
              border: Border(
                bottom: BorderSide(color: theme.colorScheme.outlineVariant),
              ),
            ),
            child: Row(
              children: [
                const Icon(Icons.star, size: 18, color: StarredColors.icon),
                const SizedBox(width: 8),
                Text(
                  l.chatV2StarredTitle,
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const Spacer(),
                IconButton(
                  icon: const Icon(Icons.close, size: 18),
                  tooltip: l.chatV2ArchivedClose,
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          Expanded(
            child: StreamBuilder<List<StarredMessageHit>>(
              stream: repo.watchStarredMessageHits(),
              builder: (ctx, snap) {
                if (snap.connectionState == ConnectionState.waiting &&
                    !snap.hasData) {
                  return const Center(child: CircularProgressIndicator());
                }
                final items = snap.data ?? const <StarredMessageHit>[];
                if (items.isEmpty) {
                  return Center(
                    child: Padding(
                      padding: const EdgeInsets.all(32),
                      child: Text(
                        l.chatV2StarredEmpty,
                        textAlign: TextAlign.center,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ),
                  );
                }
                return ListView.separated(
                  padding: const EdgeInsets.symmetric(vertical: 6),
                  itemCount: items.length,
                  separatorBuilder: (_, _) => Divider(
                    height: 1,
                    color: theme.colorScheme.outlineVariant.withValues(
                      alpha: 0.5,
                    ),
                  ),
                  itemBuilder: (_, i) => _StarredRow(
                    hit: items[i],
                    onTap: () {
                      Navigator.of(context).pop();
                      onPick(items[i].threadId, items[i].messageId);
                    },
                  ),
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _StarredRow extends StatelessWidget {
  const _StarredRow({required this.hit, required this.onTap});
  final StarredMessageHit hit;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(
              hit.role == MessageRole.user
                  ? Icons.person_outline
                  : Icons.smart_toy_outlined,
              size: 16,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    hit.threadTitle.isEmpty
                        ? l.chatV2NewThreadFallback
                        : hit.threadTitle,
                    style: theme.textTheme.labelMedium?.copyWith(
                      color: theme.colorScheme.primary,
                      fontWeight: FontWeight.w600,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    hit.snippet.isEmpty ? l.chatV2StarredNoText : hit.snippet,
                    style: theme.textTheme.bodyMedium,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    relativeTime(hit.starredAt),
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
