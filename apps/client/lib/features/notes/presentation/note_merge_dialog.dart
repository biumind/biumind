// NoteMergeDialog —— 409 三方合并的用户裁决 UI。
//
// flusher 检测到 base/local/remote 有冲突段（同段两方都改且不同）时，emit
// 带 merge bundle 的 NoteOutboxConflict（segments 含 ResolvedMergeSegment +
// ConflictMergeSegment）。note_editor_view._onConflict 据此弹本对话框：
//   * ResolvedMergeSegment（base 未改 / 仅一方改 / 两方同改）→ 折叠只读预览。
//   * ConflictMergeSegment → 三栏展示 base/local/remote + 单选「保留本地 /
//     保留服务端 / 两者都保留」（默认两者，最不丢信息）。
// 确认合并 → 按 selection 重建全文 → repository.updateNote 写回（base 已 =
// remote，入队的 update_note baseVersion=remoteVersion，下轮 flush 落库）。
// 另存为副本 → repository.saveAsCopy 作逃生口（保留本地草稿为新笔记）。
//
// 静默自动合并（无冲突段）不发 UI，flusher 直接走 onAutoMergeResolved。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../data/note_merge.dart';
import '../../../data/notes_providers.dart';
import '../../../data/outbox/note_outbox_flusher.dart';
import '../application/notes_ui_providers.dart';

/// 每个冲突段的用户选择。
enum _ConflictChoice { local, remote, both }

class NoteMergeDialog extends ConsumerStatefulWidget {
  const NoteMergeDialog({
    super.key,
    required this.noteId,
    required this.conflict,
  });

  final String noteId;
  final NoteOutboxConflict conflict;

  static Future<void> show(
    BuildContext context, {
    required String noteId,
    required NoteOutboxConflict conflict,
  }) {
    return showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (_) => NoteMergeDialog(noteId: noteId, conflict: conflict),
    );
  }

  @override
  ConsumerState<NoteMergeDialog> createState() => _NoteMergeDialogState();
}

class _NoteMergeDialogState extends ConsumerState<NoteMergeDialog> {
  late final List<MergeSegment> _segments;
  late final List<_ConflictChoice> _choices;
  bool _applying = false;

  @override
  void initState() {
    super.initState();
    _segments = widget.conflict.segments ?? const [];
    _choices = List.filled(
        _segments.whereType<ConflictMergeSegment>().length, _ConflictChoice.both);
  }

  int get _conflictCount =>
      _segments.whereType<ConflictMergeSegment>().length;

  /// 按当前 selection 重建合并全文。
  String _buildResolved() {
    final parts = <String>[];
    var ci = 0;
    for (final seg in _segments) {
      switch (seg) {
        case ResolvedMergeSegment(:final text):
          if (text.isNotEmpty) parts.add(text);
        case ConflictMergeSegment(:final region):
          switch (_choices[ci]) {
            case _ConflictChoice.local:
              parts.add(region.local);
            case _ConflictChoice.remote:
              parts.add(region.remote);
            case _ConflictChoice.both:
              if (region.local.isNotEmpty) parts.add(region.local);
              if (region.remote.isNotEmpty) parts.add(region.remote);
          }
          ci++;
      }
    }
    return parts.where((p) => p.trim().isNotEmpty).join('\n\n');
  }

