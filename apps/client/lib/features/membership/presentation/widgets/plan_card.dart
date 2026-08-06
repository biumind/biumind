// PlanCard — 单个 plan 视觉卡片. W5-10.

import 'package:flutter/material.dart';

import '../../../../core/ui/biu_card.dart';
import '../../domain/plan.dart';
import 'billing_cycle_toggle.dart';

class PlanCard extends StatelessWidget {
  final Plan plan;
  final bool isCurrent;
  final bool highlighted; // 当前选中态
  final VoidCallback? onTap;
  final VoidCallback? onSelect;
  final String? ctaLabel; // "选择" / "升级" / "降级" / "当前方案"
  final BillingCycle cycle;

  const PlanCard({
    super.key,
    required this.plan,
    this.isCurrent = false,
    this.highlighted = false,
    this.onTap,
    this.onSelect,
    this.ctaLabel,
    this.cycle = BillingCycle.monthly,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final symbol = plan.priceCurrency == 'USD' ? '\$' : '¥';
    String price;
    String? subPrice;
    if (plan.priceMonthly == 0 && plan.priceYearly == 0) {
      price = '免费';
    } else if (cycle == BillingCycle.yearly && plan.priceYearly > 0) {
      // 年付: 显示折算到月的价格作主, 标 "$XX.X / 年" 副.
      final monthly = plan.priceYearly / 12;
      price = '$symbol${monthly.toStringAsFixed(2)} / 月';
      subPrice = '$symbol${plan.priceYearly.toStringAsFixed(2)} / 年';
    } else {
      price = '$symbol${plan.priceMonthly.toStringAsFixed(2)} / 月';
      subPrice = plan.priceYearly > 0
          ? '$symbol${plan.priceYearly.toStringAsFixed(2)} / 年'
          : null;
    }

    return BiuCard(
      onTap: onTap,
      selected: highlighted,
      lift: 2, // 套餐对比卡 hover 抬起 2px (中等强度)
      padding: const EdgeInsets.all(20),
      borderRadius: BorderRadius.circular(12),
      child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      plan.name,
                      style: theme.textTheme.titleLarge?.copyWith(
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  if (isCurrent)
                    Chip(
                      label: const Text('当前'),
                      backgroundColor: theme.colorScheme.primaryContainer,
                      visualDensity: VisualDensity.compact,
                    ),
                ],
              ),
              const SizedBox(height: 8),
              Text(
                price,
                style: theme.textTheme.headlineSmall?.copyWith(
                  color: theme.colorScheme.primary,
                  fontWeight: FontWeight.w700,
                ),
              ),
              if (subPrice != null)
                Padding(
                  padding: const EdgeInsets.only(top: 2),
                  child: Text(
                    subPrice,
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.hintColor,
                    ),
                  ),
                ),
              const SizedBox(height: 12),
              Text(
                plan.description,
                style: theme.textTheme.bodyMedium,
              ),
              const SizedBox(height: 12),
              _BenefitsList(plan: plan),
              const SizedBox(height: 16),
              SizedBox(
                width: double.infinity,
                child: FilledButton.tonal(
                  onPressed: isCurrent ? null : onSelect,
                  child: Text(ctaLabel ?? (isCurrent ? '当前方案' : '选择')),
                ),
              ),
            ],
          ),
    );
  }
}

class _BenefitsList extends StatelessWidget {
  final Plan plan;
  const _BenefitsList({required this.plan});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final lines = <String>[
      if (plan.monthlyCredits > 0) '每月 ${_fmt(plan.monthlyCredits)} 积分',
      'Hub RPM ${_fmt(plan.benefits.hubRpm)}',
      'Hub TPM ${_fmt(plan.benefits.hubTpm)}',
      if (plan.benefits.sandboxDaily > 0) '沙盒任务 ${plan.benefits.sandboxDaily}/日',
      if (plan.benefits.brainProjects > 0) '${plan.benefits.brainProjects} 个 Brain 项目',
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: lines.map((s) {
        return Padding(
          padding: const EdgeInsets.only(bottom: 4),
          child: Row(
            children: [
              Icon(Icons.check_circle, size: 14, color: theme.colorScheme.primary),
              const SizedBox(width: 6),
              Expanded(child: Text(s, style: theme.textTheme.bodySmall)),
            ],
          ),
        );
      }).toList(),
    );
  }

  String _fmt(num n) {
    if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(0)}K';
    return n.toString();
  }
}
