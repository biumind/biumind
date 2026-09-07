// MaintainChangesPanel — maintain agent run 结束后的「改动清单」区
// （BiuMind-Agent-Experience-Design §1.2 P1 面板 / P2 run 关联 + OCC）。
//
// 每行：页标题 + 操作徽章（新建/修改/合并）+ 撤销按钮；点开进词级 diff
// （SectionedWordDiffView，before = 写前快照 body，after = 改后全文）。
// undo：update → restore 写前快照（带 if_match_version OCC，run 之后页面
// 被改过 → 409 → 展示当前态 vs 目标态 diff 确认后才无 if_match 重试）；
// create → 删页（二次确认）；merge → 置灰（服务端还不支持 un-delete
// duplicate）。
//
// 写前快照匹配：P2 起服务端快照带 run_id，有 runId 时精确匹配；旧 run
// 回退时间窗启发式。找不到快照 → diffUnavailable（不猜）。
//
// 已知局限（页脚诚实标注）：dialog 关闭本 run 清单即丢（历史回看走
// 「历史运行」入口）；create 删页 undo 无 OCC。

import 'package:diff_match_patch/diff_match_patch.dart';
import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../data/api/wiki_client.dart' show WikiPageRevision;
import '../../../data/wiki_repository.dart' show RepoBlock;
import '../application/maintain_changes.dart';
import 'reader/block_to_markdown.dart';
import 'selection_edit/word_diff_view.dart';

class MaintainChangesPanel extends StatefulWidget {
  const MaintainChangesPanel({
    super.key,
    required this.projectId,
    required this.changes,
    required this.audit,
    this.runId,
  });

  final String projectId;
  final List<MaintainChange> changes;
  final MaintainAuditClient audit;

  /// 本 run id（P2）：快照精确匹配用；null = 旧 run，回退时间窗匹配。
  final String? runId;

  @override
  State<MaintainChangesPanel> createState() => _MaintainChangesPanelState();
}

class _MaintainChangesPanelState extends State<MaintainChangesPanel> {
  /// 写前快照 body_md 缓存（revision id → markdown）。detail 按需拉取。
  final Map<String, String> _beforeCache = {};

  @override
  void initState() {
    super.initState();
    _resolveSnapshots();
  }

  /// 拉每页的版本列表，匹配写前快照 id。找不到 → diffUnavailable（不猜）。
  Future<void> _resolveSnapshots() async {
    for (final c in widget.changes) {
      if (c.op == MaintainChangeOp.create) continue; // create 无写前态
      try {
        final revs =
            await widget.audit.listRevisions(widget.projectId, c.pageId);
        final hit = pickBeforeRevision(
          revs,
          firstWriteAt: c.firstWriteAt,
          lastWriteDoneAt: c.lastWriteDoneAt,
          runId: widget.runId,
        );
        if (!mounted) return;
        setState(() {
          if (hit == null) {
            c.diffUnavailable = true;
          } else {
            c.beforeRevisionId = hit.id;
          }
        });
      } catch (_) {
        if (!mounted) return;
        setState(() => c.diffUnavailable = true);
      }
    }
  }

  /// 取写前快照正文：revision detail 只给 blocks_json（body_md 不下发），
  /// 用与 Go store.BlocksToMarkdown 对齐的 blocksToMarkdown 还原。
  Future<String?> _loadBefore(MaintainChange c) async {
    if (c.op == MaintainChangeOp.create) return '';
    final rid = c.beforeRevisionId;
    if (rid == null) return null;
    final cached = _beforeCache[rid];
    if (cached != null) return cached;
    final WikiPageRevision rev =
        await widget.audit.getRevision(widget.projectId, c.pageId, rid);
    final blocks = (rev.blocksJson ?? const [])
        .map(
          (b) => RepoBlock(
            id: b['id']?.toString() ?? '',
            pageId: b['page_id']?.toString() ?? '',
            position: (b['position'] as num?)?.toDouble() ?? 0,
            type: b['type']?.toString() ?? 'text',
            content:
                (b['content'] as Map?)?.cast<String, dynamic>() ?? const {},
            version: (b['version'] as num?)?.toInt() ?? 1,
          ),
        )
        .toList()
      ..sort((a, b) => a.position.compareTo(b.position));
    final md = blocksToMarkdown(blocks);
    _beforeCache[rid] = md;
    return md;
  }

