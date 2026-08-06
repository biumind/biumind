// 任务对比视图 — 主区"任务对比" Tab 的内容。
//
// 把同一 compareGroupId 的 N 个任务并排显示, 每列复用 AgentStream 完整渲染。
// 顶部 header 行展示 agent / status / cost / duration / branch, 完成后用户
// 一眼看到谁干得快、便宜、改的多。
//
// 本视图的核心差异化场景: 多 agent 同题对决。

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import '../../domain/code_task.dart';
import 'agent_stream.dart';

class CompareView extends StatelessWidget {
  const CompareView({super.key, required this.tasks});

  /// 同组任务, 已按 createdAt 排序。
  final List<CodeTask> tasks;

  @override
  Widget build(BuildContext context) {
    if (tasks.isEmpty) {
      return Center(
        child: Text(
          'No tasks in this compare group',
          style: TextStyle(color: BiuTokens.textMuted),
        ),
      );
    }
    return Row(
      children: [
        for (var i = 0; i < tasks.length; i++) ...[
          if (i > 0)
            Container(width: 1, color: BiuTokens.borderSubtle),
          Expanded(child: _Column(task: tasks[i])),
        ],
      ],
    );
  }
}

class _Column extends StatelessWidget {
  const _Column({required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _ColumnHeader(task: task),
        Container(height: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: AgentStream(task: task),
        ),
      ],
    );
  }
}

/// 列顶部 header — agent / status / cost / duration / branch
class _ColumnHeader extends StatelessWidget {
  const _ColumnHeader({required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context) {
    final (agentColor, agentLabel) = _agentVisuals(task.agent);
    final (statusColor, statusIcon, statusLabel) = _statusVisuals(task.status);
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
      color: BiuTokens.bg,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // 第一行: agent label + status chip
          Row(
            children: [
              Container(
                width: 7,
                height: 7,
                decoration: BoxDecoration(
                  color: agentColor,
                  shape: BoxShape.circle,
                ),
              ),
              const SizedBox(width: 6),
              Text(
                agentLabel,
                style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                  letterSpacing: -0.2,
                  color: agentColor,
                ),
              ),
              const Spacer(),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: statusColor.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(statusIcon, size: 10, color: statusColor),
                    const SizedBox(width: 3),
                    Text(
                      statusLabel,
                      style: TextStyle(
                        fontSize: 10.5,
                        fontFamily: 'SF Mono',
                        fontWeight: FontWeight.w600,
                        color: statusColor,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          // 第二行: 指标 (cost / duration / branch)
          Wrap(
            spacing: 10,
            runSpacing: 4,
            children: [
              if (task.cost.usd > 0)
                _Metric(
                  icon: Icons.attach_money_rounded,
                  text: task.cost.usd.toStringAsFixed(4),
                ),
              if (task.cost.outputTokens > 0)
                _Metric(
                  icon: Icons.short_text_rounded,
                  text: '${task.cost.outputTokens}t out',
                ),
              if (task.duration != null)
                _Metric(
                  icon: Icons.schedule_rounded,
                  text: _formatDuration(task.duration!),
                ),
              if (task.workspace?.branchName != null)
                _Metric(
                  icon: Icons.merge_type_rounded,
                  text: task.workspace!.branchName!,
                  highlight: true,
                ),
            ],
          ),
        ],
      ),
    );
  }

  static (Color, String) _agentVisuals(AgentKind kind) => switch (kind) {
        AgentKind.biu => (BiuTokens.purple, 'biu'),
        AgentKind.claudeCode => (AgentKindColors.claude, 'Claude'),
        AgentKind.codex => (AgentKindColors.codex, 'Codex'),
      };

  static (Color, IconData, String) _statusVisuals(CodeTaskStatus s) =>
      switch (s) {
        CodeTaskStatus.queued => (BiuTokens.textMuted, Icons.schedule, 'queued'),
        CodeTaskStatus.running =>
          (BiuTokens.purple, Icons.play_arrow_rounded, 'running'),
        CodeTaskStatus.paused =>
          (BiuTokens.textMuted, Icons.pause, 'paused'),
        CodeTaskStatus.inputRequired =>
          (Colors.orange, Icons.priority_high_rounded, 'input'),
        CodeTaskStatus.done => (BiuTokens.green, Icons.check_rounded, 'done'),
        CodeTaskStatus.failed =>
          (Colors.red, Icons.close_rounded, 'failed'),
        CodeTaskStatus.interrupted =>
          (BiuTokens.textMuted, Icons.stop_rounded, 'cancel'),
        CodeTaskStatus.detached =>
          (Colors.orange, Icons.link_off_rounded, 'detached'),
      };

  static String _formatDuration(Duration d) {
    if (d.inSeconds < 60) return '${d.inSeconds}s';
    final m = d.inMinutes;
    final s = d.inSeconds - m * 60;
    return s == 0 ? '${m}m' : '${m}m${s}s';
  }
}

class _Metric extends StatelessWidget {
  const _Metric({required this.icon, required this.text, this.highlight = false});
  final IconData icon;
  final String text;
  final bool highlight;
  @override
  Widget build(BuildContext context) {
    final color = highlight ? BiuTokens.purple : BiuTokens.textSecondary;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 11, color: color),
        const SizedBox(width: 3),
        Text(
          text,
          style: TextStyle(
            fontSize: 10.5,
            fontFamily: 'SF Mono',
            color: color,
            fontWeight: highlight ? FontWeight.w600 : FontWeight.w500,
          ),
        ),
      ],
    );
  }
}
