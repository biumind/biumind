// TokenManager 三态返回值单测。覆盖 BiuMind-Identity-Session-Design §3.5
// 的三个分支:
//   ok        — 正常刷新成功
//   expired   — Identity 返 401/403 → signOut + sessionExpiredCount +1
//   transient — 网络错 / 5xx → 保留 token,**不**踢人
//
// 注入 RefreshFn 假数据,不打真 HTTP。

import 'package:biumind/data/api/identity_client.dart';
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/wiki_providers.dart' show appDbProvider;
import 'package:biumind/features/chat/data/file_bytes_cache.dart'
    show fileBytesCacheProvider;
import 'package:biumind/features/settings/application/settings_controller.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:biumind/services/token_manager.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

/// 用 Provider override 注入假 refresh 函数,获得真实 Ref。
ProviderContainer _makeContainer({
  required InMemorySettingsRepo repo,
  required RefreshFn refreshFn,
}) {
  FlutterSecureStorage.setMockInitialValues({});
  return ProviderContainer(
    overrides: [
      settingsRepoProvider.overrideWithValue(repo),
      appDbProvider.overrideWith((ref) {
        final db = AppDb.memory();
        ref.onDispose(db.close);
        return db;
      }),
      fileBytesCacheProvider.overrideWithValue(null),
      tokenManagerProvider.overrideWith(
        (ref) => TokenManager(ref, refreshFn: refreshFn),
      ),
    ],
  );
}

IdentityAuthResult _fakeAuth({
  String access = 'new-access',
  String refresh = 'new-refresh',
}) => IdentityAuthResult.fromJson({
  'access_token': access,
  'refresh_token': refresh,
  'expires_in_seconds': 3600,
  'user': {'id': 'u1', 'email': 'u@e.com', 'email_verified': true},
});

