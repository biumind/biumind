// ReviewsPage — the wiki audit queue.
//
// One page lists all open kind=dedup|lint|sweep findings produced by
// the brain-side workers. Each row exposes:
//
//   * Title + description from the rule that flagged it
//   * The page(s) it concerns (links into the wiki page editor)
//   * Action buttons:
//       - dismiss   : "this isn't a real issue, don't re-flag"
//       - resolve   : "I fixed it manually"
//       - merge     : (dedup only) "fold duplicate into canonical"
//
// The page itself is read-only state-wise — every action mutates via
// ReviewsController and the controller drives a refetch so subsequent
// renders reflect server truth.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/layout/phone_nav.dart';
import '../../../core/ui/biu_card.dart';
import '../../../data/api/reviews_client.dart';
import '../../../shared/page_scaffold.dart';
import '../application/reviews_controller.dart';
import 'merge_dialog.dart';
import 'research_dialog.dart';
import 'review_type_config.dart';

class ReviewsPage extends ConsumerWidget {
  const ReviewsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(reviewsControllerProvider);
    // PageScaffold 无 leading 槽 —— 按 §3.3 fallback 在主体顶部加一行 ←
    // (手机形态; 桌面 shrink 零高度, 像素不变)。
    return Column(
      children: [
        const Align(alignment: Alignment.centerLeft, child: PhoneBackButton()),
        Expanded(
          child: PageScaffold(
            title: '审阅队列',
            subtitle: 'dedup / lint / sweep 后台工作进程发现的可整理点',
            actions: [
              PopupMenuButton<String>(
                tooltip: '扫描',
                icon: const Icon(Icons.find_in_page_outlined, size: 18),
                onSelected: (family) async {
                  final res = await ref
                      .read(reviewsControllerProvider.notifier)
                      .scanLint(family);
                  if (!context.mounted) return;
                  final msg = res == null
                      ? '扫描失败：未配置凭据或无活动项目'
                      : res.queued
                          ? '语义扫描已提交，后台处理中，稍后刷新查看'
                          : '结构扫描完成，新增 ${res.findingsAdded} 条';
                  ScaffoldMessenger.maybeOf(context)?.showSnackBar(
                    SnackBar(
                      content: Text(msg),
                      duration: const Duration(seconds: 3),
                    ),
                  );
                },
                itemBuilder: (_) => const [
                  PopupMenuItem(
                    value: 'structural',
                    child: ListTile(
                      leading: Icon(Icons.rule, size: 20),
                      title: Text('结构扫描'),
                      subtitle: Text('空标题 / 孤儿页 / 重复标题 等规则'),
                      dense: true,
                      contentPadding: EdgeInsets.zero,
                    ),
                  ),
                  PopupMenuItem(
                    value: 'semantic',
                    child: ListTile(
                      leading: Icon(Icons.psychology_outlined, size: 20),
                      title: Text('语义扫描'),
                      subtitle: Text('LLM 判定矛盾 / 过时 等语义问题'),
                      dense: true,
                      contentPadding: EdgeInsets.zero,
                    ),
                  ),
                ],
              ),
              IconButton(
                tooltip: '刷新',
                icon: const Icon(Icons.refresh, size: 18),
                onPressed: () =>
                    ref.read(reviewsControllerProvider.notifier).refresh(),
              ),
            ],
            child: state.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => _ErrorView(message: e.toString()),
              data: (s) {
                if (s.noCredentials) {
                  return const _NoCredsView();
                }
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _KindTabs(
                      current: s.kind,
                      onChange: (k) => ref
                          .read(reviewsControllerProvider.notifier)
                          .setKind(k),
                    ),
                    const SizedBox(height: BiuTokens.space4),
                    if (s.lastError != null)
                      Builder(
                        builder: (ctx) {
                          final c = Theme.of(ctx).extension<BiuColors>()!;
                          return Container(
                            padding: const EdgeInsets.all(BiuTokens.space3),
                            margin: const EdgeInsets.only(
                              bottom: BiuTokens.space3,
                            ),
                            decoration: BoxDecoration(
                              color: c.errorSoft,
                              borderRadius: BorderRadius.circular(
                                BiuTokens.radiusMd,
                              ),
                            ),
                            child: Text(
                              '操作失败：${s.lastError}',
                              style: TextStyle(color: c.error),
                            ),
                          );
                        },
                      ),
                    Expanded(
                      child: _ReviewList(state: s, ref: ref),
                    ),
                  ],
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}

// ─── Tabs ──────────────────────────────────────────────────────

class _KindTabs extends StatelessWidget {
  const _KindTabs({required this.current, required this.onChange});

  final ReviewsKindFilter current;
  final void Function(ReviewsKindFilter) onChange;

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: BiuTokens.space2,
      children: ReviewsKindFilter.values.map((k) {
        final selected = k == current;
        return ChoiceChip(
          label: Text(k.label()),
          selected: selected,
          onSelected: (_) {
            if (!selected) onChange(k);
          },
        );
      }).toList(),
    );
  }
}

// ─── List ──────────────────────────────────────────────────────

class _ReviewList extends StatelessWidget {
  const _ReviewList({required this.state, required this.ref});

  final ReviewsState state;
  final WidgetRef ref;

  @override
  Widget build(BuildContext context) {
    if (state.reviews.isEmpty) {
      return _EmptyView(kind: state.kind);
    }
    return ListView.separated(
      itemCount: state.reviews.length,
      separatorBuilder: (_, _) => const SizedBox(height: BiuTokens.space2),
      itemBuilder: (ctx, i) {
        final r = state.reviews[i];
        return _ReviewCard(
          review: r,
          isPending: state.pending.contains(r.id),
          ref: ref,
        );
      },
    );
  }
}

class _ReviewCard extends StatelessWidget {
  const _ReviewCard({
    required this.review,
    required this.isPending,
    required this.ref,
  });

  final WikiReview review;
  final bool isPending;
  final WidgetRef ref;

  @override
  Widget build(BuildContext context) {
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(BiuTokens.space4),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              _KindBadge(kind: review.kind),
              if (extractReviewType(review.payload) != null) ...[
                const SizedBox(width: 6),
                ReviewTypeBadge(reviewType: extractReviewType(review.payload)!),
              ],
              const SizedBox(width: BiuTokens.space3),
              Expanded(
                child: Text(
                  review.title,
                  style: const TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          if (review.description.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space2),
            Text(
              review.description,
              style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
            ),
          ],
          const SizedBox(height: BiuTokens.space3),
          _PageLinks(projectId: review.projectId, pageIds: review.pageIds),
          const SizedBox(height: BiuTokens.space3),
          _Actions(review: review, isPending: isPending, ref: ref),
        ],
      ),
    );
  }
}

