// MergeDialog — interactive page-merge UX for the dedup review queue.
//
// Backend contract (services/brain/internal/wiki/reviews/api.go):
//
//   POST /v1/wiki/pages/{canonical}/merge { from_id }
//
// All blocks of `from_id` are folded into `canonical`; `canonical`
// keeps its title; `from_id` is soft-deleted. The choice of which page
// is canonical is the only meaningful decision the user makes — the
// rest is observation.
//
// This dialog presents both pages side-by-side, lets the user pick
// canonical, and only fires the merge after explicit confirmation.

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/adaptive_dialog.dart';
import '../../../data/api/wiki_client.dart';
import '../../../data/wiki_providers.dart';
import 'mermaid/mermaid_preview.dart';
import 'mermaid/mermaid_url.dart';
import 'wikilink/wikilink_text.dart';

/// Opens the merge dialog and returns true when the user confirmed
/// (and the merge call succeeded). Caller is responsible for follow-up
/// (e.g. resolving the related dedup review).
Future<bool> showMergeDialog(
  BuildContext context, {
  required String projectId,
  required String pageAId,
  required String pageBId,
  required Future<void> Function({
    required String canonicalId,
    required String duplicateId,
  })
  onMerge,
}) async {
  // barrierDismissible:false —— 合并是破坏性确认, 防误触关闭; 宽屏透传
  // showDialog, 手机映射 sheet 的 isDismissible/enableDrag。
  final ok = await showAdaptiveDialog<bool>(
    context: context,
    barrierDismissible: false,
    builder: (_) => MergeDialog(
      projectId: projectId,
      pageAId: pageAId,
      pageBId: pageBId,
      onMerge: onMerge,
    ),
  );
  return ok ?? false;
}

class MergeDialog extends ConsumerStatefulWidget {
  const MergeDialog({
    super.key,
    required this.projectId,
    required this.pageAId,
    required this.pageBId,
    required this.onMerge,
  });

  final String projectId;
  final String pageAId;
  final String pageBId;
  final Future<void> Function({
    required String canonicalId,
    required String duplicateId,
  })
  onMerge;

  @override
  ConsumerState<MergeDialog> createState() => _MergeDialogState();
}

class _MergeDialogState extends ConsumerState<MergeDialog> {
  Future<_PagePair>? _future;

  /// Which side is canonical. true = A is canonical (B folds in).
  bool _aIsCanonical = true;
  bool _busy = false;
  String? _err;

  @override
  void initState() {
    super.initState();
    _future = _loadBoth();
  }

  Future<_PagePair> _loadBoth() async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) {
      throw StateError('请先在「设置」中登录');
    }
    final client = repo.client;
    // The merge API doesn't expose a "get page by id" endpoint, only
    // listPages — fetch the project once, pull both pages out of the
    // result. The dedup queue ensures both are in the same project.
    final pages = await client.listPages(widget.projectId);
    WikiPage? a, b;
    for (final p in pages) {
      if (p.id == widget.pageAId) a = p;
      if (p.id == widget.pageBId) b = p;
    }
    if (a == null || b == null) {
      throw StateError('找不到页面 — 可能已被删除');
    }
    final blocksA = await client.listBlocks(widget.projectId, widget.pageAId);
    final blocksB = await client.listBlocks(widget.projectId, widget.pageBId);
    return _PagePair(
      a: _PageWithBlocks(page: a, blocks: blocksA),
      b: _PageWithBlocks(page: b, blocks: blocksB),
    );
  }

  Future<void> _confirm() async {
    setState(() {
      _busy = true;
      _err = null;
    });
    try {
      final canonicalId = _aIsCanonical ? widget.pageAId : widget.pageBId;
      final duplicateId = _aIsCanonical ? widget.pageBId : widget.pageAId;
      await widget.onMerge(canonicalId: canonicalId, duplicateId: duplicateId);
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _err = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('合并重复页面'),
      content: SizedBox(
        width: 720,
        height: 480,
        child: FutureBuilder<_PagePair>(
          future: _future,
          builder: (_, snap) {
            if (snap.connectionState != ConnectionState.done) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snap.hasError) {
              return Center(
                child: Text(
                  snap.error.toString(),
                  style: const TextStyle(color: BiuTokens.error),
                ),
              );
            }
            final pair = snap.data!;
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  '选择保留为「主页面」的一侧。另一侧的所有 block 会被合并进来后软删除；'
                  '主页面的标题不变。',
                  style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                ),
                const SizedBox(height: BiuTokens.space3),
                Expanded(
                  child: Row(
                    children: [
                      Expanded(
                        child: _PageColumn(
                          page: pair.a,
                          isCanonical: _aIsCanonical,
                          onPick: () => setState(() => _aIsCanonical = true),
                        ),
                      ),
                      const SizedBox(width: BiuTokens.space3),
                      Expanded(
                        child: _PageColumn(
                          page: pair.b,
                          isCanonical: !_aIsCanonical,
                          onPick: () => setState(() => _aIsCanonical = false),
                        ),
                      ),
                    ],
                  ),
                ),
                if (_err != null)
                  Padding(
                    padding: const EdgeInsets.only(top: BiuTokens.space2),
                    child: Text(
                      '合并失败：$_err',
                      style: const TextStyle(color: BiuTokens.error),
                    ),
                  ),
              ],
            );
          },
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.of(context).pop(false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _busy ? null : _confirm,
          child: _busy
              ? const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('合并'),
        ),
      ],
    );
  }
}

