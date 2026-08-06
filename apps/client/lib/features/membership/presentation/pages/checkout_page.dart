// CheckoutPage — 支付落地页. W5-10.
//
// 入参 (extra map): plan_code, net_charge_cents (升级补差时)
// 用户先选 PaymentProvider, 然后点 "立即支付" 调 checkout endpoint.
// 响应:
//   wechat_native → 显示扫码二维码 (CodeURL 内容)
//   wechat_h5     → 直接打开浏览器跳转
//   alipay_pc/wap → 浏览器跳转 (form auto-submit / GET)
//
// 实际打开外部浏览器用 url_launcher; 如未安装则展示 URL 让用户手动打开.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../core/layout/phone_nav.dart';
import '../../application/membership_providers.dart';
import '../../domain/checkout.dart';
import '../widgets/payment_method_selector.dart';

class CheckoutPage extends ConsumerStatefulWidget {
  // 订阅模式 (默认): 走 /v1/subscriptions/checkout.
  final String planCode;
  final int? netChargeCents;

  // 单次充值模式 (W7): topup=true 时, 走 /v1/credits/checkout, 显示积分包名 + 价格.
  final bool topup;
  final String? optionID;
  final String? topupDisplayName;
  final int? topupCreditsAmount;
  final int? topupAmountCents;

  const CheckoutPage({
    super.key,
    this.planCode = '',
    this.netChargeCents,
    this.topup = false,
    this.optionID,
    this.topupDisplayName,
    this.topupCreditsAmount,
    this.topupAmountCents,
  });

  @override
  ConsumerState<CheckoutPage> createState() => _CheckoutPageState();
}

class _CheckoutPageState extends ConsumerState<CheckoutPage> {
  PaymentProvider _selected = PaymentProvider.wechatNative;
  CheckoutResponse? _resp;
  bool _busy = false;
  String? _error;

  Future<void> _pay() async {
    setState(() {
      _busy = true;
      _error = null;
      _resp = null;
    });
    try {
      CheckoutResponse r;
      if (widget.topup) {
        final client = ref.read(membershipClientProvider);
        if (client == null || widget.optionID == null) {
          throw Exception('topup checkout 客户端未就绪');
        }
        r = await client.topupCheckout(
          optionID: widget.optionID!,
          provider: _selected,
        );
      } else {
        final actions = ref.read(membershipActionsProvider);
        if (actions == null) throw Exception('subscription checkout 未就绪');
        r = await actions.checkout(CheckoutRequest(
          planCode: widget.planCode,
          provider: _selected,
        ));
      }
      setState(() => _resp = r);
      // 自动打开外链 (alipay / wechat_h5)
      final url = r.h5URL ?? r.redirectURL;
      if (url != null && url.isNotEmpty) {
        try {
          final uri = Uri.parse(url);
          await launchUrl(uri, mode: LaunchMode.externalApplication);
        } catch (_) {/* 失败时让用户手动复制 */}
      }
    } catch (e) {
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        // 子页头左位 ← (手机形态; 桌面必须为 null, 见 phone_nav.dart)。
        leading: phoneBackLeading(context),
        title: const Text('支付'),
      ),
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Card(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('订单详情', style: theme.textTheme.titleMedium),
                    const SizedBox(height: 8),
                    if (widget.topup) ...[
                      Text('充值: ${widget.topupDisplayName ?? '积分包'}'),
                      if (widget.topupCreditsAmount != null)
                        Text('+${widget.topupCreditsAmount} 积分',
                            style: theme.textTheme.bodyMedium),
                      if (widget.topupAmountCents != null)
                        Text(
                          '本次需支付: ¥${(widget.topupAmountCents! / 100).toStringAsFixed(2)}',
                          style: theme.textTheme.bodyLarge?.copyWith(
                            fontWeight: FontWeight.w700,
                            color: theme.colorScheme.primary,
                          ),
                        ),
                    ] else ...[
                      Text('套餐: ${widget.planCode}'),
                      if (widget.netChargeCents != null)
                        Text(
                          '本次需支付: ¥${(widget.netChargeCents! / 100).toStringAsFixed(2)}',
                          style: theme.textTheme.bodyLarge?.copyWith(
                            fontWeight: FontWeight.w700,
                            color: theme.colorScheme.primary,
                          ),
                        ),
                    ],
                  ],
                ),
              ),
            ),
            const SizedBox(height: 16),
            PaymentMethodSelector(
              initial: _selected,
              onSelected: (p) => setState(() => _selected = p),
            ),
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: FilledButton(
                onPressed: _busy ? null : _pay,
                child: _busy
                    ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Text('立即支付'),
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Container(
                padding: const EdgeInsets.all(12),
                color: theme.colorScheme.errorContainer,
                child: Text(_error!),
              ),
            ],
            if (_resp != null) ...[
              const SizedBox(height: 16),
              _CheckoutResult(resp: _resp!),
            ],
          ],
        ),
      ),
    );
  }
}

class _CheckoutResult extends StatelessWidget {
  final CheckoutResponse resp;
  const _CheckoutResult({required this.resp});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    Widget content;
    if (resp.codeURL != null) {
      content = Column(
        children: [
          Text('请用微信扫一扫支付', style: theme.textTheme.bodyMedium),
          const SizedBox(height: 8),
          SelectableText(resp.codeURL!, style: theme.textTheme.bodySmall),
          const SizedBox(height: 8),
          Text(
            '订单号: ${resp.outTradeNo} · 金额 ¥${(resp.amountCents / 100).toStringAsFixed(2)}',
            style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
          ),
        ],
      );
    } else if (resp.h5URL != null) {
      content = Column(
        children: [
          const Text('已打开微信 H5 支付'),
          const SizedBox(height: 8),
          SelectableText(resp.h5URL!, style: theme.textTheme.bodySmall),
        ],
      );
    } else if (resp.redirectURL != null) {
      content = Column(
        children: [
          const Text('已跳转支付页面, 请完成支付后回到客户端'),
          const SizedBox(height: 8),
          SelectableText(resp.redirectURL!, style: theme.textTheme.bodySmall),
        ],
      );
    } else if (resp.prepayID != null) {
      content = Column(
        children: [
          Text('请在微信内完成支付 (prepay_id: ${resp.prepayID})'),
        ],
      );
    } else {
      content = const Text('未知支付响应');
    }
    return Card(
      child: Padding(padding: const EdgeInsets.all(16), child: content),
    );
  }
}