class _KindBadge extends StatelessWidget {
  const _KindBadge({required this.kind});
  final String kind;

  Color _bg() => switch (kind) {
    'dedup' => WikiReviewStatus.dedupBg,
    'lint' => WikiReviewStatus.lintBg,
    'sweep' => WikiReviewStatus.sweepBg,
    'merge' => WikiReviewStatus.mergeBg,
    'contradiction' => WikiReviewStatus.contradictionBg,
    _ => WikiReviewStatus.otherBg,
  };

  Color _fg() => switch (kind) {
    'dedup' => WikiReviewStatus.dedupFg,
    'lint' => WikiReviewStatus.lintFg,
    'sweep' => WikiReviewStatus.sweepFg,
    'merge' => WikiReviewStatus.mergeFg,
    'contradiction' => WikiReviewStatus.contradictionFg,
    _ => WikiReviewStatus.otherFg,
  };

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: BiuTokens.space2,
        vertical: 2,
      ),
      decoration: BoxDecoration(
        color: _bg(),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        kind,
        style: TextStyle(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.4,
          color: _fg(),
        ),
      ),
    );
  }
}

class _PageLinks extends StatelessWidget {
  const _PageLinks({required this.projectId, required this.pageIds});
  final String projectId;
  final List<String> pageIds;

  @override
  Widget build(BuildContext context) {
    if (pageIds.isEmpty) {
      return Text(
        '(无关联页面)',
        style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
      );
    }
    return Wrap(
      spacing: BiuTokens.space2,
      runSpacing: 4,
      children: [
        for (var i = 0; i < pageIds.length; i++)
          InkWell(
            // 深链到项目浏览器的页面详情（路由已支持 pageId 路径参数）。
            onTap: () =>
                context.go('/wiki/p/$projectId/pages/${pageIds[i]}'),
            child: Container(
              padding: const EdgeInsets.symmetric(
                horizontal: BiuTokens.space2,
                vertical: 2,
              ),
              decoration: BoxDecoration(
                color: BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                _shortId(pageIds[i]),
                style: TextStyle(
                  fontSize: 11,
                  fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                  color: BiuTokens.textMuted,
                ),
              ),
            ),
          ),
      ],
    );
  }