// ── helpers ───────────────────────────────────────────────────

class _PagePair {
  final _PageWithBlocks a;
  final _PageWithBlocks b;
  const _PagePair({required this.a, required this.b});
}

class _PageWithBlocks {
  final WikiPage page;
  final List<WikiBlock> blocks;
  const _PageWithBlocks({required this.page, required this.blocks});
}

class _PageColumn extends StatelessWidget {
  const _PageColumn({
    required this.page,
    required this.isCanonical,
    required this.onPick,
  });

  final _PageWithBlocks page;
  final bool isCanonical;
  final VoidCallback onPick;

  @override
  Widget build(BuildContext context) {
    final color = isCanonical ? BiuTokens.purple : BiuTokens.borderSubtle;
    return InkWell(
      onTap: onPick,
      child: Container(
        padding: const EdgeInsets.all(BiuTokens.space3),
        decoration: BoxDecoration(
          color: isCanonical ? BiuTokens.purpleSoft : BiuTokens.surface,
          border: Border.all(color: color, width: isCanonical ? 2 : 1),
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  isCanonical
                      ? Icons.radio_button_checked
                      : Icons.radio_button_unchecked,
                  size: 16,
                  color: color,
                ),
                const SizedBox(width: BiuTokens.space2),
                Expanded(
                  child: Text(
                    page.page.title.isEmpty ? '(未命名)' : page.page.title,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                      color: isCanonical ? BiuTokens.purple : BiuTokens.text,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (isCanonical)
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 6,
                      vertical: 1,
                    ),
                    decoration: BoxDecoration(
                      color: BiuTokens.purple,
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: const Text(
                      '主页面',
                      style: TextStyle(
                        fontSize: 9,
                        fontWeight: FontWeight.w700,
                        color: Colors.white,
                      ),
                    ),
                  ),
              ],
            ),
            const SizedBox(height: BiuTokens.space2),
            Text(
              '${page.blocks.length} 个 block · v${page.page.version}',
              style: TextStyle(fontSize: 10, color: BiuTokens.textMuted),
            ),
            const Divider(height: BiuTokens.space4),
            Expanded(
              child: ListView(
                padding: EdgeInsets.zero,
                children: [
                  for (final b in page.blocks.take(20))
                    Padding(
                      padding: const EdgeInsets.only(bottom: BiuTokens.space2),
                      child: _BlockPreview(block: b),
                    ),
                  if (page.blocks.length > 20)
                    Padding(
                      padding: const EdgeInsets.only(top: BiuTokens.space2),
                      child: Text(
                        '… 还有 ${page.blocks.length - 20} 个 block',
                        style: TextStyle(
                          fontSize: 10,
                          color: BiuTokens.textMuted,
                          fontStyle: FontStyle.italic,
                        ),
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _BlockPreview extends StatelessWidget {
  const _BlockPreview({required this.block});
  final WikiBlock block;

  @override
  Widget build(BuildContext context) {
    final text = (block.content['text'] as String?) ?? '';
    if (text.isEmpty) {
      return Text(
        '(${block.type} block)',
        style: TextStyle(
          fontSize: 11,
          color: BiuTokens.textMuted,
          fontStyle: FontStyle.italic,
        ),
      );
    }
    if (block.type == 'heading') {
      final lvl = (block.content['level'] as num?)?.toInt() ?? 2;
      return Text(
        text,
        style: TextStyle(
          fontSize: lvl == 1 ? 14 : (lvl == 2 ? 13 : 12),
          fontWeight: FontWeight.w700,
          color: BiuTokens.text,
        ),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      );
    }
    if (block.type == 'code') {
      final lang = block.content['lang'] as String?;
      // Mermaid blocks render the diagram directly in the merge
      // preview so users compare diagrams visually, not source text.
      if (isMermaidCode(lang: lang, text: text)) {
        return SizedBox(
          height: 120,
          child: MermaidPreview(source: text, maxHeight: 120),
        );
      }
      return Container(
        padding: const EdgeInsets.all(6),
        decoration: BoxDecoration(
          color: BiuTokens.surfaceMuted,
          borderRadius: BorderRadius.circular(4),
        ),
        child: Text(
          text,
          style: TextStyle(
            fontFamily: 'JetBrains Mono, ui-monospace, monospace',
            fontSize: 10,
            color: BiuTokens.text,
          ),
          maxLines: 3,
          overflow: TextOverflow.ellipsis,
        ),
      );
    }
    return WikilinkText(
      text: text,
      style: TextStyle(fontSize: 11, color: BiuTokens.text, height: 1.4),
    );
  }
}
