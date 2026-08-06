// UpgradeModal — 升级 / 降级确认 modal, 展示 proration 数字. W5-10.
//
// 升级路径:
//   - 调 changePlan(targetPlan) 后端返 proration → 这个 modal 展示 net_charge
//   - 用户确认 → 触发 checkout 调起支付通道
// 降级路径:
//   - 调 changePlan 返 effective='period_end' + scheduled_at → 提示 "周期末生效"
//
// onProceed() — 用户点继续. 升级时 wrapper 通常会 push checkout_page.

import 'package:flutter/material.dart';

import '../../domain/checkout.dart';
import '../../domain/plan.dart';

class UpgradeModal extends StatelessWidget {
  final Plan oldPlan;
  final Plan newPlan;
  final ChangePlanResponse response;
  final VoidCallback onProceed;
  final VoidCallback onClose;

  const UpgradeModal({
    super.key,
    required this.oldPlan,
    required this.newPlan,
    required this.response,
    required this.onProceed,
    required this.onClose,
  });

  String _fmtCents(int cents, String currency) {
    final symbol = currency == 'USD' ? '\$' : '¥';
    return '$symbol${(cents / 100).toStringAsFixed(2)}';
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final isImmediate = response.isImmediate;
    final p = response.proration;
    final currency = newPlan.priceCurrency;

    return AlertDialog(
      title: Text(isImmediate ? '升级到 ${newPlan.name}' : '降级到 ${newPlan.name}'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('从 ${oldPlan.name} → ${newPlan.name}', style: theme.textTheme.bodyMedium),
          const SizedBox(height: 12),
          if (isImmediate && p != null) ...[
            Text('立即生效, 按比例补差', style: theme.textTheme.bodySmall),
            const SizedBox(height: 8),
            _Row('旧方案剩余抵扣', '- ${_fmtCents(p.unusedRefundCents, currency)}'),
            _Row('新方案补差', _fmtCents(p.newProrateChargeCents, currency)),
            const Divider(),
            _Row(
              '本次需支付',
              _fmtCents(p.netChargeCents, currency),
              bold: true,
              color: theme.colorScheme.primary,
            ),
          ] else ...[
            Text('降级生效时间: 当前周期末', style: theme.textTheme.bodyMedium),
            const SizedBox(height: 4),
            if (response.scheduledAt != null)
              Text(
                '预计时间: ${response.scheduledAt!.toLocal().toString().split('.').first}',
                style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
              ),
            const SizedBox(height: 8),
            Text(
              '当前 ${oldPlan.name} 服务到周期末, 不退款; 周期末自动切换到 ${newPlan.name}.',
              style: theme.textTheme.bodySmall,
            ),
          ],
        ],
      ),
      actions: [
        TextButton(onPressed: onClose, child: const Text('再想想')),
        FilledButton(
          onPressed: onProceed,
          child: Text(isImmediate ? '继续支付' : '确认降级'),
        ),
      ],
    );
  }
}

class _Row extends StatelessWidget {
  final String label;
  final String value;
  final bool bold;
  final Color? color;
  const _Row(this.label, this.value, {this.bold = false, this.color});

  @override
  Widget build(BuildContext context) {
    final style = Theme.of(context).textTheme.bodyMedium?.copyWith(
          fontWeight: bold ? FontWeight.w700 : FontWeight.w400,
          color: color,
        );
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          Expanded(child: Text(label, style: style)),
          Text(value, style: style),
        ],
      ),
    );
  }
}
