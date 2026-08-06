// SkillDetail — right-side drawer (desktop) / full-screen modal (mobile)
// for inspecting a single Skill. Two tabs (概览 / 技能功能) — design
// reviewed against Lobehub's modal layout. The "drawer doesn't lose
// list context" property is the main reason this is not a push route.
//
// Public entry point: showSkillDetail(context, skill) returns the
// menu action the user picked (Delete returns SkillTileAction.delete
// so SkillsPage can refresh / drop selection state).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../data/api/skill_client.dart';
import '../../../data/skill_providers.dart';
import 'skill_tile.dart' show SkillTileAction;

/// Open the skill detail panel. Returns null on dismiss, or
/// SkillTileAction.delete after a successful delete.
///
/// Layout adapts:
///   - >= 700px wide → 640px right-side drawer with barrier (Lobehub
///                     parity).
///   - < 700px       → full-screen modal (mobile / very narrow window).
///
/// We use showGeneralDialog so we can fully customise the layout and
/// transition; the alternative (Drawer / endDrawer) requires the host
/// page to wire a Scaffold endDrawer slot which doesn't compose well
/// with the existing SkillsPage shell.
Future<SkillTileAction?> showSkillDetail(
  BuildContext context, {
  required Skill skill,
}) {
  return showGeneralDialog<SkillTileAction>(
    context: context,
    barrierLabel: '关闭',
    barrierDismissible: true,
    barrierColor: Colors.black.withValues(alpha: 0.32),
    transitionDuration: const Duration(milliseconds: 220),
    pageBuilder: (_, _, _) => SkillDetail(skill: skill),
    transitionBuilder: (_, anim, _, child) {
      final wide = MediaQuery.of(context).size.width >= 700;
      final tween = Tween<Offset>(
        begin: wide ? const Offset(1, 0) : const Offset(0, 1),
        end: Offset.zero,
      ).chain(CurveTween(curve: Curves.easeOutCubic));
      return SlideTransition(position: anim.drive(tween), child: child);
    },
  );
}

class SkillDetail extends ConsumerStatefulWidget {
  const SkillDetail({super.key, required this.skill});
  final Skill skill;

  @override
  ConsumerState<SkillDetail> createState() => _SkillDetailState();
}

