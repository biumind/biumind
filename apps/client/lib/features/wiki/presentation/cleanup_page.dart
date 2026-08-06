// CleanupPage — wiki maintenance dashboard.
//
// Different lens on the same review_items rows that ReviewsPage shows:
//   - Top: per-rule histogram cards (orphan / empty / stub / dead /
//     stale / untitled). Click a card → drills into that rule's
//     findings list with a single dedicated action.
//   - Each finding row exposes the most useful CTA per rule:
//       orphan / empty / stub / untitled  → 删除页面  (one-click +
//                                                     auto-resolve)
//       dead_wikilink / stale_page         → 打开页面 (no auto-fix)
//
// "Ignore" is universally available (dismiss the review without
// touching the page).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_card.dart';
import '../../../data/api/reviews_client.dart';
import '../../../data/wiki_providers.dart';
import '../../../shared/page_scaffold.dart';
import '../application/wiki_controller.dart';

class CleanupPage extends ConsumerStatefulWidget {
  const CleanupPage({super.key});

  @override
  ConsumerState<CleanupPage> createState() => _CleanupPageState();
}

class _CleanupPageState extends ConsumerState<CleanupPage> {
  Future<_CleanupData>? _future;
  String? _projectId;
  String? _selectedRule; // when null show summary; else show drill-down

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final pid = ref.read(wikiControllerProvider).valueOrNull?.activeProject?.id;
    if (pid != null && pid != _projectId) {
      _projectId = pid;
      _refresh();
    }
  }

  void _refresh() {
    final pid = _projectId;
    if (pid == null) return;
    setState(() {
      _future = _load(pid);
    });
  }

  Future<_CleanupData> _load(String pid) async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) throw StateError('请先登录');
    final client = ReviewsClient(repo.client.baseUrl, repo.client.bearerToken);
    final summary = await client.summary(pid);
    final findings = _selectedRule == null
        ? <WikiReview>[]
        : await client.list(
            projectId: pid,
            kind: _kindForRule(_selectedRule!),
            status: 'open',
            limit: 200,
          );
    final filtered = _selectedRule == null
        ? findings
        : findings.where((r) => _ruleIDOf(r) == _selectedRule).toList();
    return _CleanupData(summary: summary, findings: filtered);
  }

  Future<void> _delete(String reviewId) async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    final client = ReviewsClient(repo.client.baseUrl, repo.client.bearerToken);
    await client.deletePageForReview(reviewId);
    _refresh();
  }

  Future<void> _dismiss(String reviewId) async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    final client = ReviewsClient(repo.client.baseUrl, repo.client.bearerToken);
    await client.dismiss(reviewId);
    _refresh();
  }

  @override
  Widget build(BuildContext context) {
    final pid = _projectId;
    return PageScaffold(
      title: '维护清单',
      subtitle: 'orphan / empty / dead-link / stale 一站式整理',
      actions: [
        IconButton(
          tooltip: '刷新',
          icon: const Icon(Icons.refresh, size: 18),
          onPressed: _refresh,
        ),
      ],
      child: pid == null
          ? const _NoProject()
          : FutureBuilder<_CleanupData>(
              future: _future,
              builder: (_, snap) {
                if (snap.connectionState != ConnectionState.done) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snap.hasError) {
                  return Center(
                    child: Text(snap.error.toString(),
                        style: const TextStyle(color: BiuTokens.error)),
                  );
                }
                final data = snap.data!;
                return _selectedRule == null
                    ? _SummaryView(
                        summary: data.summary,
                        onPick: (rule) {
                          setState(() => _selectedRule = rule);
                          _refresh();
                        },
                      )
                    : _DrilldownView(
                        rule: _selectedRule!,
                        findings: data.findings,
                        onBack: () {
                          setState(() => _selectedRule = null);
                          _refresh();
                        },
                        onDelete: _delete,
                        onDismiss: _dismiss,
                      );
              },
            ),
    );
  }
}

// ── data ───────────────────────────────────────────────────────

