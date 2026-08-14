// biuDaemonWatchKeyProvider 单测 — P2 多账号 Step 2。
//
// daemon 重建 key = endpoint + JWT sub: 同人 token 轮换 key 不变 (进程不
// 重启, tokenPusher 热更即可), 换账号 / 换环境 key 变 (SIGTERM + respawn)。
// 只测 key 的派生逻辑, 不 spawn 真 daemon (biuDaemonManagerProvider 的
// start 副作用不在此覆盖)。

import 'dart:convert';

import 'package:biumind/data/agent_plane/biu_daemon_manager.dart';
import 'package:biumind/features/settings/application/settings_controller.dart';
import 'package:biumind/services/account_registry.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// 造一个带 sub 的假 JWT (只解 payload, 不验签)。
String _fakeJwt(String sub) {
  String b64(Map<String, dynamic> o) =>
      base64Url.encode(utf8.encode(jsonEncode(o))).replaceAll('=', '');
  return '${b64({'alg': 'HS256', 'typ': 'JWT'})}.${b64({'sub': sub})}.sig';
}

void main() {
  late InMemorySettingsRepo repo;
  late ProviderContainer container;

  setUp(() {
    repo = InMemorySettingsRepo();
    container = ProviderContainer(overrides: [
      settingsRepoProvider.overrideWithValue(repo),
      accountRegistryStoreProvider
          .overrideWithValue(InMemoryAccountRegistryStore()),
    ]);
  });

  tearDown(() => container.dispose());

  Future<SettingsController> signIn(String url, String sub) async {
    await repo.save(AppSettings(
      identityUrl: url,
      accessToken: _fakeJwt(sub),
      refreshToken: 'rt-$sub',
    ));
    final ctl = container.read(settingsControllerProvider.notifier);
    await container.read(settingsControllerProvider.future);
    return ctl;
  }

  test('未登录 → key 为 null (daemon provider 返回 null)', () async {
    await container.read(settingsControllerProvider.future);
    expect(container.read(biuDaemonWatchKeyProvider), isNull);
  });

  test('key = endpoint|userId; 同人 token 轮换不变, 换人/换环境变', () async {
    final ctl = await signIn('http://x:8088', 'user-a');

    final keyA = container.read(biuDaemonWatchKeyProvider);
    expect(keyA, 'http://x:8088|user-a');

    // 同人 token 轮换 (同 sub 新 JWT + 新 rt) → key 不变, daemon 不重启。
    await ctl.applyRefreshed(
      accessToken: _fakeJwt('user-a'),
      refreshToken: 'rt-a-rotated',
      tokenExpiresAt:
          DateTime.now().toUtc().add(const Duration(hours: 1)).toIso8601String(),
    );
    expect(
      container.read(biuDaemonWatchKeyProvider),
      keyA,
      reason: 'token 轮换 key 必须稳定, 否则 daemon 重启死循环',
    );

    // 换账号 (不同 sub) → key 变 → daemon SIGTERM + respawn。
    await ctl.applyRefreshed(
      accessToken: _fakeJwt('user-b'),
      refreshToken: 'rt-b',
      tokenExpiresAt:
          DateTime.now().toUtc().add(const Duration(hours: 1)).toIso8601String(),
    );
    expect(container.read(biuDaemonWatchKeyProvider), 'http://x:8088|user-b');

    // 换环境 (同 sub 不同 endpoint) → key 也变。
    await ctl.updateIdentityUrl('http://y:8088');
    expect(container.read(biuDaemonWatchKeyProvider), 'http://y:8088|user-b');
  });
}
