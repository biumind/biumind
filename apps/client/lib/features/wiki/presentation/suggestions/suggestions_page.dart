/// /suggestions —— 用户反馈 + 路线图（顶层路由，不进 wiki shell）。
///
/// 简化设计：单页 + 三 tab（全部 / 我的 / 路线图）+ 投票 + 提交 dialog。
/// knowcode 拆成 list/detail/my/submit 四页，本批合并到一页
/// 让访问入口最少。Detail 后续按需补（点行展开 inline 卡片）。
library;

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../core/ui/biu_card.dart';
import '../../../../data/api/wiki_client.dart' show WikiSuggestion;
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;

enum _Scope { public, mine }

final _suggestionsProvider =
    FutureProvider.family<List<WikiSuggestion>, _Scope>((ref, scope) async {
      final repo = ref.watch(wikiRepositoryProvider);
      if (repo == null) return const [];
      return repo.client.listSuggestions(
        scope: scope == _Scope.mine ? 'mine' : 'public',
      );
    });

class SuggestionsPage extends ConsumerStatefulWidget {
  const SuggestionsPage({super.key});

  @override
  ConsumerState<SuggestionsPage> createState() => _SuggestionsPageState();
}

class _SuggestionsPageState extends ConsumerState<SuggestionsPage> {
  _Scope _scope = _Scope.public;
  String? _category;

  @override
  Widget build(BuildContext context) {
    final asyncList = ref.watch(_suggestionsProvider(_scope));
    return Scaffold(
      backgroundColor: BiuTokens.bg,
      appBar: AppBar(
        title: const Text('反馈与路线图'),
        backgroundColor: BiuTokens.surface,
        foregroundColor: BiuTokens.text,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back, size: 18),
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        actions: [
          IconButton(
            tooltip: '刷新',
            onPressed: () => ref.invalidate(_suggestionsProvider(_scope)),
            icon: const Icon(Icons.refresh, size: 18),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(0, 8, 12, 8),
            child: FilledButton.icon(
              onPressed: () => _showSubmit(context, ref),
              icon: const Icon(Icons.add, size: 14),
              label: const Text('提交反馈'),
              style: FilledButton.styleFrom(
                backgroundColor: BiuTokens.green,
                textStyle: const TextStyle(fontSize: 12),
              ),
            ),
          ),
        ],
      ),
      body: Column(
        children: [
          _ScopeTabs(
            scope: _scope,
            onChanged: (s) => setState(() => _scope = s),
          ),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          _CategoryStrip(
            current: _category,
            onChanged: (c) => setState(() => _category = c),
          ),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          Expanded(
            child: asyncList.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => Center(
                child: Padding(
                  padding: const EdgeInsets.all(24),
                  child: SelectableText(
                    e.toString(),
                    style: TextStyle(color: BiuTokens.error, fontSize: 12),
                  ),
                ),
              ),
              data: (rows) {
                final filtered = _category == null
                    ? rows
                    : rows.where((s) => s.category == _category).toList();
                if (filtered.isEmpty) {
                  return const _EmptyView();
                }
                return ListView.separated(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 12,
                  ),
                  itemCount: filtered.length,
                  separatorBuilder: (_, _) => const SizedBox(height: 10),
                  itemBuilder: (_, i) => _SuggestionCard(
                    suggestion: filtered[i],
                    onVote: () => _vote(ref, filtered[i]),
                  ),
                );
              },
            ),
          ),
        ],
      ),
    );
  }

  Future<void> _vote(WidgetRef ref, WikiSuggestion s) async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    try {
      await repo.client.voteSuggestion(s.id, up: !s.myVote);
      ref.invalidate(_suggestionsProvider(_scope));
    } on Exception catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('投票失败：$e')));
    }
  }

  Future<void> _showSubmit(BuildContext context, WidgetRef ref) async {
    final titleCtrl = TextEditingController();
    final bodyCtrl = TextEditingController();
    String category = 'feature';
    final ok = await showAdaptiveDialog<bool>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) => AlertDialog(
          title: const Text('提交反馈'),
          content: SizedBox(
            width: 480,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Wrap(
                  spacing: 6,
                  children: [
                    for (final c in const ['feature', 'bug', 'idea', 'other'])
                      ChoiceChip(
                        label: Text(_categoryLabel(c)),
                        selected: category == c,
                        onSelected: (_) => setState(() => category = c),
                      ),
                  ],
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: titleCtrl,
                  autofocus: true,
                  decoration: const InputDecoration(
                    labelText: '标题',
                    hintText: '简短描述',
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: bodyCtrl,
                  maxLines: 5,
                  decoration: const InputDecoration(
                    labelText: '详情（可选）',
                    hintText: '具体场景、期望、复现路径...',
                    alignLabelWithHint: true,
                  ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('提交'),
            ),
          ],
        ),
      ),
    );
    if (ok != true) return;
    final title = titleCtrl.text.trim();
    if (title.isEmpty) return;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    try {
      await repo.client.submitSuggestion(
        title: title,
        body: bodyCtrl.text.trim(),
        category: category,
      );
      ref.invalidate(_suggestionsProvider(_scope));
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('已提交')));
    } on Exception catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('提交失败：$e')));
    }
  }
}