class _SkillDetailState extends ConsumerState<SkillDetail>
    with TickerProviderStateMixin {
  late final TabController _tabs = TabController(length: 2, vsync: this);
  late Skill _skill = widget.skill;
  String? _selectedFile = 'SKILL.md';
  bool _busy = false;
  SkillActivationsResult? _activations;
  /// Fetched predecessor when this is a staged update_of skill.
  /// null while loading or when there's no predecessor.
  Skill? _predecessor;

  @override
  void initState() {
    super.initState();
    // Fire-and-forget — drawer shows immediately with skill metadata,
    // activation stats arrive a beat later. A failure (network blip)
    // just leaves the panel without stats; no error UI needed because
    // it's secondary information.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _loadActivations();
      _loadPredecessor();
    });
  }

  Future<void> _loadPredecessor() async {
    if (_skill.updateOfId.isEmpty) return;
    final client = ref.read(skillClientProvider);
    if (client == null) return;
    try {
      final prev = await client.get(_skill.updateOfId);
      if (!mounted) return;
      setState(() => _predecessor = prev);
    } on SkillApiError {
      // Predecessor might have been deleted; rare and non-blocking —
      // drop silently. The hash chip just won't render.
    }
  }

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  Future<void> _loadActivations() async {
    final client = ref.read(skillClientProvider);
    if (client == null) return;
    try {
      final r = await client.activations(_skill.id, limit: 1);
      if (!mounted) return;
      setState(() => _activations = r);
    } on SkillApiError {
      // Drop silently — secondary info. Don't pollute the page error
      // banner that's reserved for primary actions (approve / delete).
    }
  }

  @override
  Widget build(BuildContext context) {
    final wide = MediaQuery.of(context).size.width >= 700;
    final panel = _Panel(
      child: Column(
        children: [
          _Header(skill: _skill, onClose: () => Navigator.of(context).pop()),
          TabBar(
            controller: _tabs,
            indicatorSize: TabBarIndicatorSize.label,
            labelColor: BiuTokens.purple,
            unselectedLabelColor: BiuTokens.textSecondary,
            indicatorColor: BiuTokens.purple,
            labelStyle: const TextStyle(fontSize: 14, fontWeight: FontWeight.w600),
            tabs: const [
              Tab(text: '概览'),
              Tab(text: '技能功能'),
            ],
          ),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          Expanded(
            child: TabBarView(
              controller: _tabs,
              children: [
                _OverviewTab(
                  skill: _skill,
                  busy: _busy,
                  activations: _activations,
                  predecessor: _predecessor,
                  onApprove: _runApprove,
                  onReject: _runReject,
                  onDelete: _runDelete,
                ),
                _FunctionsTab(
                  skill: _skill,
                  selected: _selectedFile,
                  onSelect: (p) => setState(() => _selectedFile = p),
                ),
              ],
            ),
          ),
        ],
      ),
    );

    if (wide) {
      // Right-aligned 640px drawer.
      return Align(
        alignment: Alignment.centerRight,
        child: Material(
          color: Colors.transparent,
          child: SizedBox(
            width: 640,
            height: double.infinity,
            child: panel,
          ),
        ),
      );
    }
    // Full-screen modal for mobile / narrow.
    return Material(color: BiuTokens.bg, child: panel);
  }

  // ─── Actions ─────────────────────────────────────────────

  Future<void> _runApprove() async {
    final client = ref.read(skillClientProvider);
    if (client == null) return;
    setState(() => _busy = true);
    try {
      final updated = await client.approve(_skill.id, enableOnDefaultAgent: true);
      if (!mounted) return;
      setState(() {
        _skill = updated;
        _busy = false;
      });
      ref.invalidate(skillsListProvider);
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _showError('$e');
    }
  }

  Future<void> _runReject() async {
    final reason = await _promptReason();
    if (reason == null) return;
    final client = ref.read(skillClientProvider);
    if (client == null) return;
    setState(() => _busy = true);
    try {
      final updated = await client.reject(_skill.id, reason: reason);
      if (!mounted) return;
      setState(() {
        _skill = updated;
        _busy = false;
      });
      ref.invalidate(skillsListProvider);
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _showError('$e');
    }
  }

  Future<void> _runDelete() async {
    final ok = await _confirmDelete();
    if (ok != true) return;
    final client = ref.read(skillClientProvider);
    if (client == null) return;
    setState(() => _busy = true);
    try {
      await client.delete(_skill.id);
      if (!mounted) return;
      ref.invalidate(skillsListProvider);
      Navigator.of(context).pop(SkillTileAction.delete);
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _showError('$e');
    }
  }

  void _showError(String msg) {
    final ms = ScaffoldMessenger.maybeOf(context);
    ms?.showSnackBar(SnackBar(content: Text(msg)));
  }

  Future<String?> _promptReason() async {
    final ctrl = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('驳回原因'),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: const InputDecoration(border: OutlineInputBorder()),
          maxLines: 3,
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c), child: const Text('取消')),
          FilledButton(
            onPressed: () => Navigator.pop(c, ctrl.text.trim()),
            child: const Text('驳回'),
          ),
        ],
      ),
    );
  }

  Future<bool?> _confirmDelete() async {
    return showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('删除技能'),
        content: Text('确定删除「${_skill.name.isEmpty ? _skill.identifier : _skill.name}」？此操作不可撤销。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c, false), child: const Text('取消')),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () => Navigator.pop(c, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
  }
}

// ─── Panel chrome ────────────────────────────────────────────

