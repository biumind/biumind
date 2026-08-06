// PaymentMethodSelector — 选支付通道. W5-10.
//
// 三档: 微信 / 支付宝 / Stripe (海外信用卡). 桌面端默认微信扫码 + 支付宝 PC,
// 移动端默认 H5 / Wap. 选择后 onSelected(provider).

import 'package:flutter/foundation.dart' show defaultTargetPlatform, TargetPlatform;
import 'package:flutter/material.dart';

import '../../domain/checkout.dart';

class PaymentMethodSelector extends StatefulWidget {
  final ValueChanged<PaymentProvider> onSelected;
  final PaymentProvider? initial;
  final bool wechatEnabled;
  final bool alipayEnabled;
  final bool stripeEnabled;

  const PaymentMethodSelector({
    super.key,
    required this.onSelected,
    this.initial,
    this.wechatEnabled = true,
    this.alipayEnabled = true,
    this.stripeEnabled = true,
  });

  static PaymentProvider defaultFor({required bool isMobile}) {
    return isMobile ? PaymentProvider.wechatH5 : PaymentProvider.wechatNative;
  }

  @override
  State<PaymentMethodSelector> createState() => _PaymentMethodSelectorState();
}

class _PaymentMethodSelectorState extends State<PaymentMethodSelector> {
  late PaymentProvider _selected;

  @override
  void initState() {
    super.initState();
    final isMobile = defaultTargetPlatform == TargetPlatform.iOS ||
        defaultTargetPlatform == TargetPlatform.android;
    _selected = widget.initial ?? PaymentMethodSelector.defaultFor(isMobile: isMobile);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final options = <_PayOpt>[
      if (widget.wechatEnabled)
        const _PayOpt(
          provider: PaymentProvider.wechatNative,
          label: '微信支付 (扫码)',
          icon: Icons.qr_code,
        ),
      if (widget.wechatEnabled)
        const _PayOpt(
          provider: PaymentProvider.wechatH5,
          label: '微信支付 (H5)',
          icon: Icons.smartphone,
        ),
      if (widget.alipayEnabled)
        const _PayOpt(
          provider: PaymentProvider.alipayPC,
          label: '支付宝 (网页)',
          icon: Icons.web,
        ),
      if (widget.alipayEnabled)
        const _PayOpt(
          provider: PaymentProvider.alipayWap,
          label: '支付宝 (手机)',
          icon: Icons.phone_android,
        ),
      if (widget.stripeEnabled)
        const _PayOpt(
          provider: PaymentProvider.stripe,
          label: '国际信用卡',
          icon: Icons.credit_card,
        ),
    ];

    return RadioGroup<PaymentProvider>(
      groupValue: _selected,
      onChanged: (v) {
        if (v == null) return;
        setState(() => _selected = v);
        widget.onSelected(v);
      },
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('选择支付方式', style: theme.textTheme.titleMedium),
          const SizedBox(height: 8),
          ...options.map(
            (o) => RadioListTile<PaymentProvider>(
              title: Row(
                children: [
                  Icon(o.icon, size: 18),
                  const SizedBox(width: 8),
                  Text(o.label),
                ],
              ),
              value: o.provider,
            ),
          ),
        ],
      ),
    );
  }
}

class _PayOpt {
  final PaymentProvider provider;
  final String label;
  final IconData icon;
  const _PayOpt({required this.provider, required this.label, required this.icon});
}
