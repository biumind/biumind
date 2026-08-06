// Membership Riverpod providers — Phase 4 W2-10 + W5-10.
//
// 读路径:
//   membershipClientProvider  — MembershipClient 实例 (settings + auth 联动)
//   plansListProvider         — FutureProvider<List<Plan>>
//   mySubscriptionProvider    — FutureProvider<Subscription?>
//   ordersListProvider        — FutureProvider<List<Order>>
//
// 写路径 (W5-10): membershipActionsProvider —
//   cancel / changePlan / resume / checkout, 触发后刷新 mySubscriptionProvider
//   + ordersListProvider.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/cache_for.dart';
import '../../../services/auth_service.dart';
import '../../settings/application/settings_controller.dart';
import '../data/coupons_client.dart';
import '../data/membership_client.dart';
import '../data/referrals_client.dart';
import '../domain/checkout.dart';
import '../domain/order.dart';
import '../domain/plan.dart';
import '../domain/referral.dart';
import '../domain/subscription.dart';

final membershipClientProvider = Provider<MembershipClient?>((ref) {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  final creds = ref.watch(hubCredentialsProvider);
  final identityUri = settings?.identityUri;
  if (identityUri == null) return null;
  return MembershipClient(
    identityBase: identityUri,
    // /v1/plans 公开访问无 token; /v1/subscriptions/me 必须 token
    getToken: () => creds?.bearerToken,
  );
});

/// /v1/plans 公开 — 任何时候都能拉, 已订阅时高亮.
/// select(endpoint): token 轮换不重拉; cacheFor: 套餐低频变动。
final plansListProvider =
    FutureProvider.autoDispose<List<Plan>>((ref) async {
  ref.watch(membershipClientProvider.select((c) => c?.identityBase));
  final client = ref.read(membershipClientProvider);
  if (client == null) return const [];
  ref.cacheFor(const Duration(minutes: 5));
  return client.listPlans();
});

/// /v1/subscriptions/me — 必须登录. 未登录 / 客户端无 token 返虚拟空订阅.
final mySubscriptionProvider =
    FutureProvider.autoDispose<Subscription?>((ref) async {
  ref.watch(membershipClientProvider.select((c) => c?.identityBase));
  final client = ref.read(membershipClientProvider);
  if (client == null) return null;
  return client.mySubscription();
});

/// /v1/subscriptions/orders — 订单历史 (W5-10).
final ordersListProvider = FutureProvider.autoDispose<List<Order>>((ref) async {
  ref.watch(membershipClientProvider.select((c) => c?.identityBase));
  final client = ref.read(membershipClientProvider);
  if (client == null) return const [];
  return client.listOrders();
});

/// MembershipActions — W5-10 写路径, refresh 自动重拉 sub + orders.
class MembershipActions {
  final MembershipClient client;
  final void Function() onChanged;

  const MembershipActions({required this.client, required this.onChanged});

  Future<CheckoutResponse> checkout(CheckoutRequest req) async {
    final r = await client.checkout(req);
    onChanged();
    return r;
  }

  Future<void> cancel({required bool immediate}) async {
    await client.cancel(immediate: immediate);
    onChanged();
  }

  Future<ChangePlanResponse> changePlan(String planCode) async {
    final r = await client.changePlan(planCode);
    onChanged();
    return r;
  }

  Future<void> resume() async {
    await client.resume();
    onChanged();
  }
}

// ─── W6-13 coupons + referrals ────────────────────

final couponsClientProvider = Provider<CouponsClient?>((ref) {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  final creds = ref.watch(hubCredentialsProvider);
  final identityUri = settings?.identityUri;
  if (identityUri == null) return null;
  return CouponsClient(
    identityBase: identityUri,
    getToken: () => creds?.bearerToken,
  );
});

final referralsClientProvider = Provider<ReferralsClient?>((ref) {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  final creds = ref.watch(hubCredentialsProvider);
  final identityUri = settings?.identityUri;
  if (identityUri == null) return null;
  return ReferralsClient(
    identityBase: identityUri,
    getToken: () => creds?.bearerToken,
  );
});

/// referralStatsProvider — POST /v1/referrals/invite (返自己邀请码 + stats).
final referralStatsProvider =
    FutureProvider.autoDispose<ReferralStats>((ref) async {
  ref.watch(referralsClientProvider.select((c) => c?.identityBase));
  final client = ref.read(referralsClientProvider);
  if (client == null) return ReferralStats.empty;
  return client.invite();
});

final membershipActionsProvider = Provider<MembershipActions?>((ref) {
  final client = ref.watch(membershipClientProvider);
  if (client == null) return null;
  return MembershipActions(
    client: client,
    onChanged: () {
      ref.invalidate(mySubscriptionProvider);
      ref.invalidate(ordersListProvider);
      ref.invalidate(plansListProvider);
    },
  );
});
