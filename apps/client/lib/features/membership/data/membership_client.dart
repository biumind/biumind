// MembershipClient — Phase 4 W2-10 + W5-10.
//
// 读路径:
//   GET  /v1/plans              — 4 档套餐 (公开)
//   GET  /v1/subscriptions/me   — 当前用户订阅
//   GET  /v1/subscriptions/orders — 订单历史 (W5-10)
//
// 写路径 (W5-4/5/6/7):
//   POST /v1/subscriptions/checkout      — 启动支付
//   POST /v1/subscriptions/cancel        — 取消 (period_end / immediate)
//   POST /v1/subscriptions/change_plan   — 升降级
//   POST /v1/subscriptions/resume        — 撤销取消
//
// 契约对齐 services/identity/internal/api/{plans,subscriptions}.go.

import '../../../data/api/_http_helpers.dart';
import '../domain/checkout.dart';
import '../domain/order.dart';
import '../domain/plan.dart';
import '../domain/subscription.dart';

class MembershipClient {
  final Uri identityBase;
  final String? Function() getToken;

  MembershipClient({required this.identityBase, required this.getToken});

  // ─── reads ──────────────────────────────────────

  Future<List<Plan>> listPlans() async {
    final j = await apiRequest(
      method: 'GET',
      url: identityBase.resolve('/v1/plans'),
      bearerToken: getToken(),
    );
    final raw = (j['plans'] as List?) ?? const [];
    return raw
        .map((e) => Plan.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList(growable: false);
  }

  Future<Subscription> mySubscription() async {
    final j = await apiRequest(
      method: 'GET',
      url: identityBase.resolve('/v1/subscriptions/me'),
      bearerToken: getToken(),
    );
    return Subscription.fromJson(j);
  }

  Future<List<Order>> listOrders() async {
    final j = await apiRequest(
      method: 'GET',
      url: identityBase.resolve('/v1/subscriptions/orders'),
      bearerToken: getToken(),
    );
    final raw = (j['orders'] as List?) ?? const [];
    return raw
        .map((e) => Order.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList(growable: false);
  }

  // ─── writes ─────────────────────────────────────

  Future<CheckoutResponse> checkout(CheckoutRequest req) async {
    final j = await apiRequest(
      method: 'POST',
      url: identityBase.resolve('/v1/subscriptions/checkout'),
      bearerToken: getToken(),
      body: req.toJson(),
    );
    return CheckoutResponse.fromJson(j);
  }

  /// W7 单次充值 — 走 /v1/credits/checkout, 真支付路径.
  Future<CheckoutResponse> topupCheckout({
    required String optionID,
    required PaymentProvider provider,
    String? openID,
    String? clientIP,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: identityBase.resolve('/v1/credits/checkout'),
      bearerToken: getToken(),
      body: {
        'option_id': optionID,
        'provider': provider.wireValue,
        if (openID != null && openID.isNotEmpty) 'openid': openID,
        if (clientIP != null && clientIP.isNotEmpty) 'client_ip': clientIP,
      },
    );
    return CheckoutResponse.fromJson(j);
  }

  Future<void> cancel({bool immediate = false}) async {
    final url = immediate
        ? identityBase.resolve('/v1/subscriptions/cancel?immediate=true')
        : identityBase.resolve('/v1/subscriptions/cancel');
    await apiRequest(
      method: 'POST',
      url: url,
      bearerToken: getToken(),
      body: {'immediate': immediate},
    );
  }

  Future<ChangePlanResponse> changePlan(String planCode) async {
    final j = await apiRequest(
      method: 'POST',
      url: identityBase.resolve('/v1/subscriptions/change_plan'),
      bearerToken: getToken(),
      body: {'plan_code': planCode},
    );
    return ChangePlanResponse.fromJson(j);
  }

  Future<void> resume() async {
    await apiRequest(
      method: 'POST',
      url: identityBase.resolve('/v1/subscriptions/resume'),
      bearerToken: getToken(),
      body: {},
    );
  }
}