class _CleanupData {
  final List<RuleCount> summary;
  final List<WikiReview> findings;
  const _CleanupData({required this.summary, required this.findings});
}

// ── summary view ──────────────────────────────────────────────

class _SummaryView extends StatelessWidget {
  const _SummaryView({required this.summary, required this.onPick});
  final List<RuleCount> summary;
  final void Function(String rule) onPick;

  @override
  Widget build(BuildContext context) {
    final cards = summary
        .where((r) => _ruleMeta(r.ruleId) != null && r.count > 0)
        .toList();
    if (cards.isEmpty) {
      return Center(
        child: Padding(
          padding: EdgeInsets.all(BiuTokens.space5),
          child: Text(
            '当前没有需要处理的清理项 — wiki 看起来很干净 🎉',
            style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
          ),
        ),
      );
    }
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space4),
      child: Wrap(
        spacing: BiuTokens.space3,
        runSpacing: BiuTokens.space3,
        children: [
          for (final c in cards)
            _RuleCard(
              ruleId: c.ruleId,
              count: c.count,
              onTap: () => onPick(c.ruleId),
            ),
        ],
      ),
    );
  }
}

class _RuleCard extends StatelessWidget {
  const _RuleCard({
    required this.ruleId,
    required this.count,
    required this.onTap,
  });
  final String ruleId;
  final int count;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final meta = _ruleMeta(ruleId)!;
    return SizedBox(
      width: 220,
      child: BiuCard(
        onTap: onTap,
        lift: 2,
        padding: const EdgeInsets.all(BiuTokens.space3),
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(meta.icon, size: 18, color: meta.color),
                const SizedBox(width: BiuTokens.space2),
                Text(
                  meta.label,
                  style: TextStyle(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: meta.color,
                  ),
                ),
              ],
            ),
            const SizedBox(height: BiuTokens.space2),
            Text(
              '$count',
              style: TextStyle(
                fontSize: 24,
                fontWeight: FontWeight.w700,
                color: BiuTokens.text,
              ),
            ),
            const SizedBox(height: 2),
            Text(
              meta.description,
              style: TextStyle(
                fontSize: 10,
                color: BiuTokens.textMuted,
                height: 1.4,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ── drill-down view ───────────────────────────────────────────

class _DrilldownView extends StatelessWidget {
  const _DrilldownView({
    required this.rule,
    required this.findings,
    required this.onBack,
    required this.onDelete,
    required this.onDismiss,
  });

  final String rule;
  final List<WikiReview> findings;
  final VoidCallback onBack;
  final Future<void> Function(String reviewId) onDelete;
  final Future<void> Function(String reviewId) onDismiss;

  @override
  Widget build(BuildContext context) {
    final meta = _ruleMeta(rule);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.all(BiuTokens.space3),
          child: Row(
            children: [
              IconButton(
                icon: const Icon(Icons.arrow_back, size: 16),
                onPressed: onBack,
              ),
              const SizedBox(width: BiuTokens.space2),
              if (meta != null) ...[
                Icon(meta.icon, color: meta.color, size: 16),
                const SizedBox(width: BiuTokens.space2),
                Text(
                  meta.label,
                  style: TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                    color: meta.color,
                  ),
                ),
                const SizedBox(width: BiuTokens.space2),
              ],
              Text('${findings.length} 项',
                  style: TextStyle(
                      fontSize: 11, color: BiuTokens.textMuted)),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(
          child: findings.isEmpty
              ? Center(
                  child: Text('没有匹配的清理项',
                      style: TextStyle(
                          color: BiuTokens.textMuted, fontSize: 12)),
                )
              : ListView.separated(
                  itemCount: findings.length,
                  separatorBuilder: (_, _) =>
                      Divider(height: 1, color: BiuTokens.borderSubtle),
                  itemBuilder: (_, i) => _FindingRow(
                    finding: findings[i],
                    rule: rule,
                    onDelete: onDelete,
                    onDismiss: onDismiss,
                  ),
                ),
        ),
      ],
    );
  }
}

class _FindingRow extends StatelessWidget {
  const _FindingRow({
    required this.finding,
    required this.rule,
    required this.onDelete,
    required this.onDismiss,
  });

  final WikiReview finding;
  final String rule;
  final Future<void> Function(String reviewId) onDelete;
  final Future<void> Function(String reviewId) onDismiss;

  bool get _ruleSupportsDelete => switch (rule) {
        'orphaned_page' || 'empty_page' || 'stub_page' || 'untitled_page' =>
          true,
        _ => false,
      };

  String? get _firstPageId =>
      finding.pageIds.isEmpty ? null : finding.pageIds.first;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  finding.title.isEmpty ? '(untitled)' : finding.title,
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: BiuTokens.text,
                  ),
                ),
                if (finding.description.isNotEmpty) ...[
                  const SizedBox(height: 2),
                  Text(
                    finding.description,
                    style: TextStyle(
                      fontSize: 11,
                      color: BiuTokens.textMuted,
                      height: 1.3,
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(width: BiuTokens.space2),
          if (_firstPageId != null)
            TextButton.icon(
              onPressed: () =>
                  context.go('/wiki?pageId=$_firstPageId'),
              icon: const Icon(Icons.open_in_new, size: 14),
              label: const Text('打开'),
            ),
          if (_ruleSupportsDelete)
            TextButton.icon(
              onPressed: () => onDelete(finding.id),
              icon: const Icon(Icons.delete_outline,
                  size: 14, color: BiuTokens.error),
              label: const Text('删除',
                  style: TextStyle(color: BiuTokens.error)),
            ),
          TextButton(
            onPressed: () => onDismiss(finding.id),
            child: const Text('忽略'),
          ),
        ],
      ),
    );
  }
}

