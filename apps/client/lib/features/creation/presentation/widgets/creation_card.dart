// CreationCard — 我的作品 / 灵感瀑布流单卡, 三态:
//
//   1. active   (submitting/pending/queued/running): 占位 + 状态文字 + 进度条
//   2. completed: BlurHash → 真图 fade-in + hover overlay (prompt + actions)
//   3. failed/blocked/cancelled: 错误图标 + label + 重试 (failed) / 解释 (blocked)
//
// 性能:
//   - 调用方应该用 ref.watch tasksControllerProvider.select((s) => s.tasks[id])
//     做细粒度订阅, 避免单 task 变化触发整个瀑布流重建.
//
// 多输出:
//   - num_outputs>1 时只显示 outputs[0], 右下角加 +N 角标. 详情页里展开.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/tasks_controller.dart';
import '../../data/error_translator.dart';
import '../../domain/creation_task.dart';
import 'output_thumbnail.dart';

class CreationCard extends ConsumerStatefulWidget {
  const CreationCard({
    super.key,
    required this.task,
    this.onTap,
    this.onMakeSimilar,
    this.onRetry,
    this.aspect = 1.0,
  });

  final CreationTask task;
  final VoidCallback? onTap;
  final VoidCallback? onMakeSimilar;

  /// 失败时点「重试」回调. 通常是父组件用 form.syncFromTask + tasks.submit.
  final VoidCallback? onRetry;

  /// 卡片宽高比. 灵感页瀑布流可按 output.width/height 算; 这里默认 1:1.
  final double aspect;

  @override
  ConsumerState<CreationCard> createState() => _CreationCardState();
}

class _CreationCardState extends ConsumerState<CreationCard> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = widget.task;
    return MouseRegion(
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        child: ClipRRect(
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
          child: AspectRatio(
            aspectRatio: widget.aspect,
            child: Container(
              color: BiuTokens.surfaceMuted,
              child: Stack(
                fit: StackFit.expand,
                children: [
                  _buildBackground(t),
                  _buildOverlay(t),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  // ─── 背景层: 三态分支 ─────────────────────────────

  Widget _buildBackground(CreationTask t) {
    if (t.status.isActive) {
      return _ActivePlaceholder(task: t);
    }
    if (t.status == TaskStatus.completed && t.outputs.isNotEmpty) {
      return OutputThumbnail(output: t.outputs.first);
    }
    return _ErrorPlaceholder(task: t);
  }

  // ─── 前景 overlay ───────────────────────────────

  Widget _buildOverlay(CreationTask t) {
    final loc = AppLocalizations.of(context)!;

    // active 态: 状态 + 进度条 (常显, 不依赖 hover)
    if (t.status.isActive) {
      return _ActiveOverlay(task: t, loc: loc, onCancel: () => _cancel(t));
    }
    // failed/blocked/cancelled: 错误信息常显 + retry 按钮
    if (t.status != TaskStatus.completed) {
      return _ErrorOverlay(
        task: t,
        loc: loc,
        onRetry: widget.onRetry,
        onDelete: () => _delete(t),
      );
    }
    // completed: 多输出角标 + hover overlay
    return Stack(
      children: [
        if (t.outputs.length > 1) _CountBadge(n: t.outputs.length),
        AnimatedOpacity(
          opacity: _hover ? 1 : 0,
          duration: const Duration(milliseconds: 140),
          child: _CompletedOverlay(
            task: t,
            loc: loc,
            onMakeSimilar: widget.onMakeSimilar,
            onDelete: () => _delete(t),
          ),
        ),
      ],
    );
  }

  Future<void> _cancel(CreationTask t) async {
    try {
      await ref.read(tasksControllerProvider.notifier).cancel(t.id);
    } catch (e) {
      if (!mounted) return;
      _toast(translateError(e), error: true);
    }
  }

  Future<void> _delete(CreationTask t) async {
    try {
      await ref.read(tasksControllerProvider.notifier).delete(t.id);
    } catch (e) {
      if (!mounted) return;
      _toast(translateError(e), error: true);
    }
  }

  void _toast(String msg, {bool error = false}) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(msg),
      backgroundColor: error ? BiuTokens.error : null,
    ));
  }
}

// ─── Active 态 ─────────────────────────────────

class _ActivePlaceholder extends StatelessWidget {
  const _ActivePlaceholder({required this.task});
  final CreationTask task;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            BiuTokens.purpleSoft,
            BiuTokens.surfaceMuted,
          ],
        ),
      ),
      child: const Center(
        child: SizedBox(
          width: 28,
          height: 28,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      ),
    );
  }
}

class _ActiveOverlay extends StatelessWidget {
  const _ActiveOverlay({
    required this.task,
    required this.loc,
    required this.onCancel,
  });
  final CreationTask task;
  final AppLocalizations loc;
  final VoidCallback onCancel;

  @override
  Widget build(BuildContext context) {
    final label = _statusLabel(task.status, loc);
    return Container(
      alignment: Alignment.bottomLeft,
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.bottomCenter,
          end: Alignment.topCenter,
          colors: [
            Colors.black.withValues(alpha: 0.55),
            Colors.transparent,
          ],
        ),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisAlignment: MainAxisAlignment.end,
        children: [
          if (task.progress > 0) ...[
            ClipRRect(
              borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
              child: LinearProgressIndicator(
                value: task.progress / 100.0,
                minHeight: 4,
                backgroundColor: Colors.white.withValues(alpha: 0.25),
                valueColor: const AlwaysStoppedAnimation(Colors.white),
              ),
            ),
            const SizedBox(height: 6),
          ],
          Row(
            children: [
              Expanded(
                child: Text(
                  task.progress > 0 ? '$label · ${task.progress}%' : label,
                  style: const TextStyle(
                    fontSize: 12,
                    color: Colors.white,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (task.status == TaskStatus.queued ||
                  task.status == TaskStatus.running)
                _IconChip(
                  icon: Icons.close,
                  tooltip: loc.creationActionCancel,
                  onTap: onCancel,
                ),
            ],
          ),
        ],
      ),
    );
  }
}

// ─── Error 态 ─────────────────────────────────

class _ErrorPlaceholder extends StatelessWidget {
  const _ErrorPlaceholder({required this.task});
  final CreationTask task;

