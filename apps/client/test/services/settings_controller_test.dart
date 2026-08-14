import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/wiki_providers.dart' show appDbProvider;
import 'package:biumind/features/chat/data/chat_scope.dart'
    show accountIdFromEndpoint;
import 'package:biumind/features/chat/data/file_bytes_cache.dart'
    show fileBytesCacheProvider;
import 'package:biumind/features/notes/application/notes_ui_providers.dart'
    show selectedNoteIdProvider;
import 'package:biumind/features/settings/application/settings_controller.dart';
import 'package:biumind/services/account_registry.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:biumind/services/token_manager.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 造一个带 sub 的假 JWT (只解 payload, 不验签)。
String _fakeJwt(String sub) {
  String b64(Map<String, dynamic> o) =>
      base64Url.encode(utf8.encode(jsonEncode(o))).replaceAll('=', '');
  return '${b64({'alg': 'HS256', 'typ': 'JWT'})}.${b64({'sub': sub})}.sig';
}

/// 起本地假 Identity 服务: /v1/auth/login + /v1/auth/verify-email 返
/// 指定 sub 的 token 包, /v1/auth/logout 返 200。返回 (server, url)。
Future<({HttpServer server, String url})> _startFakeIdentity({
  required String sub,
}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  server.listen((req) async {
    final path = req.uri.path;
    if (path == '/v1/auth/login' || path == '/v1/auth/verify-email') {
      req.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'access_token': _fakeJwt(sub),
            'refresh_token': 'rt-$sub',
            'expires_in_seconds': 3600,
            'session_id': 'sess-$sub',
            'user': {'id': sub, 'email': '$sub@e.com', 'email_verified': true},
          }),
        );
    } else if (path == '/v1/auth/logout') {
      req.response.statusCode = 200;
    } else {
      req.response.statusCode = 404;
    }
    await req.response.close();
  });
  addTearDown(() => server.close(force: true));
  return (server: server, url: 'http://localhost:${server.port}');
}