// ── empty / no-project state ─────────────────────────────────

class _NoProject extends StatelessWidget {
  const _NoProject();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        '请先在 Wiki 中选择一个项目',
        style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
      ),
    );
  }
}

// ── rule metadata ────────────────────────────────────────────

class _RuleMeta {
  final String label;
  final String description;
  final IconData icon;
  final Color color;
  const _RuleMeta(this.label, this.description, this.icon, this.color);
}

_RuleMeta? _ruleMeta(String rule) => switch (rule) {
      'orphaned_page' => const _RuleMeta(
          '孤儿页面',
          '没有任何 wikilink 指向，且 60 天未编辑',
          Icons.unpublished_outlined,
          NamedPaletteStrong.amber,
        ),
      'empty_page' => const _RuleMeta(
          '空页面',
          '没有正文 block — 看起来是误创建',
          Icons.description_outlined,
          NamedPalette.gray, // empty page = neutral 灰
        ),
      'stub_page' => const _RuleMeta(
          'Stub 页面',
          '内容过短，可能写到一半被遗忘',
          Icons.short_text,
          NamedPaletteStrong.purple,
        ),
      'untitled_page' => const _RuleMeta(
          '未命名页面',
          '标题为空 — 重命名或删除',
          Icons.edit_note_outlined,
          NamedPaletteStrong.blue,
        ),
      'dead_wikilink' => const _RuleMeta(
          '死链',
          '[[X]] 指向不存在的页面 — 修正或新建',
          Icons.link_off,
          NamedPaletteStrong.red,
        ),
      'stale_page' => const _RuleMeta(
          '陈旧页面',
          '90 天未编辑 — 检查是否仍然准确',
          Icons.schedule,
          NamedPaletteStrong.emerald,
        ),
      _ => null,
    };

// Map rule_id → review_items.kind. lint and sweep produce different
// kinds so the list query needs the right filter.
String _kindForRule(String rule) => switch (rule) {
      'orphaned_page' || 'stale_page' => 'sweep',
      'untitled_page' || 'empty_page' || 'stub_page' || 'dead_wikilink' =>
        'lint',
      _ => 'lint',
    };

String _ruleIDOf(WikiReview r) =>
    (r.payload['rule_id'] as String?) ?? '';
