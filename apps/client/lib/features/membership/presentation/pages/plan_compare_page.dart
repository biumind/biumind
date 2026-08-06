// PlanComparePage — 套餐对比表. W5-10.
//
// 横向 4 档对比 (free/pro/team/enterprise), 行项目: 价格 / 月度积分 /
// Hub RPM/TPM / 沙盒任务 / Brain 项目数. 每列底部 CTA.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../core/layout/phone_nav.dart';
import '../../application/membership_providers.dart';
import '../../domain/plan.dart';

class PlanComparePage extends ConsumerWidget {
  const PlanComparePage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final plansAsync = ref.watch(plansListProvider);
    return Scaffold(
      appBar: AppBar(
        // 子页头左位 ← (手机形态; 桌面必须为 null, 见 phone_nav.dart)。
        leading: phoneBackLeading(context),
        title: const Text('套餐对比'),
      ),
      body: plansAsync.when(
        data: (plans) => _Table(plans: plans),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: Text('$e')),
      ),
    );
  }
}

class _Table extends StatelessWidget {
  final List<Plan> plans;
  const _Table({required this.plans});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    if (plans.isEmpty) return const Center(child: Text('暂无套餐'));

    final rows = <_RowDef>[
      _RowDef('月费', (p) => p.priceMonthly == 0
          ? '免费'
          : '${p.priceCurrency == 'USD' ? '\$' : '¥'}${p.priceMonthly.toStringAsFixed(2)}'),
      _RowDef('年费', (p) => p.priceYearly == 0
          ? '—'
          : '${p.priceCurrency == 'USD' ? '\$' : '¥'}${p.priceYearly.toStringAsFixed(2)}'),
      _RowDef('月度积分', (p) => p.monthlyCredits == 0 ? '—' : _fmt(p.monthlyCredits)),
      _RowDef('Hub RPM', (p) => _fmt(p.benefits.hubRpm)),
      _RowDef('Hub TPM', (p) => _fmt(p.benefits.hubTpm)),
      _RowDef('沙盒/日', (p) => _fmt(p.benefits.sandboxDaily)),
      _RowDef('Brain 项目', (p) => _fmt(p.benefits.brainProjects)),
    ];

    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(16),
        child: DataTable(
          columns: [
            const DataColumn(label: Text('特性')),
            for (final p in plans)
              DataColumn(
                label: Text(p.name, style: theme.textTheme.titleSmall),
              ),
          ],
          rows: [
            for (final row in rows)
              DataRow(cells: [
                DataCell(Text(row.label)),
                for (final p in plans) DataCell(Text(row.value(p))),
              ]),
            DataRow(cells: [
              const DataCell(Text('')),
              for (final p in plans)
                DataCell(
                  TextButton(
                    onPressed: p.isCurrent
                        ? null
                        : () => context.push('/membership/checkout', extra: <String, dynamic>{
                              'plan_code': p.code.wireValue,
                            }),
                    child: Text(p.isCurrent ? '当前' : '选择'),
                  ),
                ),
            ]),
          ],
        ),
      ),
    );
  }

  static String _fmt(num n) {
    if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(0)}K';
    return n.toString();
  }
}

class _RowDef {
  final String label;
  final String Function(Plan p) value;
  _RowDef(this.label, this.value);
}
