// CancelConfirmDialog — 取消订阅确认. W5-10.
//
// 用户取消订阅前的最后一道, 给两个选项:
//   1. period_end (默认): 周期末止服, 已付款不退
//   2. immediate: 立即停服 + 按 proration 退款
//
// onConfirm(immediate: bool) — 用户点确认. 取消按钮关闭对话框.

import 'package:flutter/material.dart';

import '../../domain/subscription.dart';

class CancelConfirmDialog extends StatefulWidget {
  final Subscription subscription;
  final void Function(bool immediate) onConfirm;
  final bool busy;

  const CancelConfirmDialog({
    super.key,
    required this.subscription,
    required this.onConfirm,
    this.busy = false,
  });

  @override
  State<CancelConfirmDialog> createState() => _CancelConfirmDialogState();
}

class _CancelConfirmDialogState extends State<CancelConfirmDialog> {
  bool _immediate = false;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final endStr = widget.subscription.currentPeriodEnd != null
        ? widget.subscription.currentPeriodEnd!.toLocal().toString().split(' ').first
        : '当前周期结束';

    return AlertDialog(
      title: const Text('取消订阅'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '当前方案: ${widget.subscription.plan.name}',
            style: theme.textTheme.bodyMedium,
          ),
          const SizedBox(height: 12),
          RadioListTile<bool>(
            title: const Text('周期结束时停止'),
            subtitle: Text('继续服务至 $endStr, 不退款'),
            value: false,
            groupValue: _immediate,
            onChanged: (v) => setState(() => _immediate = v ?? false),
          ),
          RadioListTile<bool>(
            title: const Text('立即停止 + 按比例退款'),
            subtitle: const Text('剩余周期按 proration 计算退款'),
            value: true,
            groupValue: _immediate,
            onChanged: (v) => setState(() => _immediate = v ?? true),
          ),
          const SizedBox(height: 8),
          Text(
            '取消后随时可在 period_end 前点 "恢复" 撤销操作',
            style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: widget.busy ? null : () => Navigator.of(context).pop(),
          child: const Text('再想想'),
        ),
        FilledButton(
          onPressed: widget.busy ? null : () => widget.onConfirm(_immediate),
          child: widget.busy
              ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
              : const Text('确认取消'),
        ),
      ],
    );
  }
}
