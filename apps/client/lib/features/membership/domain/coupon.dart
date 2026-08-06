// CouponRedeemResult — W6-13 客户端兑换响应模型.
// 与 services/identity/internal/api/coupons_referrals.go redeemResp 对齐.

class CouponRedeemResult {
  final String redemptionID;
  final String couponCode;
  final String kind; // amount_off / percent_off / credits_grant / trial_extend
  final int discountCents;
  final int creditsGranted;
  final int trialExtraDays;

  const CouponRedeemResult({
    required this.redemptionID,
    required this.couponCode,
    required this.kind,
    this.discountCents = 0,
    this.creditsGranted = 0,
    this.trialExtraDays = 0,
  });

  factory CouponRedeemResult.fromJson(Map<String, dynamic> j) =>
      CouponRedeemResult(
        redemptionID: (j['redemption_id'] ?? '') as String,
        couponCode: (j['coupon_code'] ?? '') as String,
        kind: (j['kind'] ?? '') as String,
        discountCents: ((j['discount_cents'] ?? 0) as num).toInt(),
        creditsGranted: ((j['credits_granted'] ?? 0) as num).toInt(),
        trialExtraDays: ((j['trial_extra_days'] ?? 0) as num).toInt(),
      );

  /// UI 友好的简短描述, 跟服务端 kind 对应.
  String summary() {
    switch (kind) {
      case 'amount_off':
        return '立减 ¥${(discountCents / 100).toStringAsFixed(2)}';
      case 'percent_off':
        return '已减 ¥${(discountCents / 100).toStringAsFixed(2)}';
      case 'credits_grant':
        return '已发放 $creditsGranted 积分 (30 天有效)';
      case 'trial_extend':
        return '试用期延长 $trialExtraDays 天';
      default:
        return '已兑换';
    }
  }
}