void main() {
  group('TokenManager three-state outcome', () {
    late InMemorySettingsRepo repo;

    setUp(() async {
      repo = InMemorySettingsRepo();
      await repo.save(
        const AppSettings(
          identityUrl: 'http://localhost:7004',
          accessToken: 'old-access',
          refreshToken: 'old-refresh',
          userEmail: 'u@e.com',
          tokenExpiresAt: '2026-01-01T00:00:00.000Z',
        ),
      );
    });

    test('ok: refresh 成功写回新 token,不影响 sessionExpiredCount', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => _fakeAuth(),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.ok);
      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, 'new-access');
      expect(s.refreshToken, 'new-refresh');
      expect(container.read(sessionExpiredCountProvider), 0);
    });

    test('expired: 401 → signOut + sessionExpiredCount +1', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw const IdentityApiError(
          path: '/v1/auth/refresh',
          status: 401,
          body: '{"error":{"code":"expired_token"}}',
        ),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.expired);
      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, isNull, reason: 'signOut 应清掉 access');
      expect(s.refreshToken, isNull, reason: 'signOut 应清掉 refresh');
      expect(container.read(sessionExpiredCountProvider), 1);
    });

    test('expired: 401 token_reuse → reason=tokenReuse', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw const IdentityApiError(
          path: '/v1/auth/refresh',
          status: 401,
          body: '{"error":{"code":"token_reuse"}}',
        ),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.expired);
      expect(
        container.read(sessionExpiredReasonProvider),
        SessionExpiredReason.tokenReuse,
        reason: 'token_reuse error code 应映射到 tokenReuse reason',
      );
      expect(container.read(sessionExpiredCountProvider), 1);
    });

    test('expired: 401 invalid_token → reason=expired', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw const IdentityApiError(
          path: '/v1/auth/refresh',
          status: 401,
          body: '{"error":{"code":"invalid_token"}}',
        ),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.expired);
      expect(
        container.read(sessionExpiredReasonProvider),
        SessionExpiredReason.expired,
        reason: '其他 401 code 走默认 expired reason',
      );
    });

    test('expired: 403 同 401 一样 → signOut', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw const IdentityApiError(
          path: '/v1/auth/refresh',
          status: 403,
          body: '{"error":{"code":"forbidden"}}',
        ),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.expired);
      expect(container.read(sessionExpiredCountProvider), 1);
    });

    test('transient: 网络错 → 保留 token,不踢人', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw Exception('Connection refused'),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.transient);
      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, 'old-access', reason: 'transient 必须保留旧 token');
      expect(s.refreshToken, 'old-refresh');
      expect(
        container.read(sessionExpiredCountProvider),
        0,
        reason: 'transient 不能 bump sessionExpiredCount',
      );
    });

    test('transient: 5xx → 保留 token', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw const IdentityApiError(
          path: '/v1/auth/refresh',
          status: 503,
          body: 'service unavailable',
        ),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.transient);
      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, 'old-access');
      expect(container.read(sessionExpiredCountProvider), 0);
    });

    test('handle401 ok → 返回新 access_token', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => _fakeAuth(),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final result = await container.read(tokenManagerProvider).handle401();
      expect(result, 'new-access');
    });

    test('handle401 expired → 返回 null,已 signOut', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw const IdentityApiError(
          path: '/v1/auth/refresh',
          status: 401,
          body: '',
        ),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final result = await container.read(tokenManagerProvider).handle401();
      expect(result, isNull);
      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, isNull);
    });

    test('handle401 transient → 返回 null,但 token 保留(关键!)', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw Exception('network down'),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final result = await container.read(tokenManagerProvider).handle401();
      expect(result, isNull);
      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, 'old-access', reason: 'transient 失败不能踢人 — 这是修复的核心');
      expect(container.read(sessionExpiredCountProvider), 0);
    });

    test('connectivity: ok refresh → online', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => _fakeAuth(),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);
      // 先把状态弄成非 online 看 ok 后能否回到 online
      container.read(connectivityStateProvider.notifier).state =
          ConnectivityState.reconnecting;

      await container.read(tokenManagerProvider).refreshNow();

      expect(
        container.read(connectivityStateProvider),
        ConnectivityState.online,
      );
    });

    test('connectivity: transient + access 仍有效 → reconnecting', () async {
      // 用未来的 expiry 让 access token 看起来还在
      final futureRepo = InMemorySettingsRepo();
      await futureRepo.save(
        AppSettings(
          identityUrl: 'http://localhost:7004',
          accessToken: 'old-access',
          refreshToken: 'old-refresh',
          userEmail: 'u@e.com',
          tokenExpiresAt: DateTime.now()
              .toUtc()
              .add(const Duration(hours: 1))
              .toIso8601String(),
        ),
      );
      final container = _makeContainer(
        repo: futureRepo,
        refreshFn: (url, token) async => throw Exception('network down'),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      await container.read(tokenManagerProvider).refreshNow();

      expect(
        container.read(connectivityStateProvider),
        ConnectivityState.reconnecting,
        reason: 'access 还有效, 业务请求仍能用 → reconnecting',
      );
    });

    test('connectivity: transient + access 已过期 → offlineWithCache', () async {
      // tokenExpiresAt 在过去 → 业务请求会 401
      final pastRepo = InMemorySettingsRepo();
      await pastRepo.save(
        AppSettings(
          identityUrl: 'http://localhost:7004',
          accessToken: 'old-access',
          refreshToken: 'old-refresh',
          userEmail: 'u@e.com',
          tokenExpiresAt: DateTime.now()
              .toUtc()
              .subtract(const Duration(minutes: 1))
              .toIso8601String(),
        ),
      );
      final container = _makeContainer(
        repo: pastRepo,
        refreshFn: (url, token) async => throw Exception('network down'),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      await container.read(tokenManagerProvider).refreshNow();

      expect(
        container.read(connectivityStateProvider),
        ConnectivityState.offlineWithCache,
        reason: 'access 已过期, 写操作没法做 → offlineWithCache',
      );
    });

    test('connectivity: transient → ok 后状态回到 online', () async {
      var failing = true;
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async {
          if (failing) {
            throw Exception('network');
          }
          return _fakeAuth();
        },
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      // 先来一次失败
      await container.read(tokenManagerProvider).refreshNow();
      expect(
        container.read(connectivityStateProvider),
        isNot(ConnectivityState.online),
      );

      // 网络恢复, 下次 refresh 成功
      failing = false;
      await container.read(tokenManagerProvider).refreshNow();
      expect(
        container.read(connectivityStateProvider),
        ConnectivityState.online,
      );
    });

    test('inflight 锁: 并发 refreshNow 共享同一 Future', () async {
      var callCount = 0;
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async {
          callCount++;
          await Future<void>.delayed(const Duration(milliseconds: 50));
          return _fakeAuth();
        },
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);

      final tm = container.read(tokenManagerProvider);
      final results = await Future.wait([
        tm.refreshNow(),
        tm.refreshNow(),
        tm.refreshNow(),
      ]);

      expect(results, [
        RefreshOutcome.ok,
        RefreshOutcome.ok,
        RefreshOutcome.ok,
      ]);
      expect(callCount, 1, reason: '三次 refreshNow 应共享同一次实际调用');
    });

    test('compare-and-use: 磁盘 rt 比内存新 → 用磁盘 rt 刷新', () async {
      String? usedToken;
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async {
          usedToken = token;
          return _fakeAuth();
        },
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);
      // 模拟同机另一实例已轮换: 直接写 repo, controller state 仍持旧 rt
      await repo.save(
        const AppSettings(
          identityUrl: 'http://localhost:7004',
          accessToken: 'other-access',
          refreshToken: 'rotated-refresh',
          userEmail: 'u@e.com',
        ),
      );

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.ok);
      expect(
        usedToken,
        'rotated-refresh',
        reason: '磁盘 rt 与内存不同 → 说明别的实例已轮换, 必须用磁盘 rt',
      );
    });

    test('compare-and-clear: expired + 磁盘有不同 rt → 收编不清盘不弹窗', () async {
      final container = _makeContainer(
        repo: repo,
        refreshFn: (url, token) async => throw const IdentityApiError(
          path: '/v1/auth/refresh',
          status: 401,
          body: '{"error":{"code":"expired_token"}}',
        ),
      );
      addTearDown(container.dispose);
      await container.read(settingsControllerProvider.future);
      // 另一实例写入新凭证(本实例内存 rt 是旧的, 所以 refresh 401)
      await repo.save(
        const AppSettings(
          identityUrl: 'http://localhost:7004',
          accessToken: 'other-access',
          refreshToken: 'rotated-refresh',
          userEmail: 'u@e.com',
        ),
      );

      final outcome = await container.read(tokenManagerProvider).refreshNow();

      expect(outcome, RefreshOutcome.expired);
      final s = await container.read(settingsControllerProvider.future);
      expect(s.refreshToken, 'rotated-refresh', reason: '磁盘已有新凭证 → 收编磁盘值, 不清盘');
      expect(
        (await repo.load()).refreshToken,
        'rotated-refresh',
        reason: '磁盘上的新凭证不能被抹掉',
      );
      expect(
        container.read(sessionExpiredCountProvider),
        0,
        reason: '收编路径不 bump 计数器 — 会话其实还活着, 不弹窗',
      );
    });
  });
}
