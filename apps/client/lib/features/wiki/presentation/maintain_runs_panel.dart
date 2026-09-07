// MaintainRunsHistoryDialog — maintain agent run 历史回看
// （BiuMind-Agent-Experience-Design §1.2 P2 run 持久化）。
//
// 两级视图：run 列表（mode/model/指令/状态/起止时间/改动页数）→ 单次 run
// 改动页清单（该 run_id 的写前快照，操作类型为服务端推断）。
//
// 只读为主；update 行允许 undo（restore 写前快照，if_match = 打开时刻的
// 当前 version——读取到恢复之间页面被改过 → 409 兜住，确认后才显式覆盖）。
// merge 行 undo 置灰（同本 run 面板）；create 无快照、天然不在清单内
// （服务端语义，见 store.AgentRunChange 注释）。

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../data/api/wiki_client.dart'
    show WikiAgentRun, WikiAgentRunChange;
import '../application/maintain_changes.dart';
import 'maintain_changes_panel.dart' show SectionedWordDiffView;
import 'reader/block_to_markdown.dart';
import '../../../data/wiki_repository.dart' show RepoBlock;

/// 打开 run 历史对话框。
Future<void> showMaintainRunsHistory(
  BuildContext context, {
  required String projectId,
  required MaintainAuditClient audit,
}) {
  return showDialog<void>(
    context: context,
    builder: (_) => MaintainRunsHistoryDialog(projectId: projectId, audit: audit),
  );
}

class MaintainRunsHistoryDialog extends StatefulWidget {
  const MaintainRunsHistoryDialog({
    super.key,
    required this.projectId,
    required this.audit,
  });

  final String projectId;
  final MaintainAuditClient audit;

  @override
  State<MaintainRunsHistoryDialog> createState() =>
      _MaintainRunsHistoryDialogState();
}