class _Panel extends StatelessWidget {
  const _Panel({required this.child});
  final Widget child;
  @override
  Widget build(BuildContext context) {
    return Container(
      color: Colors.white,
      child: SafeArea(child: child),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.skill, required this.onClose});
  final Skill skill;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    final icon = skill.manifest.icon;
    return Padding(
      padding: const EdgeInsets.fromLTRB(BiuTokens.space5, BiuTokens.space4,
          BiuTokens.space3, BiuTokens.space4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const Spacer(),
              Text(
                '技能详情',
                style: TextStyle(fontSize: 14, fontWeight: FontWeight.w600,
                    color: BiuTokens.text),
              ),
              const Spacer(),
              IconButton(
                onPressed: onClose,
                icon: const Icon(Icons.close, size: 20),
                tooltip: '关闭',
              ),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              Container(
                width: 56, height: 56,
                decoration: BoxDecoration(
                  color: icon.isEmpty
                      ? BiuTokens.purpleSoft
                      : BiuTokens.purple.withValues(alpha: 0.10),
                  borderRadius: BorderRadius.circular(14),
                ),
                alignment: Alignment.center,
                child: Text(
                  icon.isEmpty
                      ? (skill.identifier.isEmpty ? '?' : skill.identifier[0].toUpperCase())
                      : icon,
                  style: TextStyle(
                    fontSize: icon.isEmpty ? 22 : 28,
                    fontWeight: FontWeight.w600,
                    color: icon.isEmpty ? BiuTokens.purple : null,
                  ),
                ),
              ),
              const SizedBox(width: BiuTokens.space3),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      skill.name.isEmpty ? skill.identifier : skill.name,
                      style: TextStyle(
                        fontSize: 20,
                        fontWeight: FontWeight.w700,
                        color: BiuTokens.text,
                      ),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      skill.description.isEmpty ? skill.identifier : skill.description,
                      style: TextStyle(
                        fontSize: 13,
                        color: BiuTokens.textSecondary,
                        height: 1.4,
                      ),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ─── Tab 1: Overview ─────────────────────────────────────────

class _OverviewTab extends StatelessWidget {
  const _OverviewTab({
    required this.skill,
    required this.busy,
    required this.activations,
    required this.predecessor,
    required this.onApprove,
    required this.onReject,
    required this.onDelete,
  });
  final Skill skill;
  final bool busy;
  /// Async-loaded activation stats. Null while in flight; rendered as
  /// "—" or hidden once stats arrive.
  final SkillActivationsResult? activations;
  /// When skill.updateOfId points at an existing row, this is the
  /// predecessor's resolved Skill — drives the "基于 v<hash> · diff"
  /// hint above the action buttons.
  final Skill? predecessor;
  final VoidCallback onApprove;
  final VoidCallback onReject;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // 1. 该技能能访问 — permissions 人话化
          const _SectionLabel('该技能能访问'),
          const SizedBox(height: 8),
          ..._permissionsRows(skill.permissions),
          const SizedBox(height: BiuTokens.space5),

          // 2. 详情 — manifest 元信息 + 调用统计
          const _SectionLabel('详情'),
          const SizedBox(height: 8),
          _MetaTable(rows: _detailsRows(skill)),

          // 3. 第三方提示（仅非 bundled）
          if (skill.source != 'bundled') ...[
            const SizedBox(height: BiuTokens.space5),
            _ThirdPartyDisclaimer(source: skill.source),
          ],

          // 4. 路径自动附加（如果有）
          if (skill.paths.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space5),
            const _SectionLabel('自动附加路径'),
            const SizedBox(height: 8),
            Wrap(
              spacing: 6, runSpacing: 6,
              children: skill.paths
                  .map((p) => Chip(
                        label: Text(p, style: const TextStyle(fontSize: 12, fontFamily: 'monospace')),
                        visualDensity: VisualDensity.compact,
                      ))
                  .toList(),
            ),
          ],

          // 5. 基于哪个版本（仅 staged + 有 predecessor 时）
          if (predecessor != null && skill.updateOfId.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space5),
            _UpdateOfBanner(predecessor: predecessor!, current: skill),
          ],

          const SizedBox(height: BiuTokens.space6),

          // 6. 操作按钮
          _ActionRow(
            skill: skill,
            busy: busy,
            onApprove: onApprove,
            onReject: onReject,
            onDelete: onDelete,
          ),
        ],
      ),
    );
  }

  List<Widget> _permissionsRows(List<String> perms) {
    if (perms.isEmpty) {
      return [const _PermLine(text: '无额外权限（只调用模型本身）')];
    }
    return perms.map((p) => _PermLine(text: _permissionLabel(p))).toList();
  }

  List<(String, Widget)> _detailsRows(Skill s) {
    final m = s.manifest;
    final rows = <(String, Widget)>[];
    rows.add((
      '作者',
      Text(
        m.authorName.isNotEmpty
            ? m.authorName
            : (s.source == 'bundled' ? 'BiuMind 官方' : '—'),
        style: TextStyle(fontSize: 13, color: BiuTokens.text),
      ),
    ));
    rows.add(('版本', Text(m.version.isEmpty ? '—' : 'v${m.version}',
        style: TextStyle(fontSize: 13, color: BiuTokens.text))));
    rows.add(('许可', Text(m.license.isEmpty ? '—' : m.license,
        style: TextStyle(fontSize: 13, color: BiuTokens.text))));
    rows.add((
      '仓库',
      m.repository.isEmpty
          ? Text('—', style: TextStyle(fontSize: 13, color: BiuTokens.text))
          : _ExternalLink(url: m.repository),
    ));
    rows.add(('更新', Text(_relativeTime(s.updatedAt),
        style: TextStyle(fontSize: 13, color: BiuTokens.text))));
    // Activation stats — only render once fetched. Loading state
    // (activations==null) hides the rows so the panel doesn't shift
    // mid-render once stats arrive.
    final a = activations;
    if (a != null) {
      rows.add(('调用次数', Text(
        a.count == 0 ? '尚未调用' : '${a.count} 次',
        style: TextStyle(fontSize: 13, color: BiuTokens.text),
      )));
      if (a.lastAt != null) {
        rows.add(('最后调用', Text(_relativeTime(a.lastAt!),
            style: TextStyle(fontSize: 13, color: BiuTokens.text))));
      }
    }
    return rows;
  }
}

