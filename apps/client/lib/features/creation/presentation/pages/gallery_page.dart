// 创意广场 — 公开瀑布流, 类型 tab 过滤 + 关键词搜索 + 详情 sheet「做同款」.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/aigc_providers.dart';
import '../../domain/creation_task.dart';
import '../widgets/creation_card.dart';
import 'task_detail_sheet.dart';

class GalleryPage extends ConsumerStatefulWidget {
  const GalleryPage({super.key});

  @override
  ConsumerState<GalleryPage> createState() => _GalleryPageState();
}

class _GalleryPageState extends ConsumerState<GalleryPage> {
  String? _typeFilter; // null = 全部
  String _keyword = '';
  late final TextEditingController _searchCtrl;

  @override
  void initState() {
    super.initState();
    _searchCtrl = TextEditingController();
  }

  @override
  void dispose() {
    _searchCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final query = GalleryQuery(
      type: _typeFilter,
      keyword: _keyword.isEmpty ? null : _keyword,
    );
    final asyncList = ref.watch(aigcGalleryProvider(query));
    final t = AppLocalizations.of(context)!;

    return Container(
      color: BiuTokens.bg,
      child: Column(
        children: [
          _Toolbar(
            current: _typeFilter,
            onPickType: (v) => setState(() => _typeFilter = v),
            controller: _searchCtrl,
            onSubmit: (v) => setState(() => _keyword = v.trim()),
          ),
          Expanded(
            child: asyncList.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => Center(
                child: Text('$e', style: TextStyle(color: BiuTokens.error)),
              ),
              data: (raw) {
                final tasks = raw.whereType<CreationTask>().toList();
                if (tasks.isEmpty) {
                  return _GalleryEmpty(localizations: t);
                }
                return _Grid(tasks: tasks);
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _Toolbar extends StatelessWidget {
  const _Toolbar({
    required this.current,
    required this.onPickType,
    required this.controller,
    required this.onSubmit,
  });
  final String? current;
  final ValueChanged<String?> onPickType;
  final TextEditingController controller;
  final ValueChanged<String> onSubmit;

  @override
  Widget build(BuildContext context) {
    final filters = <(String?, String, IconData)>[
      (null, '全部', Icons.apps),
      ('image', '图片', Icons.image_outlined),
      ('video', '视频', Icons.movie_outlined),
      ('digital_human', '数字人', Icons.person_outline),
    ];
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 12),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Row(
        children: [
          for (final f in filters) ...[
            _FilterChip(
              label: f.$2,
              icon: f.$3,
              active: current == f.$1,
              onTap: () => onPickType(f.$1),
            ),
            const SizedBox(width: 6),
          ],
          const Spacer(),
          SizedBox(
            width: 240,
            height: 36,
            child: TextField(
              controller: controller,
              decoration: InputDecoration(
                hintText: '搜索关键词',
                prefixIcon: Icon(Icons.search, size: 16, color: BiuTokens.textMuted),
                contentPadding: const EdgeInsets.symmetric(horizontal: 8, vertical: 0),
                isDense: true,
              ),
              onSubmitted: onSubmit,
            ),
          ),
        ],
      ),
    );
  }
}

class _FilterChip extends StatelessWidget {
  const _FilterChip({
    required this.label,
    required this.icon,
    required this.active,
    required this.onTap,
  });
  final String label;
  final IconData icon;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: active ? BiuTokens.purpleSoft : Colors.transparent,
      borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon,
                  size: 14,
                  color: active ? BiuTokens.purple : BiuTokens.textSecondary),
              const SizedBox(width: 4),
              Text(
                label,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: active ? FontWeight.w600 : FontWeight.w500,
                  color: active ? BiuTokens.purple : BiuTokens.textSecondary,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _GalleryEmpty extends StatelessWidget {
  const _GalleryEmpty({required this.localizations});
  final AppLocalizations localizations;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.collections_outlined, size: 56, color: BiuTokens.textMuted),
          const SizedBox(height: 12),
          Text(
            '广场暂无公开作品',
            style: TextStyle(
              fontSize: 16,
              fontWeight: FontWeight.w600,
              color: BiuTokens.text,
            ),
          ),
          const SizedBox(height: 6),
          Text(
            '换个类型试试 — 或去创作一张, 公开后会出现在这里',
            style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
          ),
          const SizedBox(height: 16),
          FilledButton.icon(
            onPressed: () => context.go('/creation/center'),
            icon: const Icon(Icons.auto_awesome, size: 16),
            label: const Text('去创作'),
          ),
        ],
      ),
    );
  }
}

class _Grid extends ConsumerWidget {
  const _Grid({required this.tasks});
  final List<CreationTask> tasks;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final w = MediaQuery.of(context).size.width;
    final cols = (w / 220).floor().clamp(2, 6);
    return GridView.builder(
      padding: const EdgeInsets.all(16),
      itemCount: tasks.length,
      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: cols,
        mainAxisSpacing: 12,
        crossAxisSpacing: 12,
      ),
      itemBuilder: (_, i) {
        final t = tasks[i];
        return CreationCard(
          task: t,
          onTap: () => showTaskDetailSheet(
            context,
            ref,
            t,
            ownedByCurrentUser: false,
          ),
        );
      },
    );
  }
}