class _MaintainRunsHistoryDialogState
    extends State<MaintainRunsHistoryDialog> {
  List<WikiAgentRun>? _runs;
  String? _error;

  /// 选中的 run 详情（null = 列表视图）。
  WikiAgentRun? _selected;
  List<WikiAgentRunChange>? _changes;
  String? _detailError;

  /// 已撤销的 revision id 集合（行标「已撤销」）。
  final Set<String> _undone = {};
  final Map<String, String> _undoErrors = {};

  @override
  void initState() {
    super.initState();
    _loadRuns();
  }

  Future<void> _loadRuns() async {
    try {
      final runs = await widget.audit.listAgentRuns(widget.projectId);
      if (!mounted) return;
      setState(() => _runs = runs);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    }
  }

  Future<void> _openRun(WikiAgentRun run) async {
    setState(() {
      _selected = run;
      _changes = null;
      _detailError = null;
    });
    try {
      final (_, changes) =
          await widget.audit.getAgentRun(widget.projectId, run.runId);
      if (!mounted) return;
      setState(() => _changes = changes);
    } catch (e) {
      if (!mounted) return;
      setState(() => _detailError = e.toString());
    }
  }

  /// 历史 undo（仅 update 行）：if_match = 此刻当前 version，读取到恢复之间
  /// 被改 → 409 → 展示当前态 vs 目标态 diff 确认后显式覆盖。
  Future<void> _undo(WikiAgentRunChange c) async {
    setState(() => _undoErrors.remove(c.revisionId));
    try {
      final page = await widget.audit.getPage(widget.projectId, c.pageId);
      await widget.audit.restoreRevision(
        widget.projectId,
        c.pageId,
        c.revisionId,
        ifMatchVersion: page.version,
      );
      if (!mounted) return;
      setState(() => _undone.add(c.revisionId));
    } catch (e) {
      if (!mounted) return;
      if (isVersionConflict(e)) {
        await _confirmConflictRetry(c);
        return;
      }
      setState(() => _undoErrors[c.revisionId] = e.toString());
    }
  }

  Future<void> _confirmConflictRetry(WikiAgentRunChange c) async {
    final String current;
    final String target;
    try {
      final page = await widget.audit.getPage(widget.projectId, c.pageId);
      current = page.bodyMd;
      final rev = await widget.audit
          .getRevision(widget.projectId, c.pageId, c.revisionId);
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
      target = blocksToMarkdown(blocks);
    } catch (e) {
      if (!mounted) return;
      setState(() => _undoErrors[c.revisionId] = '读取当前内容失败：$e');
      return;
    }
    if (!mounted) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('页面有新修改'),
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
                    '页面「${c.title}」在你查看期间被再次修改。继续撤销将覆盖新修改'
                    '（覆盖前服务端会自动备份当前态）。',
                    style: TextStyle(
                      fontSize: 12,
                      color: BiuTokens.textMuted,
                      height: 1.5,
                    ),
                  ),
                  const SizedBox(height: 8),
                  SectionedWordDiffView(before: current, after: target),
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
    try {
      await widget.audit
          .restoreRevision(widget.projectId, c.pageId, c.revisionId);
      if (!mounted) return;
      setState(() => _undone.add(c.revisionId));
    } catch (e) {
      if (!mounted) return;
      setState(() => _undoErrors[c.revisionId] = e.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    final selected = _selected;
    return AlertDialog(
      title: Text(selected == null ? '历史运行' : '运行详情 · ${_statusLabel(selected.status)}'),
      content: SizedBox(
        width: 560,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 460),
          child: selected == null ? _buildList() : _buildDetail(selected),
        ),
      ),
      actions: [
        if (selected != null)
          TextButton(
            onPressed: () => setState(() => _selected = null),
            child: const Text('返回列表'),
          ),
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('关闭'),
        ),
      ],
    );
  }

  Widget _buildList() {
    if (_error != null) {
      return Text('加载失败：$_error',
          style: const TextStyle(fontSize: 12, color: BiuTokens.error));
    }
    final runs = _runs;
    if (runs == null) {
      return const Center(
        child: SizedBox(
          width: 18,
          height: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    if (runs.isEmpty) {
      return Text(
        '还没有历史运行（仅服务端升级后的 run 会落库）',
        style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
      );
    }
    return SingleChildScrollView(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final r in runs) _RunRow(run: r, onTap: () => _openRun(r)),
        ],
      ),
    );
  }

  Widget _buildDetail(WikiAgentRun run) {
    if (_detailError != null) {
      return Text('加载失败：$_detailError',
          style: const TextStyle(fontSize: 12, color: BiuTokens.error));
    }
    final changes = _changes;
    if (changes == null) {
      return const Center(
        child: SizedBox(
          width: 18,
          height: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '${run.instruction.isEmpty ? "（无指令）" : run.instruction}\n'
            '${run.mode} · ${run.model} · ${_fmtTime(run.startedAt)}',
            style: TextStyle(
              fontSize: 11,
              color: BiuTokens.textMuted,
              height: 1.5,
            ),
          ),
          if (run.error.isNotEmpty)
            Text('错误：${run.error}',
                style:
                    const TextStyle(fontSize: 11, color: BiuTokens.error)),
          const SizedBox(height: 8),
          if (changes.isEmpty)
            Text(
              '无改动记录（新建页无写前快照不进清单；或被快照跳过/窗口合并）',
              style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
            )
          else
            for (final c in changes) _buildChangeRow(c),
        ],
      ),
    );
  }

  Widget _buildChangeRow(WikiAgentRunChange c) {
    final undone = _undone.contains(c.revisionId);
    final err = _undoErrors[c.revisionId];
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              _OpLabel(op: c.op),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  c.title.isEmpty ? c.pageId : c.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontSize: 12, color: BiuTokens.text),
                ),
              ),
              const SizedBox(width: 6),
              if (undone)
                Text('已撤销',
                    style:
                        TextStyle(fontSize: 11, color: BiuTokens.textMuted))
              else if (c.op == 'merge')
                const Tooltip(
                  message: '合并撤销将在后续版本支持',
                  child: TextButton(
                    onPressed: null,
                    child: Text('撤销', style: TextStyle(fontSize: 11)),
                  ),
                )
              else
                TextButton(
                  onPressed: () => _undo(c),
                  child: const Text('撤销', style: TextStyle(fontSize: 11)),
                ),
            ],
          ),
          if (err != null)
            Text('撤销失败：$err',
                style:
                    const TextStyle(fontSize: 11, color: BiuTokens.error)),
        ],
      ),
    );
  }

  static String _statusLabel(String status) => switch (status) {
        'done' => '完成',
        'failed' => '失败',
        'cancelled' => '已取消',
        _ => '进行中',
      };

  static String _fmtTime(DateTime t) {
    final l = t.toLocal();
    String two(int v) => v.toString().padLeft(2, '0');
    return '${l.month}/${l.day} ${two(l.hour)}:${two(l.minute)}';
  }
}

class _RunRow extends StatelessWidget {
  const _RunRow({required this.run, required this.onTap});
  final WikiAgentRun run;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final statusColor = switch (run.status) {
      'done' => BiuTokens.green,
      'failed' => BiuTokens.error,
      'cancelled' => SemanticTokens.warning,
      _ => BiuTokens.purple,
    };
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: Row(
          children: [
            Container(
              width: 8,
              height: 8,
              decoration: BoxDecoration(
                color: statusColor,
                shape: BoxShape.circle,
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    run.instruction.isEmpty ? '（无指令）' : run.instruction,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(fontSize: 12, color: BiuTokens.text),
                  ),
                  Text(
                    '${run.mode} · ${run.model} · '
                    '${run.startedAt.toLocal().month}/${run.startedAt.toLocal().day} '
                    '${run.startedAt.toLocal().hour.toString().padLeft(2, '0')}:'
                    '${run.startedAt.toLocal().minute.toString().padLeft(2, '0')}'
                    ' · 改动 ${run.changedPages} 页',
                    style: TextStyle(fontSize: 10, color: BiuTokens.textMuted),
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

class _OpLabel extends StatelessWidget {
  const _OpLabel({required this.op});
  final String op;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (op) {
      'merge' => ('合并', SemanticTokens.warning),
      _ => ('修改', BiuTokens.purple),
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
