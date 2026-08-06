// RechargePacksSection — W7 IA 重设计.
//
// 「单次购买积分包」section, 可嵌入会员中心 / 兑换码页. 数据源跟原
// RechargePage 一致 — `rechargeOptionsProvider` (GET /v1/credits/recharge-options).
// 一次性消耗品, 不影响订阅 / 月度积分.
//
// 点击行为: 不再直接调 mock /credits/recharge (W1 占位), 改成跳 /membership/checkout
// 走真支付通道 (W7 接入 /v1/credits/checkout endpoint, webhook 收到 SUCCESS 后 Grant).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../creation/application/credits_controller.dart';
import '../../../creation/data/credits_client.dart';

class RechargePacksSection extends ConsumerWidget {
  const RechargePacksSection({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final options = ref.watch(rechargeOptionsProvider);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text('单次充值积分包', style: theme.textTheme.titleMedium),
            const SizedBox(width: 8),
            Text(
              '· 一次购买, 不影响订阅',
              style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
            ),
          ],
        ),
        const SizedBox(height: 12),
        options.when(
          loading: () => const Padding(
            padding: EdgeInsets.all(24),
            child: Center(child: CircularProgressIndicator()),
          ),
          error: (e, _) => Text('$e', style: TextStyle(color: BiuTokens.error)),
          data: (list) => _PacksGrid(options: list),
        ),
      ],
    );
  }
}

class _PacksGrid extends ConsumerWidget {
  const _PacksGrid({required this.options});
  final List<RechargeOption> options;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (options.isEmpty) {
      return Container(
        padding: const EdgeInsets.all(24),
        alignment: Alignment.center,
        child: Text('暂无可用充值套餐', style: TextStyle(color: BiuTokens.textMuted)),
      );
    }
    final w = MediaQuery.of(context).size.width;
    final cols = w > 1100 ? 4 : (w > 720 ? 3 : (w > 480 ? 2 : 1));
    return GridView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      itemCount: options.length,
      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: cols,
        mainAxisSpacing: 12,
        crossAxisSpacing: 12,
        childAspectRatio: 1.5,
      ),
      itemBuilder: (_, i) => _PackCard(
        option: options[i],
        onPick: () => _pick(context, options[i]),
      ),
    );
  }

  void _pick(BuildContext context, RechargeOption o) {
    // 跳支付页, kind=topup 触发 /v1/credits/checkout 路径.
    context.push('/membership/checkout', extra: <String, dynamic>{
      'topup': true,
      'option_id': o.id,
      'amount_cents': (o.priceCny * 100).round(),
      'display_name': o.displayName,
      'credits_amount': o.creditsAmount,
    });
  }
}

class _PackCard extends StatelessWidget {
  const _PackCard({
    required this.option,
    required this.onPick,
  });
  final RechargeOption option;
  final VoidCallback onPick;

  @override
  Widget build(BuildContext context) {
    final isTimeLimited = option.kind == 'time_limited';
    return Material(
      color: BiuTokens.surface,
      borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        onTap: onPick,
        child: Container(
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            border: Border.all(color: BiuTokens.border),
            borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      option.displayName,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                        color: BiuTokens.text,
                      ),
                    ),
                  ),
                  if (isTimeLimited)
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                      decoration: BoxDecoration(
                        color: BiuTokens.purpleSoft,
                        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
                      ),
                      child: Text(
                        '${option.validDays}d',
                        style: TextStyle(
                          fontSize: 10,
                          fontWeight: FontWeight.w600,
                          color: BiuTokens.purple,
                        ),
                      ),
                    ),
                ],
              ),
              Row(
                crossAxisAlignment: CrossAxisAlignment.baseline,
                textBaseline: TextBaseline.alphabetic,
                children: [
                  Icon(Icons.bolt, size: 18, color: BiuTokens.purple),
                  const SizedBox(width: 2),
                  Text(
                    '${option.creditsAmount}',
                    style: TextStyle(
                      fontSize: 24,
                      fontWeight: FontWeight.w700,
                      color: BiuTokens.text,
                    ),
                  ),
                  const SizedBox(width: 6),
                  Text('积分',
                      style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary)),
                ],
              ),
              Row(
                children: [
                  Text(
                    '¥${option.priceCny.toStringAsFixed(2)}',
                    style: TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                      color: BiuTokens.purple,
                    ),
                  ),
                  const Spacer(),
                  Icon(Icons.arrow_forward, size: 14, color: BiuTokens.textMuted),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
