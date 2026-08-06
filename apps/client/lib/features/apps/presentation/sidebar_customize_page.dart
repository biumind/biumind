// SidebarCustomizePage — desktop sidebar editor (Settings → 侧边栏定制).
//
// Three sections:
//   1. System (Chat / Wiki / Graph / Code / Apps / Skills / Settings):
//      checkbox to toggle hidden flag — system entries are never
//      removable, only hidden.
//   2. Pinned apps: ordered list with up/down arrows + remove (×).
//      Drag-and-drop reordering lands in v2.0 (pulling in
//      flutter_reorderable_list). v1.5 uses arrow buttons which
//      are accessible by default and need no extra deps.
//   3. Available apps: installed apps not currently pinned, with
//      "+ 固定到侧边栏" buttons.
//
// Conflict handling: PUT collides with another device → SnackBar
// "另一设备已改动侧边栏，已重新载入" + auto-refresh. The user
// edits-and-saves again from the new state.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/layout/phone_nav.dart';
import '../../../data/api/apps_client.dart';
import '../../../data/api/sidebar_client.dart';
import '../../../data/apps_providers.dart';
import '../../../data/sidebar_providers.dart';
import '../../../l10n/app_localizations.dart';
import '../../../shared/page_scaffold.dart';
import '../sync/sidebar_events_realtime.dart';
import 'apps_error.dart';

class SidebarCustomizePage extends ConsumerStatefulWidget {
  const SidebarCustomizePage({super.key});
  @override
  ConsumerState<SidebarCustomizePage> createState() => _SidebarCustomizePageState();
}

