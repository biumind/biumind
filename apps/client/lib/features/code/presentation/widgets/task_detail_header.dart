// 任务详情头 — 主区顶部。
//
// 展示:标题 + 标记完成/停止 + 状态 chip;Agent · 权限模式 副标题;
// 工作目录路径;时长 / TOKENS / 费用 指标 chip。
//
// 数据全部来自已有的 CodeTask 字段(title/agent/mode/status/workspace/
// cost/duration),不新增模型字段。重命名 / AI 命名 / 真实会话文件路径留 CORE-2/6。

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../application/projects_controller.dart';
import '../../application/session_export.dart';
import '../../application/tasks_controller.dart';
import '../../data/code_bridge_provider.dart';
import '../../domain/code_task.dart';
import '../../domain/workspace.dart';

/// worktree 任务相对 base 的增删行数(G3)。按 (worktreePath, baseBranch) 缓存,
/// autoDispose 让切任务后释放。daemon 未就绪 / 非 worktree → 返回零。
final taskDiffStatsProvider = FutureProvider.autoDispose
    .family<({int additions, int deletions}), ({String path, String base})>(
        (ref, key) async {
  final bridge = ref.watch(codeBridgeClientProvider);
  if (bridge == null) return (additions: 0, deletions: 0);
  return bridge.gitWorktreeDiffStats(key.path, key.base);
});

bool _isCancellable(CodeTaskStatus s) =>
    s == CodeTaskStatus.queued ||
    s == CodeTaskStatus.running ||
    s == CodeTaskStatus.inputRequired ||
    s == CodeTaskStatus.detached; // 进程仍活,可直接停(killPty 按 id,连接无关)

