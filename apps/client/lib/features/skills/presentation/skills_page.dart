// SkillsPage — top-level "技能管理" tab.
//
// Lobehub-style single-column list:
//   AppBar:    title + refresh + "+ 添加" + "🏪 技能商店"
//   Filter:    segmented chips (全部 / 内置 / 组织 / 我的 / 市场 / 待审)
//   Body:      ListView<SkillTile>; tap row → push SkillDetailPage
//
// Detail / install / approve flows live in dedicated widgets to keep
// this file focused on layout. Cloud calls go through
// skillClientProvider so the page degrades gracefully when model-relay
// credentials are absent.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../data/api/skill_client.dart';
import '../../../data/skill_providers.dart';
import '../../../l10n/app_localizations.dart';
import '../../../shared/page_scaffold.dart';
import '../sync/skill_events_realtime.dart';
import 'install_dialog.dart';
import 'skill_detail_page.dart';
import 'skill_tile.dart';

class SkillsPage extends ConsumerStatefulWidget {
  const SkillsPage({super.key});

  @override
  ConsumerState<SkillsPage> createState() => _SkillsPageState();
}

class _SkillsPageState extends ConsumerState<SkillsPage> {
  /// Filter state — empty = all, otherwise filter by source / status.
  /// 'pending' is a pseudo-source for staged + staged_org rows.
  String _filter = '';
  String? _error;

