/// /wiki/p/:pid/research —— Deep Research 任务列表 + 新建。
///
/// 接 brain wiki/research 端点（已齐）：
///   GET  /v1/wiki/projects/{pid}/research      list
///   POST /v1/wiki/projects/{pid}/research      kick off (topic + queries)
///   GET  /v1/wiki/projects/{pid}/research/{id} read single
///
/// 任务状态：queued / searching / synthesizing / saving / done / error。
/// done 时 page_id 链入 /wiki/p/:pid/pages/:pageId 查看 LLM 综合后落地的 wiki 页。
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/api/research_client.dart';
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;

final _researchListProvider =
    FutureProvider.family<List<ResearchTask>, String>(
  (ref, projectId) async {
    final repo = ref.watch(wikiRepositoryProvider);
    if (repo == null || projectId.isEmpty) return const [];
    // 与 research_dialog 同款：复用 wiki client 的 baseUrl + bearer。
    return ResearchClient(repo.client.baseUrl, repo.client.bearerToken)
        .listTasks(projectId);
  },
);

class ResearchPage extends ConsumerWidget {
  const ResearchPage({super.key, required this.projectId});
  final String projectId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tasks = ref.watch(_researchListProvider(projectId));
    return Column(
      children: [
        _Header(
          onNew: () => _showNewDialog(context, ref),
          onRefresh: () =>
              ref.invalidate(_researchListProvider(projectId)),
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: tasks.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, _) => Center(
              child: SelectableText(
                e.toString(),
                style: TextStyle(color: BiuTokens.error, fontSize: 12),
              ),
            ),
            data: (items) {
              if (items.isEmpty) {
                return _EmptyView(
                  onNew: () => _showNewDialog(context, ref),
                );
              }
              return ListView.separated(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 12,
                ),
                itemCount: items.length,
                separatorBuilder: (_, _) => const SizedBox(height: 10),
                itemBuilder: (_, i) =>
                    _TaskCard(task: items[i], projectId: projectId),
              );
            },
          ),
        ),
      ],
    );
  }

  Future<void> _showNewDialog(BuildContext context, WidgetRef ref) async {
    final topicCtrl = TextEditingController();
    final queriesCtrl = TextEditingController();
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('新建研究主题'),
        content: SizedBox(
          width: 400,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: topicCtrl,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: '主题',
                  hintText: '例：transformers 的注意力机制',
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: queriesCtrl,
                maxLines: 3,
                decoration: const InputDecoration(
                  labelText: '辅助查询（每行一条，可选）',
                  hintText: 'attention is all you need\n…',
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
            child: const Text('开始研究'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final topic = topicCtrl.text.trim();
    if (topic.isEmpty) return;
    final queries = queriesCtrl.text
        .split('\n')
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toList();
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    try {
      await ResearchClient(repo.client.baseUrl, repo.client.bearerToken)
          .startTask(
        projectId,
        topic: topic,
        queries: queries,
      );
      ref.invalidate(_researchListProvider(projectId));
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已启动研究：$topic')),
      );
    } on Exception catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('启动失败：$e')),
      );
    }
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.onNew, required this.onRefresh});
  final VoidCallback onNew;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 48,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
          const PhoneBackButton(),
          Icon(Icons.travel_explore_outlined,
              size: 16, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Text(
            '深度研究',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const Spacer(),
          IconButton(
            tooltip: '刷新',
            onPressed: onRefresh,
            icon: const Icon(Icons.refresh, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          const SizedBox(width: 4),
          FilledButton.icon(
            onPressed: onNew,
            icon: const Icon(Icons.add, size: 14),
            label: const Text('新研究'),
            style: FilledButton.styleFrom(
              backgroundColor: BiuTokens.purple,
              padding:
                  const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              textStyle: const TextStyle(fontSize: 12),
              minimumSize: const Size(0, 32),
            ),
          ),
        ],
      ),
    );
  }
}

class _TaskCard extends StatelessWidget {
  const _TaskCard({required this.task, required this.projectId});
  final ResearchTask task;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (task.status) {
      'queued' => ('排队中', BiuTokens.textMuted),
      'searching' => ('搜索中', BiuTokens.purple),
      'synthesizing' => ('综合中', BiuTokens.purple),
      'saving' => ('落页中', BiuTokens.purple),
      'done' => ('完成', BiuTokens.success),
      'error' => ('失败', BiuTokens.error),
      _ => (task.status, BiuTokens.textMuted),
    };
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.travel_explore_outlined,
                  size: 16, color: BiuTokens.purple),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  task.topic,
                  style: TextStyle(
                    color: BiuTokens.text,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: color.withValues(alpha: 0.1),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  label,
                  style: TextStyle(
                    color: color,
                    fontSize: 10,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          if (task.queries.isNotEmpty) ...[
            const SizedBox(height: 8),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              children: task.queries
                  .map((q) => Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: BiuTokens.surfaceMuted,
                          borderRadius: BorderRadius.circular(4),
                          border: Border.all(color: BiuTokens.borderSubtle),
                        ),
                        child: Text(
                          q,
                          style: TextStyle(
                            color: BiuTokens.textSecondary,
                            fontSize: 11,
                          ),
                        ),
                      ))
                  .toList(),
            ),
          ],
          if (task.synthesis.isNotEmpty) ...[
            const SizedBox(height: 10),
            Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              ),
              child: Text(
                task.synthesis,
                maxLines: 4,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: BiuTokens.textSecondary,
                  fontSize: 12,
                  height: 1.5,
                ),
              ),
            ),
          ],
          if (task.error != null && task.error!.isNotEmpty) ...[
            const SizedBox(height: 10),
            Container(
              padding: const EdgeInsets.all(8),
              decoration: BoxDecoration(
                color: BiuTokens.errorSoft,
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              ),
              child: Text(
                task.error!,
                style: TextStyle(color: BiuTokens.error, fontSize: 11),
              ),
            ),
          ],
          const SizedBox(height: 10),
          Row(
            children: [
              Text(
                _formatDate(task.updatedAt),
                style: TextStyle(color: BiuTokens.textMuted, fontSize: 11),
              ),
              const Spacer(),
              if (task.pageId != null)
                TextButton.icon(
                  onPressed: () => context.go(
                    '/wiki/p/$projectId/pages/${task.pageId}',
                  ),
                  icon: const Icon(Icons.description_outlined, size: 14),
                  label: const Text('打开生成页'),
                  style: TextButton.styleFrom(
                    foregroundColor: BiuTokens.purple,
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    minimumSize: const Size(0, 28),
                    textStyle: const TextStyle(fontSize: 12),
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }

  String _formatDate(DateTime t) {
    return '${t.year}-${t.month.toString().padLeft(2, '0')}-'
        '${t.day.toString().padLeft(2, '0')} '
        '${t.hour.toString().padLeft(2, '0')}:'
        '${t.minute.toString().padLeft(2, '0')}';
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView({required this.onNew});
  final VoidCallback onNew;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.travel_explore_outlined,
                size: 48, color: BiuTokens.textMuted),
            const SizedBox(height: 12),
            Text(
              '还没有研究主题',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              '指定一个主题，LLM 会做联网搜索 + 综合，把结果落到一个 wiki 页面',
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
            const SizedBox(height: 16),
            FilledButton.icon(
              onPressed: onNew,
              icon: const Icon(Icons.add, size: 14),
              label: const Text('新建研究主题'),
              style: FilledButton.styleFrom(
                backgroundColor: BiuTokens.purple,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
