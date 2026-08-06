// ActiveTaskBanner — 创作壳顶部「生成进度 / 终态」浮条 (R2 任务2, 替代 SnackBar).
//
// 两态 (互斥, terminal 优先):
//   * terminal 非空 → 终态浮条 (完成 ✨ / 失败 / 退还), 2s 后由调用方清空.
//   * activeTasks 非空 → 进度浮条 (正在生成 · NN% + 线性进度 + 取消).
//   * 否则 → SizedBox.shrink (无 active 无终态).
//
// 纯展示: 不 watch provider, 由 CreationShell 传 activeTasks + terminal
// (+ onCancel). 这样可单测 (造 CreationTask fixture 即可, 不必 override
// StateNotifier). 视觉复用 ConnectionBanner._BannerBar 同款全宽色条.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import '../../domain/creation_task.dart';

/// 终态消息 (完成/失败/退还). 由 CreationShell._onNotification 构造,
/// 2s 后置 null 触发浮条隐藏.
class TerminalMessage {
  const TerminalMessage({
    required this.color,
    required this.icon,
    required this.text,
  });
  final Color color;
  final IconData icon;
  final String text;
}

class ActiveTaskBanner extends StatelessWidget {
  const ActiveTaskBanner({
    super.key,
    required this.activeTasks,
    this.terminal,
    this.onCancel,
  });

  /// active 任务 (pending/queued/running/submitting), 调用方按 createdAt desc
  /// 排好序. 空列表 → 不显进度浮条.
  final List<CreationTask> activeTasks;

  /// 终态消息. 非空时覆盖进度浮条显示 2s.
  final TerminalMessage? terminal;

  /// 取消最新 active 任务的回调 (浮条 ✕). null 不显取消按钮.
  final VoidCallback? onCancel;

  @override
  Widget build(BuildContext context) {
    final term = terminal;
    if (term != null) {
      return _bar(
        bg: term.color.withValues(alpha: 0.14),
        fg: term.color,
        icon: term.icon,
        message: term.text,
      );
    }
    if (activeTasks.isEmpty) return const SizedBox.shrink();
    // 最新 active (调用方已按 createdAt desc 排序).
    final task = activeTasks.first;
    final multiple = activeTasks.length > 1;
    final message = multiple
        ? '${activeTasks.length} 个生成中… (最新 ${task.progress}%)'
        : '正在生成 · ${task.progress}%';
    return _bar(
      bg: BiuTokens.purpleSoft,
      fg: BiuTokens.purple,
      icon: Icons.auto_awesome,
      message: message,
      trailing: LinearProgressIndicator(
        value: task.progress.clamp(0, 100) / 100,
        minHeight: 3,
        backgroundColor: BiuTokens.purple.withValues(alpha: 0.15),
        valueColor: AlwaysStoppedAnimation<Color>(BiuTokens.purple),
      ),
      onCancel: onCancel,
    );
  }

  /// 全宽色条 (与 ConnectionBanner._BannerBar 同款视觉语言).
  Widget _bar({
    required Color bg,
    required Color fg,
    required IconData icon,
    required String message,
    Widget? trailing,
    VoidCallback? onCancel,
  }) {
    return Container(
      width: double.infinity,
      color: bg,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        children: [
          Icon(icon, size: 14, color: fg),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  message,
                  style: TextStyle(
                    fontSize: 12,
                    color: fg,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                if (trailing != null) ...[
                  const SizedBox(height: 4),
                  trailing,
                ],
              ],
            ),
          ),
          if (onCancel != null)
            IconButton(
              tooltip: '取消',
              onPressed: onCancel,
              icon: Icon(Icons.close, size: 16, color: fg),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
            ),
        ],
      ),
    );
  }
}
