// Credits providers — 用户余额 + 充值套餐.
//
// creditsClientProvider: 与 hubCredentialsProvider 同生命周期, 未登录返 null.
// creditsBalanceProvider: 5min stale, watch 它的 widget 自动 rebuild 显示新余额.
// 任务提交 / 充值 后调 ref.invalidate(creditsBalanceProvider) 强制刷新.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/cache_for.dart';
import '../../../services/auth_service.dart';
import '../../settings/application/settings_controller.dart';
import '../data/credits_client.dart';

final creditsClientProvider = Provider<CreditsClient?>((ref) {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  final creds = ref.watch(hubCredentialsProvider);
  if (settings == null || creds == null) return null;
  final identityUri = settings.identityUri;
  if (identityUri == null) return null;
  return CreditsClient(
    identityBaseUrl: identityUri,
    bearerProvider: () => creds.bearerToken,
  );
});

/// 实时余额. autoDispose 避免离开 creation 模块后 leak; 5min 自动失效让长时间
/// 不刷新的页面在切回时显示最新数字.
final creditsBalanceProvider =
    FutureProvider.autoDispose<CreditsBalance>((ref) async {
  // select(identityBaseUrl): token 轮换不重拉 (余额/套餐不闪).
  ref.watch(creditsClientProvider.select((c) => c?.identityBaseUrl));
  final client = ref.read(creditsClientProvider);
  if (client == null) return CreditsBalance.empty();
  // 5min cacheFor — 用户切页面再切回不会立刻打 API, 但失效后会刷新.
  ref.cacheFor(const Duration(minutes: 5));
  return client.fetchBalance();
});

/// 充值套餐列表 (公开端点; 不需登录也能预加载).
final rechargeOptionsProvider =
    FutureProvider.autoDispose<List<RechargeOption>>((ref) async {
  // select(identityBaseUrl): token 轮换不重拉 (余额/套餐不闪).
  ref.watch(creditsClientProvider.select((c) => c?.identityBaseUrl));
  final client = ref.read(creditsClientProvider);
  if (client == null) return const [];
  return client.fetchRechargeOptions();
});
