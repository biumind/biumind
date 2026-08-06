// ChangelogDialog — chronological event timeline for one wiki page.
//
// Reads /v1/wiki/projects/{pid}/pages/{id}/changelog and renders a
// vertical timeline grouped by day. Each event_type gets its own
// icon + label so a glance at the timeline tells you "block edited 3
// times this morning, page renamed yesterday, created last week".
//
// We don't show diffs — events carry the post-change content, not a
// patch. The block.updated row shows the new text excerpt; comparing
// to the previous row visually is good enough for a self-serve
// audit log.

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/adaptive_dialog.dart';
import '../../../data/api/wiki_client.dart';
import '../../../data/wiki_providers.dart';

Future<void> showChangelogDialog(
  BuildContext context, {
  required String projectId,
  required String pageId,
  required String pageTitle,
}) {
  // 宽屏 = showDialog(barrierColor black54, dismissible 默认 true) 与原来
  // 一致；手机 = bottom sheet，AlertDialog 内容宽度自适应钳位 (§4.5)。
  return showAdaptiveDialog<void>(
    context: context,
    builder: (_) => ChangelogDialog(
      projectId: projectId,
      pageId: pageId,
      pageTitle: pageTitle,
    ),
  );
}

class ChangelogDialog extends ConsumerStatefulWidget {
  const ChangelogDialog({
    super.key,
    required this.projectId,
    required this.pageId,
    required this.pageTitle,
  });

  final String projectId;
  final String pageId;
  final String pageTitle;

  @override
  ConsumerState<ChangelogDialog> createState() => _ChangelogDialogState();
}

class _ChangelogDialogState extends ConsumerState<ChangelogDialog> {
  Future<List<WikiPageEvent>>? _future;

  @override
  void initState() {
    super.initState();
    _future = _load();
  }

  Future<List<WikiPageEvent>> _load() async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) throw StateError('请先登录');
    return repo.client.listChangelog(widget.projectId, widget.pageId);
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Row(
        children: [
          const Icon(Icons.history, size: 18),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: Text(
              '历史 · ${widget.pageTitle.isEmpty ? "(未命名)" : widget.pageTitle}',
              style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
      content: SizedBox(
        width: 580,
        height: 480,
        child: FutureBuilder<List<WikiPageEvent>>(
          future: _future,
          builder: (_, snap) {
            if (snap.connectionState != ConnectionState.done) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snap.hasError) {
              return Center(
                child: Text(
                  snap.error.toString(),
                  style: const TextStyle(color: BiuTokens.error),
                ),
              );
            }
            final events = snap.data ?? const <WikiPageEvent>[];
            if (events.isEmpty) {
              return Center(
                child: Text(
                  '没有历史记录 — page 可能是从外部导入而非通过 API 创建',
                  style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
                ),
              );
            }
            return _Timeline(events: events);
          },
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('关闭'),
        ),
      ],
    );
  }
}

class _Timeline extends StatelessWidget {
  const _Timeline({required this.events});
  final List<WikiPageEvent> events;

  @override
  Widget build(BuildContext context) {
    // Group consecutive events by day so the timeline isn't a single
    // wall of timestamps. We rely on the API order (newest first) so
    // we can walk linearly.
    final groups = <(_DayKey, List<WikiPageEvent>)>[];
    for (final e in events) {
      final k = _DayKey.fromDate(e.createdAt.toLocal());
      if (groups.isNotEmpty && groups.last.$1 == k) {
        groups.last.$2.add(e);
      } else {
        groups.add((k, [e]));
      }
    }
    return ListView.builder(
      itemCount: groups.length,
      itemBuilder: (_, i) {
        final group = groups[i];
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(
                top: BiuTokens.space3,
                bottom: BiuTokens.space2,
                left: 4,
              ),
              child: Text(
                group.$1.label(),
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                  color: BiuTokens.textMuted,
                  letterSpacing: 0.5,
                ),
              ),
            ),
            for (final e in group.$2) _EventRow(event: e),
          ],
        );
      },
    );
  }
}