  Future<void> _openDiff(MaintainChange c) async {
    // 先 await 取写前快照再弹窗——失败/缺快照直接走降级态文案，省去
    // dialog 内异步加载态。
    String? before;
    Object? error;
    try {
      before = await _loadBefore(c);
    } catch (e) {
      error = e;
    }
    if (!mounted) return;
    await showDialog<void>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('变更对比 · ${c.title.isEmpty ? c.pageId : c.title}'),
        content: SizedBox(
          width: 560,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 420),
            child: SingleChildScrollView(
              child: switch ((before, error)) {
                (final String b, _) => SectionedWordDiffView(
                    before: b,
                    after: c.afterBodyMd,
                  ),
                (_, final Object e) => Text(
                    'diff 加载失败：$e',
                    style:
                        const TextStyle(fontSize: 12, color: BiuTokens.error),
                  ),
                _ => Text(
                    'diff unavailable：缺少写前快照（页面过大或服务端已跳过快照）',
                    style: TextStyle(
                      fontSize: 12,
                      color: BiuTokens.textMuted,
                      height: 1.5,
                    ),
                  ),
              },
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
  }

  Future<void> _undo(MaintainChange c) async {
    switch (c.op) {
      case MaintainChangeOp.merge:
        return; // 按钮已置灰，双保险
      case MaintainChangeOp.create:
        final ok = await showDialog<bool>(
          context: context,
          builder: (ctx) => AlertDialog(
            title: const Text('删除此新建页'),
            content: Text('撤销新建将删除页面「${c.title}」，确定继续？'),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(ctx).pop(false),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: () => Navigator.of(ctx).pop(true),
                child: const Text('删除'),
              ),
            ],
          ),
        );
        if (ok != true) return;
        await _runUndo(c, () => widget.audit.deletePage(widget.projectId, c.pageId));
      case MaintainChangeOp.update:
        final rid = c.beforeRevisionId;
        if (rid == null) return; // 无快照，按钮已禁用
        await _runUndo(
          c,
          () => widget.audit.restoreRevision(
            widget.projectId,
            c.pageId,
            rid,
            // P2 OCC：本 run 最后写完的 version。run 之后页面被改过 → 409。
            ifMatchVersion: c.afterVersion,
          ),
          onConflict: () => _confirmConflictRetry(c, rid),
        );
    }
  }

  /// 409（run 之后有新修改）→ 展示「当前态 vs 恢复目标态」diff，用户确认后
  /// 无 if_match 重试（显式覆盖）。取消则保持现状，行内不标错误。
  Future<void> _confirmConflictRetry(MaintainChange c, String rid) async {
    String? current;
    String? target;
    try {
      final page = await widget.audit.getPage(widget.projectId, c.pageId);
      current = page.bodyMd;
      target = await _loadBefore(c);
    } catch (e) {
      if (!mounted) return;
      setState(() => c.undoError = '读取当前内容失败：$e');
      return;
    }
    if (!mounted) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('run 之后有新修改'),
        content: SizedBox(
          width: 560,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 420),
            child: SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    '页面「${c.title}」在本次维护后被再次修改。继续撤销将覆盖这些新修改'
                    '（覆盖前服务端会自动备份当前态，仍可从版本历史找回）。',
                    style: TextStyle(
                      fontSize: 12,
                      color: BiuTokens.textMuted,
                      height: 1.5,
                    ),
                  ),
                  const SizedBox(height: 8),
                  if (target != null)
                    SectionedWordDiffView(
                      before: current ?? '',
                      after: target,
                    ),
                ],
              ),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('仍要撤销'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await _runUndo(
      c,
      () => widget.audit.restoreRevision(widget.projectId, c.pageId, rid),
    );
  }

  Future<void> _runUndo(
    MaintainChange c,
    Future<void> Function() op, {
    Future<void> Function()? onConflict,
  }) async {
    setState(() => c.undoError = null);
    try {
      await op();
      if (!mounted) return;
      setState(() => c.undone = true);
    } catch (e) {
      if (!mounted) return;
      // P2 OCC：409 = run 之后有新修改 → 走 diff 确认流程（不是失败）。
      if (onConflict != null && isVersionConflict(e)) {
        await onConflict();
        return;
      }
      // 其余失败（网络/删页冲突等）——行内展示，不静默。
      setState(() => c.undoError = e.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space2),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '改动清单 (${widget.changes.length})',
            style: TextStyle(
              fontSize: 10,
              fontWeight: FontWeight.w600,
              color: BiuTokens.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          for (final c in widget.changes)
            _ChangeRow(
              change: c,
              onTap: () => _openDiff(c),
              onUndo: () => _undo(c),
            ),
          const SizedBox(height: 4),
          Text(
            '清单仅本次对话内可见（历史回看走「历史运行」）；撤销带版本校验，'
            'run 之后有新修改时会先提示确认。',
            style: TextStyle(
              fontSize: 10,
              color: BiuTokens.textMuted,
              height: 1.4,
            ),
          ),
        ],
      ),
    );
  }
}

class _ChangeRow extends StatelessWidget {
  const _ChangeRow({
    required this.change,
    required this.onTap,
    required this.onUndo,
  });

  final MaintainChange change;
  final VoidCallback onTap;
  final VoidCallback onUndo;

