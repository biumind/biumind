// 创作灵感页 — Hero + GenerationPanel + 推荐瀑布流 (画廊节选).
//
// 推荐瀑布流走 aigcGalleryProvider(GalleryQuery(limit: 12)), 横滑预览;
// 「查看更多」跳 /creation/gallery.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/aigc_providers.dart';
import '../../domain/creation_task.dart';
import '../widgets/creation_card.dart';
import '../widgets/generation_panel.dart';
import 'task_detail_sheet.dart';

class InspirationPage extends ConsumerWidget {
  const InspirationPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = AppLocalizations.of(context)!;
    final asyncList =
        ref.watch(aigcGalleryProvider(const GalleryQuery(limit: 12)));

    return Container(
      color: BiuTokens.bg,
      child: SingleChildScrollView(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 960),
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  _Hero(title: t.creationHeroTitle, subtitle: t.creationHeroSubtitle),
                  const SizedBox(height: 24),
                  const GenerationPanel(),
                  const SizedBox(height: 32),
                  Row(
                    children: [
                      Text(
                        '热门作品',
                        style: TextStyle(
                          fontSize: 16,
                          fontWeight: FontWeight.w600,
                          color: BiuTokens.text,
                        ),
                      ),
                      const Spacer(),
                      TextButton.icon(
                        onPressed: () => context.go('/creation/gallery'),
                        icon: const Icon(Icons.arrow_forward, size: 14),
                        label: const Text('更多'),
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  asyncList.when(
                    loading: () => const Padding(
                      padding: EdgeInsets.all(24),
                      child: Center(child: CircularProgressIndicator()),
                    ),
                    error: (e, _) => Text('$e',
                        style: TextStyle(color: BiuTokens.error)),
                    data: (raw) {
                      final tasks = raw.whereType<CreationTask>().toList();
                      if (tasks.isEmpty) {
                        return Padding(
                          padding: const EdgeInsets.all(16),
                          child: Text(
                            '暂无公开作品 — 点上方面板生成第一张',
                            style: TextStyle(color: BiuTokens.textMuted),
                          ),
                        );
                      }
                      return _RecommendStrip(tasks: tasks);
                    },
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _Hero extends StatelessWidget {
  const _Hero({required this.title, required this.subtitle});
  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          style: TextStyle(
            fontSize: 36,
            fontWeight: FontWeight.w700,
            color: BiuTokens.text,
            letterSpacing: -0.5,
          ),
        ),
        const SizedBox(height: 8),
        Text(
          subtitle,
          style: TextStyle(fontSize: 14, color: BiuTokens.textMuted),
        ),
      ],
    );
  }
}

class _RecommendStrip extends ConsumerWidget {
  const _RecommendStrip({required this.tasks});
  final List<CreationTask> tasks;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return SizedBox(
      height: 200,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: tasks.length,
        separatorBuilder: (_, i) => const SizedBox(width: 12),
        itemBuilder: (_, i) => SizedBox(
          width: 200,
          child: CreationCard(
            task: tasks[i],
            onTap: () => showTaskDetailSheet(
              context,
              ref,
              tasks[i],
              ownedByCurrentUser: false,
            ),
          ),
        ),
      ),
    );
  }
}