  @override
  void initState() {
    super.initState();
    // Kick off the SSE subscription so propose/approve/reject events
    // from other devices refresh the list automatically. Listener
    // tracks its own start state — safe to call from initState.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(skillEventsListenerProvider).start();
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    // select(null-bool): 仅 login/logout 翻转重建; token 轮换不闪.
    ref.watch(skillClientProvider.select((c) => c == null));
    final client = ref.read(skillClientProvider);
    final asyncList = ref.watch(skillsListProvider);

    // Surface remote skill events as one-shot SnackBars.
    ref.listen<AsyncValue<SkillEventNotice>>(skillEventNoticesProvider,
        (_, next) {
      next.whenData((n) => _showRemoteEventToast(context, t, n));
    });

    return PageScaffold(
      title: t.skillsTitle,
      maxWidth: 960,
      actions: [
        IconButton(
          tooltip: t.skillsRefresh,
          onPressed: client == null
              ? null
              : () => ref.invalidate(skillsListProvider),
          icon: const Icon(Icons.refresh),
        ),
        FilledButton.icon(
          icon: const Icon(Icons.add),
          label: Text(t.skillsAdd),
          onPressed: client == null
              ? null
              : () => _openInstallDialog(client, initialTab: 'inline'),
        ),
        const SizedBox(width: 8),
        OutlinedButton.icon(
          icon: const Icon(Icons.storefront_outlined),
          label: const Text('技能商店'),
          onPressed: client == null
              ? null
              : () => _openInstallDialog(client, initialTab: 'url'),
        ),
      ],
      child: client == null
          ? _NoCredsHint(t: t)
          : Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _filterChips(t, asyncList.valueOrNull ?? const []),
                if (_error != null)
                  Container(
                    margin: const EdgeInsets.only(top: 8),
                    padding: const EdgeInsets.all(12),
                    color: Theme.of(context).colorScheme.errorContainer,
                    child: Text(_error!),
                  ),
                const SizedBox(height: 8),
                Divider(height: 1, color: BiuTokens.borderSubtle),
                Expanded(
                  child: asyncList.when(
                    loading: () =>
                        const Center(child: CircularProgressIndicator()),
                    error: (e, _) => Center(child: Text('$e')),
                    data: (rows) => _list(t, _filtered(rows)),
                  ),
                ),
              ],
            ),
    );
  }

  // ─── Filter ───────────────────────────────────────────────

  Widget _filterChips(AppLocalizations t, List<Skill> all) {
    int countOf(String f) {
      if (f.isEmpty) return all.length;
      if (f == 'pending') {
        return all
            .where((s) => s.status == 'staged' || s.status == 'staged_org')
            .length;
      }
      return all.where((s) => s.source == f).length;
    }

    final entries = <(String, String)>[
      ('', t.skillsFilterAll),
      ('bundled', t.skillsFilterBundled),
      ('org', t.skillsFilterOrg),
      ('user', t.skillsFilterMy),
      ('marketplace', t.skillsFilterMarketplace),
      ('pending', t.skillsFilterPending),
    ];

    return Wrap(
      spacing: 8,
      runSpacing: 4,
      children: entries.map((e) {
        final value = e.$1;
        final label = e.$2;
        final n = countOf(value);
        final selected = _filter == value;
        return ChoiceChip(
          label: Text(n > 0 ? '$label · $n' : label),
          selected: selected,
          onSelected: (_) => setState(() => _filter = value),
        );
      }).toList(),
    );
  }

  List<Skill> _filtered(List<Skill> rows) {
    Iterable<Skill> filtered;
    if (_filter.isEmpty) {
      filtered = rows;
    } else if (_filter == 'pending') {
      filtered = rows
          .where((s) => s.status == 'staged' || s.status == 'staged_org');
    } else {
      filtered = rows.where((s) => s.source == _filter);
    }
    // Stable sort: bundled first, then by updated_at desc. Bundled stays
    // top so first-time users see platform skills above their own drafts.
    final sorted = filtered.toList()
      ..sort((a, b) {
        final ab = a.source == 'bundled' ? 0 : 1;
        final bb = b.source == 'bundled' ? 0 : 1;
        if (ab != bb) return ab.compareTo(bb);
        return b.updatedAt.compareTo(a.updatedAt);
      });
    return sorted;
  }

  // ─── List ─────────────────────────────────────────────────

  Widget _list(AppLocalizations t, List<Skill> rows) {
    if (rows.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(40),
          child: Text(
            t.skillsEmpty,
            style: TextStyle(color: BiuTokens.textMuted),
          ),
        ),
      );
    }
    return ListView.separated(
      itemCount: rows.length,
      separatorBuilder: (_, _) =>
          Divider(height: 1, color: BiuTokens.borderSubtle),
      itemBuilder: (_, i) {
        final s = rows[i];
        return SkillTile(
          skill: s,
          onTap: () => _openDetail(s),
          onMenuAction: (a) => _handleMenuAction(s, a),
        );
      },
    );
  }

  // ─── Actions ──────────────────────────────────────────────

  Future<void> _openDetail(Skill s) async {
    final result = await showSkillDetail(context, skill: s);
    // The drawer returns SkillTileAction.delete after a successful
    // delete so we can drop any stale selection state. The drawer
    // itself already invalidated skillsListProvider.
    if (result?.isDelete == true) {
      ref.invalidate(skillsListProvider);
    }
  }

  Future<void> _handleMenuAction(Skill s, SkillTileAction action) async {
    if (action.isDetail) {
      _openDetail(s);
      return;
    }
    final client = ref.read(skillClientProvider);
    if (client == null) return;

    if (action.isApprove) {
      await _runApprove(client, s);
    } else if (action.isReject) {
      await _runReject(client, s);
    } else if (action.isDelete) {
      await _runDelete(client, s);
    } else if (action.isToggleEnable) {
      await _runToggle(client, s, isEnabled: true, pinned: false, label: '已启用');
    } else if (action.isToggleDisable) {
      await _runToggle(client, s, isEnabled: false, pinned: false, label: '已停用');
    } else if (action.isPinHome) {
      await _runToggle(client, s, isEnabled: true, pinned: true, label: '已置顶');
    }
  }

  /// Toggle a skill on/off (and optionally pin) for the user's default
  /// agent. Server side defaults agent_id to deriveAgentID(uid, "biu")
  /// when we send empty, so the UI doesn't need to surface agent
  /// selection until multi-agent lands.
  Future<void> _runToggle(
    SkillClient client,
    Skill s, {
    required bool isEnabled,
    required bool pinned,
    required String label,
  }) async {
    try {
      await client.toggle(
        s.id,
        agentId: '', // server fills in deriveAgentID(uid, "biu")
        isEnabled: isEnabled,
        pinned: pinned,
      );
      if (!mounted) return;
      ref.invalidate(skillsListProvider);
      final ms = ScaffoldMessenger.maybeOf(context);
      ms?.showSnackBar(SnackBar(
        content: Text('${s.name.isEmpty ? s.identifier : s.name} · $label'),
        duration: const Duration(seconds: 2),
      ));
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    }
  }

  Future<void> _runApprove(SkillClient client, Skill s) async {
    try {
      await client.approve(s.id, enableOnDefaultAgent: true);
      if (!mounted) return;
      ref.invalidate(skillsListProvider);
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
      });
    }
  }

  Future<void> _runReject(SkillClient client, Skill s) async {
    final reason = await _promptReason();
    if (reason == null) return;
    try {
      await client.reject(s.id, reason: reason);
      if (!mounted) return;
      ref.invalidate(skillsListProvider);
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
      });
    }
  }

  Future<void> _runDelete(SkillClient client, Skill s) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(AppLocalizations.of(c)!.skillsConfirmDeleteTitle),
        content: Text(AppLocalizations.of(c)!.skillsConfirmDeleteBody(s.name)),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: Text(AppLocalizations.of(c)!.skillsCancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () => Navigator.pop(c, true),
            child: Text(AppLocalizations.of(c)!.skillsDelete),
          ),
        ],
      ),
    );
    if (ok != true) return;
    try {
      await client.delete(s.id);
      if (!mounted) return;
      ref.invalidate(skillsListProvider);
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
      });
    }
  }

  Future<String?> _promptReason() async {
    final ctrl = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(AppLocalizations.of(c)!.skillsReject),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: InputDecoration(
            labelText: AppLocalizations.of(c)!.skillsRejectReason,
            border: const OutlineInputBorder(),
          ),
          maxLines: 3,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: Text(AppLocalizations.of(c)!.skillsCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, ctrl.text.trim()),
            child: Text(AppLocalizations.of(c)!.skillsReject),
          ),
        ],
      ),
    );
  }

  Future<void> _openInstallDialog(SkillClient client,
      {required String initialTab}) async {
    final created = await showDialog<Skill>(
      context: context,
      builder: (_) => InstallSkillDialog(client: client, initialTab: initialTab),
    );
    if (created != null && mounted) {
      ref.invalidate(skillsListProvider);
    }
  }

  // ─── Realtime toasts ──────────────────────────────────────

  void _showRemoteEventToast(
      BuildContext c, AppLocalizations t, SkillEventNotice n) {
    if (!mounted) return;
    final messenger = ScaffoldMessenger.maybeOf(c);
    if (messenger == null) return;
    final label = n.identifier.isEmpty ? n.skillId : n.identifier;
    String text;
    switch (n.verb) {
      case 'proposed':
        text = '$label · proposed';
      case 'approved':
        text = '$label · approved';
      case 'rejected':
        text = n.reason != null && n.reason!.isNotEmpty
            ? '$label · rejected — ${n.reason}'
            : '$label · rejected';
      case 'shared':
        text = '$label · shared with org';
      default:
        text = '$label · ${n.verb}';
    }
    messenger.showSnackBar(SnackBar(
      content: Text(text),
      duration: const Duration(seconds: 3),
    ));
  }
}

class _NoCredsHint extends StatelessWidget {
  const _NoCredsHint({required this.t});
  final AppLocalizations t;

  @override
  Widget build(BuildContext c) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.cloud_off_outlined, size: 48, color: BiuTokens.textMuted),
          const SizedBox(height: 12),
          Text(t.skillsConfigureHint, style: TextStyle(color: BiuTokens.textMuted)),
        ],
      ),
    );
  }
}