class _SidebarCustomizePageState extends ConsumerState<SidebarCustomizePage> {
  /// Local working copy of the layout. Modifications mutate this
  /// list; "保存" PUTs and refreshes the canonical version.
  List<SidebarItem>? _items;
  int? _version;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    // Kick the Realtime listener so multi-device sidebar edits hot-
    // reload this page (v1.5#3). Listener is idempotent — safe to call
    // every page entry.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(sidebarEventsListenerProvider).start();
    });
  }

  /// System entries the user can toggle (matches router._systemItems).
  static const _systemDefaults = [
    ('chat', 'Chat'),
    ('search', '搜索'),
    ('wiki', 'Wiki'),
    ('notes', '笔记'),
    // ('memory', 'Memory'), // TODO(memory): 后端未完全实现, 先下线, 恢复见 router.dart 顶部说明
    ('skills', 'Skills'),
    ('apps', 'App Center'),
    ('code', 'Code'),
  ];

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final layoutAsync = ref.watch(sidebarLayoutProvider('desktop'));
    final installs = ref.watch(installationsProvider('user')).valueOrNull ?? const [];

    // Multi-device toast — when another device pushes a sidebar change
    // we drop our in-progress local working copy back to nil so the
    // next layoutAsync re-seed picks up the canonical state. Saves the
    // user from a stale 409 on their next "保存".
    ref.listen<AsyncValue<SidebarChangeNotice>>(
      sidebarChangeNoticesProvider,
      (_, next) {
        next.whenData((n) {
          if (n.scope != 'desktop') return;
          if (!mounted) return;
          setState(() {
            _items = null;
            _version = null;
          });
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: const Text('另一设备已修改侧边栏，已重新载入'),
              duration: const Duration(seconds: 2),
            ),
          );
        });
      },
    );

    return PageScaffold(
      title: l10n.sidebarCustomizeTitle,
      // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
      leading: const PhoneBackButton(),
      actions: [
        TextButton(
          onPressed: _saving ? null : _reset,
          child: Text(l10n.sidebarRestoreDefaults),
        ),
        FilledButton(
          onPressed: (_saving || _items == null) ? null : _save,
          child: Text(_saving ? l10n.sidebarSaving : l10n.sidebarSave),
        ),
      ],
      child: layoutAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: SelectableText('$e')),
        data: (layout) {
          // First entry into the page (or after PUT) — seed the
          // working copy from the canonical layout.
          _items ??= [...?layout?.items];
          _version ??= layout?.version ?? 1;
          // 'pending-local' 标志由 sidebarLayoutProvider 在 merge outbox
          // 时设上 — 表明用户的编辑还在 outbox 等待同步。
          final hasPending = layout?.updatedByDevice == 'pending-local';
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (hasPending) _OutboxBanner(text: l10n.sidebarOutboxBanner),
              Expanded(
                child: _Editor(
                  l10n: l10n,
                  items: _items!,
                  installs: installs,
                  onMutate: (updated) => setState(() => _items = updated),
                ),
              ),
            ],
          );
        },
      ),
    );
  }

  Future<void> _save() async {
    final client = ref.read(sidebarClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    setState(() => _saving = true);
    try {
      final result = await putSidebarOrQueue(
        ref: ref,
        client: client,
        token: token,
        scope: 'desktop',
        items: _items ?? const [],
        expectedVersion: _version ?? 0,
      );
      ref.invalidate(sidebarLayoutProvider);
      if (!mounted) return;
      final l10n = AppLocalizations.of(context)!;
      switch (result) {
        case SidebarPutResult.ok:
          // 重新拉一次拿到 bumped version, 让连续保存走真乐观锁。
          final fresh = await client.get(scope: 'desktop', token: token);
          if (!mounted) return;
          _version = fresh.version;
          _items = [...fresh.items];
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(l10n.sidebarSaved)),
          );
        case SidebarPutResult.queuedOffline:
          // 编辑暂存 outbox, 重连后自动 flush。本地 _items 不动 (用户
          // 看到的还是自己的编辑结果, 走乐观读路径)。
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(l10n.sidebarQueuedOffline)),
          );
      }
    } on SidebarConflict {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(AppLocalizations.of(context)!.sidebarConflict)),
      );
      // Drop local edits and refetch.
      _items = null;
      _version = null;
      ref.invalidate(sidebarLayoutProvider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(humanizeAppsError(context, e))),
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _reset() async {
    final client = ref.read(sidebarClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    setState(() => _saving = true);
    try {
      final layout = await client.reset(scope: 'desktop', device: 'desktop-client', token: token);
      _items = [...layout.items];
      _version = layout.version;
      ref.invalidate(sidebarLayoutProvider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(humanizeAppsError(context, e))),
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }
}

class _Editor extends StatelessWidget {
  const _Editor({
    required this.l10n,
    required this.items,
    required this.installs,
    required this.onMutate,
  });
  final AppLocalizations l10n;
  final List<SidebarItem> items;
  final List<Installation> installs;
  final void Function(List<SidebarItem>) onMutate;

  @override
  Widget build(BuildContext context) {
    final hiddenSystem = <String>{
      for (final i in items)
        if (i.kind == 'system' && i.hidden) i.ref,
    };
    final pinnedAppRefs = <String>{
      for (final i in items)
        if (i.kind == 'app') i.ref,
    };
    final installById = {for (final i in installs) i.id: i};

    return ListView(
      children: [
        _SectionHeader(l10n.sidebarSectionSystem),
        _buildSystemReorderable(context, items, hiddenSystem, onMutate, l10n),
        const SizedBox(height: BiuTokens.space4),
        _SectionHeader(l10n.sidebarSectionPinned),
        if (pinnedAppRefs.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: BiuTokens.space3, vertical: 4),
            child: Text(l10n.sidebarPinnedEmpty,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant)),
          )
        else
          _buildPinnedReorderable(context, items, installById, onMutate, l10n),
        const SizedBox(height: BiuTokens.space4),
        _SectionHeader(l10n.sidebarSectionAvailable),
        for (final inst in installs)
          if (!pinnedAppRefs.contains(inst.id))
            ListTile(
              leading: const Icon(Icons.widgets_outlined),
              title: Text(inst.identifier),
              subtitle: Text('v${inst.version}'),
              trailing: TextButton.icon(
                onPressed: () {
                  final updated = [...items, SidebarItem(kind: 'app', ref: inst.id)];
                  onMutate(updated);
                },
                icon: const Icon(Icons.add, size: 16),
                label: Text(l10n.sidebarPin),
              ),
            ),
      ],
    );
  }

  /// Pinned-apps reorderable list (v1.5#5). ReorderableListView gives
  /// drag handles + native drop animations across all platforms — no
  /// new dep needed.
  ///
  /// Implementation note: ReorderableListView reorders by *visual*
  /// indices, but the underlying SidebarItem list interleaves
  /// system-hidden + app rows. We materialise the app-only sublist,
  /// reorder there, and splice it back into the canonical list so the
  /// hidden flags / system-row positions stay untouched.
  /// System 段可拖拽 + checkbox 行 (设计 §10A 编辑模式; v2.0 支持 system
  /// 顺序自定)。
  ///
  /// 数据来源约束: items 数组里只存"用户改过的 system 项"
  /// (kind=system + ref + hidden 标志)。本方法把"完整 system 序列"
  /// 还原成: ① items 里出现过的按其相对顺序; ② 默认列表里没出现的
  /// 按 _systemDefaults 默认顺序 append。这样老用户首次打开页面看到
  /// 的就是默认顺序, 改过之后则按自己改过的顺序持久化。
  ///
  /// onReorder: 把 items 里所有 kind=system 项按新顺序回写, app 项
  /// 索引保持不变 (跟 _buildPinnedReorderable 同套路, 反过来)。
  Widget _buildSystemReorderable(
    BuildContext context,
    List<SidebarItem> items,
    Set<String> hiddenSystem,
    void Function(List<SidebarItem>) onMutate,
    AppLocalizations l10n,
  ) {
    final defaults = _SidebarCustomizePageState._systemDefaults;
    final labelById = {for (final (id, label) in defaults) id: label};

    // 计算"完整 system 序列"。
    final orderedIds = <String>[];
    for (final i in items) {
      if (i.kind == 'system' && !orderedIds.contains(i.ref)) {
        orderedIds.add(i.ref);
      }
    }
    final knownIds = {for (final (id, _) in defaults) id};
    for (final id in [...orderedIds]) {
      if (!knownIds.contains(id)) orderedIds.remove(id); // 升级删了的 id 静默清
    }
    for (final (id, _) in defaults) {
      if (!orderedIds.contains(id)) orderedIds.add(id);
    }

    return ReorderableListView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      buildDefaultDragHandles: false,
      itemCount: orderedIds.length,
      itemBuilder: (ctx, i) {
        final id = orderedIds[i];
        final label = labelById[id] ?? id;
        final hidden = hiddenSystem.contains(id);
        return ListTile(
          key: ValueKey('sys-$id'),
          dense: true,
          leading: ReorderableDragStartListener(
            index: i,
            child: const Icon(Icons.drag_indicator),
          ),
          title: Text(label),
          subtitle: hidden
              ? Text(l10n.sidebarHidden,
                  style: TextStyle(
                      color: Theme.of(ctx).colorScheme.onSurfaceVariant))
              : null,
          trailing: Checkbox(
            value: !hidden,
            onChanged: (v) {
              final updated = _withSystemHidden(items, id, v == false);
              onMutate(updated);
            },
          ),
        );
      },
      onReorder: (oldIdx, newIdx) {
        if (newIdx > oldIdx) newIdx -= 1;
        final reorderedIds = [...orderedIds];
        final moved = reorderedIds.removeAt(oldIdx);
        reorderedIds.insert(newIdx, moved);

        final updated = _rewriteSystemOrder(items, reorderedIds, hiddenSystem);
        onMutate(updated);
      },
    );
  }

  /// 切换 system 项的 hidden 标志, 保持其在 items 里的相对位置。如果
  /// 该 id 之前不在 items 里 (用户从未碰过), append 一条新 entry。
  List<SidebarItem> _withSystemHidden(
      List<SidebarItem> items, String id, bool hide) {
    final updated = <SidebarItem>[];
    var found = false;
    for (final it in items) {
      if (it.kind == 'system' && it.ref == id) {
        found = true;
        updated.add(SidebarItem(kind: 'system', ref: id, hidden: hide));
      } else {
        updated.add(it);
      }
    }
    if (!found) {
      updated.add(SidebarItem(kind: 'system', ref: id, hidden: hide));
    }
    return updated;
  }

  /// 用 [orderedIds] 重排 items 里所有 kind=system 项, app 项相对位置
  /// 不变。hiddenSystem 用来保留 hidden 状态。
  List<SidebarItem> _rewriteSystemOrder(
      List<SidebarItem> items, List<String> orderedIds, Set<String> hiddenSystem) {
    // 把 kind=system 索引位置抽出来, 之后按 orderedIds 顺序填回去。
    final sysSlots = <int>[];
    for (var i = 0; i < items.length; i++) {
      if (items[i].kind == 'system') sysSlots.add(i);
    }
    // orderedIds 可能比 sysSlots 多 (有 system 项还没在 items 里),
    // 需要给这些新增一个 slot — 放在 sysSlots 的相对位置中比较 tricky;
    // 简化: 把所有 system 项移到 items 顶部 (最前面), app 紧跟其后,
    // 保留 app 之间的相对顺序。这跟用户视觉一致 (system 段在上 / 应用
    // 段在下)。
    final apps = <SidebarItem>[];
    for (final it in items) {
      if (it.kind != 'system') apps.add(it);
    }
    final out = <SidebarItem>[];
    for (final id in orderedIds) {
      out.add(SidebarItem(
        kind: 'system',
        ref: id,
        hidden: hiddenSystem.contains(id),
      ));
    }
    out.addAll(apps);
    return out;
  }

  Widget _buildPinnedReorderable(
    BuildContext context,
    List<SidebarItem> items,
    Map<String, Installation> installById,
    void Function(List<SidebarItem>) onMutate,
    AppLocalizations l10n,
  ) {
    // Pull out the canonical-index of every app row; we restore by
    // index after reorder so non-app rows don't shift.
    final appIdx = <int>[];
    for (var i = 0; i < items.length; i++) {
      if (items[i].kind == 'app') appIdx.add(i);
    }
    final appRows = [for (final i in appIdx) items[i]];

    return ReorderableListView.builder(
      shrinkWrap: true,
      buildDefaultDragHandles: false, // we render our own handle for clarity
      physics: const NeverScrollableScrollPhysics(), // outer ListView scrolls
      itemCount: appRows.length,
      itemBuilder: (ctx, i) {
        final it = appRows[i];
        final inst = installById[it.ref];
        final label = inst?.identifier ?? '⟨removed⟩ ${it.ref}';
        return ListTile(
          key: ValueKey('pinned-${it.ref}'),
          leading: ReorderableDragStartListener(
            index: i,
            child: const Icon(Icons.drag_indicator),
          ),
          title: Text(label),
          subtitle: inst == null
              ? Text(l10n.sidebarPinnedOrphan,
                  style: TextStyle(
                      color: Theme.of(context).colorScheme.error))
              : Text('v${inst.version}'),
          trailing: IconButton(
            tooltip: l10n.sidebarRemove,
            onPressed: () {
              final canonical = appIdx[i];
              final updated = [...items]..removeAt(canonical);
              onMutate(updated);
            },
            icon: const Icon(Icons.close, size: 16),
          ),
        );
      },
      onReorder: (oldIdx, newIdx) {
        // ReorderableListView convention: when moving down, newIndex
        // is the post-removal index (one past target). Normalise.
        if (newIdx > oldIdx) newIdx -= 1;
        final reordered = [...appRows];
        final moved = reordered.removeAt(oldIdx);
        reordered.insert(newIdx, moved);

        // Splice reordered apps back into the canonical positions.
        final updated = [...items];
        for (var k = 0; k < appIdx.length; k++) {
          updated[appIdx[k]] = reordered[k];
        }
        onMutate(updated);
      },
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        BiuTokens.space3, BiuTokens.space2, 0, 4,
      ),
      child: Text(text,
          style: Theme.of(context).textTheme.titleSmall?.copyWith(
              fontWeight: FontWeight.w700)),
    );
  }
}

/// outbox 有 pending edit 时顶部显示的提示条 (设计 §10A.12)。
class _OutboxBanner extends StatelessWidget {
  const _OutboxBanner({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
      decoration: BoxDecoration(
        color: scheme.tertiaryContainer,
        border: Border(
          bottom: BorderSide(color: scheme.outlineVariant),
        ),
      ),
      child: Row(
        children: [
          Icon(Icons.cloud_off_outlined,
              size: 16, color: scheme.onTertiaryContainer),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: Text(
              text,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: scheme.onTertiaryContainer,
                  ),
            ),
          ),
        ],
      ),
    );
  }
}
