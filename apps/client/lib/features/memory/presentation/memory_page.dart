// MemoryPage — top-level Memory tab.
//
// Layout:
//   AppBar:   title + active project name + refresh
//   Top:      kind chip filter (recall / preference / habit / all)
//   Body:     two modes via SegmentedButton —
//             "List": chronological recent memories
//             "Recall": query input + ranked hits with score chip
//   Bottom:   add-memory bar (TextField + kind dropdown + submit)
//
// State is managed inline with setState — no controller / repository
// abstraction, just direct MemoryClient calls. This is intentional:
// memories are simple CRUD, optimistic local cache adds complexity
// without a clear win until offline editing becomes a requirement.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../data/api/memory_client.dart';
import '../../../data/memory_providers.dart';
import '../../../l10n/app_localizations.dart';
import '../../../shared/page_scaffold.dart';
import '../../wiki/application/wiki_controller.dart';

class MemoryPage extends ConsumerStatefulWidget {
  const MemoryPage({super.key});

  @override
  ConsumerState<MemoryPage> createState() => _MemoryPageState();
}

enum _Mode { list, recall }

class _MemoryPageState extends ConsumerState<MemoryPage> {
  _Mode _mode = _Mode.list;
  String? _kindFilter; // null → all kinds
  String _query = '';
  String _addKind = 'recall';
  RecallMode? _lastRecallMode;
  String? _error;
  bool _busy = false;

  // Local cache of last fetched results so the body doesn't blank during
  // background refresh. Cleared when the active project changes.
  List<Memory> _items = const [];
  String? _projectIdSnapshot;

  final _addCtrl = TextEditingController();
  final _queryCtrl = TextEditingController();

  @override
  void dispose() {
    _addCtrl.dispose();
    _queryCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext c) {
    final t = AppLocalizations.of(c)!;
    // select(null-bool): 仅 login/logout 翻转重建; token 轮换 (非 null 两边) 不闪.
    ref.watch(memoryClientProvider.select((c) => c == null));
    final client = ref.read(memoryClientProvider);
    final wiki = ref.watch(wikiControllerProvider).valueOrNull;
    final project = wiki?.activeProject;

    if (project != null && project.id != _projectIdSnapshot) {
      _projectIdSnapshot = project.id;
      _items = const [];
      Future.microtask(() => _refresh(client, project.id));
    }

    return PageScaffold(
      title: t.memoryTitle,
      subtitle: '· ${project?.name ?? t.memoryProjectMissing}',
      actions: [
        IconButton(
          tooltip: t.memoryRefresh,
          onPressed: client == null || project == null
              ? null
              : () => _refresh(client, project.id),
          icon: const Icon(Icons.refresh, size: 18),
          color: BiuTokens.textSecondary,
        ),
      ],
      child: _body(c, client, project),
    );
  }

