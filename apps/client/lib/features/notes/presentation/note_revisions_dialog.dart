/// 历史版本弹层（N3）—— 左版本列表 + 右预览（标题 + 正文纯文本）。
///
/// 数据纯服务端（notesRepository.listRevisions/getRevision），列表走
/// noteRevisionsProvider，预览按需拉单版本详情（列表响应不含 content_md）。
///
/// 两个动作：
///   * 恢复到此版本 —— 二次确认 → restoreRevision（服务端自动备份当前
///     状态）→ 返回 note 落 Drift，编辑器经 noteByIdProvider 流自动刷新；
///     恢复会新增恢复点，故 invalidate 版本列表。
///   * 另存为副本 —— saveRevisionAsCopy → 新 note 落 Drift（笔记列表是
///     Drift watch 流，自动刷新）→ toast「已另存为新笔记」。
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../data/api/notes_client.dart' as api;
import '../../../data/notes_providers.dart';
import '../application/notes_ui_providers.dart';
import 'notes_home_page.dart' show relativeTime;

/// change_type 显示文案：edit → 编辑，restore → 恢复点，未知值原样显示。
String revisionChangeTypeLabel(String changeType) => switch (changeType) {
      'edit' => '编辑',
      'restore' => '恢复点',
      _ => changeType,
    };

/// 从历史版本弹层返回的结果（调用方不需要区分，关闭即刷新靠 Drift 流）。
class NoteRevisionsDialog extends ConsumerStatefulWidget {
  const NoteRevisionsDialog({super.key, required this.noteId});

  final String noteId;

  /// 以 showDialog 打开。
  static Future<void> show(BuildContext context, {required String noteId}) {
    return showDialog<void>(
      context: context,
      builder: (_) => NoteRevisionsDialog(noteId: noteId),
    );
  }

  @override
  ConsumerState<NoteRevisionsDialog> createState() =>
      _NoteRevisionsDialogState();
}

class _NoteRevisionsDialogState extends ConsumerState<NoteRevisionsDialog> {
  /// 当前选中预览的版本 id + 详情 future（详情含 content_md，按需拉）。
  String? _selectedId;
  Future<api.NoteRevision>? _detail;

  /// 动作进行中（防连点）。
  bool _acting = false;