class _SectionLabel extends StatelessWidget {
  const _SectionLabel(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Text(
      text,
      style: TextStyle(
        fontSize: 12,
        fontWeight: FontWeight.w600,
        color: BiuTokens.textSecondary,
        letterSpacing: 0.4,
      ),
    );
  }
}

class _PermLine extends StatelessWidget {
  const _PermLine({required this.text});
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.circle, size: 6, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              text,
              style: TextStyle(fontSize: 13, color: BiuTokens.text, height: 1.5),
            ),
          ),
        ],
      ),
    );
  }
}

class _MetaTable extends StatelessWidget {
  const _MetaTable({required this.rows});
  final List<(String, Widget)> rows;
  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: rows.map((r) {
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                width: 64,
                child: Text(
                  r.$1,
                  style: TextStyle(
                    fontSize: 13,
                    color: BiuTokens.textMuted,
                  ),
                ),
              ),
              Expanded(child: r.$2),
            ],
          ),
        );
      }).toList(),
    );
  }
}

class _ExternalLink extends StatelessWidget {
  const _ExternalLink({required this.url});
  final String url;
  @override
  Widget build(BuildContext context) {
    return Text.rich(
      TextSpan(
        children: [
          TextSpan(
            text: url,
            style: TextStyle(
              fontSize: 13,
              color: BiuTokens.purple,
              decoration: TextDecoration.underline,
              decorationColor: BiuTokens.purple,
            ),
          ),
          TextSpan(text: ' ↗', style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
        ],
      ),
    );
  }
}

class _ThirdPartyDisclaimer extends StatelessWidget {
  const _ThirdPartyDisclaimer({required this.source});
  final String source;
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: WarningCallout.bg,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: const [
          Icon(Icons.info_outline, size: 16, color: WarningCallout.iconFg),
          SizedBox(width: 8),
          Expanded(
            child: Text(
              '该技能由第三方提供，BiuMind 不保证其行为符合预期，请评估后使用。',
              style: TextStyle(fontSize: 13, color: WarningCallout.textFg, height: 1.5),
            ),
          ),
        ],
      ),
    );
  }
}

/// Banner shown above the action buttons for staged skills that
/// propose a replacement: surfaces the predecessor's hash + a diff
/// summary so the approver doesn't have to round-trip elsewhere to
/// see what changed. Tapping the hash chip jumps to the predecessor's
/// detail drawer (TODO — currently just informational).
class _UpdateOfBanner extends StatelessWidget {
  const _UpdateOfBanner({required this.predecessor, required this.current});
  final Skill predecessor;
  final Skill current;

  String _short(String hash) =>
      hash.length >= 16 ? hash.substring(0, 16) : hash;

  @override
  Widget build(BuildContext context) {
    final oldHash = _short(predecessor.contentHash);
    final newHash = _short(current.contentHash);
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: IndigoCallout.bg,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: IndigoCallout.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: const [
              Icon(Icons.history, size: 16, color: IndigoCallout.iconFg),
              SizedBox(width: 8),
              Text(
                '基于现有版本提出的修改',
                style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: IndigoCallout.titleFg),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            '识别符 ${predecessor.identifier} · v$oldHash… → v$newHash…',
            style: const TextStyle(
              fontSize: 12,
              color: IndigoCallout.iconFg,
              fontFamily: 'monospace',
            ),
          ),
          if (predecessor.updatedAt.isAfter(DateTime.fromMillisecondsSinceEpoch(0))) ...[
            const SizedBox(height: 4),
            Text(
              '原版本 ${_relativeTime(predecessor.updatedAt)}发布',
              style: const TextStyle(fontSize: 12, color: IndigoCallout.subtitleFg),
            ),
          ],
        ],
      ),
    );
  }
}

