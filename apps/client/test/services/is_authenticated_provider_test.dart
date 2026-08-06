// is_authenticated_provider_test — 锁 L0+L1 防回归: token 轮换不得触发
// isAuthenticatedProvider 重 emit (router 的 refreshListenable 桥听它)。
//
// 背景 (commit 7826bbf1): resume → token refresh → hubCredentialsProvider
// 重 emit 新对象 (resolve() 每次返新实例 + 无 ==) → _RouterListenable 若听
// 原值会让 GoRouter refresh → 整路由栈 rebuild → 所有页面闪。修法是 router
// 改听 isAuthenticatedProvider (bool), 只在登录↔登出翻转。本测试锁死这个
// 派生关系不被未来改回 raw listen。
//
// 不 pump GoRouter (集成成本高, 见 router_public_route_test 顶部说明) ——
// 只在 ProviderContainer 层验证派生行为。

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/services/auth_service.dart';

/// 共用: 建一个 container, hubCredentialsProvider 覆写为读 [initial] 的
/// StateProvider, 返回 (container, 切换 creds 的回调)。
({ProviderContainer container, void Function(HubCredentials?) set}) _harness(
  HubCredentials? initial,
) {
  final creds = StateProvider<HubCredentials?>((_) => initial);
  final container = ProviderContainer(
    overrides: [hubCredentialsProvider.overrideWith((ref) => ref.watch(creds))],
  );
  return (
    container: container,
    set: (next) => container.read(creds.notifier).state = next,
  );
}

void main() {
  group('isAuthenticatedProvider (L0+L1 防回归)', () {
    test('token 轮换 (同 endpoint, 新 token) 不触发 isAuthenticated 重 emit', () {
      final h = _harness(HubCredentials(
        endpoint: Uri.parse('https://relay.example.com'),
        bearerToken: 'token-A',
      ));
      addTearDown(h.container.dispose);

      var authNotifications = 0;
      h.container.listen<bool>(
        isAuthenticatedProvider,
        (_, _) => authNotifications++,
      );
      expect(h.container.read(isAuthenticatedProvider), isTrue);

      // 轮换: 同 endpoint, 新 token。
      h.set(HubCredentials(
        endpoint: Uri.parse('https://relay.example.com'),
        bearerToken: 'token-B',
      ));
      // read 触发链路 flush (Riverpod 通知在依赖被读取时落定)。
      expect(
        h.container.read(hubCredentialsProvider)?.bearerToken,
        'token-B',
        reason: '前置: hubCredentialsProvider 确实换成了新 token',
      );

      // 关键断言: bool 没翻转 → 不通知。若失败 = 有人把 router 改回听
      // hubCredentialsProvider 原值, 所有页面会重新每小时闪。
      expect(
        authNotifications,
        0,
        reason: 'token 轮换不应触发 isAuthenticatedProvider 通知',
      );
      expect(h.container.read(isAuthenticatedProvider), isTrue);
    });

    test('登出 (creds → null) 翻转 bool 并通知一次', () {
      final h = _harness(HubCredentials(
        endpoint: Uri.parse('https://relay.example.com'),
        bearerToken: 'token-A',
      ));
      addTearDown(h.container.dispose);

      var authNotifications = 0;
      h.container.listen<bool>(
        isAuthenticatedProvider,
        (_, _) => authNotifications++,
      );
      expect(h.container.read(isAuthenticatedProvider), isTrue);

      h.set(null);
      // read flush 后通知落定。
      expect(h.container.read(isAuthenticatedProvider), isFalse);
      expect(authNotifications, 1);
    });

    test('登录 (null → creds) 翻转 bool 并通知一次', () {
      final h = _harness(null);
      addTearDown(h.container.dispose);

      var authNotifications = 0;
      h.container.listen<bool>(
        isAuthenticatedProvider,
        (_, _) => authNotifications++,
      );
      expect(h.container.read(isAuthenticatedProvider), isFalse);

      h.set(HubCredentials(
        endpoint: Uri.parse('https://relay.example.com'),
        bearerToken: 'token-A',
      ));
      expect(h.container.read(isAuthenticatedProvider), isTrue);
      expect(authNotifications, 1);
    });
  });
}
