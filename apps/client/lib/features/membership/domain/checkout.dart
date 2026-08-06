// Checkout — W5-4 checkout 请求 / 响应模型.

enum PaymentProvider {
  stripe,
  wechatNative,
  wechatJsapi,
  wechatH5,
  alipayPC,
  alipayWap;

  String get wireValue {
    switch (this) {
      case PaymentProvider.stripe:
        return 'stripe';
      case PaymentProvider.wechatNative:
        return 'wechat_native';
      case PaymentProvider.wechatJsapi:
        return 'wechat_jsapi';
      case PaymentProvider.wechatH5:
        return 'wechat_h5';
      case PaymentProvider.alipayPC:
        return 'alipay_pc';
      case PaymentProvider.alipayWap:
        return 'alipay_wap';
    }
  }

  /// UI 简标签.
  String get displayLabel {
    switch (this) {
      case PaymentProvider.stripe:
        return 'Stripe (国际信用卡)';
      case PaymentProvider.wechatNative:
      case PaymentProvider.wechatJsapi:
      case PaymentProvider.wechatH5:
        return '微信支付';
      case PaymentProvider.alipayPC:
      case PaymentProvider.alipayWap:
        return '支付宝';
    }
  }
}

class CheckoutRequest {
  final String planCode;
  final String billingCycle; // monthly / yearly
  final PaymentProvider provider;
  final bool trial;
  final String? deviceFP;
  final String? openID; // wechat_jsapi 必填
  final String? clientIP; // wechat_h5 必填

  const CheckoutRequest({
    required this.planCode,
    required this.provider,
    this.billingCycle = 'monthly',
    this.trial = false,
    this.deviceFP,
    this.openID,
    this.clientIP,
  });

  Map<String, dynamic> toJson() => {
        'plan_code': planCode,
        'billing_cycle': billingCycle,
        'provider': provider.wireValue,
        if (trial) 'trial': true,
        if (deviceFP != null && deviceFP!.isNotEmpty) 'device_fp': deviceFP,
        if (openID != null && openID!.isNotEmpty) 'openid': openID,
        if (clientIP != null && clientIP!.isNotEmpty) 'client_ip': clientIP,
      };
}

class CheckoutResponse {
  final String provider;
  final String outTradeNo;
  final int amountCents;
  final String currency;
  final String? codeURL; // wechat_native (扫码二维码内容)
  final String? prepayID; // wechat_jsapi (前端再调起)
  final String? h5URL; // wechat_h5 (跳转地址)
  final String? redirectURL; // alipay / stripe (浏览器跳)

  const CheckoutResponse({
    required this.provider,
    required this.outTradeNo,
    required this.amountCents,
    required this.currency,
    this.codeURL,
    this.prepayID,
    this.h5URL,
    this.redirectURL,
  });

  factory CheckoutResponse.fromJson(Map<String, dynamic> j) => CheckoutResponse(
        provider: (j['provider'] ?? '') as String,
        outTradeNo: (j['out_trade_no'] ?? '') as String,
        amountCents: ((j['amount_cents'] ?? 0) as num).toInt(),
        currency: (j['currency'] ?? 'CNY') as String,
        codeURL: j['code_url'] as String?,
        prepayID: j['prepay_id'] as String?,
        h5URL: j['h5_url'] as String?,
        redirectURL: j['redirect_url'] as String?,
      );
}

class ChangePlanResponse {
  final String oldPlan;
  final String newPlan;
  final String effective; // immediate / period_end
  final ProrationView? proration;
  final DateTime? scheduledAt;

  const ChangePlanResponse({
    required this.oldPlan,
    required this.newPlan,
    required this.effective,
    this.proration,
    this.scheduledAt,
  });

  factory ChangePlanResponse.fromJson(Map<String, dynamic> j) => ChangePlanResponse(
        oldPlan: (j['old_plan'] ?? '') as String,
        newPlan: (j['new_plan'] ?? '') as String,
        effective: (j['effective'] ?? '') as String,
        proration: j['proration'] is Map<String, dynamic>
            ? ProrationView.fromJson(j['proration'] as Map<String, dynamic>)
            : null,
        scheduledAt: DateTime.tryParse((j['scheduled_at'] ?? '') as String),
      );

  bool get isImmediate => effective == 'immediate';
}

class ProrationView {
  final int unusedRefundCents;
  final int newProrateChargeCents;
  final int netChargeCents;
  final double remainingRatio;

  const ProrationView({
    required this.unusedRefundCents,
    required this.newProrateChargeCents,
    required this.netChargeCents,
    required this.remainingRatio,
  });

  factory ProrationView.fromJson(Map<String, dynamic> j) => ProrationView(
        unusedRefundCents: ((j['unused_refund_cents'] ?? 0) as num).toInt(),
        newProrateChargeCents:
            ((j['new_prorate_charge_cents'] ?? 0) as num).toInt(),
        netChargeCents: ((j['net_charge_cents'] ?? 0) as num).toInt(),
        remainingRatio: ((j['remaining_ratio'] ?? 0) as num).toDouble(),
      );
}
