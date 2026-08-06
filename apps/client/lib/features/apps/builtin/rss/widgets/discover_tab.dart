// Discover tab — starter packs grid + URL discover + OPML import.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';

class DiscoverTab extends ConsumerWidget {
  const DiscoverTab({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(starterPacksProvider);
    return ListView(
      padding: const EdgeInsets.all(BiuTokens.space5),
      children: [
        const _SectionTitle('🚀 主题包', '一键订阅一组高质量源'),
        const SizedBox(height: BiuTokens.space3),
        async.when(
          loading: () => const _GridSkeleton(),
          error: (e, _) => Text('加载失败: $e',
              style: TextStyle(color: BiuTokens.textSecondary)),
          data: (packs) => _PacksGrid(packs: packs),
        ),
        const SizedBox(height: BiuTokens.space6),
        const _SectionTitle('📥 OPML 导入', '从 Feedly / Inoreader 导出 OPML 全量迁移'),
        const SizedBox(height: BiuTokens.space3),
        _OpmlImport(),
      ],
    );
  }
}

class _SectionTitle extends StatelessWidget {
  const _SectionTitle(this.title, this.sub);
  final String title;
  final String sub;
  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title,
            style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700)),
        const SizedBox(height: 4),
        Text(sub,
            style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary)),
      ],
    );
  }
}

class _PacksGrid extends ConsumerWidget {
  const _PacksGrid({required this.packs});
  final List<StarterPack> packs;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final width = MediaQuery.of(context).size.width;
    final cols = width >= 1100 ? 3 : (width >= 700 ? 2 : 1);
    return GridView.count(
      crossAxisCount: cols,
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      childAspectRatio: 2.0,
      mainAxisSpacing: BiuTokens.space3,
      crossAxisSpacing: BiuTokens.space3,
      children: packs.map((p) => _PackCard(pack: p)).toList(),
    );
  }
}

class _PackCard extends ConsumerStatefulWidget {
  const _PackCard({required this.pack});
  final StarterPack pack;
  @override
  ConsumerState<_PackCard> createState() => _PackCardState();
}

class _PackCardState extends ConsumerState<_PackCard> {
  bool _busy = false;

  Future<void> _install() async {
    final api = ref.read(rssApiProvider);
    if (api == null) return;
    setState(() => _busy = true);
    try {
      final r = await api.invoke(
          'starter_packs_install', {'pack_id': widget.pack.id});
      final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
      final added = result['added'] ?? 0;
      final skipped = result['skipped'] ?? 0;
      ref.refreshFeeds();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('已订阅 $added 个 · 跳过 $skipped 个 (已存在)'),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('订阅失败: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return InkWell(
      onTap: _busy ? null : _install,
      borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
      child: Container(
        padding: const EdgeInsets.all(BiuTokens.space4),
        decoration: BoxDecoration(
          color: scheme.primary.withValues(alpha: 0.06),
          borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
          border: Border.all(color: scheme.primary.withValues(alpha: 0.18)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text(widget.pack.iconEmoji,
                    style: const TextStyle(fontSize: 22)),
                const SizedBox(width: 8),
                Text(widget.pack.name,
                    style: const TextStyle(
                        fontSize: 15, fontWeight: FontWeight.w700)),
                const Spacer(),
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                  decoration: BoxDecoration(
                    color: scheme.primary.withValues(alpha: 0.12),
                    borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                  ),
                  child: Text('${widget.pack.feedCount} 个源',
                      style: TextStyle(
                          fontSize: 10,
                          fontWeight: FontWeight.w600,
                          color: scheme.primary)),
                ),
              ],
            ),
            const SizedBox(height: 4),
            Expanded(
              child: Text(
                widget.pack.description,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
              ),
            ),
            const SizedBox(height: 4),
            Align(
              alignment: Alignment.bottomRight,
              child: _busy
                  ? const SizedBox(
                      width: 16, height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2))
                  : Icon(Icons.add_circle_outline,
                      size: 18, color: scheme.primary),
            ),
          ],
        ),
      ),
    );
  }
}

class _GridSkeleton extends StatelessWidget {
  const _GridSkeleton();
  @override
  Widget build(BuildContext context) {
    return GridView.count(
      crossAxisCount: 3,
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      childAspectRatio: 2.0,
      mainAxisSpacing: BiuTokens.space3,
      crossAxisSpacing: BiuTokens.space3,
      children: List.generate(3, (_) => Container(
        decoration: BoxDecoration(
          color: BiuTokens.surfaceMuted,
          borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        ),
      )),
    );
  }
}

class _OpmlImport extends ConsumerStatefulWidget {
  @override
  ConsumerState<_OpmlImport> createState() => _OpmlImportState();
}

class _OpmlImportState extends ConsumerState<_OpmlImport> {
  final _ctrl = TextEditingController();
  bool _busy = false;

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _import() async {
    final xml = _ctrl.text.trim();
    if (xml.isEmpty) return;
    final api = ref.read(rssApiProvider);
    if (api == null) return;
    setState(() => _busy = true);
    try {
      final r = await api.invoke('opml_import', {'xml': xml});
      final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
      final added = result['added'] ?? 0;
      final skipped = result['skipped'] ?? 0;
      final failed = (result['failed'] as List?)?.length ?? 0;
      ref.refreshFeeds();
      if (!mounted) return;
      _ctrl.clear();
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('已导入 $added 个 · 跳过 $skipped · 失败 $failed'),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('导入失败: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _export() async {
    final api = ref.read(rssApiProvider);
    if (api == null) return;
    try {
      final r = await api.invoke('opml_export');
      final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
      final xml = result['xml'] as String? ?? '';
      _ctrl.text = xml;
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已导出 ${result['count']} 个源到下方文本框')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('导出失败: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TextField(
          controller: _ctrl,
          maxLines: 6,
          enabled: !_busy,
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            hintText: '<opml version="1.0"> ... </opml>',
            isDense: true,
          ),
          style: const TextStyle(fontFamily: 'monospace', fontSize: 11),
        ),
        const SizedBox(height: BiuTokens.space2),
        Row(
          children: [
            FilledButton.tonalIcon(
              onPressed: _busy ? null : _import,
              icon: _busy
                  ? const SizedBox(
                      width: 14, height: 14,
                      child: CircularProgressIndicator(strokeWidth: 2))
                  : const Icon(Icons.upload, size: 16),
              label: const Text('导入到我的订阅'),
            ),
            const SizedBox(width: BiuTokens.space2),
            OutlinedButton.icon(
              onPressed: _export,
              icon: const Icon(Icons.download, size: 16),
              label: const Text('导出我的订阅'),
            ),
          ],
        ),
      ],
    );
  }
}