  @override
  Widget build(BuildContext context) {
    final color = task.status == TaskStatus.blocked
        ? WarningCallout.bg
        : BiuTokens.errorSoft;
    return Container(color: color);
  }
}

class _ErrorOverlay extends StatelessWidget {
  const _ErrorOverlay({
    required this.task,
    required this.loc,
    required this.onRetry,
    required this.onDelete,
  });
  final CreationTask task;
  final AppLocalizations loc;
  final VoidCallback? onRetry;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final isBlocked = task.status == TaskStatus.blocked;
    final isCancelled = task.status == TaskStatus.cancelled;
    final label = _statusLabel(task.status, loc);

    return Padding(
      padding: const EdgeInsets.all(12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                isBlocked
                    ? Icons.shield_outlined
                    : (isCancelled
                        ? Icons.block
                        : Icons.error_outline),
                size: 18,
                color: isBlocked
                    ? NamedPaletteStrong.amberMid
                    : BiuTokens.error,
              ),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: isBlocked
                        ? WarningCallout.textFg
                        : BiuTokens.error,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Expanded(
            child: SingleChildScrollView(
              child: Text(
                task.errorMessage ?? task.prompt,
                style: TextStyle(
                  fontSize: 11,
                  color: BiuTokens.text,
                  height: 1.4,
                ),
              ),
            ),
          ),
          const SizedBox(height: 6),
          Row(
            children: [
              if (task.refundedCredits > 0) ...[
                Icon(Icons.bolt, size: 12, color: BiuTokens.green),
                const SizedBox(width: 2),
                Text(
                  loc.creationCreditRefunded(task.refundedCredits),
                  style: TextStyle(fontSize: 10, color: BiuTokens.green),
                ),
              ],
              const Spacer(),
              if (task.status == TaskStatus.failed && onRetry != null)
                _TextChip(
                  label: loc.creationActionRetry,
                  icon: Icons.refresh,
                  onTap: onRetry!,
                ),
              const SizedBox(width: 6),
              _TextChip(
                label: loc.creationActionDelete,
                icon: Icons.delete_outline,
                onTap: onDelete,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ─── Completed 态 hover ──────────────────────

class _CompletedOverlay extends StatelessWidget {
  const _CompletedOverlay({
    required this.task,
    required this.loc,
    required this.onMakeSimilar,
    required this.onDelete,
  });
  final CreationTask task;
  final AppLocalizations loc;
  final VoidCallback? onMakeSimilar;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.bottomCenter,
          end: Alignment.topCenter,
          colors: [
            Colors.black.withValues(alpha: 0.7),
            Colors.transparent,
          ],
        ),
      ),
      padding: const EdgeInsets.all(10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisAlignment: MainAxisAlignment.end,
        children: [
          Text(
            task.prompt,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(
              fontSize: 12,
              color: Colors.white,
              height: 1.3,
            ),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              if (onMakeSimilar != null)
                _IconChip(
                  icon: Icons.auto_awesome,
                  tooltip: loc.creationActionMakeSimilar,
                  onTap: onMakeSimilar!,
                ),
              const Spacer(),
              _IconChip(
                icon: Icons.delete_outline,
                tooltip: loc.creationActionDelete,
                onTap: onDelete,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ─── Helpers ─────────────────────────────────────

String _statusLabel(TaskStatus s, AppLocalizations loc) {
  switch (s) {
    case TaskStatus.submitting:
    case TaskStatus.pending:
      return loc.creationCardPending;
    case TaskStatus.queued:
      return loc.creationCardQueued;
    case TaskStatus.running:
      return loc.creationCardRunning;
    case TaskStatus.completed:
      return loc.creationCardCompleted;
    case TaskStatus.failed:
      return loc.creationCardFailed;
    case TaskStatus.blocked:
      return loc.creationCardBlocked;
    case TaskStatus.cancelled:
      return loc.creationCardCancelled;
  }
}

class _CountBadge extends StatelessWidget {
  const _CountBadge({required this.n});
  final int n;

  @override
  Widget build(BuildContext context) {
    return Positioned(
      right: 8,
      bottom: 8,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        decoration: BoxDecoration(
          color: Colors.black.withValues(alpha: 0.55),
          borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
        ),
        child: Text(
          '+$n',
          style: const TextStyle(
            fontSize: 10,
            color: Colors.white,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}

class _IconChip extends StatelessWidget {
  const _IconChip({
    required this.icon,
    required this.onTap,
    this.tooltip,
  });
  final IconData icon;
  final VoidCallback onTap;
  final String? tooltip;

  @override
  Widget build(BuildContext context) {
    final btn = Material(
      color: Colors.white.withValues(alpha: 0.15),
      borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(6),
          child: Icon(icon, size: 14, color: Colors.white),
        ),
      ),
    );
    return tooltip == null ? btn : Tooltip(message: tooltip!, child: btn);
  }
}

class _TextChip extends StatelessWidget {
  const _TextChip({
    required this.label,
    required this.icon,
    required this.onTap,
  });
  final String label;
  final IconData icon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: BiuTokens.surface,
      borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 12, color: BiuTokens.text),
              const SizedBox(width: 3),
              Text(
                label,
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.text,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