class _ActionRow extends StatelessWidget {
  const _ActionRow({
    required this.skill,
    required this.busy,
    required this.onApprove,
    required this.onReject,
    required this.onDelete,
  });
  final Skill skill;
  final bool busy;
  final VoidCallback onApprove;
  final VoidCallback onReject;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final isStaged = skill.status == 'staged' || skill.status == 'staged_org';
    final isBundled = skill.source == 'bundled';
    return Row(
      children: [
        if (isStaged) ...[
          FilledButton.icon(
            icon: const Icon(Icons.check),
            label: const Text('批准'),
            onPressed: busy ? null : onApprove,
          ),
          const SizedBox(width: BiuTokens.space2),
          OutlinedButton.icon(
            icon: const Icon(Icons.close),
            label: const Text('驳回'),
            onPressed: busy ? null : onReject,
          ),
        ],
        const Spacer(),
        if (!isBundled)
          TextButton.icon(
            icon: const Icon(Icons.delete_outline, color: Colors.red),
            label: const Text('删除', style: TextStyle(color: Colors.red)),
            onPressed: busy ? null : onDelete,
          ),
      ],
    );
  }
}

// ─── Tab 2: Functions (file tree + content) ──────────────────

class _FunctionsTab extends StatelessWidget {
  const _FunctionsTab({
    required this.skill,
    required this.selected,
    required this.onSelect,
  });
  final Skill skill;
  final String? selected;
  final void Function(String path) onSelect;

  @override
  Widget build(BuildContext context) {
    final files = _treeEntries(skill);
    return Row(
      children: [
        SizedBox(
          width: 180,
          child: Container(
            color: Theme.of(context).extension<BiuColors>()!.surface1,
            child: ListView(
              padding: const EdgeInsets.symmetric(vertical: 8),
              children: files.map((f) => _FileRow(
                    path: f,
                    selected: f == selected,
                    onTap: () => onSelect(f),
                  )).toList(),
            ),
          ),
        ),
        VerticalDivider(width: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: _Viewer(skill: skill, path: selected ?? 'SKILL.md'),
        ),
      ],
    );
  }

  /// Build a flat sorted list of paths for the tree pane.
  /// SKILL.md always appears first, then resources sorted by their
  /// vpath with directory prefixes preserved (references/foo before
  /// scripts/bar). Empty resource maps yield the single SKILL.md row.
  List<String> _treeEntries(Skill s) {
    final out = <String>['SKILL.md'];
    final keys = s.resources.keys.toList()..sort();
    out.addAll(keys);
    return out;
  }
}

class _FileRow extends StatelessWidget {
  const _FileRow({required this.path, required this.selected, required this.onTap});
  final String path;
  final bool selected;
  final VoidCallback onTap;

  IconData _iconFor(String p) {
    if (p == 'SKILL.md') return Icons.article_outlined;
    if (p.startsWith('scripts/')) return Icons.terminal;
    if (p.startsWith('references/')) return Icons.menu_book_outlined;
    if (p.startsWith('assets/')) return Icons.image_outlined;
    return Icons.description_outlined;
  }