  static String _shortId(String id) => id.length >= 8 ? id.substring(0, 8) : id;
}

class _Actions extends StatelessWidget {
  const _Actions({
    required this.review,
    required this.isPending,
    required this.ref,
  });

  final WikiReview review;
  final bool isPending;
  final WidgetRef ref;

  /// review → Deep Research 一键链路：以 title 为话题、description 为细化
  /// 查询直接开跑（autoStart），sourceReviewId 让服务端在研究落页后自动
  /// resolve 本条审阅项。对话框关闭后刷新队列反映自动 resolve。
  Future<void> _research(BuildContext context, ReviewsController ctrl) async {
    await showResearchDialog(
      context,
      projectId: review.projectId,
      initialTopic: review.title.isNotEmpty
          ? review.title
          : '审阅项 ${review.id.substring(0, 8)}',
      initialQueries: [
        if (review.description.isNotEmpty) review.description,
      ],
      autoStart: true,
      sourceReviewId: review.id,
    );
    await ctrl.refresh();
  }

  @override
  Widget build(BuildContext context) {
    final ctrl = ref.read(reviewsControllerProvider.notifier);
    return Row(
      mainAxisAlignment: MainAxisAlignment.end,
      children: [
        OutlinedButton.icon(
          onPressed: isPending ? null : () => _research(context, ctrl),
          icon: const Icon(Icons.travel_explore, size: 14),
          label: const Text('研究'),
        ),
        const SizedBox(width: BiuTokens.space2),
        if ((review.kind == 'dedup' || review.kind == 'merge') &&
            review.isPair)
          OutlinedButton.icon(
            onPressed: isPending
                ? null
                : () => showMergeDialog(
                    context,
                    projectId: review.projectId,
                    pageAId: review.pageIds[0],
                    pageBId: review.pageIds[1],
                    onMerge:
                        ({
                          required String canonicalId,
                          required String duplicateId,
                        }) => ctrl.merge(
                          reviewId: review.id,
                          canonicalId: canonicalId,
                          duplicateId: duplicateId,
                        ),
                  ),
            icon: const Icon(Icons.merge_type, size: 14),
            label: const Text('合并…'),
          ),
        const SizedBox(width: BiuTokens.space2),
        if (review.kind == 'contradiction') ...[
          OutlinedButton.icon(
            onPressed: isPending ? null : () => ctrl.createQueryPage(review),
            icon: const Icon(Icons.help_outline, size: 14),
            label: const Text('建查询页'),
          ),
          const SizedBox(width: BiuTokens.space2),
        ],
        OutlinedButton(
          onPressed: isPending ? null : () => ctrl.resolve(review.id),
          child: const Text('已处理'),
        ),
        const SizedBox(width: BiuTokens.space2),
        TextButton(
          onPressed: isPending ? null : () => ctrl.dismiss(review.id),
          child: const Text('忽略'),
        ),
      ],
    );
  }
}

// ─── Empty + error ────────────────────────────────────────────

class _EmptyView extends StatelessWidget {
  const _EmptyView({required this.kind});
  final ReviewsKindFilter kind;

  String _hint() {
    switch (kind) {
      case ReviewsKindFilter.dedup:
        return '当前 dedup 工作进程没有发现重复页面候选 (cosine ≥ 0.92)。\n'
            'embedding 还在补齐时这里也会暂时为空。';
      case ReviewsKindFilter.lint:
        return '所有页面都通过了 untitled / empty / stub / dead-wikilink 4 条规则。';
      case ReviewsKindFilter.sweep:
        return '没有页面进入陈旧或孤立状态（默认 90 天 / 60 天 + 无入链）。';
      case ReviewsKindFilter.all:
        return '当前没有打开的审阅项。';
    }
  }

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.task_alt, size: 40, color: BiuTokens.textMuted),
            const SizedBox(height: BiuTokens.space3),
            Text(
              _hint(),
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.message});
  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline, size: 40, color: BiuTokens.textMuted),
            const SizedBox(height: BiuTokens.space3),
            Text(
              message,
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

class _NoCredsView extends StatelessWidget {
  const _NoCredsView();
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: EdgeInsets.all(BiuTokens.space5),
        child: Text(
          '请先在「设置」中登录 BiuMind 账号。',
          style: TextStyle(color: BiuTokens.textMuted),
        ),
      ),
    );
  }
}