class _ScopeTabs extends StatelessWidget {
  const _ScopeTabs({required this.scope, required this.onChanged});
  final _Scope scope;
  final ValueChanged<_Scope> onChanged;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        children: [
          for (final pair in const [(_Scope.public, '全部'), (_Scope.mine, '我的')])
            Padding(
              padding: const EdgeInsets.only(right: 6),
              child: ChoiceChip(
                label: Text(pair.$2),
                selected: scope == pair.$1,
                onSelected: (_) => onChanged(pair.$1),
              ),
            ),
        ],
      ),
    );
  }
}

class _CategoryStrip extends StatelessWidget {
  const _CategoryStrip({required this.current, required this.onChanged});
  final String? current;
  final ValueChanged<String?> onChanged;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      child: Row(
        children: [
          _CatChip(
            label: '全部',
            active: current == null,
            onTap: () => onChanged(null),
          ),
          for (final c in const ['feature', 'bug', 'idea', 'other'])
            _CatChip(
              label: _categoryLabel(c),
              active: current == c,
              onTap: () => onChanged(c),
            ),
        ],
      ),
    );
  }
}

class _CatChip extends StatelessWidget {
  const _CatChip({
    required this.label,
    required this.active,
    required this.onTap,
  });
  final String label;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(right: 6),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(14),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            color: active
                ? BiuTokens.green.withValues(alpha: 0.1)
                : BiuTokens.surfaceMuted,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(
              color: active ? BiuTokens.green : BiuTokens.borderSubtle,
            ),
          ),
          child: Text(
            label,
            style: TextStyle(
              color: active ? BiuTokens.green : BiuTokens.textSecondary,
              fontSize: 12,
              fontWeight: active ? FontWeight.w600 : FontWeight.w400,
            ),
          ),
        ),
      ),
    );
  }
}

String _categoryLabel(String c) => switch (c) {
  'feature' => '✨ 新功能',
  'bug' => '🐞 缺陷',
  'idea' => '💡 想法',
  'other' => '📌 其他',
  _ => c,
};

class _SuggestionCard extends StatelessWidget {
  const _SuggestionCard({required this.suggestion, required this.onVote});
  final WikiSuggestion suggestion;
  final VoidCallback onVote;

  @override
  Widget build(BuildContext context) {
    final s = suggestion;
    final (statusLabel, statusColor) = switch (s.status) {
      'planned' => ('计划中', BiuTokens.purple),
      'shipped' => ('已发布', BiuTokens.success),
      'rejected' => ('已关闭', BiuTokens.textMuted),
      _ => ('待评估', BiuTokens.textSecondary),
    };
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(14),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // 投票按钮
          InkWell(
            onTap: onVote,
            borderRadius: BorderRadius.circular(8),
            child: Container(
              width: 48,
              padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 8),
              decoration: BoxDecoration(
                color: s.myVote
                    ? BiuTokens.green.withValues(alpha: 0.1)
                    : BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(8),
                border: Border.all(
                  color: s.myVote ? BiuTokens.green : BiuTokens.borderSubtle,
                ),
              ),
              child: Column(
                children: [
                  Icon(
                    Icons.arrow_upward,
                    size: 14,
                    color: s.myVote ? BiuTokens.green : BiuTokens.textSecondary,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    '${s.votes}',
                    style: TextStyle(
                      color: s.myVote ? BiuTokens.green : BiuTokens.text,
                      fontSize: 13,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        s.title,
                        style: TextStyle(
                          color: BiuTokens.text,
                          fontSize: 14,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 6,
                        vertical: 1,
                      ),
                      decoration: BoxDecoration(
                        color: statusColor.withValues(alpha: 0.1),
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Text(
                        statusLabel,
                        style: TextStyle(
                          color: statusColor,
                          fontSize: 10,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 4),
                Row(
                  children: [
                    Text(
                      _categoryLabel(s.category),
                      style: TextStyle(
                        color: BiuTokens.textMuted,
                        fontSize: 11,
                      ),
                    ),
                    if (s.authorEmail != null) ...[
                      const SizedBox(width: 8),
                      Text(
                        '· ${s.authorEmail}',
                        style: TextStyle(
                          color: BiuTokens.textMuted,
                          fontSize: 11,
                        ),
                      ),
                    ],
                  ],
                ),
                if (s.body.isNotEmpty) ...[
                  const SizedBox(height: 6),
                  Text(
                    s.body,
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: BiuTokens.textSecondary,
                      fontSize: 12,
                      height: 1.5,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView();
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.feedback_outlined, size: 48, color: BiuTokens.textMuted),
            const SizedBox(height: 12),
            Text(
              '还没有反馈',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              '点击右上「提交反馈」分享你的想法',
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}
