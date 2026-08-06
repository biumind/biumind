// DraftsDialogV2 —— 列所有 thread 当前的 composer 草稿。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 草稿索引）。
//
// 行为：
//   * 启动时 ComposerDraftStore.listAll() 一次取齐 Map<threadId, content>
//   * 每行 thread title + 草稿摘要（前 100 字） + 点击切到 thread + 删除按钮
//   * 删除 = ComposerDraftStore.clear(threadId)，列表 reactive 减一

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../application/composer_draft_store.dart';
import '../../domain/chat_models.dart';

Future<void> showDraftsDialog(
  BuildContext context, {
  required void Function(String threadId) onPick,
}) {
  return showAdaptiveDialog<void>(
    context: context,
    builder: (_) => _DraftsDialog(onPick: onPick),
  );
}

class _DraftsDialog extends ConsumerStatefulWidget {
  const _DraftsDialog({required this.onPick});
  final void Function(String threadId) onPick;

  @override
  ConsumerState<_DraftsDialog> createState() => _DraftsDialogState();
}

class _DraftsDialogState extends ConsumerState<_DraftsDialog> {
  Map<String, String>? _drafts;
  Map<String, Thread> _threads = const {};
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final drafts = await ComposerDraftStore.listAll();
    final repo = ref.read(chatControllerDepsProvider).repo;
    final threads = <String, Thread>{};
    for (final tid in drafts.keys) {
      final t = await repo.getThread(tid);
      if (t != null) threads[tid] = t;
    }
    if (!mounted) return;
    setState(() {
      _drafts = drafts;
      _threads = threads;
      _loading = false;
    });
  }

  Future<void> _delete(String tid) async {
    await ComposerDraftStore.clear(tid);
    setState(() {
      final next = {..._drafts ?? const <String, String>{}};
      next.remove(tid);
      _drafts = next;
    });
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final drafts = _drafts ?? const <String, String>{};
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
                Icon(
                  Icons.edit_note,
                  size: 18,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 8),
                Text(
                  l.chatV2DraftsTitle,
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
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : drafts.isEmpty
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(32),
                      child: Text(
                        l.chatV2DraftsEmpty,
                        textAlign: TextAlign.center,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ),
                  )
                : ListView.separated(
                    padding: const EdgeInsets.symmetric(vertical: 6),
                    itemCount: drafts.length,
                    separatorBuilder: (_, _) => Divider(
                      height: 1,
                      color: theme.colorScheme.outlineVariant.withValues(
                        alpha: 0.5,
                      ),
                    ),
                    itemBuilder: (_, i) {
                      final tid = drafts.keys.elementAt(i);
                      final content = drafts[tid] ?? '';
                      final t = _threads[tid];
                      return _DraftRow(
                        threadId: tid,
                        threadTitle: t?.title ?? '',
                        content: content,
                        onTap: () {
                          Navigator.of(context).pop();
                          widget.onPick(tid);
                        },
                        onDelete: () => _delete(tid),
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }
}

class _DraftRow extends StatelessWidget {
  const _DraftRow({
    required this.threadId,
    required this.threadTitle,
    required this.content,
    required this.onTap,
    required this.onDelete,
  });
  final String threadId;
  final String threadTitle;
  final String content;
  final VoidCallback onTap;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final preview = content.length > 100
        ? '${content.substring(0, 100)}…'
        : content;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(
              Icons.edit_outlined,
              size: 14,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    threadTitle.isEmpty ? l.chatV2DraftsUnnamed : threadTitle,
                    style: theme.textTheme.labelMedium?.copyWith(
                      color: theme.colorScheme.primary,
                      fontWeight: FontWeight.w600,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    preview.replaceAll('\n', ' '),
                    style: theme.textTheme.bodyMedium,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    l.chatV2DraftsCharCount(content.length),
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ),
            ),
            IconButton(
              icon: const Icon(Icons.delete_outline, size: 16),
              tooltip: l.chatV2DraftsDiscard,
              visualDensity: VisualDensity.compact,
              color: theme.colorScheme.error,
              onPressed: onDelete,
            ),
          ],
        ),
      ),
    );
  }
}