  String _displayPath(String p) {
    // Indent resource entries one level so the tree visually groups
    // by directory without rendering an actual collapsible widget.
    if (p.contains('/')) {
      final i = p.indexOf('/');
      return '  ${p.substring(i + 1)}';
    }
    return p;
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: selected ? BiuTokens.purpleSoft : Colors.transparent,
        ),
        child: Row(
          children: [
            Icon(
              _iconFor(path),
              size: 16,
              color: selected ? BiuTokens.purple : BiuTokens.textSecondary,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                _displayPath(path),
                style: TextStyle(
                  fontSize: 13,
                  color: selected ? BiuTokens.purple : BiuTokens.text,
                  fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Viewer extends StatelessWidget {
  const _Viewer({required this.skill, required this.path});
  final Skill skill;
  final String path;

  @override
  Widget build(BuildContext context) {
    if (path == 'SKILL.md') {
      return _BodyView(text: skill.content);
    }
    final res = skill.resources[path];
    if (res == null) {
      return const _PlaceholderView(text: '资源不存在或已被移除');
    }
    if (res.isInline) {
      return _BodyView(text: res.inline, footer: _ResourceFooter(res: res));
    }
    if (res.isCAS) {
      return _CASPlaceholder(res: res);
    }
    return const _PlaceholderView(text: '该资源没有可显示的内容');
  }
}

class _BodyView extends StatelessWidget {
  const _BodyView({required this.text, this.footer});
  final String text;
  final Widget? footer;
  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(BiuTokens.space4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SelectableText(
            text.isEmpty ? '(empty)' : text,
            style: TextStyle(
              fontFamily: 'monospace',
              fontSize: 13,
              height: 1.55,
              color: BiuTokens.text,
            ),
          ),
          if (footer != null) ...[
            const SizedBox(height: BiuTokens.space3),
            footer!,
          ],
        ],
      ),
    );
  }
}

class _ResourceFooter extends StatelessWidget {
  const _ResourceFooter({required this.res});
  final SkillResource res;
  @override
  Widget build(BuildContext context) {
    final parts = <String>[
      _humanSize(res.sizeBytes),
      if (res.mimeType.isNotEmpty) res.mimeType,
      if (res.sha256.isNotEmpty)
        'sha256: ${res.sha256.substring(0, res.sha256.length.clamp(0, 16))}…',
    ];
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: Theme.of(context).extension<BiuColors>()!.surface2,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        parts.join('  ·  '),
        style: TextStyle(
          fontSize: 11,
          color: BiuTokens.textMuted,
          fontFamily: 'monospace',
        ),
      ),
    );
  }
}

class _CASPlaceholder extends StatelessWidget {
  const _CASPlaceholder({required this.res});
  final SkillResource res;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            Icon(Icons.cloud_outlined, size: 18, color: BiuTokens.textMuted),
            SizedBox(width: 8),
            Text('该资源存储在云端 CAS',
                style: TextStyle(
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.text,
                )),
          ]),
          const SizedBox(height: 12),
          _MetaTable(rows: [
            ('大小', Text(_humanSize(res.sizeBytes),
                style: TextStyle(fontSize: 13, color: BiuTokens.text))),
            if (res.mimeType.isNotEmpty)
              ('类型', Text(res.mimeType,
                  style: TextStyle(fontSize: 13, color: BiuTokens.text))),
            ('sha256', SelectableText(res.sha256,
                style: TextStyle(
                  fontSize: 12,
                  fontFamily: 'monospace',
                  color: BiuTokens.text,
                ))),
          ]),
          const SizedBox(height: 12),
          Text(
            '云端拉取在沙盒挂载链路就绪后开放（PS3.6 续）。',
            style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
          ),
        ],
      ),
    );
  }
}

class _PlaceholderView extends StatelessWidget {
  const _PlaceholderView({required this.text});
  final String text;
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Text(
          text,
          style: TextStyle(color: BiuTokens.textMuted),
        ),
      ),
    );
  }
}

String _humanSize(int bytes) {
  if (bytes < 1024) return '$bytes B';
  if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
  if (bytes < 1024 * 1024 * 1024) {
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
  return '${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(1)} GB';
}

// ─── helpers ─────────────────────────────────────────────────

String _permissionLabel(String permission) {
  switch (permission) {
    case 'sandbox.exec':
      return '可在沙盒里运行代码';
    case 'network.fetch':
      return '可访问外部网络';
    case 'wiki.read':
      return '可读你的知识库';
    case 'wiki.write':
      return '可写入你的知识库';
    case 'memory.recall':
      return '可读你的记忆';
    case 'memory.store':
      return '可写入你的记忆';
    case 'graph.read':
      return '可查询知识图谱';
    case 'graph.write':
      return '可修改知识图谱';
    case 'files.read':
      return '可读你的文件';
    case 'files.write':
      return '可写入你的文件';
    default:
      return permission; // fall through unchanged for unknown perms
  }
}

String _relativeTime(DateTime t) {
  final delta = DateTime.now().difference(t);
  if (delta.isNegative) return '刚刚';
  if (delta.inMinutes < 1) return '刚刚';
  if (delta.inHours < 1) return '${delta.inMinutes} 分钟前';
  if (delta.inDays < 1) return '${delta.inHours} 小时前';
  if (delta.inDays < 30) return '${delta.inDays} 天前';
  if (delta.inDays < 365) return '${(delta.inDays / 30).floor()} 个月前';
  return '${(delta.inDays / 365).floor()} 年前';
}