  Future<void> _apply() async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    setState(() => _applying = true);
    try {
      await repo.updateNote(widget.noteId, contentMd: _buildResolved());
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('合并失败：$e')));
      }
    } finally {
      if (mounted) setState(() => _applying = false);
    }
  }

  Future<void> _saveAsCopy() async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    try {
      final copy = await repo.saveAsCopy(widget.noteId);
      ref.read(selectedNoteIdProvider.notifier).state = copy.id;
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('已另存为副本，可手动合并后删除本条')),
        );
        Navigator.of(context).pop();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('另存失败：$e')));
      }
    }
  }

  void _setAll(_ConflictChoice choice) {
    setState(() {
      for (var i = 0; i < _choices.length; i++) {
        _choices[i] = choice;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final tt = Theme.of(context).textTheme;
    return AlertDialog(
      title: const Text('笔记冲突，请选择保留内容'),
      content: SizedBox(
        width: double.maxFinite,
        child: _applying
            ? const Padding(
                padding: EdgeInsets.symmetric(vertical: 32),
                child: Center(child: CircularProgressIndicator()),
              )
            : SingleChildScrollView(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '服务端与本机各有修改（$_conflictCount 处冲突）。'
                      '不冲突的部分已自动合并；冲突处请逐段选择保留哪一版。',
                      style: tt.bodySmall?.apply(color: BiuTokens.textMuted),
                    ),
                    const SizedBox(height: BiuTokens.space2),
                    ..._buildSegmentWidgets(),
                  ],
                ),
              ),
      ),
      actions: [
        TextButton(
          onPressed: _applying ? null : _saveAsCopy,
          child: const Text('另存为副本'),
        ),
        if (_choices.isNotEmpty) ...[
          TextButton(
            onPressed: _applying ? null : () => _setAll(_ConflictChoice.local),
            child: const Text('全部用本地'),
          ),
          TextButton(
            onPressed: _applying ? null : () => _setAll(_ConflictChoice.remote),
            child: const Text('全部用服务端'),
          ),
        ],
        FilledButton(
          onPressed: _applying ? null : _apply,
          child: const Text('确认合并'),
        ),
      ],
    );
  }

  List<Widget> _buildSegmentWidgets() {
    final widgets = <Widget>[];
    var ci = 0;
    var autoIdx = 0;
    for (final seg in _segments) {
      switch (seg) {
        case ResolvedMergeSegment(:final text):
          autoIdx++;
          widgets.add(_AutoMergedTile(index: autoIdx, text: text));
        case ConflictMergeSegment(:final region):
          widgets.add(_ConflictCard(
            index: ci + 1,
            region: region,
            choice: _choices[ci],
            onChanged: (c) => setState(() => _choices[ci] = c),
          ));
          ci++;
      }
    }
    return widgets;
  }
}

/// 已自动合并段（折叠预览，默认收起减少噪声）。
class _AutoMergedTile extends StatelessWidget {
  const _AutoMergedTile({required this.index, required this.text});
  final int index;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space1),
      child: Container(
        decoration: BoxDecoration(
          color: BiuTokens.surfaceMuted,
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        ),
        child: ExpansionTile(
          initiallyExpanded: false,
          dense: true,
          tilePadding: const EdgeInsets.symmetric(horizontal: BiuTokens.space2),
          shape: const Border(),
          title: Text('已自动合并（第 $index 段）',
              style: Theme.of(context)
                  .textTheme
                  .bodySmall
                  ?.apply(color: BiuTokens.textMuted)),
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(
                  BiuTokens.space2, 0, BiuTokens.space2, BiuTokens.space2),
              child: Align(
                alignment: Alignment.centerLeft,
                child: Text(
                  text,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 一个冲突段的三栏展示 + 单选。
class _ConflictCard extends StatelessWidget {
  const _ConflictCard({
    required this.index,
    required this.region,
    required this.choice,
    required this.onChanged,
  });

  final int index;
  final MergeRegion region;
  final _ConflictChoice choice;
  final ValueChanged<_ConflictChoice> onChanged;

  @override
  Widget build(BuildContext context) {
    final tt = Theme.of(context).textTheme;
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space2),
      child: Container(
        padding: const EdgeInsets.all(BiuTokens.space2),
        decoration: BoxDecoration(
          border: Border.all(color: BiuTokens.borderSubtle),
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('冲突 $index', style: tt.titleSmall),
            const SizedBox(height: BiuTokens.space1),
            _versionBlock('原版', region.base, BiuTokens.textMuted, tt),
            const SizedBox(height: BiuTokens.space1),
            _versionBlock('本机', region.local, BiuTokens.purple, tt),
            const SizedBox(height: BiuTokens.space1),
            _versionBlock('服务端', region.remote, BiuTokens.green, tt),
            const SizedBox(height: BiuTokens.space1),
            Wrap(
              spacing: BiuTokens.space1,
              children: [
                _choiceChip('保留本机', _ConflictChoice.local),
                _choiceChip('保留服务端', _ConflictChoice.remote),
                _choiceChip('两者都保留', _ConflictChoice.both),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _choiceChip(String label, _ConflictChoice value) {
    final selected = choice == value;
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: (_) => onChanged(value),
      selectedColor: BiuTokens.purple.withValues(alpha: 0.18),
    );
  }

  Widget _versionBlock(
      String label, String text, Color color, TextTheme tt) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(BiuTokens.space1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label,
              style: tt.labelSmall?.apply(color: color, fontWeightDelta: 2)),
          const SizedBox(height: 2),
          Text(
            text.isEmpty ? '（空）' : text,
            style: tt.bodySmall,
          ),
        ],
      ),
    );
  }
}