class _EventRow extends StatelessWidget {
  const _EventRow({required this.event});
  final WikiPageEvent event;

  @override
  Widget build(BuildContext context) {
    final meta = _eventMeta(event.type);
    return Padding(
      padding: const EdgeInsets.only(left: 4, bottom: BiuTokens.space2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 28,
            height: 28,
            margin: const EdgeInsets.only(top: 2),
            decoration: BoxDecoration(
              color: meta.color.withValues(alpha: 0.12),
              shape: BoxShape.circle,
            ),
            child: Icon(meta.icon, size: 14, color: meta.color),
          ),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  meta.label,
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: BiuTokens.text,
                  ),
                ),
                if (_excerpt() case String s when s.isNotEmpty) ...[
                  const SizedBox(height: 2),
                  Text(
                    s,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 11,
                      color: BiuTokens.textMuted,
                      height: 1.4,
                    ),
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(width: BiuTokens.space2),
          Text(
            _hhmm(event.createdAt.toLocal()),
            style: TextStyle(
              fontSize: 10,
              color: BiuTokens.textMuted,
              fontFamily: 'JetBrains Mono, ui-monospace, monospace',
            ),
          ),
        ],
      ),
    );
  }

  /// One-line text excerpt extracted from the event payload — picks
  /// whichever field tends to carry the human-meaningful content per
  /// event_type. Returns "" when there's nothing relevant.
  String _excerpt() {
    final p = event.payload;
    if (event.type.startsWith('block.') &&
        p['content'] is Map<String, dynamic>) {
      final c = (p['content'] as Map).cast<String, dynamic>();
      final t = c['text'] as String?;
      if (t != null && t.isNotEmpty) return t;
    }
    if (event.type.startsWith('page.') && (p['title'] is String)) {
      return p['title'] as String;
    }
    return '';
  }

  String _hhmm(DateTime t) =>
      '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
}

class _DayKey {
  final int year, month, day;
  const _DayKey(this.year, this.month, this.day);
  factory _DayKey.fromDate(DateTime d) => _DayKey(d.year, d.month, d.day);
  @override
  bool operator ==(Object other) =>
      other is _DayKey &&
      other.year == year &&
      other.month == month &&
      other.day == day;
  @override
  int get hashCode => Object.hash(year, month, day);

  String label() {
    final today = DateTime.now();
    if (year == today.year && month == today.month && day == today.day) {
      return '今天';
    }
    final yesterday = today.subtract(const Duration(days: 1));
    if (year == yesterday.year &&
        month == yesterday.month &&
        day == yesterday.day) {
      return '昨天';
    }
    return '$year-${month.toString().padLeft(2, '0')}-${day.toString().padLeft(2, '0')}';
  }
}

class _EventMeta {
  final String label;
  final IconData icon;
  final Color color;
  const _EventMeta(this.label, this.icon, this.color);
}

_EventMeta _eventMeta(String type) => switch (type) {
  'page.created' => const _EventMeta(
    '页面创建',
    Icons.fiber_new,
    NamedPaletteStrong.emerald,
  ),
  'page.updated' => const _EventMeta(
    '页面更新',
    Icons.edit_outlined,
    NamedPaletteStrong.blue,
  ),
  'page.deleted' => const _EventMeta(
    '页面删除',
    Icons.delete_outline,
    NamedPaletteStrong.red,
  ),
  'block.created' => const _EventMeta(
    '新增块',
    Icons.add_box_outlined,
    NamedPaletteStrong.emerald,
  ),
  'block.updated' => const _EventMeta(
    '编辑块',
    Icons.edit_note_outlined,
    NamedPaletteStrong.blue,
  ),
  'block.deleted' => const _EventMeta(
    '删除块',
    Icons.delete_sweep_outlined,
    NamedPaletteStrong.red,
  ),
  _ => _EventMeta(type, Icons.event_note_outlined, BiuTokens.textMuted),
};
