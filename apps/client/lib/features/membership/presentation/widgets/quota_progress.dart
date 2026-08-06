// QuotaProgress — W4-7 显示套餐月度配额使用进度.
//
// 用在两个地方:
//   - 设置-会员中心 → 当前 plan + 进度条
//   - 主 sidebar CreditIndicator hover/expand 区域 (compact mode)
//
// 进度条颜色:
//   < 70%   绿
//   70-90%  橙
//   ≥ 90%   红 (推动用户升级)

import 'package:flutter/material.dart';

import '../../domain/subscription.dart';

class QuotaProgress extends StatelessWidget {
  final QuotaUsage usage;
  final String label;
  final bool compact;

  const QuotaProgress({
    super.key,
    required this.usage,
    required this.label,
    this.compact = false,
  });

  Color _barColor() {
    final p = usage.progress;
    if (p < 0.70) return Colors.green;
    if (p < 0.90) return Colors.orange;
    return Colors.red;
  }

  @override
  Widget build(BuildContext context) {
    if (usage.monthly <= 0) {
      // free 用户或 ref_type 无配额 — 不显示进度条, 只显示提示.
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Text(
          '$label · 不在套餐配额内',
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: Theme.of(context).hintColor,
              ),
        ),
      );
    }

    final theme = Theme.of(context);
    final pct = (usage.progress * 100).clamp(0, 100).toStringAsFixed(0);

    return Padding(
      padding: EdgeInsets.symmetric(vertical: compact ? 2 : 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  label,
                  style: compact
                      ? theme.textTheme.bodySmall
                      : theme.textTheme.bodyMedium,
                ),
              ),
              Text(
                '${usage.used}/${usage.monthly} ($pct%)',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.hintColor,
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: LinearProgressIndicator(
              value: usage.progress,
              minHeight: compact ? 4 : 6,
              backgroundColor: theme.dividerColor.withValues(alpha: 0.3),
              valueColor: AlwaysStoppedAnimation(_barColor()),
            ),
          ),
        ],
      ),
    );
  }
}
