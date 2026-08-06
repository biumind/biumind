/// 回收站 —— /notes/trash。
///
/// 已删笔记列表（按丢弃时间倒序，DAO 排好）+ 还原 / 彻底删除（二次确认）。
/// 还原 / 删除走 repository 乐观落库 + outbox，离线可用。
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../data/notes_providers.dart';
import '../../../data/notes_repository.dart';
import '../application/notes_ui_providers.dart';
import 'notes_home_page.dart' show relativeTime;

class NotesTrashPage extends ConsumerWidget {
  const NotesTrashPage({super.key});

  Future<void> _restore(WidgetRef ref, RepoNote note) async {
    await ref.read(notesRepositoryProvider)?.restoreNote(note.id);
  }

  Future<void> _purge(BuildContext context, WidgetRef ref, RepoNote note) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('彻底删除'),
        content: Text('「${note.title.isEmpty ? '无标题笔记' : note.title}」'
            '将被永久删除，无法恢复。'),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('彻底删除'),
          ),
        ],
      ),
    );
    if (confirmed == true) {
      await ref.read(notesRepositoryProvider)?.purgeNote(note.id);
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // 保持 flusher + 轮询活着（还原/删除的 outbox 需要冲刷）。
    ref.watch(notesSyncPollerProvider);

    final notes = ref.watch(notesTrashProvider).valueOrNull;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: <Widget>[
        Container(
          height: 44,
          color: BiuTokens.surface,
          padding: const EdgeInsets.symmetric(horizontal: 8),
          child: Row(
            children: <Widget>[
              IconButton(
                tooltip: '返回笔记',
                onPressed: () => context.go('/notes'),
                icon: const Icon(Icons.arrow_back, size: 18),
                padding: EdgeInsets.zero,
                constraints:
                    const BoxConstraints(minWidth: 28, minHeight: 28),
              ),
              const SizedBox(width: 8),
              Text(
                '回收站',
                style: TextStyle(
                  color: BiuTokens.text,
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: notes == null
              ? const Center(child: CircularProgressIndicator())
              : notes.isEmpty
                  ? Center(
                      child: Text(
                        '回收站是空的',
                        style: TextStyle(
                            color: BiuTokens.textMuted, fontSize: 13),
                      ),
                    )
                  : ListView.separated(
                      itemCount: notes.length,
                      separatorBuilder: (_, _) => Divider(
                          height: 1, color: BiuTokens.borderSubtle),
                      itemBuilder: (context, i) {
                        final note = notes[i];
                        return ListTile(
                          title: Text(
                            note.title.isEmpty ? '无标题笔记' : note.title,
                            style: TextStyle(
                                fontSize: 13, color: BiuTokens.text),
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                          subtitle: Text(
                            note.trashedAt == null
                                ? ''
                                : '删除于 ${relativeTime(note.trashedAt!)}',
                            style: TextStyle(
                                fontSize: 11, color: BiuTokens.textMuted),
                          ),
                          trailing: Row(
                            mainAxisSize: MainAxisSize.min,
                            children: <Widget>[
                              TextButton(
                                onPressed: () => _restore(ref, note),
                                child: const Text('还原'),
                              ),
                              TextButton(
                                onPressed: () => _purge(context, ref, note),
                                child: Text(
                                  '彻底删除',
                                  style: TextStyle(color: BiuTokens.error),
                                ),
                              ),
                            ],
                          ),
                        );
                      },
                    ),
        ),
      ],
    );
  }
}
