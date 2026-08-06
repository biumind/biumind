// CouponRedeemPage — W6-13 兑换券页. 输入码 + 提交 + 展示结果.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/layout/phone_nav.dart';
import '../../application/membership_providers.dart';
import '../../domain/coupon.dart';

class CouponRedeemPage extends ConsumerStatefulWidget {
  const CouponRedeemPage({super.key});

  @override
  ConsumerState<CouponRedeemPage> createState() => _CouponRedeemPageState();
}

class _CouponRedeemPageState extends ConsumerState<CouponRedeemPage> {
  final _ctrl = TextEditingController();
  bool _busy = false;
  String? _error;
  CouponRedeemResult? _result;

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final code = _ctrl.text.trim().toUpperCase();
    if (code.isEmpty) return;
    final client = ref.read(couponsClientProvider);
    if (client == null) {
      setState(() => _error = '请先登录');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
      _result = null;
    });
    try {
      final r = await client.redeem(code: code);
      setState(() => _result = r);
      // 兑换成功刷新订阅 / 订单 (credits_grant / trial_extend 影响这俩)
      ref.invalidate(mySubscriptionProvider);
      ref.invalidate(ordersListProvider);
    } catch (e) {
      setState(() => _error = _humanize('$e'));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  /// _humanize — 把 server 错误码翻成中文人话.
  String _humanize(String raw) {
    if (raw.contains('coupon_not_found')) return '兑换码无效';
    if (raw.contains('coupon_expired')) return '兑换码已过期';
    if (raw.contains('coupon_inactive')) return '兑换码已停用';
    if (raw.contains('coupon_already_used')) return '此兑换码您已使用过';
    if (raw.contains('coupon_plan_mismatch')) return '此券不适用当前套餐';
    if (raw.contains('coupon_currency_mismatch')) return '币种不匹配';
    if (raw.contains('coupon_max_uses')) return '兑换码已达使用上限';
    return raw;
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        // 子页头左位 ← (手机形态; 桌面必须为 null, 见 phone_nav.dart)。
        leading: phoneBackLeading(context),
        title: const Text('兑换码'),
      ),
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '输入兑换码立即领取奖励',
              style: theme.textTheme.titleMedium,
            ),
            const SizedBox(height: 4),
            Text(
              '支持 4 类: 立减 / 折扣 / 积分礼包 / 试用延长',
              style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
            ),
            const SizedBox(height: 20),
            TextField(
              controller: _ctrl,
              autofocus: true,
              textCapitalization: TextCapitalization.characters,
              decoration: const InputDecoration(
                labelText: '兑换码',
                hintText: 'NEWUSER20',
                border: OutlineInputBorder(),
              ),
              onSubmitted: (_) => _submit(),
            ),
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: FilledButton(
                onPressed: _busy ? null : _submit,
                child: _busy
                    ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Text('兑换'),
              ),
            ),
            const SizedBox(height: 16),
            if (_error != null)
              Card(
                color: theme.colorScheme.errorContainer,
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Row(
                    children: [
                      Icon(Icons.error_outline, color: theme.colorScheme.onErrorContainer),
                      const SizedBox(width: 8),
                      Expanded(child: Text(_error!)),
                    ],
                  ),
                ),
              ),
            if (_result != null) _SuccessCard(result: _result!),
          ],
        ),
      ),
    );
  }
}

class _SuccessCard extends StatelessWidget {
  final CouponRedeemResult result;
  const _SuccessCard({required this.result});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      color: Colors.green.withValues(alpha: 0.08),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: Colors.green.withValues(alpha: 0.4)),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Row(
          children: [
            const Icon(Icons.check_circle, color: Colors.green, size: 32),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '兑换成功',
                    style: theme.textTheme.titleMedium
                        ?.copyWith(color: Colors.green, fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 4),
                  Text(result.summary()),
                  const SizedBox(height: 4),
                  Text(
                    '券码: ${result.couponCode}',
                    style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
