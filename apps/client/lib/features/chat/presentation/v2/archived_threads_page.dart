// ArchivedThreadsPageV2 —— 归档对话管理页。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 归档管理）。
//
// 行为：
//   * 全屏 modal，watch ChatRepo.watchArchivedThreads()
//   * 每条显示 title / 模式 / updatedAt；右侧 "解归档" + "永久删除" 按钮
//   * 永久删除走二次确认；解归档直接调 repo.unarchiveThread（视觉上立刻
//     从列表消失，因为 watch 流自然 reactive）

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/greeting.dart';

Future<void> showArchivedThreadsPage(BuildContext ctx) {
  return showAdaptiveDialog<void>(
    context: ctx,
    builder: (_) => const _ArchivedThreadsDialog(),
  );
}

class _ArchivedThreadsDialog extends ConsumerWidget {
  const _ArchivedThreadsDialog();

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
          // header
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            decoration: BoxDecoration(
              border: Border(
                bottom: BorderSide(color: theme.colorScheme.outlineVariant),
              ),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.archive_outlined,
                  size: 18,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 8),
                Text(
                  l.chatV2ArchivedTitle,
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
          // body
          Expanded(
            child: StreamBuilder<List<Thread>>(
              stream: repo.watchArchivedThreads(),
              builder: (ctx, snap) {
                if (snap.connectionState == ConnectionState.waiting) {
                  return const Center(child: CircularProgressIndicator());
                }
                final items = snap.data ?? const <Thread>[];
                if (items.isEmpty) {
                  return Center(
                    child: Padding(
                      padding: const EdgeInsets.all(32),
                      child: Text(
                        l.chatV2ArchivedEmpty,
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
                  itemBuilder: (_, i) => _ArchivedRow(thread: items[i]),
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _ArchivedRow extends ConsumerWidget {
  const _ArchivedRow({required this.thread});
  final Thread thread;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final repo = ref.watch(chatControllerDepsProvider).repo;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  thread.title.isEmpty
                      ? l.chatV2NewThreadFallback
                      : thread.title,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    fontWeight: FontWeight.w500,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Text(
                  '${_modeLabel(thread.mode)} · ${relativeTime(thread.updatedAt)}',
                  style: theme.textTheme.labelSmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
          ),
          TextButton.icon(
            icon: const Icon(Icons.unarchive_outlined, size: 14),
            label: Text(l.chatV2ArchivedUnarchive),
            style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
            onPressed: () => repo.unarchiveThread(thread.id),
          ),
          TextButton.icon(
            icon: const Icon(Icons.delete_outline, size: 14),
            label: Text(l.chatV2ArchivedHardDelete),
            style: TextButton.styleFrom(
              foregroundColor: theme.colorScheme.error,
              visualDensity: VisualDensity.compact,
            ),
            onPressed: () => _confirmDelete(context, ref),
          ),
        ],
      ),
    );
  }

  Future<void> _confirmDelete(BuildContext context, WidgetRef ref) async {
    final l = AppLocalizations.of(context)!;
    final title = thread.title.isEmpty
        ? l.chatV2NewThreadFallback
        : thread.title;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l.chatV2ArchivedHardDeleteTitle),
        content: Text(l.chatV2ArchivedHardDeleteBody(title)),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(l.chatV2DialogCancel),
          ),
          TextButton(
            style: TextButton.styleFrom(
              foregroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(l.chatV2DialogDelete),
          ),
        ],
      ),
    );
    if (ok != true) return;
    // ops: 先 best-effort 上行 brain 再本地级联删(跨设备一致)。
    await ref.read(chatThreadOpsProvider).deleteThread(thread.id);
  }

  static String _modeLabel(ThreadMode m) => switch (m) {
    ThreadMode.chat => 'Chat',
    ThreadMode.agent => 'Agent',
    ThreadMode.task => 'Task',
  };
}