  void _select(api.NoteRevision rev) {
    if (_selectedId == rev.id) return;
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    setState(() {
      _selectedId = rev.id;
      _detail = repo.getRevision(widget.noteId, rev.id);
    });
  }

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _restore() async {
    final rid = _selectedId;
    final repo = ref.read(notesRepositoryProvider);
    if (rid == null || repo == null || _acting) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('恢复到此版本'),
        content: const Text('当前内容会被该版本覆盖（服务端会自动备份当前状态）。'),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('恢复'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    setState(() => _acting = true);
    try {
      await repo.restoreRevision(widget.noteId, rid);
      // 恢复会新增一条恢复点，刷新版本列表；编辑器内容靠 Drift 流自动刷新。
      ref.invalidate(noteRevisionsProvider(widget.noteId));
      _toast('已恢复到该版本');
      if (mounted) Navigator.of(context).pop();
    } on Exception catch (e) {
      _toast('恢复失败：$e');
    } finally {
      if (mounted) setState(() => _acting = false);
    }
  }

  Future<void> _saveAsCopy() async {
    final rid = _selectedId;
    final repo = ref.read(notesRepositoryProvider);
    if (rid == null || repo == null || _acting) return;
    setState(() => _acting = true);
    try {
      await repo.saveRevisionAsCopy(widget.noteId, rid);
      // 新笔记落 Drift，中栏列表（watch 流）自动刷新。
      ref.invalidate(noteRevisionsProvider(widget.noteId));
      _toast('已另存为新笔记');
    } on Exception catch (e) {
      _toast('另存失败：$e');
    } finally {
      if (mounted) setState(() => _acting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final size = MediaQuery.sizeOf(context);
    final revisions = ref.watch(noteRevisionsProvider(widget.noteId));
    return Dialog(
      insetPadding: const EdgeInsets.all(24),
      child: SizedBox(
        width: size.width < 720 ? size.width - 48 : 672,
        height: size.height < 520 ? size.height - 48 : 480,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: <Widget>[
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 8, 8),
              child: Row(
                children: <Widget>[
                  const Expanded(
                    child: Text(
                      '历史版本',
                      style:
                          TextStyle(fontSize: 15, fontWeight: FontWeight.w600),
                    ),
                  ),
                  IconButton(
                    tooltip: '关闭',
                    onPressed: () => Navigator.of(context).pop(),
                    icon: const Icon(Icons.close, size: 18),
                  ),
                ],
              ),
            ),
            Divider(height: 1, color: BiuTokens.borderSubtle),
            Expanded(
              child: revisions.when(
                loading: () =>
                    const Center(child: CircularProgressIndicator()),
                error: (e, _) => Center(
                  child: Text(
                    '加载失败：$e',
                    style:
                        TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                  ),
                ),
                data: (list) => list.isEmpty
                    ? Center(
                        child: Text(
                          '暂无历史版本',
                          style: TextStyle(
                              fontSize: 12, color: BiuTokens.textMuted),
                        ),
                      )
                    : Row(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: <Widget>[
                          SizedBox(
                            width: 232,
                            child: ListView.builder(
                              itemCount: list.length,
                              itemBuilder: (_, i) =>
                                  _RevisionTile(
                                revision: list[i],
                                selected: list[i].id == _selectedId,
                                onTap: () => _select(list[i]),
                              ),
                            ),
                          ),
                          VerticalDivider(
                              width: 1, color: BiuTokens.borderSubtle),
                          Expanded(
                            child: _RevisionPreview(
                              detail: _detail,
                              acting: _acting,
                              onRestore: _restore,
                              onSaveAsCopy: _saveAsCopy,
                            ),
                          ),
                        ],
                      ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _RevisionTile extends StatelessWidget {
  const _RevisionTile({
    required this.revision,
    required this.selected,
    required this.onTap,
  });

  final api.NoteRevision revision;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Container(
        color: selected ? BiuTokens.purpleSoft : null,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: <Widget>[
            Row(
              children: <Widget>[
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                  decoration: BoxDecoration(
                    color: BiuTokens.surfaceMuted,
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Text(
                    revisionChangeTypeLabel(revision.changeType),
                    style: TextStyle(
                        fontSize: 10, color: BiuTokens.textSecondary),
                  ),
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    relativeTime(revision.createdAt),
                    style:
                        TextStyle(fontSize: 11, color: BiuTokens.textMuted),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            if (revision.changeSummary.isNotEmpty) ...<Widget>[
              const SizedBox(height: 3),
              Text(
                revision.changeSummary,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 11, color: BiuTokens.textSecondary),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _RevisionPreview extends StatelessWidget {
  const _RevisionPreview({
    required this.detail,
    required this.acting,
    required this.onRestore,
    required this.onSaveAsCopy,
  });

  final Future<api.NoteRevision>? detail;
  final bool acting;
  final VoidCallback onRestore;
  final VoidCallback onSaveAsCopy;

  @override
  Widget build(BuildContext context) {
    final future = detail;
    if (future == null) {
      return Center(
        child: Text(
          '选择左侧版本查看内容',
          style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
        ),
      );
    }
    return FutureBuilder<api.NoteRevision>(
      future: future,
      builder: (context, snap) {
        if (snap.hasError) {
          return Center(
            child: Text(
              '加载失败：${snap.error}',
              style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
            ),
          );
        }
        final rev = snap.data;
        if (rev == null) {
          return const Center(child: CircularProgressIndicator());
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: <Widget>[
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 6),
              child: Text(
                rev.title.isEmpty ? '无标题笔记' : rev.title,
                style:
                    const TextStyle(fontSize: 14, fontWeight: FontWeight.w600),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            Expanded(
              child: SingleChildScrollView(
                padding:
                    const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
                child: SelectableText(
                  rev.contentMd ?? '',
                  style: TextStyle(
                      fontSize: 12, height: 1.5, color: BiuTokens.text),
                ),
              ),
            ),
            Divider(height: 1, color: BiuTokens.borderSubtle),
            Padding(
              padding: const EdgeInsets.all(8),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: <Widget>[
                  TextButton(
                    onPressed: acting ? null : onSaveAsCopy,
                    child: const Text('另存为副本'),
                  ),
                  const SizedBox(width: 8),
                  FilledButton(
                    onPressed: acting ? null : onRestore,
                    child: const Text('恢复到此版本'),
                  ),
                ],
              ),
            ),
          ],
        );
      },
    );
  }
}