void main() {
  late InMemorySettingsRepo repo;
  late InMemoryAccountRegistryStore accountsStore;
  late AppDb db;
  late ProviderContainer container;

  setUp(() {
    repo = InMemorySettingsRepo();
    accountsStore = InMemoryAccountRegistryStore();
    // signOut 调 purgeUserData (清本地数据), 需 appDb (内存) + secure storage
    // (mock) 就绪; fileBytesCache override null 跳过 creds 链.
    db = AppDb.memory();
    FlutterSecureStorage.setMockInitialValues({});
    container = ProviderContainer(
      overrides: [
        settingsRepoProvider.overrideWithValue(repo),
        accountRegistryStoreProvider.overrideWithValue(accountsStore),
        appDbProvider.overrideWithValue(db),
        fileBytesCacheProvider.overrideWithValue(null),
      ],
    );
  });

  tearDown(() async {
    container.dispose();
    await db.close();
  });

  test('initial load reads repo', () async {
    await repo.save(
      const AppSettings(identityUrl: 'http://x'),
    );
    final s = await container.read(settingsControllerProvider.future);
    expect(s.identityUrl, 'http://x');
  });


  test('updateChatModel persists', () async {
    await container.read(settingsControllerProvider.future);
    final ctl = container.read(settingsControllerProvider.notifier);
    await ctl.updateChatModel('claude-haiku-4-5-20251001');
    final s = await container.read(settingsControllerProvider.future);
    expect(s.defaultChatModel, 'claude-haiku-4-5-20251001');
  });

  test('updateSearchIncludeNotes persists（默认关，可开可关）', () async {
    final initial = await container.read(settingsControllerProvider.future);
    expect(initial.searchIncludeNotes, isFalse, reason: '默认关');

    final ctl = container.read(settingsControllerProvider.notifier);
    await ctl.updateSearchIncludeNotes(true);
    var s = await container.read(settingsControllerProvider.future);
    expect(s.searchIncludeNotes, isTrue);
    expect((await repo.load()).searchIncludeNotes, isTrue,
        reason: '落盘持久化');

    await ctl.updateSearchIncludeNotes(false);
    s = await container.read(settingsControllerProvider.future);
    expect(s.searchIncludeNotes, isFalse);
  });

  test('updateIdentityUrl persists', () async {
    await container.read(settingsControllerProvider.future);
    final ctl = container.read(settingsControllerProvider.notifier);
    await ctl.updateIdentityUrl('http://my-server:7004');
    final s = await container.read(settingsControllerProvider.future);
    expect(s.identityUrl, 'http://my-server:7004');
    // 单 origin: hubUri 等于 identityUrl, 不换端口。
    expect(s.hubUri.toString(), 'http://my-server:7004');
  });

  test('updateCodingPaths 不丢字段 (accessTtl/sessionId/installationId 等)', () async {
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't',
        refreshToken: 'r',
        tokenExpiresAt: '2026-08-10T13:00:00Z',
        accessTtlSeconds: 3600,
        refreshTokenExpiresAt: '2026-09-09T13:00:00Z',
        sessionId: 'sess-1',
        userEmail: 'u@e.com',
        installationId: 'inst-1',
        codeWorkingDir: '/tmp/work',
      ),
    );
    await container.read(settingsControllerProvider.future);
    final ctl = container.read(settingsControllerProvider.notifier);

    await ctl.updateCodingPaths(biuPath: 'custom-biu');
    var s = await container.read(settingsControllerProvider.future);
    expect(s.codeBiuPath, 'custom-biu');
    expect(s.codeWorkingDir, '/tmp/work', reason: 'null 参数不动旧值');
    // 回归: 这四个字段曾被手敲字段列表漏掉而抹掉。
    expect(s.accessTtlSeconds, 3600);
    expect(s.refreshTokenExpiresAt, '2026-09-09T13:00:00Z');
    expect(s.sessionId, 'sess-1');
    expect(s.installationId, 'inst-1');
    expect(s.accessToken, 't');
    expect(s.refreshToken, 'r');

    // 空串 = 清空到默认 (null)。
    await ctl.updateCodingPaths(workingDir: '   ');
    s = await container.read(settingsControllerProvider.future);
    expect(s.codeWorkingDir, isNull, reason: '空串清成语义');
    expect(s.sessionId, 'sess-1', reason: '清空路径也不波及其他字段');
  });

  test('signOut clears tokens but keeps identity URL', () async {
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't',
        refreshToken: 'r',
        userEmail: 'u@e.com',
      ),
    );
    container.invalidate(settingsControllerProvider);
    await container.read(settingsControllerProvider.future);
    await container.read(settingsControllerProvider.notifier).signOut();
    final s = await container.read(settingsControllerProvider.future);
    expect(s.identityUrl, 'http://x');
    expect(s.userEmail, 'u@e.com');
    expect(s.accessToken, isNull);
    expect(s.refreshToken, isNull);
  });

  test('pingHub fails when no URL', () async {
    await container.read(settingsControllerProvider.future);
    expect(
      () => container.read(settingsControllerProvider.notifier).pingHub(),
      throwsA(isA<HubPingError>()),
    );
  });

  test('signOut compareAndClear: 磁盘有不同 rt → 收编磁盘值, 不清盘', () async {
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't',
        refreshToken: 'r-old',
      ),
    );
    await container.read(settingsControllerProvider.future);
    // 模拟同机另一实例写入新凭证
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't2',
        refreshToken: 'r-new',
        userEmail: 'u@e.com',
      ),
    );

    final cleared = await container
        .read(settingsControllerProvider.notifier)
        .signOut(compareAndClear: true);

    expect(cleared, isFalse, reason: '收编路径不算清盘');
    final s = await container.read(settingsControllerProvider.future);
    expect(s.refreshToken, 'r-new', reason: 'state 应收编成磁盘最新值');
    expect((await repo.load()).refreshToken, 'r-new', reason: '磁盘上的新凭证不能被抹掉');
  });

  test('signOut compareAndClear: 磁盘同 rt → 走原清盘路径', () async {
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't',
        refreshToken: 'r',
        userEmail: 'u@e.com',
      ),
    );
    await container.read(settingsControllerProvider.future);

    final cleared = await container
        .read(settingsControllerProvider.notifier)
        .signOut(compareAndClear: true);

    expect(cleared, isTrue);
    final s = await container.read(settingsControllerProvider.future);
    expect(s.refreshToken, isNull);
    expect(s.accessToken, isNull);
    expect(s.identityUrl, 'http://x', reason: '清 token 但保留 identity URL');
  });

  test('ensureInstallationId: settings 丢失时从 SharedPreferences 恢复', () async {
    SharedPreferences.setMockInitialValues({
      'biumind.installation_id': 'inst-from-prefs',
    });
    await container.read(settingsControllerProvider.future);

    await container
        .read(settingsControllerProvider.notifier)
        .ensureInstallationId('generated-new');

    final s = await container.read(settingsControllerProvider.future);
    expect(
      s.installationId,
      'inst-from-prefs',
      reason: 'settings 空 → 从 prefs 找回同一个 id, 不生成新 family',
    );
    expect(
      (await repo.load()).installationId,
      'inst-from-prefs',
      reason: '找回的 id 应 _save 回 settings',
    );
  });

  test('ensureInstallationId: 全新 → 用生成值并双写 prefs', () async {
    SharedPreferences.setMockInitialValues({});
    await container.read(settingsControllerProvider.future);

    await container
        .read(settingsControllerProvider.notifier)
        .ensureInstallationId('generated-1');

    final s = await container.read(settingsControllerProvider.future);
    expect(s.installationId, 'generated-1');
    final prefs = await SharedPreferences.getInstance();
    expect(
      prefs.getString('biumind.installation_id'),
      'generated-1',
      reason: '永远写回 prefs 做兜底',
    );
  });

  test('ensureInstallationId: settings 已有 → 不变, 但同步回 prefs', () async {
    SharedPreferences.setMockInitialValues({});
    await repo.save(const AppSettings(installationId: 'inst-existing'));
    container.invalidate(settingsControllerProvider);
    await container.read(settingsControllerProvider.future);

    await container
        .read(settingsControllerProvider.notifier)
        .ensureInstallationId('generated-2');

    final s = await container.read(settingsControllerProvider.future);
    expect(s.installationId, 'inst-existing');
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('biumind.installation_id'), 'inst-existing');
  });

  group('P2 多账号 Step 1: account registry', () {
    test('login 后 registry upsert; 同账号重复 login 不产生重复记录', () async {
      final id = await _startFakeIdentity(sub: 'user-1');
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);

      await ctl.login(
        identityUrl: id.url,
        email: 'user-1@e.com',
        password: 'pw',
      );

      var accounts = await accountsStore.load();
      expect(accounts.length, 1);
      final rec = accounts.single;
      expect(rec.userId, 'user-1');
      expect(rec.email, 'user-1@e.com');
      expect(rec.refreshToken, 'rt-user-1');
      expect(rec.identityUrl, id.url);
      expect(
        rec.accountId,
        accountIdFromEndpoint(Uri.parse(id.url), _fakeJwt('user-1')),
        reason: 'accountId 必须与 Drift ownerKey 同构',
      );
      expect(rec.lastActiveAt, isNotEmpty);

      // 同账号再登一次 (同 endpoint + 同 sub) → 替换而非追加。
      await ctl.login(
        identityUrl: id.url,
        email: 'user-1@e.com',
        password: 'pw',
      );
      accounts = await accountsStore.load();
      expect(accounts.length, 1, reason: '同 accountId upsert, 不重复');
    });

    test('迁移播种: registry 空 + 已登录 settings → build 后种一条', () async {
      await repo.save(
        AppSettings(
          identityUrl: 'http://x:8088',
          accessToken: _fakeJwt('user-9'),
          refreshToken: 'rt-9',
          userEmail: 'u9@e.com',
        ),
      );
      expect(await accountsStore.load(), isEmpty);

      await container.read(settingsControllerProvider.future);

      final accounts = await accountsStore.load();
      expect(accounts.length, 1, reason: '存量登录用户升级后应播种');
      expect(accounts.single.userId, 'user-9');
      expect(accounts.single.refreshToken, 'rt-9');
    });

    test('未登录 settings 不播种', () async {
      await container.read(settingsControllerProvider.future);
      expect(await accountsStore.load(), isEmpty);
    });

    test('switchAccount: 原子换 identity slice, 设备级字段保留, 无 null token 中间态',
        () async {
      final id1 = await _startFakeIdentity(sub: 'user-1');
      final id2 = await _startFakeIdentity(sub: 'user-2');
      // 设备级字段 installationId 预置 (首读前写入, 避免 invalidate 重建
      // notifier 的 late final 重赋值问题)。
      await repo.save(const AppSettings(installationId: 'inst-1'));
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);

      // 设备级 UI 偏好: theme, 切账号后应保留。
      await ctl.updateTheme(ThemePreference.dark);

      // 登录两个账号: registry 两条, active = user-2。
      await ctl.login(
        identityUrl: id1.url,
        email: 'user-1@e.com',
        password: 'pw',
      );
      await ctl.login(
        identityUrl: id2.url,
        email: 'user-2@e.com',
        password: 'pw',
      );
      expect((await accountsStore.load()).length, 2);
      expect(
        (await container.read(settingsControllerProvider.future)).identityUrl,
        id2.url,
      );

      // 收集 settings watch 事件, 断言切换中途无 null token 中间态。
      final events = <AppSettings>[];
      final sub = repo.watch().listen(events.add);

      final targetId =
          accountIdFromEndpoint(Uri.parse(id1.url), _fakeJwt('user-1'))!;
      await ctl.switchAccount(targetId);

      final s = await container.read(settingsControllerProvider.future);
      expect(s.identityUrl, id1.url);
      expect(s.accessToken, _fakeJwt('user-1'));
      expect(s.refreshToken, 'rt-user-1');
      expect(s.userEmail, 'user-1@e.com');
      expect(s.theme, ThemePreference.dark, reason: 'UI 偏好是设备级, 保留');
      expect(s.installationId, 'inst-1', reason: 'installationId 设备级, 保留');

      expect(events, hasLength(1), reason: '单次原子 _save, 只 emit 一次');
      expect(
        events.every((e) => e.accessToken != null && e.accessToken!.isNotEmpty),
        isTrue,
        reason: '切换中途不出现 null token — router 不会弹登录页',
      );
      await sub.cancel();

      // 目标账号 lastActiveAt 被刷新。
      final accounts = await accountsStore.load();
      final t = accounts.firstWhere((a) => a.accountId == targetId);
      expect(t.lastActiveAt, isNotEmpty);
    });

    test('switchAccount: 记录不存在 → StateError', () async {
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);
      expect(
        () => ctl.switchAccount('nope'),
        throwsA(isA<StateError>()),
      );
    });

    test('applyRefreshed 同步更新 registry 槽位 (找不到不 throw)', () async {
      final id = await _startFakeIdentity(sub: 'user-1');
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);
      await ctl.login(
        identityUrl: id.url,
        email: 'user-1@e.com',
        password: 'pw',
      );

      // 轮换后 access token 变了但 sub 不变 → accountId 不变。
      await ctl.applyRefreshed(
        accessToken: _fakeJwt('user-1'),
        refreshToken: 'rt-user-1-rotated',
        tokenExpiresAt: '2026-08-10T14:00:00Z',
        accessTtlSeconds: 7200,
        refreshTokenExpiresAt: '2026-09-09T14:00:00Z',
        sessionId: 'sess-1-new',
      );

      final rec = (await accountsStore.load()).single;
      expect(rec.refreshToken, 'rt-user-1-rotated');
      expect(rec.tokenExpiresAt, '2026-08-10T14:00:00Z');
      expect(rec.accessTtlSeconds, 7200);
      expect(rec.refreshTokenExpiresAt, '2026-09-09T14:00:00Z');
      expect(rec.sessionId, 'sess-1-new');

      // 找不到槽位 (registry 被清空) → 跳过, 不 throw。
      await accountsStore.save([]);
      await ctl.applyRefreshed(
        accessToken: _fakeJwt('user-1'),
        refreshToken: 'rt-x',
        tokenExpiresAt: '2026-08-10T15:00:00Z',
      );
      expect(await accountsStore.load(), isEmpty);
    });

    test('signOut 移除 active 记录, 其余账号保留', () async {
      final id1 = await _startFakeIdentity(sub: 'user-1');
      final id2 = await _startFakeIdentity(sub: 'user-2');
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);
      await ctl.login(
        identityUrl: id1.url,
        email: 'user-1@e.com',
        password: 'pw',
      );
      await ctl.login(
        identityUrl: id2.url,
        email: 'user-2@e.com',
        password: 'pw',
      );

      await ctl.signOut();

      final accounts = await accountsStore.load();
      expect(accounts.length, 1);
      expect(accounts.single.userId, 'user-1', reason: '只移除登出的 active 账号');
      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, isNull);
    });

    test('removeAccount 非 active: 只删记录, 当前登录态不动', () async {
      final id1 = await _startFakeIdentity(sub: 'user-1');
      final id2 = await _startFakeIdentity(sub: 'user-2');
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);
      await ctl.login(
        identityUrl: id1.url,
        email: 'user-1@e.com',
        password: 'pw',
      );
      await ctl.login(
        identityUrl: id2.url,
        email: 'user-2@e.com',
        password: 'pw',
      );

      final dormantId =
          accountIdFromEndpoint(Uri.parse(id1.url), _fakeJwt('user-1'))!;
      await ctl.removeAccount(dormantId);

      final accounts = await accountsStore.load();
      expect(accounts.length, 1);
      expect(accounts.single.userId, 'user-2');
      final s = await container.read(settingsControllerProvider.future);
      expect(s.identityUrl, id2.url, reason: 'active 会话不受影响');
      expect(s.accessToken, isNotNull);
    });

    test('removeAccount active: 等价登出 (清 token + 删记录)', () async {
      final id1 = await _startFakeIdentity(sub: 'user-1');
      final id2 = await _startFakeIdentity(sub: 'user-2');
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);
      await ctl.login(
        identityUrl: id1.url,
        email: 'user-1@e.com',
        password: 'pw',
      );
      await ctl.login(
        identityUrl: id2.url,
        email: 'user-2@e.com',
        password: 'pw',
      );

      final activeId =
          accountIdFromEndpoint(Uri.parse(id2.url), _fakeJwt('user-2'))!;
      await ctl.removeAccount(activeId);

      final s = await container.read(settingsControllerProvider.future);
      expect(s.accessToken, isNull, reason: 'active 移除 = 登出');
      expect(s.refreshToken, isNull);
      final accounts = await accountsStore.load();
      expect(accounts.length, 1);
      expect(accounts.single.userId, 'user-1', reason: '其余账号保留, 不自动切换');
    });

    test('switchAccount 重置 per-account 全局状态 (连通/过期计数/选中笔记)', () async {
      final id1 = await _startFakeIdentity(sub: 'user-1');
      final id2 = await _startFakeIdentity(sub: 'user-2');
      await container.read(settingsControllerProvider.future);
      final ctl = container.read(settingsControllerProvider.notifier);
      await ctl.login(
        identityUrl: id1.url,
        email: 'user-1@e.com',
        password: 'pw',
      );
      await ctl.login(
        identityUrl: id2.url,
        email: 'user-2@e.com',
        password: 'pw',
      );

      // 模拟 account user-2 会话期间积累的全局状态。
      container.read(connectivityStateProvider.notifier).state =
          ConnectivityState.offlineWithCache;
      container.read(sessionExpiredCountProvider.notifier).state = 2;
      container.read(sessionExpiredReasonProvider.notifier).state =
          SessionExpiredReason.tokenReuse;
      container.read(selectedNoteIdProvider.notifier).state = 'note-of-user-2';

      final targetId =
          accountIdFromEndpoint(Uri.parse(id1.url), _fakeJwt('user-1'))!;
      await ctl.switchAccount(targetId);

      expect(container.read(connectivityStateProvider),
          ConnectivityState.online);
      expect(container.read(sessionExpiredCountProvider), 0);
      expect(container.read(sessionExpiredReasonProvider),
          SessionExpiredReason.expired);
      expect(container.read(selectedNoteIdProvider), isNull,
          reason: '选中笔记属于旧账号, 新账号 scope 下不存在');
    });
  });
}