class TaskDetailHeader extends ConsumerWidget {
  const TaskDetailHeader({super.key, required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cwd = task.workspace?.localPath;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(20, 12, 16, 12),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Flexible(
                child: Text(
                  task.title,
                  style: const TextStyle(
                    fontSize: 15.5,
                    fontWeight: FontWeight.w600,
                    height: 1.3,
                  ),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              const SizedBox(width: 4),
              _HeaderIconBtn(
                icon: Icons.edit_outlined,
                tip: '重命名',
                onTap: () => _rename(context, ref),
              ),
              _HeaderIconBtn(
                icon: Icons.auto_awesome_outlined,
                tip: 'AI 命名',
                onTap: () => _aiName(context, ref),
              ),
              if (task.events.isNotEmpty)
                _HeaderIconBtn(
                  icon: Icons.ios_share_outlined,
                  tip: '导出为 Markdown',
                  onTap: () => _export(context),
                ),
              const Spacer(),
              if (task.status != CodeTaskStatus.done &&
                  task.status != CodeTaskStatus.failed)
                _MarkDoneButton(task: task),
              if (task.canResume) ...[
                const SizedBox(width: 6),
                _ResumeButton(task: task),
              ],
              if (_isCancellable(task.status)) ...[
                const SizedBox(width: 6),
                _StopButton(task: task),
              ],
              if (_canMerge) ...[
                const SizedBox(width: 6),
                _MergeButton(task: task),
              ],
              const SizedBox(width: 8),
              _StatusChip(status: task.status),
            ],
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Icon(Icons.auto_awesome_rounded, size: 13, color: BiuTokens.purple),
              const SizedBox(width: 5),
              Text(
                task.agent.label,
                style: TextStyle(
                  fontSize: 11.5,
                  fontFamily: 'SF Mono',
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.purple,
                ),
              ),
              if (task.model != null && task.model!.isNotEmpty) ...[
                _dot(),
                Flexible(
                  child: Text(
                    task.model!,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 11.5,
                      fontFamily: 'SF Mono',
                      color: BiuTokens.textSecondary,
                    ),
                  ),
                ),
              ],
              _dot(),
              Text(
                task.mode.label,
                style: TextStyle(fontSize: 11.5, color: BiuTokens.textMuted),
              ),
            ],
          ),
          if (cwd != null && cwd.isNotEmpty) ...[
            const SizedBox(height: 4),
            Row(
              children: [
                Icon(Icons.folder_outlined, size: 12, color: BiuTokens.textMuted),
                const SizedBox(width: 5),
                Expanded(
                  child: Text(
                    cwd,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 11,
                      fontFamily: 'SF Mono',
                      color: BiuTokens.textMuted,
                    ),
                  ),
                ),
              ],
            ),
          ],
          // 会话标识(daemon 只回传 sessionId,
          // 不回传路径,故诚实展示 session id,不臆造路径)。
          if (task.resumeSessionId != null) ...[
            const SizedBox(height: 4),
            Row(
              children: [
                Icon(Icons.tag_rounded, size: 12, color: BiuTokens.textMuted),
                const SizedBox(width: 5),
                Flexible(
                  child: Text(
                    '会话 ${task.resumeSessionId}',
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 11,
                      fontFamily: 'SF Mono',
                      color: BiuTokens.textMuted,
                    ),
                  ),
                ),
              ],
            ),
          ],
          if (_hasMetrics) ...[
            const SizedBox(height: 10),
            Wrap(
              spacing: 8,
              runSpacing: 6,
              children: [
                if (task.duration != null)
                  _MetricChip(label: '时长', value: _fmtDuration(task.duration!)),
                if (_totalTokens > 0)
                  _MetricChip(label: 'TOKENS', value: _fmtTokens(_totalTokens)),
                if (task.cost.contextTokens > 0)
                  _MetricChip(label: '上下文', value: _ctxValue),
                if (task.cost.usd > 0)
                  _MetricChip(
                    label: '费用',
                    value: '\$${task.cost.usd.toStringAsFixed(4)}',
                  ),
                if (_isWorktree) _DiffStatsChip(task: task),
              ],
            ),
          ],
        ],
      ),
    );
  }

  /// 是 git worktree 任务(可合并 / 可看 diff 统计)。
  bool get _isWorktree =>
      task.workspace?.kind == WorkspaceKind.localGitWorktree &&
      (task.workspace?.branchName?.isNotEmpty ?? false);

  /// 完成的 worktree 任务才给「合并到主干」。
  bool get _canMerge => _isWorktree && task.status == CodeTaskStatus.done;

  bool get _hasMetrics =>
      task.duration != null ||
      _totalTokens > 0 ||
      task.cost.usd > 0 ||
      task.cost.contextTokens > 0 ||
      _isWorktree;

  int get _totalTokens => task.cost.inputTokens + task.cost.outputTokens;

  /// 上下文利用率展示:窗口已知 → "90K/200K · 45%";未知(Claude)→ "90K"。
  String get _ctxValue {
    final ctx = task.cost.contextTokens;
    final win = task.cost.contextWindow;
    if (win > 0) {
      final pct = ((ctx / win) * 100).clamp(0, 100).toStringAsFixed(0);
      return '${_fmtTokens(ctx)}/${_fmtTokens(win)} · $pct%';
    }
    return _fmtTokens(ctx);
  }

  Widget _dot() => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 7),
        child: Text('·',
            style: TextStyle(fontSize: 11.5, color: BiuTokens.textMuted)),
      );

  Future<void> _rename(BuildContext context, WidgetRef ref) async {
    final ctrl = TextEditingController(text: task.title);
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重命名任务', style: TextStyle(fontSize: 15)),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: const InputDecoration(hintText: '任务标题'),
          onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('取消')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    if (name != null && name.isNotEmpty) {
      ref.read(codeTasksProvider.notifier).renameTask(task.id, name);
    }
  }

  Future<void> _aiName(BuildContext context, WidgetRef ref) async {
    final bridge = ref.read(codeBridgeClientProvider);
    final messenger = ScaffoldMessenger.of(context);
    if (bridge == null) {
      messenger.showSnackBar(
        const SnackBar(content: Text('本地 daemon 未就绪,无法 AI 命名')),
      );
      return;
    }
    messenger.showSnackBar(
      const SnackBar(content: Text('AI 命名中…'), duration: Duration(seconds: 1)),
    );
    try {
      final name = await bridge.generateAgentName(task.prompt);
      if (name.isNotEmpty) {
        ref.read(codeTasksProvider.notifier).renameTask(task.id, name);
      }
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('AI 命名失败: $e')));
    }
  }

  /// 导出会话为 Markdown。Native 落盘 app Documents + 提供复制;Web 直接复制到剪贴板。
  Future<void> _export(BuildContext context) async {
    final messenger = ScaffoldMessenger.of(context);
    try {
      final md = buildSessionMarkdown(task);
      final path = await exportSessionToFile(task);
      if (path == null) {
        // Web:无文件系统,复制到剪贴板。
        await Clipboard.setData(ClipboardData(text: md));
        messenger.showSnackBar(
          const SnackBar(content: Text('已复制 Markdown 到剪贴板')),
        );
        return;
      }
      messenger.showSnackBar(
        SnackBar(
          content: Text('已导出到 $path'),
          action: SnackBarAction(
            label: '复制内容',
            onPressed: () => Clipboard.setData(ClipboardData(text: md)),
          ),
        ),
      );
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('导出失败: $e')));
    }
  }
}

