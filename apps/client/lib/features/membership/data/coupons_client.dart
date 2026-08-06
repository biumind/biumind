// CouponsClient — W6-13 优惠券兑换.
// POST /v1/coupons/redeem.

import '../../../data/api/_http_helpers.dart';
import '../domain/coupon.dart';

class CouponsClient {
  final Uri identityBase;
  final String? Function() getToken;

  CouponsClient({required this.identityBase, required this.getToken});

  Future<CouponRedeemResult> redeem({
    required String code,
    String? planCode,
    int? amountCents,
    String? currency,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: identityBase.resolve('/v1/coupons/redeem'),
      bearerToken: getToken(),
      body: {
        'code': code,
        'plan_code': ?planCode,
        'amount_cents': ?amountCents,
        'currency': ?currency,
      },
    );
    return CouponRedeemResult.fromJson(j);
  }
}