  @override
  Widget build(BuildContext context) {
    final c = change;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              _OpBadge(op: c.op),
              const SizedBox(width: 6),
              Expanded(
                child: InkWell(
                  onTap: onTap,
                  child: Text(
                    c.title.isEmpty ? c.pageId : c.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 12,
                      color: BiuTokens.text,
                      decoration: TextDecoration.underline,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 6),
              _undoWidget(),
            ],
          ),
          if (c.undoError != null)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                '撤销失败：${c.undoError}',
                style: const TextStyle(fontSize: 11, color: BiuTokens.error),
              ),
            ),
        ],
      ),
    );
  }

  Widget _undoWidget() {
    final c = change;
    if (c.undone) {
      return Text(
        '已撤销',
        style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
      );
    }
    switch (c.op) {
      case MaintainChangeOp.merge:
        return const Tooltip(
          message: '合并撤销将在后续版本支持',
          child: TextButton(
            onPressed: null,
            child: Text('撤销', style: TextStyle(fontSize: 11)),
          ),
        );
      case MaintainChangeOp.create:
        return TextButton(
          onPressed: onUndo,
          child: const Text('撤销', style: TextStyle(fontSize: 11)),
        );
      case MaintainChangeOp.update:
        if (c.diffUnavailable || c.beforeRevisionId == null) {
          return const Tooltip(
            message: '找不到写前快照，无法撤销',
            child: TextButton(
              onPressed: null,
              child: Text('撤销', style: TextStyle(fontSize: 11)),
            ),
          );
        }
        return TextButton(
          onPressed: onUndo,
          child: const Text('撤销', style: TextStyle(fontSize: 11)),
        );
    }
  }
}

class _OpBadge extends StatelessWidget {
  const _OpBadge({required this.op});
  final MaintainChangeOp op;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (op) {
      MaintainChangeOp.create => ('新建', BiuTokens.green),
      MaintainChangeOp.update => ('修改', BiuTokens.purple),
      MaintainChangeOp.merge => ('合并', SemanticTokens.warning),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(3),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 10,
          fontWeight: FontWeight.w600,
          color: color,
        ),
      ),
    );
  }
}

/// 整页 before/after 的词级 diff，按段落切片防长文性能问题：
/// 段落（空行分段）先经 lines-to-chars 映射跑 diff_match_patch 对齐出
/// 「相等段 / 变更 hunk」，hunk 内再走 WordDiffView 词级着色；相等段折叠为
/// 「N 段未变更」。超过私用区容量（6400 个不同段落，远超实际页）时退化为
/// 整文 WordDiffView。
class SectionedWordDiffView extends StatelessWidget {
  const SectionedWordDiffView({
    super.key,
    required this.before,
    required this.after,
  });

  final String before;
  final String after;

  static const _maxDistinctParagraphs = 0xF800 - 0xE000;

  static List<String> _splitParagraphs(String text) => text
      .split(RegExp(r'\n\s*\n'))
      .map((p) => p.trim())
      .where((p) => p.isNotEmpty)
      .toList();

  @override
  Widget build(BuildContext context) {
    final beforeParas = _splitParagraphs(before);
    final afterParas = _splitParagraphs(after);

    final codes = <String, int>{};
    int codeOf(String p) =>
        codes.putIfAbsent(p, () => 0xE000 + codes.length);
    final encBefore = beforeParas.map((p) => codeOf(p)).toList();
    final encAfter = afterParas.map((p) => codeOf(p)).toList();
    if (codes.length > _maxDistinctParagraphs) {
      return WordDiffView(before: before, after: after);
    }

    String encode(List<int> cs) =>
        cs.map((c) => String.fromCharCode(c)).join();
    final encB = encode(encBefore);
    final encA = encode(encAfter);
    if (encB == encA) {
      return Text(
        '内容无变化',
        style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
      );
    }

    // 段落级对齐：diff_match_patch 在编码串上跑（1 char = 1 段落），
    // 线性扫描产出有序 segment 序列：int = 连续相等段数（折叠行）；
    // (beforeSeg, afterSeg) record = 变更 hunk（WordDiffView 词级着色）。
    final segments = <Object>[];
    final pendingDel = <String>[];
    final pendingIns = <String>[];
    var bi = 0;
    var ai = 0;
    var equalCount = 0;
    void flushHunk() {
      if (pendingDel.isNotEmpty || pendingIns.isNotEmpty) {
        segments.add((
          List<String>.from(pendingDel),
          List<String>.from(pendingIns),
        ));
        pendingDel.clear();
        pendingIns.clear();
      }
    }

    void flushEqual() {
      if (equalCount > 0) {
        segments.add(equalCount);
        equalCount = 0;
      }
    }

    for (final d in diff(encB, encA)) {
      final n = d.text.length;
      switch (d.operation) {
        case DIFF_EQUAL:
          flushHunk();
          equalCount += n;
          bi += n;
          ai += n;
        case DIFF_DELETE:
          flushEqual();
          for (var k = 0; k < n; k++) {
            pendingDel.add(beforeParas[bi++]);
          }
        case DIFF_INSERT:
          flushEqual();
          for (var k = 0; k < n; k++) {
            pendingIns.add(afterParas[ai++]);
          }
      }
    }
    flushHunk();
    flushEqual();

    final children = <Widget>[
      for (final s in segments)
        switch (s) {
          final int eq => Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Text(
                '…… $eq 段未变更 ……',
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
              ),
            ),
          final (List<String>, List<String>) h => Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: WordDiffView(
                before: h.$1.join('\n\n'),
                after: h.$2.join('\n\n'),
              ),
            ),
          _ => const SizedBox.shrink(),
        },
    ];
    if (children.isEmpty) {
      return Text(
        '内容无变化',
        style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: children,
    );
  }
}