class _HeaderIconBtn extends StatelessWidget {
  const _HeaderIconBtn(
      {required this.icon, required this.tip, required this.onTap});
  final IconData icon;
  final String tip;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: tip,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
        child: Padding(
          padding: const EdgeInsets.all(4),
          child: Icon(icon, size: 15, color: BiuTokens.textMuted),
        ),
      ),
    );
  }
}

String _fmtDuration(Duration d) {
  if (d.inHours > 0) {
    final m = d.inMinutes.remainder(60).toString().padLeft(2, '0');
    return '${d.inHours}h${m}m';
  }
  if (d.inMinutes > 0) {
    final s = d.inSeconds.remainder(60).toString().padLeft(2, '0');
    return '${d.inMinutes}m${s}s';
  }
  return '${d.inSeconds}s';
}

String _fmtTokens(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  if (n >= 1000) return '${(n / 1000).toStringAsFixed(n >= 100000 ? 0 : 1)}K';
  return '$n';
}

// ─── 指标 chip ─────────────────────────────────────────

class _MetricChip extends StatelessWidget {
  const _MetricChip({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            label,
            style: TextStyle(
              fontSize: 9.5,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.5,
              color: BiuTokens.textMuted,
            ),
          ),
          const SizedBox(width: 5),
          Text(
            value,
            style: TextStyle(
              fontSize: 11,
              fontFamily: 'SF Mono',
              fontWeight: FontWeight.w600,
              color: BiuTokens.textSecondary,
            ),
          ),
        ],
      ),
    );
  }
}

// ─── 状态 chip ─────────────────────────────────────────

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.status});
  final CodeTaskStatus status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      CodeTaskStatus.queued => ('排队中', BiuTokens.textMuted),
      CodeTaskStatus.running => ('运行中', BiuTokens.purple),
      CodeTaskStatus.paused => ('已暂停', BiuTokens.textMuted),
      CodeTaskStatus.inputRequired => ('需要确认', Colors.orange),
      CodeTaskStatus.done => ('已完成', BiuTokens.green),
      CodeTaskStatus.failed => ('失败', Colors.red),
      CodeTaskStatus.interrupted => ('已取消', BiuTokens.textMuted),
      CodeTaskStatus.detached => ('终端连接断开', Colors.orange),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 10.5,
          fontWeight: FontWeight.w600,
          color: color,
        ),
      ),
    );
  }
}

// ─── 标记完成 ──────────────────────────────────────────

class _MarkDoneButton extends ConsumerWidget {
  const _MarkDoneButton({required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return OutlinedButton.icon(
      onPressed: () =>
          ref.read(codeTasksProvider.notifier).markComplete(task.id),
      icon: Icon(Icons.check_circle_outline_rounded,
          size: 14, color: BiuTokens.green),
      label: Text(
        '标记完成',
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          color: BiuTokens.green,
        ),
      ),
      style: OutlinedButton.styleFrom(
        visualDensity: VisualDensity.compact,
        padding: const EdgeInsets.symmetric(horizontal: 10),
        minimumSize: const Size(0, 28),
        side: BorderSide(color: BiuTokens.green.withValues(alpha: 0.4)),
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      ),
    );
  }
}

// ─── 续跑 (G5) ─────────────────────────────────────────

class _ResumeButton extends ConsumerWidget {
  const _ResumeButton({required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return OutlinedButton.icon(
      onPressed: () => ref.read(codeTasksProvider.notifier).resume(task.id),
      icon: Icon(Icons.play_arrow_rounded, size: 15, color: BiuTokens.purple),
      label: Text(
        '续跑',
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          color: BiuTokens.purple,
        ),
      ),
      style: OutlinedButton.styleFrom(
        visualDensity: VisualDensity.compact,
        padding: const EdgeInsets.symmetric(horizontal: 10),
        minimumSize: const Size(0, 28),
        side: BorderSide(color: BiuTokens.purple.withValues(alpha: 0.4)),
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      ),
    );
  }
}

// ─── 合并 worktree 到主干 (G1) ─────────────────────────

class _MergeButton extends ConsumerStatefulWidget {
  const _MergeButton({required this.task});
  final CodeTask task;

  @override
  ConsumerState<_MergeButton> createState() => _MergeButtonState();
}

