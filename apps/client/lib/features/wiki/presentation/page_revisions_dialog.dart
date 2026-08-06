/// Wiki 页历史版本弹层（迁移 00065）—— 左版本列表 + 右预览（标题 + blocks）。
///
/// 镜像 note_revisions_dialog（N3），差异：正文非单 content_md 字段，预览渲染
/// 该版本的 blocks_json（写前全部 live blocks 快照）。数据纯服务端：
/// pageRevisionsProvider 拉列表（不含 blocks_json），repo.getPageRevision 按需拉详情。
///
/// 两个动作：
///   * 恢复到此版本 —— 二次确认 → restorePageRevision（服务端 block 对账 + 自动
///     备份当前态）→ 本地 Drift 对账 → 编辑器经 watch 流自动刷新；恢复新增恢复点，
///     故 invalidate 版本列表。
///   * 另存为副本 —— savePageRevisionAsCopy → 新页落 Drift（页列表 watch 流刷新）。
///
/// 与 changelog_dialog 职责分离：changelog = 谁做了什么事件时间线（无 restore）；
/// 本弹层 = 内容快照恢复。
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../data/api/wiki_client.dart' as api;
import '../../../data/wiki_providers.dart';

/// change_type 显示文案：edit → 编辑，restore → 恢复点，未知值原样显示。
String pageRevisionChangeTypeLabel(String changeType) => switch (changeType) {
      'edit' => '编辑',
      'restore' => '恢复点',
      _ => changeType,
    };

class PageRevisionsDialog extends ConsumerStatefulWidget {
  const PageRevisionsDialog({
    super.key,
    required this.projectId,
    required this.pageId,
    required this.pageTitle,
  });

  final String projectId;
  final String pageId;
  final String pageTitle;

  static Future<void> show(
    BuildContext context, {
    required String projectId,
    required String pageId,
    required String pageTitle,
  }) {
    return showDialog<void>(
      context: context,
      builder: (_) => PageRevisionsDialog(
        projectId: projectId,
        pageId: pageId,
        pageTitle: pageTitle,
      ),
    );
  }

  @override
  ConsumerState<PageRevisionsDialog> createState() =>
      _PageRevisionsDialogState();
}

class _PageRevisionsDialogState extends ConsumerState<PageRevisionsDialog> {
  String? _selectedId;
  Future<api.WikiPageRevision>? _detail;
  bool _acting = false;

  void _select(api.WikiPageRevision rev) {
    if (_selectedId == rev.id) return;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    setState(() {
      _selectedId = rev.id;
      _detail = repo.getPageRevision(widget.projectId, widget.pageId, rev.id);
    });
  }

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _restore() async {
    final rid = _selectedId;
    final repo = ref.read(wikiRepositoryProvider);
    if (rid == null || repo == null || _acting) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('恢复到此版本'),
        content: const Text('当前页面会被该版本覆盖（服务端会自动备份当前状态）。'),
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
      await repo.restorePageRevision(widget.projectId, widget.pageId, rid);
      // 恢复新增一条恢复点；编辑器内容靠 Drift watch 流自动刷新。
      ref.invalidate(pageRevisionsProvider(
        (projectId: widget.projectId, pageId: widget.pageId),
      ));
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
    final repo = ref.read(wikiRepositoryProvider);
    if (rid == null || repo == null || _acting) return;
    setState(() => _acting = true);
    try {
      await repo.savePageRevisionAsCopy(widget.projectId, widget.pageId, rid);
      ref.invalidate(pageRevisionsProvider(
        (projectId: widget.projectId, pageId: widget.pageId),
      ));
      _toast('已另存为新页面');
    } on Exception catch (e) {
      _toast('另存失败：$e');
    } finally {
      if (mounted) setState(() => _acting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final size = MediaQuery.sizeOf(context);
    final revisions = ref.watch(pageRevisionsProvider(
      (projectId: widget.projectId, pageId: widget.pageId),
    ));
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
                              itemBuilder: (_, i) => _RevisionTile(
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

  final api.WikiPageRevision revision;
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
                    pageRevisionChangeTypeLabel(revision.changeType),
                    style: TextStyle(
                        fontSize: 10, color: BiuTokens.textSecondary),
                  ),
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    revision.createdAt.toLocal().toString().substring(0, 16),
                    style:
                        TextStyle(fontSize: 11, color: BiuTokens.textMuted),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 3),
            Text(
              revision.title.isEmpty ? '无标题' : revision.title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(fontSize: 11, color: BiuTokens.textSecondary),
            ),
            if (revision.changeSummary.isNotEmpty) ...<Widget>[
              const SizedBox(height: 2),
              Text(
                revision.changeSummary,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 10, color: BiuTokens.textMuted),
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

  final Future<api.WikiPageRevision>? detail;
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
    return FutureBuilder<api.WikiPageRevision>(
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
        final blocks = rev.blocksJson ?? const <Map<String, dynamic>>[];
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: <Widget>[
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 6),
              child: Text(
                rev.title.isEmpty ? '无标题' : rev.title,
                style:
                    const TextStyle(fontSize: 14, fontWeight: FontWeight.w600),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            Expanded(
              child: blocks.isEmpty
                  ? Center(
                      child: Text(
                        '空页面',
                        style: TextStyle(
                            fontSize: 12, color: BiuTokens.textMuted),
                      ),
                    )
                  : ListView.builder(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 4),
                      itemCount: blocks.length,
                      itemBuilder: (_, i) {
                        final b = blocks[i];
                        return _BlockPreviewRow(
                          type: (b['type'] as String?) ?? 'text',
                          content:
                              (b['content'] as Map?)?.cast<String, dynamic>() ??
                                  const {},
                        );
                      },
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

/// 单 block 预览行：type 徽章 + 文本内容（text/heading 取 content.text）。
class _BlockPreviewRow extends StatelessWidget {
  const _BlockPreviewRow({required this.type, required this.content});

  final String type;
  final Map<String, dynamic> content;

  @override
  Widget build(BuildContext context) {
    final text = (content['text'] as String?) ?? '';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
            decoration: BoxDecoration(
              color: BiuTokens.surfaceMuted,
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              type,
              style: TextStyle(fontSize: 9, color: BiuTokens.textMuted),
            ),
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              text.isEmpty ? '（空）' : text,
              style: TextStyle(fontSize: 12, height: 1.4, color: BiuTokens.text),
              maxLines: 4,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}
