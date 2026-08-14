// account_registry 单测 — P2 多账号 Step 1 凭证层。
//
// 覆盖: AccountRecord JSON round-trip 字段完整性 / InMemory store
// load-save-watch / SecureAccountRegistryStore 的 secure 故障 fallback
// 文件 (原子写) 行为。fake secure storage 模式照抄 settings_repo_test。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/services/account_registry.dart';
import 'package:flutter/services.dart' show PlatformException;
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

/// 可控故障的 fake secure storage (同 settings_repo_test 的 _FakeSecureStorage)。
class _FakeSecureStorage extends FlutterSecureStorage {
  _FakeSecureStorage({this.alwaysFail = false});

  /// 读写一律抛 PlatformException。
  final bool alwaysFail;

  final Map<String, String> store = {};

  @override
  Future<String?> read({
    required String key,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async {
    if (alwaysFail) {
      throw PlatformException(code: 'read_error', message: 'keystore gone');
    }
    return store[key];
  }

  @override
  Future<void> write({
    required String key,
    required String? value,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async {
    if (alwaysFail) {
      throw PlatformException(code: 'write_error', message: 'keystore gone');
    }
    if (value == null) {
      store.remove(key);
    } else {
      store[key] = value;
    }
  }
}

Future<Directory> _tempDir() async {
  final d = await Directory.systemTemp.createTemp('account_registry_test');
  addTearDown(() => d.delete(recursive: true));
  return d;
}

const _rec1 = AccountRecord(
  accountId: 'hash1:user-1',
  identityUrl: 'http://localhost:8088',
  userId: 'user-1',
  email: 'u1@e.com',
  accessToken: 'tok-1',
  refreshToken: 'rt-1',
  tokenExpiresAt: '2026-08-10T13:00:00Z',
  accessTtlSeconds: 3600,
  refreshTokenExpiresAt: '2026-09-09T13:00:00Z',
  sessionId: 'sess-1',
  lastActiveAt: '2026-08-10T12:00:00Z',
);

const _rec2 = AccountRecord(
  accountId: 'hash2:user-2',
  identityUrl: 'https://cloud.example.com',
  userId: 'user-2',
  lastActiveAt: '2026-08-09T12:00:00Z',
);

void main() {
  test('AccountRecord json round-trip: 全字段完整保留', () {
    final r = AccountRecord.fromJson(_rec1.toJson());
    expect(r.accountId, _rec1.accountId);
    expect(r.identityUrl, _rec1.identityUrl);
    expect(r.userId, _rec1.userId);
    expect(r.email, _rec1.email);
    expect(r.accessToken, _rec1.accessToken);
    expect(r.refreshToken, _rec1.refreshToken);
    expect(r.tokenExpiresAt, _rec1.tokenExpiresAt);
    expect(r.accessTtlSeconds, _rec1.accessTtlSeconds);
    expect(r.refreshTokenExpiresAt, _rec1.refreshTokenExpiresAt);
    expect(r.sessionId, _rec1.sessionId);
    expect(r.lastActiveAt, _rec1.lastActiveAt);
  });

  test('AccountRecord json: 可空字段缺省 → null, 序列化不写 null 键', () {
    final j = _rec2.toJson();
    expect(j.containsKey('email'), isFalse);
    expect(j.containsKey('access_token'), isFalse);
    final r = AccountRecord.fromJson(j);
    expect(r.email, isNull);
    expect(r.accessToken, isNull);
    expect(r.accessTtlSeconds, isNull);
    expect(r.sessionId, isNull);
  });

  test('InMemoryAccountRegistryStore load / save / watch', () async {
    final store = InMemoryAccountRegistryStore();
    expect(await store.load(), isEmpty);

    final events = <List<AccountRecord>>[];
    final sub = store.watch().listen(events.add);

    await store.save([_rec1]);
    expect((await store.load()).single.accountId, 'hash1:user-1');

    await store.save([_rec1, _rec2]);
    expect((await store.load()).length, 2);

    await Future<void>.delayed(const Duration(milliseconds: 5));
    expect(events.length, 2, reason: '每次 save 后 emit 一次');
    expect(events.last.length, 2);

    await sub.cancel();
  });

  test('SecureAccountRegistryStore: secure round-trip (version 信封)', () async {
    final dir = await _tempDir();
    final storage = _FakeSecureStorage();
    final store = SecureAccountRegistryStore(
      storage: storage,
      fallbackPath: '${dir.path}/accounts.json',
    );

    await store.save([_rec1, _rec2]);

    final raw = storage.store['biumind.accounts'];
    expect(raw, isNotNull);
    final envelope = jsonDecode(raw!) as Map<String, dynamic>;
    expect(envelope['version'], 1);
    expect((envelope['accounts'] as List).length, 2);

    final loaded = await store.load();
    expect(loaded.length, 2);
    expect(loaded.first.accountId, 'hash1:user-1');
    expect(loaded.last.refreshToken, isNull);
  });

  test('SecureAccountRegistryStore: secure 故障 → 当次 fallback 文件, 原子写无 .tmp 残留',
      () async {
    final dir = await _tempDir();
    final fallback = '${dir.path}/accounts.json';
    final store = SecureAccountRegistryStore(
      storage: _FakeSecureStorage(alwaysFail: true),
      fallbackPath: fallback,
    );

    await store.save([_rec1]);
    expect(await File(fallback).exists(), isTrue, reason: 'secure 写失败落文件');
    expect(
      await File('$fallback.tmp').exists(),
      isFalse,
      reason: 'rename 后临时文件不应残留',
    );

    final loaded = await store.load();
    expect(loaded.single.accountId, 'hash1:user-1', reason: 'secure 读失败读文件');
  });

  test('SecureAccountRegistryStore: 空存储 → 空列表, 损坏 blob → 空列表', () async {
    final dir = await _tempDir();
    final storage = _FakeSecureStorage();
    final store = SecureAccountRegistryStore(
      storage: storage,
      fallbackPath: '${dir.path}/accounts.json',
    );
    expect(await store.load(), isEmpty);

    storage.store['biumind.accounts'] = '{not json';
    expect(await store.load(), isEmpty, reason: '损坏 blob 按空处理, 不抛');
  });
}