class _MergeButtonState extends ConsumerState<_MergeButton> {
  bool _busy = false;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      onPressed: _busy ? null : _merge,
      icon: _busy
          ? const SizedBox(
              width: 11,
              height: 11,
              child: CircularProgressIndicator(strokeWidth: 1.4),
            )
          : Icon(Icons.merge_rounded, size: 14, color: BiuTokens.purple),
      label: Text(
        '合并到主干',
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          color: BiuTokens.purple,
        ),
      ),
      style: OutlinedButton.styleFrom(
        visualDensity: VisualDensity.compact,
        padding: const EdgeInsets.symmetric(horizontal: 10),
        minimumSize: const Size(0, 28),
        side: BorderSide(color: BiuTokens.purple.withValues(alpha: 0.4)),
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      ),
    );
  }

  Future<void> _merge() async {
    final ws = widget.task.workspace;
    final project = ref.read(activeCodeProjectProvider);
    final bridge = ref.read(codeBridgeClientProvider);
    final messenger = ScaffoldMessenger.of(context);
    if (ws == null || bridge == null || project == null) {
      messenger.showSnackBar(
        const SnackBar(content: Text('无法合并:缺少工作区 / 项目 / daemon')),
      );
      return;
    }
    final branch = ws.branchName ?? '';
    final base = (ws.baseBranch?.isNotEmpty ?? false) ? ws.baseBranch! : 'main';
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('合并到主干', style: TextStyle(fontSize: 15)),
        content: Text('将分支 `$branch` 合并回 `$base`?\n冲突时会失败并保留 worktree。'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消')),
          FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('合并')),
        ],
      ),
    );
    if (ok != true) return;
    setState(() => _busy = true);
    try {
      final out =
          await bridge.gitMergeWorktree(project.path, ws.localPath, branch, base);
      if (mounted) {
        messenger.showSnackBar(SnackBar(
          content: Text('已合并 `$branch` → `$base`'
              '${out.trim().isNotEmpty ? '\n${out.trim()}' : ''}'),
        ));
      }
    } catch (e) {
      if (mounted) {
        messenger.showSnackBar(SnackBar(content: Text('合并失败: $e')));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }
}

// ─── worktree diff 统计 chip (G3) ──────────────────────

class _DiffStatsChip extends ConsumerWidget {
  const _DiffStatsChip({required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ws = task.workspace;
    if (ws == null) return const SizedBox.shrink();
    final base = (ws.baseBranch?.isNotEmpty ?? false) ? ws.baseBranch! : 'main';
    final stats = ref.watch(
        taskDiffStatsProvider((path: ws.localPath, base: base)));
    final (add, del) = switch (stats) {
      AsyncData(:final value) => (value.additions, value.deletions),
      _ => (0, 0),
    };
    if (add == 0 && del == 0) return const SizedBox.shrink();
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('+$add',
              style: TextStyle(
                  fontSize: 11,
                  fontFamily: 'SF Mono',
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.green)),
          const SizedBox(width: 6),
          Text('−$del',
              style: TextStyle(
                  fontSize: 11,
                  fontFamily: 'SF Mono',
                  fontWeight: FontWeight.w600,
                  color: Colors.red.shade400)),
        ],
      ),
    );
  }
}

// ─── 停止 ──────────────────────────────────────────────

class _StopButton extends ConsumerStatefulWidget {
  const _StopButton({required this.task});
  final CodeTask task;

  @override
  ConsumerState<_StopButton> createState() => _StopButtonState();
}

class _StopButtonState extends ConsumerState<_StopButton> {
  bool _busy = false;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      onPressed: _busy
          ? null
          : () async {
              setState(() => _busy = true);
              try {
                await ref
                    .read(codeTasksProvider.notifier)
                    .submitCancel(widget.task);
              } catch (e) {
                if (context.mounted) {
                  ScaffoldMessenger.of(context).showSnackBar(
                    SnackBar(content: Text('停止失败: $e')),
                  );
                }
              } finally {
                if (mounted) setState(() => _busy = false);
              }
            },
      icon: _busy
          ? const SizedBox(
              width: 11,
              height: 11,
              child: CircularProgressIndicator(strokeWidth: 1.4),
            )
          : Icon(Icons.stop_rounded, size: 14, color: Colors.red.shade400),
      label: Text(
        '停止',
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          color: Colors.red.shade400,
        ),
      ),
      style: OutlinedButton.styleFrom(
        visualDensity: VisualDensity.compact,
        padding: const EdgeInsets.symmetric(horizontal: 10),
        minimumSize: const Size(0, 28),
        side: BorderSide(color: Colors.red.shade200),
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      ),
    );
  }
}