  Widget _body(BuildContext c, MemoryClient? client, dynamic project) {
    final t = AppLocalizations.of(c)!;
    if (client == null) {
      return _Hint(text: t.memoryHintNoCreds);
    }
    if (project == null) {
      return _Hint(
        text: t.memoryHintNoProject,
        icon: Icons.folder_open,
      );
    }
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: Row(children: [
            SegmentedButton<_Mode>(
              segments: [
                ButtonSegment(
                  value: _Mode.list,
                  icon: const Icon(Icons.list),
                  label: Text(t.memoryListTab),
                ),
                ButtonSegment(
                  value: _Mode.recall,
                  icon: const Icon(Icons.search),
                  label: Text(t.memoryRecallTab),
                ),
              ],
              selected: {_mode},
              onSelectionChanged: (s) async {
                setState(() => _mode = s.first);
                if (_mode == _Mode.list) {
                  await _refresh(client, project.id);
                } else {
                  setState(() => _items = const []);
                }
              },
            ),
            const SizedBox(width: 16),
            Wrap(spacing: 4, children: [
              _kindChip(null, t.memoryFilterAll),
              _kindChip('recall', t.memoryKindRecall),
              _kindChip('preference', t.memoryKindPreference),
              _kindChip('habit', t.memoryKindHabit),
            ]),
          ]),
        ),
        if (_mode == _Mode.recall)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
            child: TextField(
              controller: _queryCtrl,
              decoration: InputDecoration(
                hintText: t.memoryRecallQueryHint,
                border: const OutlineInputBorder(),
                suffixIcon: IconButton(
                  icon: const Icon(Icons.search),
                  onPressed: () => _runRecall(client, project.id),
                ),
              ),
              onSubmitted: (_) => _runRecall(client, project.id),
            ),
          ),
        if (_lastRecallMode != null && _mode == _Mode.recall)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Chip(
                avatar: Icon(
                  _lastRecallMode == RecallMode.hybrid
                      ? Icons.auto_awesome
                      : Icons.text_fields,
                  size: 16,
                ),
                label: Text(_lastRecallMode == RecallMode.hybrid
                    ? t.memoryModeHybrid
                    : t.memoryModeLexical),
              ),
            ),
          ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.all(12),
            child: Material(
              color: Theme.of(c).colorScheme.errorContainer,
              borderRadius: BorderRadius.circular(8),
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: SelectableText(_error!),
              ),
            ),
          ),
        Expanded(
          child: _busy && _items.isEmpty
              ? const Center(child: CircularProgressIndicator())
              : _items.isEmpty
                  ? _Hint(
                      text: _mode == _Mode.list
                          ? t.memoryHintEmptyList
                          : t.memoryHintEmptyRecall,
                      icon: _mode == _Mode.list
                          ? Icons.psychology_outlined
                          : Icons.search_off,
                    )
                  : _list(c, client),
        ),
        const Divider(height: 1),
        _addBar(c, client, project.id),
      ],
    );
  }

  Widget _kindChip(String? value, String label) {
    final selected = _kindFilter == value;
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: (_) async {
        setState(() => _kindFilter = value);
        final client = ref.read(memoryClientProvider);
        if (client == null || _projectIdSnapshot == null) return;
        if (_mode == _Mode.list) {
          await _refresh(client, _projectIdSnapshot!);
        } else if (_query.isNotEmpty) {
          await _runRecall(client, _projectIdSnapshot!);
        }
      },
    );
  }

  Widget _list(BuildContext c, MemoryClient client) {
    final t = AppLocalizations.of(c)!;
    return ListView.separated(
      itemCount: _items.length,
      separatorBuilder: (_, _) => const Divider(height: 1),
      itemBuilder: (_, i) {
        final m = _items[i];
        return ListTile(
          leading: _kindIcon(m.kind),
          title: Text(m.content),
          subtitle: Text(t.memorySubtitle(
            m.kind,
            m.salience.toStringAsFixed(2),
            m.score != null
                ? ' · score=${m.score!.toStringAsFixed(3)}'
                : '',
            _relTime(t, m.lastAccessedAt),
          )),
          trailing: IconButton(
            icon: const Icon(Icons.delete_outline),
            onPressed: () => _delete(client, m.id),
          ),
        );
      },
    );
  }

  Icon _kindIcon(String kind) {
    switch (kind) {
      case 'preference':
        return const Icon(Icons.tune);
      case 'habit':
      // 'skill' is the legacy alias — still rendered for old clients
      // / cached rows until the 90-day window closes (2026-08-25).
      case 'skill':
        return const Icon(Icons.school);
      default:
        return const Icon(Icons.bookmark_outline);
    }
  }

  Widget _addBar(BuildContext c, MemoryClient client, String pid) {
    final t = AppLocalizations.of(c)!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
      child: Row(children: [
        DropdownButton<String>(
          value: _addKind,
          items: [
            DropdownMenuItem(value: 'recall', child: Text(t.memoryKindRecall)),
            DropdownMenuItem(
                value: 'preference', child: Text(t.memoryKindPreference)),
            DropdownMenuItem(value: 'habit', child: Text(t.memoryKindHabit)),
          ],
          onChanged: (v) => setState(() => _addKind = v ?? 'recall'),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: TextField(
            controller: _addCtrl,
            decoration: InputDecoration(
              hintText: t.memoryAddHint,
              border: const OutlineInputBorder(),
              isDense: true,
            ),
            onSubmitted: (_) => _add(client, pid),
          ),
        ),
        const SizedBox(width: 8),
        FilledButton.icon(
          icon: const Icon(Icons.add),
          label: Text(t.memoryAddButton),
          onPressed: () => _add(client, pid),
        ),
      ]),
    );
  }

  // ─── actions ────────────────────────────────────────────

  Future<void> _refresh(MemoryClient? client, String pid) async {
    if (client == null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final items = await client.list(projectId: pid, kind: _kindFilter);
      if (!mounted) return;
      setState(() => _items = items);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _runRecall(MemoryClient client, String pid) async {
    final q = _queryCtrl.text.trim();
    if (q.isEmpty) return;
    setState(() {
      _busy = true;
      _error = null;
      _query = q;
    });
    try {
      final res = await client.recall(
        projectId: pid,
        query: q,
        kind: _kindFilter,
      );
      if (!mounted) return;
      setState(() {
        _items = res.memories;
        _lastRecallMode = res.mode;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _add(MemoryClient client, String pid) async {
    final txt = _addCtrl.text.trim();
    if (txt.isEmpty) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await client.store(projectId: pid, content: txt, kind: _addKind);
      if (!mounted) return;
      _addCtrl.clear();
      // After adding, switch to List view so the user sees their entry.
      setState(() => _mode = _Mode.list);
      await _refresh(client, pid);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _delete(MemoryClient client, String id) async {
    setState(() => _busy = true);
    try {
      await client.delete(id);
      if (!mounted) return;
      setState(() => _items = _items.where((m) => m.id != id).toList());
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }
}

String _relTime(AppLocalizations t, DateTime when) {
  final d = DateTime.now().difference(when);
  if (d.inSeconds < 60) return t.relTimeSeconds(d.inSeconds);
  if (d.inMinutes < 60) return t.relTimeMinutes(d.inMinutes);
  if (d.inHours < 24) return t.relTimeHours(d.inHours);
  if (d.inDays < 30) return t.relTimeDays(d.inDays);
  return t.relTimeMonths((d.inDays / 30).floor());
}

class _Hint extends StatelessWidget {
  const _Hint({required this.text, this.icon = Icons.info_outline});
  final String text;
  final IconData icon;
  @override
  Widget build(BuildContext c) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(40),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 48, color: Theme.of(c).colorScheme.outline),
            const SizedBox(height: 12),
            Text(text, textAlign: TextAlign.center,
                style: Theme.of(c).textTheme.bodyLarge),
          ],
        ),
      ),
    );
  }
}
